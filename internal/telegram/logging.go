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

	return nil
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
	return setSafeTDLibLogVerbosity(r.native)
}
