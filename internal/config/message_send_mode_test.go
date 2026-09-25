package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMessageSendModeAcceptsDirect(t *testing.T) {
	t.Parallel()

	got, err := ParseMessageSendMode("direct")
	if err != nil {
		t.Fatalf("ParseMessageSendMode() error = %v", err)
	}
	if got != MessageSendModeDirect {
		t.Fatalf("ParseMessageSendMode() = %q, want %q", got, MessageSendModeDirect)
	}
}

func TestParseMessageSendModeAcceptsDurable(t *testing.T) {
	t.Parallel()

	got, err := ParseMessageSendMode("durable")
	if err != nil {
		t.Fatalf("ParseMessageSendMode() error = %v", err)
	}
	if got != MessageSendModeDurable {
		t.Fatalf("ParseMessageSendMode() = %q, want %q", got, MessageSendModeDurable)
	}
}

func TestParseMessageSendModeUsesMigrationDefault(t *testing.T) {
	t.Parallel()

	got, err := ParseMessageSendMode("")
	if err != nil {
		t.Fatalf("ParseMessageSendMode() error = %v", err)
	}
	if got != DefaultMessageSendMode {
		t.Fatalf("ParseMessageSendMode() = %q, want %q", got, DefaultMessageSendMode)
	}
}

func TestParseMessageSendModeIsCaseAndWhitespaceInsensitive(t *testing.T) {
	t.Parallel()

	got, err := ParseMessageSendMode("  DURABLE ")
	if err != nil {
		t.Fatalf("ParseMessageSendMode() error = %v", err)
	}
	if got != MessageSendModeDurable {
		t.Fatalf("ParseMessageSendMode() = %q, want %q", got, MessageSendModeDurable)
	}
}

func TestParseMessageSendModeRejectsUnknownValue(t *testing.T) {
	t.Parallel()

	_, err := ParseMessageSendMode("automatic")
	if err == nil {
		t.Fatal("ParseMessageSendMode() error = nil, want non-nil")
	}
}

func TestMessageSendModeValidateAcceptsDirect(t *testing.T) {
	t.Parallel()

	if err := MessageSendModeDirect.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMessageSendModeValidateAcceptsDurable(t *testing.T) {
	t.Parallel()

	if err := MessageSendModeDurable.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMessageSendModeValidateRejectsZeroValue(t *testing.T) {
	t.Parallel()

	if err := MessageSendMode("").Validate(); err == nil {
		t.Fatal("Validate() error = nil, want non-nil")
	}
}

func TestMessageSendModeValidateRejectsUnknownValue(t *testing.T) {
	t.Parallel()

	if err := MessageSendMode("automatic").Validate(); err == nil {
		t.Fatal("Validate() error = nil, want non-nil")
	}
}

func TestLoadDefaultsMessageSendMode(t *testing.T) {
	t.Parallel()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if cfg.MessageDelivery.Mode != DefaultMessageSendMode {
		t.Fatalf("MessageDelivery.Mode = %q, want %q", cfg.MessageDelivery.Mode, DefaultMessageSendMode)
	}
}

func TestLoadExplicitMessageSendMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[message_delivery]\nmode = \"durable\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MessageDelivery.Mode != MessageSendModeDurable {
		t.Fatalf("MessageDelivery.Mode = %q, want %q", cfg.MessageDelivery.Mode, MessageSendModeDurable)
	}
}

func TestLoadRejectsUnknownMessageSendMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[message_delivery]\nmode = \"automatic\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want non-nil")
	}
}
