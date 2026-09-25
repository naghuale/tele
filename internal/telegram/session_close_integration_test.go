//go:build tdlib_integration && cgo && (darwin || linux)

package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"telecli/internal/telemetry/recorder"
)

// Automated integration test for the runtime close sequence.
//
// Required environment:
//
//	TELECLI_TDLIB_INTEGRATION  must equal "1"
//	TELECLI_TDLIB_LIBRARY      absolute path to libtdjson
//	TELECLI_TDLIB_API_ID       positive integer (application credentials)
//	TELECLI_TDLIB_API_HASH     string (application credentials)
//
// Application credentials are required so that TDLib accepts
// setTdlibParameters and advances to authorizationStateWaitPhoneNumber.
//
// No user authentication is performed: no phone number, login code, or
// user password is needed. The test uses real application credentials
// and may interact with Telegram infrastructure.

func requireAutomatedIntegrationEnv(t *testing.T) (
	libraryPath string,
	apiID int,
	apiHash string,
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

	return libraryPath, apiID, apiHash
}

// setIntegrationLogVerbosity lowers TDLib's own log level so the
// integration output stays readable. Level 1 keeps errors only.
//
// setLogVerbosityLevel is synchronous and may be issued before
// initialization.
func setIntegrationLogVerbosity(t *testing.T, native Native) {
	t.Helper()

	response, err := native.Execute(
		RawMessage(`{"@type":"setLogVerbosityLevel","new_verbosity_level":1}`),
	)
	if err != nil {
		t.Fatalf("setLogVerbosityLevel: %v", err)
	}
	if len(response) == 0 {
		t.Fatal("setLogVerbosityLevel returned empty response")
	}
}

// sendGetAuthorizationState issues the first request for a logical
// client.
//
// A freshly created TDLib client does not spontaneously emit an
// initial updateAuthorizationState: td_receive returns empty until the
// client receives its first request. getAuthorizationState is an
// offline method that can be called before initialization and returns
// the current authorization state as a direct response object.
func sendGetAuthorizationState(
	t *testing.T,
	rt *Runtime,
	clientID int,
) {
	t.Helper()

	if err := rt.Send(
		clientID,
		RawMessage(`{"@type":"getAuthorizationState"}`),
	); err != nil {
		t.Fatalf("send getAuthorizationState: %v", err)
	}
}

