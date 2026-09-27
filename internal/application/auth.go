package application

import (
	"context"
	"fmt"
	"strings"

	"telecli/internal/config"
	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// Environment variables that carry a complete Telegram credential set.
//
// They are used for CI and developer setups. They are never combined with
// a credential profile.
const (
	telegramAPIIDEnvironment   = "TELECLI_TDLIB_API_ID"
	telegramAPIHashEnvironment = "TELECLI_TDLIB_API_HASH"
	telegramPhoneEnvironment   = "TELECLI_TDLIB_PHONE"
)

// TUIAuthProvider implements telegram.AuthProvider using the TUI auth
// prompts.
type TUIAuthProvider struct{}

// Compile-time assertion: TUIAuthProvider implements telegram.AuthProvider.
var _ telegram.AuthProvider = TUIAuthProvider{}

// ProvidePhoneNumber prompts for a phone number.
func (TUIAuthProvider) ProvidePhoneNumber(ctx context.Context) (string, error) {
	return tui.RunAuthContext(ctx, tui.AuthPromptPhone)
}

// ProvideCode prompts for the authentication code.
func (TUIAuthProvider) ProvideCode(ctx context.Context) (string, error) {
	return tui.RunAuthContext(ctx, tui.AuthPromptCode)
}

// ProvidePassword prompts for the two-factor password.
func (TUIAuthProvider) ProvidePassword(ctx context.Context) (string, error) {
	return tui.RunAuthContext(ctx, tui.AuthPromptPassword)
}

// TdlibParametersFromEnv builds TDLib parameters from a resolved
// credential set and the configuration.
//
// The credentials must come from a single source. Validation of that
// source is the resolver's job, not this function's.
func TdlibParametersFromEnv(
	cfg config.Config,
	credentials TelegramCredentials,
) (telegram.TdlibParameters, error) {
	if credentials.APIID <= 0 {
		return telegram.TdlibParameters{}, fmt.Errorf(
			"%w: Telegram API ID must be positive",
			ErrTelegramCredentialsInvalid,
		)
	}

	if credentials.APIHash == "" {
		return telegram.TdlibParameters{}, fmt.Errorf(
			"%w: Telegram API hash is empty",
			ErrTelegramCredentialsInvalid,
		)
	}

	if strings.TrimSpace(credentials.Phone) == "" {
		return telegram.TdlibParameters{}, fmt.Errorf(
			"%w: Telegram phone is empty",
			ErrTelegramCredentialsInvalid,
		)
	}

	return telegram.TdlibParameters{
		APIID:              credentials.APIID,
		APIHash:            credentials.APIHash,
		DatabaseDirectory:  cfg.TDLib.DatabaseDir,
		FilesDirectory:     cfg.TDLib.FilesDir,
		SystemLanguageCode: "en",
		DeviceModel:        "telecli",
		ApplicationVersion: "0.1.0",
	}, nil
}
