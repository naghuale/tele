package config

import (
	"fmt"
	"strings"
)

type MessageSendMode string

const (
	MessageSendModeDirect  MessageSendMode = "direct"
	MessageSendModeDurable MessageSendMode = "durable"
	DefaultMessageSendMode                 = MessageSendModeDirect
)

func ParseMessageSendMode(value string) (MessageSendMode, error) {
	normalized := MessageSendMode(strings.ToLower(strings.TrimSpace(value)))

	switch normalized {
	case "":
		return DefaultMessageSendMode, nil
	case MessageSendModeDirect:
		return MessageSendModeDirect, nil
	case MessageSendModeDurable:
		return MessageSendModeDurable, nil
	default:
		return "", fmt.Errorf("unsupported message send mode %q", value)
	}
}

func (m MessageSendMode) Validate() error {
	switch m {
	case MessageSendModeDirect, MessageSendModeDurable:
		return nil
	default:
		return fmt.Errorf("unsupported message send mode %q", m)
	}
}
