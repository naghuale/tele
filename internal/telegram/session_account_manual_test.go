//go:build tdlib_account_integration && cgo && (darwin || linux)

package telegram

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"telecli/internal/telemetry/recorder"
)

// Manual integration test against a real Telegram account.
//
// Run explicitly:
//
//	go test \
//	  -tags tdlib_account_integration \
//	  ./internal/telegram/... \
//	  -run '^TestAuthorizedAccountSessionManual$' \
//	  -count=1 -v
//
// Required environment:
//
//	TELECLI_TDLIB_INTEGRATION  must equal "1"
//	TELECLI_TDLIB_LIBRARY      absolute path to libtdjson
//	TELECLI_TDLIB_API_ID       positive integer
//	TELECLI_TDLIB_API_HASH     string
//	TELECLI_TDLIB_PHONE        phone number, e.g. +15551234567
//
// Optional:
//
//	TELECLI_TDLIB_PASSWORD     2FA password. Required if the account
//	                           has 2FA enabled.
//
// The authentication code is read interactively after TDLib reaches
// authorizationStateWaitCode. The 2FA password is never read from
// stdin: it must be provided via the environment to avoid echoing the
// secret in a terminal.

type consoleProvider struct {
	phone    string
	password string
	input    *bufio.Reader
	output   *os.File
}

func (p *consoleProvider) ProvidePhoneNumber(context.Context) (string, error) {
	return p.phone, nil
}

func (p *consoleProvider) ProvideCode(context.Context) (string, error) {
	fmt.Fprint(p.output, "Telegram code: ")
	line, err := p.input.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read Telegram code: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// ProvidePassword returns the 2FA password from the environment. It
// never reads the password from stdin so the secret is not echoed to
// the terminal.
func (p *consoleProvider) ProvidePassword(context.Context) (string, error) {
	if p.password == "" {
		return "", fmt.Errorf(
			"%w: TELECLI_TDLIB_PASSWORD is required for 2FA",
			ErrInvalidAuthParameters,
		)
	}
	return p.password, nil
}

// compile-time guard.
var _ AuthProvider = (*consoleProvider)(nil)

func requireAccountIntegrationEnv(t *testing.T) (
	apiID int,
	apiHash string,
	phone string,
	password string,
	libraryPath string,
) {
	t.Helper()

	if os.Getenv("TELECLI_TDLIB_INTEGRATION") != "1" {
		t.Skip("TELECLI_TDLIB_INTEGRATION != 1")
	}

	libraryPath = os.Getenv("TELECLI_TDLIB_LIBRARY")
	if libraryPath == "" {
		t.Fatal("TELECLI_TDLIB_LIBRARY is required")
	}

	apiIDValue := os.Getenv("TELECLI_TDLIB_API_ID")
	if apiIDValue == "" {
		t.Fatal("TELECLI_TDLIB_API_ID is required")
	}
	parsed, err := strconv.Atoi(apiIDValue)
	if err != nil || parsed <= 0 {
		t.Fatalf("TELECLI_TDLIB_API_ID invalid: %q", apiIDValue)
	}
	apiID = parsed

	apiHash = os.Getenv("TELECLI_TDLIB_API_HASH")
	if apiHash == "" {
		t.Fatal("TELECLI_TDLIB_API_HASH is required")
	}

	phone = os.Getenv("TELECLI_TDLIB_PHONE")
	if phone == "" {
		t.Fatal("TELECLI_TDLIB_PHONE is required")
	}

	password = os.Getenv("TELECLI_TDLIB_PASSWORD")
	return apiID, apiHash, phone, password, libraryPath
}

func TestAuthorizedAccountSessionManual(t *testing.T) {
	apiID, apiHash, phone, password, libraryPath := requireAccountIntegrationEnv(t)

	dir := t.TempDir()
	params := TdlibParameters{
		APIID:              apiID,
		APIHash:            apiHash,
		DatabaseDirectory:  filepath.Join(dir, "database"),
		FilesDirectory:     filepath.Join(dir, "files"),
		SystemLanguageCode: "en",
		DeviceModel:        "telecli-account-manual",
		ApplicationVersion: "test",
	}
	if err := os.MkdirAll(params.DatabaseDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(params.FilesDirectory, 0o700); err != nil {
		t.Fatal(err)
	}

	native, err := LoadNative(libraryPath)
	if err != nil {
		t.Fatalf("LoadNative: %v", err)
	}

	cfg := DefaultConfig()
	cfg.ReceiveTimeout = 100 * time.Millisecond
	cfg.ShutdownTimeout = 15 * time.Second
	cfg.UpdateBuffer = 4096

	rt, err := NewRuntime(cfg, native, recorder.NewNoop())
	if err != nil {
		_ = native.Close()
		t.Fatalf("NewRuntime: %v", err)
	}

	// Ensure the runtime is stopped on any fatal path. The native
	// handle is owned by the runtime from this point on.
	t.Cleanup(func() {
		if rt.State() == LifecycleClosed {
			return
		}
		closeContext, cancel := context.WithTimeout(
			context.Background(),
			cfg.ShutdownTimeout,
		)
		defer cancel()
		_ = rt.Close(closeContext)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	provider := &consoleProvider{
		phone:    phone,
		password: password,
		input:    bufio.NewReader(os.Stdin),
		output:   os.Stdout,
	}

	session, err := Authorize(ctx, rt, params, provider)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}

	if session.ClientID() <= 0 {
		t.Fatalf("ClientID = %d, want > 0", session.ClientID())
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelClose()

	if err := session.Close(closeCtx); err != nil {
		t.Fatalf("session.Close: %v", err)
	}

	if got := rt.State(); got != LifecycleClosed {
		t.Fatalf("runtime state = %s, want closed", got)
	}
}
