package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
)

// DefaultLogVerbosity is the TDLib log verbosity asked for when the
// configuration does not say.
//
// TDLib defaults to verbosity level 5 (see td/telegram/Log.h), which
// dumps every incoming request to stderr, including
// setTdlibParameters. That dump contains api_hash and must never reach
// a terminal, a log file, or a release artifact.
//
// Level 1 keeps TDLib errors only and drops verbose request dumps, which
// is what a user wants from a journal nobody reads: the library says when
// something went wrong and stays quiet while it works.
const DefaultLogVerbosity = 1

// MaxLogVerbosity is the loudest TDLib journal telecli writes.
//
// Level 2 and above are refused rather than honoured, at this layer and
// again in internal/config, because what they add over level 1 is the
// dump of the requests themselves — and a request carries the api_hash of
// setTdlibParameters or the text of a message. A configuration that asked
// for them would put a credential into a file on the user's disk, which is
// nobody's copy to make.
const MaxLogVerbosity = 1

// ErrTDLibLogConfiguration reports a failure to install the safe
// production TDLib log stream and verbosity.
var ErrTDLibLogConfiguration = errors.New(
	"telegram: configure TDLib log stream and verbosity",
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

// setSafeTDLibLogVerbosity lowers TDLib's own log verbosity to level.
//
// It is the single source of truth for the request payload: production
// startup and the integration tests must both go through it so the raw
// JSON is never duplicated.
//
// setLogVerbosityLevel is a global TDLib request and does not need a
// logical client, so this is safe to call before any client exists.
func setSafeTDLibLogVerbosity(native Native, level int) error {
	if native == nil {
		return fmt.Errorf("%w: native is required", ErrTDLibLogConfiguration)
	}

	request, err := buildSetLogVerbosityRequest(level)
	if err != nil {
		return err
	}

	response, err := native.Execute(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTDLibLogConfiguration, err)
	}

	return requireOKResponse(response)
}

// requireOKResponse reads the answer of a synchronous TDLib request.
//
// td_execute reports a rejected request as a successful call that returns
// an error object. Only an explicit ok proves the request took effect;
// anything else must fail closed, because a request this program cannot
// confirm is a library left in a state the user did not ask for.
func requireOKResponse(response []byte) error {
	if len(response) == 0 {
		return fmt.Errorf("%w: empty response", ErrTDLibLogConfiguration)
	}

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

// ConfigureSafeLogging decides where TDLib's journal goes and how much of
// it there is.
//
// Two requests, in this order. The stream is set first, so that the lines
// TDLib writes while it is still at its default verbosity land in the file
// instead of on the terminal; the verbosity comes second, and both of them
// are complete before the first request of the program and before the
// first logical client exists. Nothing that carries a credential is ever
// sent between the two, and nothing is sent at all until the level is down.
//
// It must complete before any credential-bearing request is sent. In
// particular it must run before the first setTdlibParameters, which
// carries api_hash.
//
// The method fails closed: callers must abort startup when it returns an
// error instead of continuing with a library that logs to the terminal.
func (r *Runtime) ConfigureSafeLogging() error {
	if r == nil {
		return fmt.Errorf(
			"%w: runtime is required",
			ErrTDLibLogConfiguration,
		)
	}
	// Start already moves the journal and lowers the verbosity before the
	// first receive; the setting is process-wide, so a later call has
	// nothing to change.
	if r.logSecured.Load() {
		return nil
	}
	if !r.keepNativeLogOnStderr {
		if err := redirectTDLibLog(r.native, r.cfg.LogFilePath); err != nil {
			return err
		}
	}
	if err := setSafeTDLibLogVerbosity(
		r.native,
		r.cfg.LogVerbosity,
	); err != nil {
		return err
	}
	r.logSecured.Store(true)
	return nil
}