// parseIntegrationAuthState recognizes both the direct response format
// of getAuthorizationState and the update format of
// updateAuthorizationState.
//
//   - {"@type":"authorizationStateX"} → parsed as AuthStateX
//   - {"@type":"updateAuthorizationState","authorization_state":{"@type":"authorizationStateX"}} → parsed as AuthStateX
//   - any other object → (AuthStateUnknown, nil)
func parseIntegrationAuthState(raw RawMessage) (AuthState, error) {
	if state, err := ParseAuthUpdate(raw); err != nil {
		return AuthStateUnknown, err
	} else if state != AuthStateUnknown {
		return state, nil
	}

	var envelope struct {
		Type string `json:"@type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return AuthStateUnknown, fmt.Errorf(
			"decode authorization state response: %w", err,
		)
	}
	if state, exists := tdlibAuthStateTypes[envelope.Type]; exists {
		return state, nil
	}
	return AuthStateUnknown, nil
}

// waitForAuthState drains client.Updates() and client.Errors() until
// the requested auth state arrives or the timeout fires.
//
// A native runtime error aborts the wait immediately instead of being
// masked by the timeout. A closed error channel disables that branch
// and the loop continues reading updates.
func waitForAuthState(
	t *testing.T,
	client *Client,
	want AuthState,
	timeout time.Duration,
) {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	updates := client.Updates()
	runtimeErrors := client.Errors()

	for {
		select {
		case runtimeErr, ok := <-runtimeErrors:
			if !ok {
				runtimeErrors = nil
				continue
			}
			if runtimeErr != nil {
				t.Fatalf(
					"runtime error while waiting for %s: %v",
					want,
					runtimeErr,
				)
			}

		case update, ok := <-updates:
			if !ok {
				t.Fatalf(
					"update channel closed while waiting for %s",
					want,
				)
			}

			state, err := parseIntegrationAuthState(update.Raw)
			if err != nil {
				t.Fatalf(
					"parse authorization message while waiting for %s: %v",
					want,
					err,
				)
			}
			if state == AuthStateUnknown {
				continue
			}
			if state == want {
				return
			}

		case <-timer.C:
			t.Fatalf(
				"timed out waiting for auth state %s",
				want,
			)
		}
	}
}

// waitForCloseStates reads client.Updates() and client.Errors() until
// authorizationStateClosed is observed.
//
// It records whether authorizationStateClosing was seen along the way
// so the caller can log it as a diagnostic fact for the pinned
// runtime.
//
// A single consumer loop is required: a separate wait for Closing
// followed by a separate wait for Closed can miss Closed when it
// arrives while the first helper is still draining unrelated updates.
//
// Returns true if authorizationStateClosing was observed before
// authorizationStateClosed. A false return is not a failure: only
// Closed is the required terminal state for graceful close.
//
// A native runtime error aborts the wait immediately. A closed error
// channel disables that branch and the loop continues reading updates.
func waitForCloseStates(
	t *testing.T,
	client *Client,
	timeout time.Duration,
) bool {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	updates := client.Updates()
	runtimeErrors := client.Errors()
	sawClosing := false

	for {
		select {
		case runtimeErr, ok := <-runtimeErrors:
			if !ok {
				runtimeErrors = nil
				continue
			}
			if runtimeErr != nil {
				t.Fatalf(
					"runtime error while waiting for "+
						"authorizationStateClosed: %v",
					runtimeErr,
				)
			}

		case update, ok := <-updates:
			if !ok {
				t.Fatal(
					"update channel closed before " +
						"authorizationStateClosed",
				)
			}

			state, err := parseIntegrationAuthState(update.Raw)
			if err != nil {
				t.Fatalf(
					"parse authorization message: %v",
					err,
				)
			}

			switch state {
			case AuthStateUnknown:
				continue

			case AuthStateClosing:
				sawClosing = true

			case AuthStateClosed:
				return sawClosing
			}

		case <-timer.C:
			t.Fatal(
				"timed out waiting for " +
					"authorizationStateClosed",
			)
		}
	}
}

// sendSetTdlibParameters sends initialization parameters using the
// given directories and application credentials.
//
// TDLib opens the local database and advances to
// authorizationStateWaitPhoneNumber. The request may interact with
// Telegram infrastructure; the test does not assume otherwise.
func sendSetTdlibParameters(
	t *testing.T,
	rt *Runtime,
	clientID int,
	databaseDir string,
	filesDir string,
	apiID int,
	apiHash string,
) {
	t.Helper()

	request := map[string]any{
		"@type":                   "setTdlibParameters",
		"use_test_dc":             false,
		"database_directory":      databaseDir,
		"files_directory":         filesDir,
		"database_encryption_key": "",
		"use_file_database":       true,
		"use_chat_info_database":  true,
		"use_message_database":    true,
		"use_secret_chats":        false,
		"api_id":                  apiID,
		"api_hash":                apiHash,
		"system_language_code":    "en",
		"device_model":            "telecli-integration",
		"system_version":          "test",
		"application_version":     "test",
	}

	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal setTdlibParameters: %v", err)
	}
	if err := rt.Send(clientID, raw); err != nil {
		t.Fatalf("send setTdlibParameters: %v", err)
	}
}

func sendRawClose(t *testing.T, rt *Runtime, clientID int) {
	t.Helper()
	raw, err := buildCloseRequest()
	if err != nil {
		t.Fatalf("buildCloseRequest: %v", err)
	}
	if err := rt.Send(clientID, raw); err != nil {
		t.Fatalf("send close: %v", err)
	}
}

func TestCloseBeforeAuthorizationIntegration(t *testing.T) {
	libraryPath, apiID, apiHash := requireAutomatedIntegrationEnv(t)

	dir := t.TempDir()
	databaseDir := filepath.Join(dir, "database")
	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(databaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		t.Fatal(err)
	}

	runOnce := func(label string) {
		native, err := LoadNative(libraryPath)
		if err != nil {
			t.Fatalf("%s: LoadNative: %v", label, err)
		}

		setIntegrationLogVerbosity(t, native)

		cfg := DefaultConfig()
		cfg.ReceiveTimeout = 100 * time.Millisecond
		cfg.ShutdownTimeout = 10 * time.Second
		cfg.UpdateBuffer = 1024

		rt, err := NewRuntime(cfg, native, recorder.NewNoop())
		if err != nil {
			_ = native.Close()
			t.Fatalf("%s: NewRuntime: %v", label, err)
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

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		if err := rt.Start(ctx); err != nil {
			t.Fatalf("%s: Start: %v", label, err)
		}

		client, err := rt.NewClient()
		if err != nil {
			t.Fatalf("%s: NewClient: %v", label, err)
		}

		// A freshly created client does not emit updates until its
		// first request. getAuthorizationState is offline and returns
		// the current state as a direct response.
		sendGetAuthorizationState(t, rt, client.ID())
		waitForAuthState(t, client, AuthStateWaitTdlibParameters, 20*time.Second)

		sendSetTdlibParameters(
			t,
			rt,
			client.ID(),
			databaseDir,
			filesDir,
			apiID,
			apiHash,
		)

		// WaitPhoneNumber proves TDLib accepted the initialization
		// parameters and opened the local database.
		waitForAuthState(t, client, AuthStateWaitPhoneNumber, 30*time.Second)

		sendRawClose(t, rt, client.ID())

		sawClosing := waitForCloseStates(t, client, 30*time.Second)
		t.Logf("%s: authorizationStateClosing observed: %t", label, sawClosing)

		if err := rt.Close(context.Background()); err != nil {
			t.Fatalf("%s: Runtime.Close: %v", label, err)
		}
		if got := rt.State(); got != LifecycleClosed {
			t.Fatalf("%s: runtime state = %s, want closed", label, got)
		}
	}

	// First run: initialize the database, close cleanly.
	runOnce("first")

	// Second run: initialize the same database directory. Success
	// proves TDLib accepted the directory again after the prior close.
	runOnce("second")
}

// ---- parser unit tests (still under the integration tag) ----

func TestParseIntegrationAuthStateDirectResponse(t *testing.T) {
	raw := RawMessage(`{"@type":"authorizationStateWaitTdlibParameters"}`)

	state, err := parseIntegrationAuthState(raw)
	if err != nil {
		t.Fatalf("parseIntegrationAuthState() error = %v", err)
	}
	if state != AuthStateWaitTdlibParameters {
		t.Fatalf(
			"state = %s, want %s",
			state,
			AuthStateWaitTdlibParameters,
		)
	}
}

func TestParseIntegrationAuthStateUpdate(t *testing.T) {
	raw := RawMessage(
		`{"@type":"updateAuthorizationState",` +
			`"authorization_state":{` +
			`"@type":"authorizationStateClosed"}}`,
	)

	state, err := parseIntegrationAuthState(raw)
	if err != nil {
		t.Fatalf("parseIntegrationAuthState() error = %v", err)
	}
	if state != AuthStateClosed {
		t.Fatalf("state = %s, want %s", state, AuthStateClosed)
	}
}

func TestParseIntegrationAuthStateUnknownObject(t *testing.T) {
	raw := RawMessage(`{"@type":"updateNewChat"}`)

	state, err := parseIntegrationAuthState(raw)
	if err != nil {
		t.Fatalf("parseIntegrationAuthState() error = %v", err)
	}
	if state != AuthStateUnknown {
		t.Fatalf("state = %s, want %s", state, AuthStateUnknown)
	}
}
