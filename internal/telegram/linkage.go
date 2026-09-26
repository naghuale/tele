package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// LinkageStatus is a closed verdict for one linkage check. It never carries a
// payload, a library path or a backend message.
type LinkageStatus uint8

const (
	LinkageStatusUnknown LinkageStatus = iota
	LinkageStatusPass
	LinkageStatusFail
)

func (s LinkageStatus) String() string {
	switch s {
	case LinkageStatusPass:
		return "PASS"
	case LinkageStatusFail:
		return "FAIL"
	default:
		return "UNKNOWN"
	}
}

// LinkageResult is the entire observable outcome of a credential-free linkage
// probe. It deliberately has no field for a raw request, a raw response, a
// backend message, a library path, a credential or a data directory.
type LinkageResult struct {
	Source          NativeLibrarySource
	Version         string
	Commit          string
	Compatibility   CompatibilityMode
	ClientCreated   LinkageStatus
	SendReceive     LinkageStatus
	ExtraRoundTrip  LinkageStatus
	ClientIDMatched LinkageStatus
}

// Failed reports whether any required check did not pass.
func (r LinkageResult) Failed() bool {
	return r.ClientCreated != LinkageStatusPass ||
		r.SendReceive != LinkageStatusPass ||
		r.ExtraRoundTrip != LinkageStatusPass ||
		r.ClientIDMatched != LinkageStatusPass
}

const (
	// linkageMaxIgnoredUpdates bounds how many unrelated objects the probe
	// will step over while looking for its own response. A response never
	// arrives first by accident beyond this bound.
	linkageMaxIgnoredUpdates = 8

	// linkageReceiveTimeout bounds one wait for the probe's own response.
	linkageReceiveTimeout = 10 * time.Second
)

// ErrLinkageProbe reports that the probe could not run at all, as opposed to a
// check that ran and failed.
var ErrLinkageProbe = errors.New("telegram: credential-free linkage probe")

// linkageVersionRequest is the only request the probe is permitted to send. It
// is a metadata read that TDLib answers before authorization.
type linkageVersionRequest struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

func buildLinkageVersionRequest() (RawMessage, error) {
	encoded, err := json.Marshal(linkageVersionRequest{
		Type: "getOption",
		Name: "version",
	})
	if err != nil {
		return nil, fmt.Errorf("%w: build version request: %w", ErrLinkageProbe, err)
	}
	return RawMessage(encoded), nil
}

// RunLinkageProbe loads the pinned TDLib through the production loader and
// proves, without any credential and without any authorization request, that
// the modern asynchronous interface delivers a response correlated by @extra
// to the client that sent it.
//
// It never reads credentials, a keychain, a configuration file or a Telegram
// data directory, and it never sends setTdlibParameters or any authorization
// request.
func RunLinkageProbe(
	ctx context.Context,
	configuredPath string,
) (LinkageResult, error) {
	if ctx == nil {
		return LinkageResult{}, fmt.Errorf("%w: context is required", ErrLinkageProbe)
	}
	if err := ctx.Err(); err != nil {
		return LinkageResult{}, err
	}

	loaded, err := LoadNativeWithSource(configuredPath)
	if err != nil {
		return LinkageResult{}, fmt.Errorf("%w: load native runtime: %w", ErrLinkageProbe, err)
	}
	if loaded.Native == nil {
		return LinkageResult{}, fmt.Errorf("%w: nil native runtime", ErrLinkageProbe)
	}

	result := LinkageResult{Source: loaded.Source}

	runtime, err := NewRuntime(DefaultConfig(), loaded.Native, nil)
	if err != nil {
		_ = loaded.Native.Close()
		return LinkageResult{}, fmt.Errorf("%w: create runtime: %w", ErrLinkageProbe, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = runtime.Close(closeCtx)
	}()

	if err := runtime.Start(runCtx); err != nil {
		return result, fmt.Errorf("%w: start runtime: %w", ErrLinkageProbe, err)
	}

	// Metadata goes through the production synchronous inspection path so the
	// probe does not duplicate getOption handling.
	if info, inspectErr := runtime.Inspect(runCtx, RuntimeCompatibility()); inspectErr == nil {
		result.Version = info.Version
		result.Commit = info.Commit
		result.Compatibility = info.Mode
	}

	client, err := runtime.NewClient()
	if err != nil || client == nil || client.ID() <= 0 {
		result.ClientCreated = LinkageStatusFail
		return result, nil
	}
	result.ClientCreated = LinkageStatusPass

	request, err := buildLinkageVersionRequest()
	if err != nil {
		result.SendReceive = LinkageStatusFail
		return result, nil
	}

	queryID := newQueryID(client.ID())
	tagged, err := withQueryID(request, queryID)
	if err != nil {
		result.SendReceive = LinkageStatusFail
		return result, nil
	}

	if err := runtime.Send(client.ID(), tagged); err != nil {
		result.SendReceive = LinkageStatusFail
		return result, nil
	}
	result.SendReceive = LinkageStatusPass

	matched, response, err := awaitLinkageResponse(runCtx, client, queryID)
	if err != nil {
		return result, nil
	}
	if !matched {
		return result, nil
	}
	result.ExtraRoundTrip = LinkageStatusPass
	result.ClientIDMatched = LinkageStatusPass

	var value optionValueString
	if err := json.Unmarshal(response, &value); err != nil {
		return result, nil
	}
	if value.Type != "optionValueString" || value.Value == "" {
		return result, nil
	}
	if result.Version == "" {
		result.Version = value.Value
	}

	return result, nil
}

// awaitLinkageResponse accepts only an object that belongs to this client and
// carries this probe's own @extra. Anything else is stepped over up to a fixed
// bound, and an error object never counts as success.
func awaitLinkageResponse(
	ctx context.Context,
	client *Client,
	queryID QueryID,
) (bool, RawMessage, error) {
	deadline := time.NewTimer(linkageReceiveTimeout)
	defer deadline.Stop()

	ignored := 0

	for {
		select {
		case <-ctx.Done():
			return false, nil, ctx.Err()

		case <-client.Errors():
			// The backend message is deliberately not propagated.
			return false, nil, fmt.Errorf("%w: send-receive", ErrLinkageProbe)

		case update, open := <-client.Updates():
			if !open {
				return false, nil, fmt.Errorf("%w: updates closed", ErrLinkageProbe)
			}
			if update.ClientID != client.ID() {
				if ignored++; ignored > linkageMaxIgnoredUpdates {
					return false, nil, fmt.Errorf("%w: update limit", ErrLinkageProbe)
				}
				continue
			}

			var envelope struct {
				Type  string `json:"@type"`
				Extra string `json:"@extra"`
			}
			if err := json.Unmarshal(update.Raw, &envelope); err != nil {
				if ignored++; ignored > linkageMaxIgnoredUpdates {
					return false, nil, fmt.Errorf("%w: update limit", ErrLinkageProbe)
				}
				continue
			}
			if envelope.Type == "error" {
				return false, nil, fmt.Errorf("%w: send-receive", ErrLinkageProbe)
			}

			got, present, err := responseQueryID(update.Raw)
			if err != nil || !present || got != queryID {
				if ignored++; ignored > linkageMaxIgnoredUpdates {
					return false, nil, fmt.Errorf("%w: update limit", ErrLinkageProbe)
				}
				continue
			}

			return true, update.Raw, nil

		case <-deadline.C:
			return false, nil, fmt.Errorf("%w: timeout", ErrLinkageProbe)
		}
	}
}
