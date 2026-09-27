package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
)

// productionTDLibVerbosityLevel is the TDLib log verbosity used by
// production startup.
//
// TDLib defaults to verbosity level 5 (see td/telegram/Log.h), which
// dumps every incoming request to stderr, including
// setTdlibParameters. That dump contains api_hash and must never reach
// a terminal, a log file, or a release artifact.
//
// Level 1 keeps TDLib errors only and drops verbose request dumps.
// Levels >= 2 are forbidden for production startup.
const productionTDLibVerbosityLevel = 1

// ErrTDLibLogConfiguration reports a failure to install the safe
// production TDLib log verbosity.
var ErrTDLibLogConfiguration = errors.New(
	"telegram: configure TDLib log verbosity",
)

// setLogVerbosityRequest is the TDLib request that lowers the internal
// log verbosity of the whole TDLib instance.
type setLogVerbosityRequest struct {
	Type              string `json:"@type"`
	NewVerbosityLevel int    `json:"new_verbosity_level"`
}

// buildSetLogVerbosityRequest renders the setLogVerbosityLevel request
// for the given level.
func buildSetLogVerbosityRequest(level int) (RawMessage, error) {
	if level < 0 {
		return nil, fmt.Errorf(
			"%w: negative verbosity level %d",
			ErrTDLibLogConfiguration,
			level,
		)
	}

	raw, err := json.Marshal(setLogVerbosityRequest{
		Type:              "setLogVerbosityLevel",
		NewVerbosityLevel: level,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"%w: build request: %w",
			ErrTDLibLogConfiguration,
			err,
		)
	}

	return RawMessage(raw), nil
}

// setSafeTDLibLogVerbosity lowers TDLib's own log verbosity to
// productionTDLibVerbosityLevel.
//
// It is the single source of truth for the request payload: production
// startup and the integration tests must both go through it so the raw
// JSON is never duplicated.
//
// setLogVerbosityLevel is a global TDLib request and does not need a
// logical client, so this is safe to call before any client exists.
func setSafeTDLibLogVerbosity(native Native) error {
	if native == nil {
		return fmt.Errorf("%w: native is required", ErrTDLibLogConfiguration)
	}

	request, err := buildSetLogVerbosityRequest(
		productionTDLibVerbosityLevel,
	)
	if err != nil {
		return err
	}

	response, err := native.Execute(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTDLibLogConfiguration, err)
	}
	if len(response) == 0 {
		return fmt.Errorf(
			"%w: empty response",
			ErrTDLibLogConfiguration,
		)
	}

	// td_execute reports a rejected request as a successful call that
	// returns an error object. Only an explicit ok proves the verbosity
	// was lowered; anything else must fail closed.
	var envelope struct {
		Type    string `json:"@type"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return fmt.Errorf(
			"%w: decode response: %w",
			ErrTDLibLogConfiguration,
			err,
		)
	}
	switch envelope.Type {
	case "ok":
		return nil
	case "error":
		return fmt.Errorf(
			"%w: TDLib error code=%d message=%q",
			ErrTDLibLogConfiguration,
			envelope.Code,
			envelope.Message,
		)
	default:
		return fmt.Errorf(
			"%w: unexpected response type %q",
			ErrTDLibLogConfiguration,
			envelope.Type,
		)
	}
}

// ConfigureSafeLogging lowers TDLib's log verbosity for production use.
//
// It must complete before any credential-bearing request is sent. In
// particular it must run before the first setTdlibParameters, which
// carries api_hash.
//
// The method fails closed: callers must abort startup when it returns
// an error instead of continuing with TDLib's default verbosity.
func (r *Runtime) ConfigureSafeLogging() error {
	if r == nil {
		return fmt.Errorf(
			"%w: runtime is required",
			ErrTDLibLogConfiguration,
		)
	}
	// Start already lowers the verbosity before the first receive; the
	// setting is process-wide, so a later call has nothing to change.
	if r.logSecured.Load() {
		return nil
	}
	if err := setSafeTDLibLogVerbosity(r.native); err != nil {
		return err
	}
	r.logSecured.Store(true)
	return nil
}
