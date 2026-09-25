package application

import (
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/telegram"
)

func TestTdlibParametersFromEnvRequiresAPIID(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_API_ID", "")
	t.Setenv("TELECLI_TDLIB_API_HASH", "test-hash")
	t.Setenv("TELECLI_TDLIB_PHONE", "+15551234567")

	if _, err := TdlibParametersFromEnv(config.Default()); err == nil {
		t.Fatal("expected error for missing API ID")
	}
}

func TestTdlibParametersFromEnvRequiresAPIHash(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_API_ID", "12345")
	t.Setenv("TELECLI_TDLIB_API_HASH", "")
	t.Setenv("TELECLI_TDLIB_PHONE", "+15551234567")

	if _, err := TdlibParametersFromEnv(config.Default()); err == nil {
		t.Fatal("expected error for missing API hash")
	}
}

func TestTdlibParametersFromEnvRequiresPhone(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_API_ID", "12345")
	t.Setenv("TELECLI_TDLIB_API_HASH", "test-hash")
	t.Setenv("TELECLI_TDLIB_PHONE", "")

	if _, err := TdlibParametersFromEnv(config.Default()); err == nil {
		t.Fatal("expected error for missing phone")
	}
}

func TestTdlibParametersFromEnvRejectsMissingPhone(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_API_ID", "12345")
	t.Setenv("TELECLI_TDLIB_API_HASH", "test-hash")
	t.Setenv("TELECLI_TDLIB_PHONE", "")

	_, err := TdlibParametersFromEnv(config.Default())
	if err == nil {
		t.Fatal("expected error for missing phone")
	}
	if !strings.Contains(err.Error(), "TELECLI_TDLIB_PHONE") {
		t.Fatalf("error = %v, want phone variable", err)
	}
}

func TestTdlibParametersFromEnvRejectsWhitespacePhone(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_API_ID", "12345")
	t.Setenv("TELECLI_TDLIB_API_HASH", "test-hash")
	t.Setenv("TELECLI_TDLIB_PHONE", "   ")

	if _, err := TdlibParametersFromEnv(config.Default()); err == nil {
		t.Fatal("expected error for whitespace-only phone")
	}
}

func TestTdlibParametersFromEnvRejectsNonNumericAPIID(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_API_ID", "not-a-number")
	t.Setenv("TELECLI_TDLIB_API_HASH", "test-hash")
	t.Setenv("TELECLI_TDLIB_PHONE", "+15551234567")

	if _, err := TdlibParametersFromEnv(config.Default()); err == nil {
		t.Fatal("expected error for non-numeric API ID")
	}
}

func TestTdlibParametersFromEnvRejectsNonPositiveAPIID(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TELECLI_TDLIB_API_ID", value)
			t.Setenv("TELECLI_TDLIB_API_HASH", "test-hash")
			t.Setenv("TELECLI_TDLIB_PHONE", "+15551234567")

			if _, err := TdlibParametersFromEnv(config.Default()); err == nil {
				t.Fatalf("API ID %s: expected error", value)
			}
		})
	}
}

func TestTdlibParametersFromEnvHappyPath(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_API_ID", "12345")
	t.Setenv("TELECLI_TDLIB_API_HASH", "test-hash")
	t.Setenv("TELECLI_TDLIB_PHONE", "+15551234567")

	cfg := config.Default()
	params, err := TdlibParametersFromEnv(cfg)
	if err != nil {
		t.Fatalf("TdlibParametersFromEnv: %v", err)
	}

	if params.APIID != 12345 {
		t.Fatalf("APIID = %d, want 12345", params.APIID)
	}
	if params.APIHash != "test-hash" {
		t.Fatalf("APIHash = %q, want test-hash", params.APIHash)
	}
	if params.DatabaseDirectory != cfg.TDLib.DatabaseDir {
		t.Fatalf(
			"DatabaseDirectory = %q, want %q",
			params.DatabaseDirectory, cfg.TDLib.DatabaseDir,
		)
	}
	if params.FilesDirectory != cfg.TDLib.FilesDir {
		t.Fatalf(
			"FilesDirectory = %q, want %q",
			params.FilesDirectory, cfg.TDLib.FilesDir,
		)
	}
	if params.SystemLanguageCode == "" ||
		params.DeviceModel == "" ||
		params.ApplicationVersion == "" {
		t.Fatalf("expected non-empty defaults, got %+v", params)
	}
}

func TestTdlibCredentialsPresent(t *testing.T) {
	cases := []struct {
		name  string
		id    string
		hash  string
		phone string
		want  bool
	}{
		{"none", "", "", "", false},
		{"only id", "1", "", "", true},
		{"only hash", "", "h", "", true},
		{"only phone", "", "", "+1", true},
		{"id with whitespace", "  ", "", "", false},
		{"phone with whitespace", "", "", "  ", false},
		{"complete", "1", "h", "+1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TELECLI_TDLIB_API_ID", c.id)
			t.Setenv("TELECLI_TDLIB_API_HASH", c.hash)
			t.Setenv("TELECLI_TDLIB_PHONE", c.phone)

			if got := TDLibCredentialsPresent(); got != c.want {
				t.Fatalf("TDLibCredentialsPresent() = %t, want %t", got, c.want)
			}
		})
	}
}

func TestTUIAuthProviderImplementsTelegramAuthProvider(t *testing.T) {
	var _ telegram.AuthProvider = TUIAuthProvider{}
}
