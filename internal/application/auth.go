package application

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"telecli/internal/config"
	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// TUIAuthProvider implements telegram.AuthProvider using the TUI auth
// prompts.
type TUIAuthProvider struct{}

// Compile-time assertion: TUIAuthProvider implements telegram.AuthProvider.
var _ telegram.AuthProvider = TUIAuthProvider{}

// ProvidePhoneNumber prompts for a phone number.
func (TUIAuthProvider) ProvidePhoneNumber(_ context.Context) (string, error) {
	return tui.RunAuth(tui.AuthPromptPhone)
}

// ProvideCode prompts for the authentication code.
func (TUIAuthProvider) ProvideCode(_ context.Context) (string, error) {
	return tui.RunAuth(tui.AuthPromptCode)
}

// ProvidePassword prompts for the two-factor password.
func (TUIAuthProvider) ProvidePassword(_ context.Context) (string, error) {
	return tui.RunAuth(tui.AuthPromptPassword)
}

// TDLibCredentialsPresent reports whether at least one of the
// TELECLI_TDLIB_* authentication variables is set.
//
// It is used to distinguish three cases:
//
//   - none set:        mock-only TUI, runtime is not created
//   - all required set: production authorization
//   - some set:         configuration error, never silent mock
//
// Empty and whitespace-only values are treated as unset. Full
// validation of the credential set is performed by
// TdlibParametersFromEnv.
func TDLibCredentialsPresent() bool {
	for _, name := range []string{
		"TELECLI_TDLIB_API_ID",
		"TELECLI_TDLIB_API_HASH",
		"TELECLI_TDLIB_PHONE",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

// TdlibParametersFromEnv builds TDLib parameters from environment
// variables and config.
func TdlibParametersFromEnv(cfg config.Config) (telegram.TdlibParameters, error) {
	apiIDValue := os.Getenv("TELECLI_TDLIB_API_ID")
	if apiIDValue == "" {
		return telegram.TdlibParameters{}, fmt.Errorf(
			"TELECLI_TDLIB_API_ID is not set",
		)
	}

	apiID, err := strconv.Atoi(apiIDValue)
	if err != nil {
		return telegram.TdlibParameters{}, fmt.Errorf(
			"TELECLI_TDLIB_API_ID: %w",
			err,
		)
	}

	if apiID <= 0 {
		return telegram.TdlibParameters{}, fmt.Errorf(
			"TELECLI_TDLIB_API_ID must be positive",
		)
	}

	apiHash := os.Getenv("TELECLI_TDLIB_API_HASH")
	if apiHash == "" {
		return telegram.TdlibParameters{}, fmt.Errorf(
			"TELECLI_TDLIB_API_HASH is not set",
		)
	}

	phone := os.Getenv("TELECLI_TDLIB_PHONE")
	if strings.TrimSpace(phone) == "" {
		return telegram.TdlibParameters{}, fmt.Errorf(
			"TELECLI_TDLIB_PHONE is not set",
		)
	}

	return telegram.TdlibParameters{
		APIID:              apiID,
		APIHash:            apiHash,
		DatabaseDirectory:  cfg.TDLib.DatabaseDir,
		FilesDirectory:     cfg.TDLib.FilesDir,
		SystemLanguageCode: "en",
		DeviceModel:        "telecli",
		ApplicationVersion: "0.1.0",
	}, nil
}
