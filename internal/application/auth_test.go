package application

import (
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/telegram"
)

// The environment parsing and validation that used to live in
// TdlibParametersFromEnv now belongs to TelegramCredentialResolver; see
// auth_credentials_test.go. These tests cover only the mapping from an
// already resolved credential set to TDLib parameters.

func completeCredentials() TelegramCredentials {
	return TelegramCredentials{
		APIID:   12345,
		APIHash: "test-hash",
		Phone:   "+15551234567",
	}
}

func TestTdlibParametersFromEnvRejectsNonPositiveAPIID(t *testing.T) {
	for _, apiID := range []int{0, -1} {
		credentials := completeCredentials()
		credentials.APIID = apiID

		_, err := TdlibParametersFromEnv(
			config.Default(),
			credentials,
		)
		if err == nil {
			t.Fatalf("expected error for API ID %d", apiID)
		}
		if !strings.Contains(err.Error(), "API ID") {
			t.Fatalf("error = %v, want an API ID message", err)
		}
	}
}

func TestTdlibParametersFromEnvRejectsEmptyAPIHash(t *testing.T) {
	credentials := completeCredentials()
	credentials.APIHash = ""

	if _, err := TdlibParametersFromEnv(
		config.Default(),
		credentials,
	); err == nil {
		t.Fatal("expected error for empty API hash")
	}
}

func TestTdlibParametersFromEnvRejectsEmptyPhone(t *testing.T) {
	for _, phone := range []string{"", "   "} {
		credentials := completeCredentials()
		credentials.Phone = phone

		if _, err := TdlibParametersFromEnv(
			config.Default(),
			credentials,
		); err == nil {
			t.Fatalf("expected error for phone %q", phone)
		}
	}
}

func TestTdlibParametersFromEnvHappyPath(t *testing.T) {
	credentials := completeCredentials()

	cfg := config.Default()
	params, err := TdlibParametersFromEnv(cfg, credentials)
	if err != nil {
		t.Fatalf("TdlibParametersFromEnv: %v", err)
	}

	if params.APIID != credentials.APIID {
		t.Fatalf("APIID = %d, want %d", params.APIID, credentials.APIID)
	}
	if params.APIHash != credentials.APIHash {
		t.Fatalf(
			"APIHash = %q, want %q",
			params.APIHash,
			credentials.APIHash,
		)
	}
	// The phone number is validated here but is not part of
	// TdlibParameters: TDLib receives it later through
	// AuthInput.PhoneNumber at the authorization prompt.
	if params.DatabaseDirectory != cfg.TDLib.DatabaseDir {
		t.Fatalf(
			"DatabaseDirectory = %q, want %q",
			params.DatabaseDirectory,
			cfg.TDLib.DatabaseDir,
		)
	}
	if params.FilesDirectory != cfg.TDLib.FilesDir {
		t.Fatalf(
			"FilesDirectory = %q, want %q",
			params.FilesDirectory,
			cfg.TDLib.FilesDir,
		)
	}
	if params.SystemLanguageCode == "" ||
		params.DeviceModel == "" ||
		params.ApplicationVersion == "" {
		t.Fatalf("expected non-empty defaults, got %+v", params)
	}
}

func TestTdlibParametersErrorsDoNotLeakCredentials(t *testing.T) {
	const secretHash = "resolver-secret-hash-value"
	const secretPhone = "+15550009999"

	cases := []struct {
		name        string
		credentials TelegramCredentials
	}{
		{
			name: "empty hash",
			credentials: TelegramCredentials{
				APIID: 1,
				Phone: secretPhone,
			},
		},
		{
			name: "non positive id",
			credentials: TelegramCredentials{
				APIHash: secretHash,
				Phone:   secretPhone,
			},
		},
		{
			name: "empty phone",
			credentials: TelegramCredentials{
				APIID:   1,
				APIHash: secretHash,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := TdlibParametersFromEnv(
				config.Default(),
				c.credentials,
			)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, secret := range []string{secretHash, secretPhone} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("the error leaked a credential value")
				}
			}
		})
	}
}

func TestTUIAuthProviderImplementsTelegramAuthProvider(t *testing.T) {
	var _ telegram.AuthProvider = TUIAuthProvider{}
}
