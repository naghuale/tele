package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The retired "direct" mode must load, normalise to durable, and report
// itself as legacy so the caller can warn once.
func TestParseMessageSendModeMigratesDirectToDurable(t *testing.T) {
	t.Parallel()

	got, legacy, err := ParseMessageSendMode("direct")
	if err != nil {
		t.Fatalf("ParseMessageSendMode() error = %v", err)
	}
	if got != MessageSendModeDurable {
		t.Fatalf("ParseMessageSendMode() = %q, want %q", got, MessageSendModeDurable)
	}
	if !legacy {
		t.Fatal("legacy = false, want true for a retired direct mode")
	}
}

func TestParseMessageSendModeAcceptsDurable(t *testing.T) {
	t.Parallel()

	got, legacy, err := ParseMessageSendMode("durable")
	if err != nil {
		t.Fatalf("ParseMessageSendMode() error = %v", err)
	}
	if got != MessageSendModeDurable {
		t.Fatalf("ParseMessageSendMode() = %q, want %q", got, MessageSendModeDurable)
	}
	if legacy {
		t.Fatal("legacy = true, want false for durable")
	}
}

func TestParseMessageSendModeDefaultsToDurable(t *testing.T) {
	t.Parallel()

	got, legacy, err := ParseMessageSendMode("")
	if err != nil {
		t.Fatalf("ParseMessageSendMode() error = %v", err)
	}
	if got != DefaultMessageSendMode {
		t.Fatalf("ParseMessageSendMode() = %q, want %q", got, DefaultMessageSendMode)
	}
	if DefaultMessageSendMode != MessageSendModeDurable {
		t.Fatalf(
			"DefaultMessageSendMode = %q, want durable: direct send loses messages",
			DefaultMessageSendMode,
		)
	}
	if legacy {
		t.Fatal("legacy = true, want false for an absent value")
	}
}

func TestParseMessageSendModeIsCaseAndWhitespaceInsensitive(t *testing.T) {
	t.Parallel()

	got, _, err := ParseMessageSendMode("  DURABLE ")
	if err != nil {
		t.Fatalf("ParseMessageSendMode() error = %v", err)
	}
	if got != MessageSendModeDurable {
		t.Fatalf("ParseMessageSendMode() = %q, want %q", got, MessageSendModeDurable)
	}
}

func TestParseMessageSendModeRejectsUnknownValue(t *testing.T) {
	t.Parallel()

	got, legacy, err := ParseMessageSendMode("automatic")
	if err == nil {
		t.Fatal("ParseMessageSendMode() error = nil, want non-nil")
	}
	if got != "" {
		t.Fatalf("ParseMessageSendMode() = %q, want empty on error", got)
	}
	if legacy {
		t.Fatal("legacy = true, want false for an unknown value")
	}
}

func TestMessageSendModeValidateRejectsDirect(t *testing.T) {
	t.Parallel()

	if err := MessageSendModeDirect.Validate(); err == nil {
		t.Fatal("Validate() error = nil: direct is not a usable mode")
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

func TestMessageSendModeIsLegacy(t *testing.T) {
	t.Parallel()

	if !MessageSendModeDirect.IsLegacy() {
		t.Fatal("direct.IsLegacy() = false, want true")
	}
	if MessageSendModeDurable.IsLegacy() {
		t.Fatal("durable.IsLegacy() = true, want false")
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

	path := writeModeConfig(t, "durable")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MessageDelivery.Mode != MessageSendModeDurable {
		t.Fatalf("MessageDelivery.Mode = %q, want %q", cfg.MessageDelivery.Mode, MessageSendModeDurable)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none for a supported mode", cfg.Warnings)
	}
}

// An existing configuration that says "direct" must still start, on
// durable, with a warning the user can act on.
func TestLoadAcceptsLegacyDirectWithWarning(t *testing.T) {
	t.Parallel()

	path := writeModeConfig(t, "direct")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MessageDelivery.Mode != MessageSendModeDurable {
		t.Fatalf("MessageDelivery.Mode = %q, want %q", cfg.MessageDelivery.Mode, MessageSendModeDurable)
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one", cfg.Warnings)
	}
	if !strings.Contains(cfg.Warnings[0], "direct") {
		t.Fatalf("warning %q does not mention the retired value", cfg.Warnings[0])
	}
}

func TestLoadRejectsUnknownMessageSendMode(t *testing.T) {
	t.Parallel()

	path := writeModeConfig(t, "automatic")

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want non-nil")
	}
}

func writeModeConfig(t *testing.T, mode string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[message_delivery]\nmode = \"" + mode + "\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
