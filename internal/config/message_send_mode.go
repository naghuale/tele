package config

import (
	"fmt"
	"strings"
)

// MessageSendMode is the delivery mode for outgoing messages.
type MessageSendMode string

const (
	// MessageSendModeDirect is a legacy configuration value.
	//
	// It is no longer a production send mode: with no durable outbox a
	// message can be lost between Enter and TDLib's answer. The constant
	// survives only so an existing configuration that still says
	// "direct" can be recognised and migrated, never to be selected.
	MessageSendModeDirect MessageSendMode = "direct"

	// MessageSendModeDurable is the only supported send mode.
	MessageSendModeDurable MessageSendMode = "durable"

	// DefaultMessageSendMode is durable.
	//
	// Direct send lost messages on exit, so an absent setting must not
	// select it.
	DefaultMessageSendMode = MessageSendModeDurable
)

// ParseMessageSendMode reads a configured send mode.
//
// The second result reports a legacy value that was accepted and
// normalised, so the caller can warn once instead of failing. "direct"
// is such a value: existing configurations carry it, and refusing to
// start would be worse than running with the mode that does not lose
// messages.
//
// An unrecognised value is a configuration error, as before.
func ParseMessageSendMode(value string) (MessageSendMode, bool, error) {
	normalized := MessageSendMode(strings.ToLower(strings.TrimSpace(value)))

	switch normalized {
	case "":
		return DefaultMessageSendMode, false, nil
	case MessageSendModeDirect:
		return DefaultMessageSendMode, true, nil
	case MessageSendModeDurable:
		return MessageSendModeDurable, false, nil
	default:
		return "", false, fmt.Errorf("unsupported message send mode %q", value)
	}
}

// Validate reports whether the mode is one that may be used.
//
// A legacy "direct" is not valid as a mode: it is rewritten to durable
// while parsing, so a model still carrying it came from somewhere that
// bypassed ParseMessageSendMode.
func (m MessageSendMode) Validate() error {
	switch m {
	case MessageSendModeDurable:
		return nil
	default:
		return fmt.Errorf("unsupported message send mode %q", m)
	}
}

// IsLegacy reports whether the value is a retired send mode that should
// be reported to the user rather than used.
func (m MessageSendMode) IsLegacy() bool {
	return m == MessageSendModeDirect
}
