//go:build tdlib_send_integration && cgo && (darwin || linux)

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

// Manual integration test that sends a real Telegram message.
//
// This test produces an external side effect: it writes a message into
// a real chat. It is guarded by TELECLI_TDLIB_SEND_CONFIRM=1 and is
// built only under the tdlib_send_integration tag. Do not combine this
// tag with tdlib_account_integration.
//
// Run explicitly:
//
//	go test \
//	  -tags tdlib_send_integration \
//	  ./internal/telegram/... \
//	  -run '^TestSendTextMessageAccountManual$' \
//	  -count=1 -v -timeout=6m
//
// Required environment:
//
//	TELECLI_TDLIB_INTEGRATION      must equal "1"
//	TELECLI_TDLIB_SEND_CONFIRM     must equal "1"
//	TELECLI_TDLIB_LIBRARY          absolute path to libtdjson
//	TELECLI_TDLIB_API_ID           positive integer
//	TELECLI_TDLIB_API_HASH         string
//	TELECLI_TDLIB_PHONE            phone number, e.g. +15551234567
//	TELECLI_TDLIB_SEND_CHAT_ID     numeric chat ID to send into
//	TELECLI_TDLIB_SEND_TEXT        non-empty text to send
//
// Optional:
//
//	TELECLI_TDLIB_PASSWORD         2FA password, required if enabled
//
// The authentication code is read from /dev/tty so the prompt works
// under both go test and a direct test-binary run.
//
// Diagnostic step before sending:
//
// The test logs the size of the first GetChats page and whether the
// requested chat ID appears in it. Absence from that page does not
// prove the chat is unreachable: TDLib's list can be paged, filtered,
// or ordered differently. The send attempt is therefore always made,
// regardless of the diagnostic result.
//
// The diagnostic deliberately does not log chat titles, last message
// previews, or chat IDs other than the target, to keep personal data
// out of terminal output and CI artifacts.
//
// What this test verifies on success:
//
//   - sendMessage is accepted by TDLib and returns a message object
//     with non-zero id, matching chat_id, and is_outgoing == true;
//   - the session closes gracefully: it observes
//     authorizationStateClosed before Runtime.Close;
//   - Runtime.Close invokes the native loader close path and the
//     runtime state becomes LifecycleClosed.
//
// What this test does NOT verify:
//
//   - final delivery on the server or to the recipient;
//   - delivery-state transitions (updateMessageSendSucceeded,
//     updateMessageSendFailed);
//   - a specific physical dlclose of the native handle, which is not
//     instrumented here.

type sendConsoleProvider struct {
	phone    string
	password string
	input    *bufio.Reader
	output   *os.File
}

func (p *sendConsoleProvider) ProvidePhoneNumber(context.Context) (string, error) {
	return p.phone, nil
}

func (p *sendConsoleProvider) ProvideCode(context.Context) (string, error) {
	fmt.Fprint(p.output, "Telegram code: ")
	line, err := p.input.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read Telegram code: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func (p *sendConsoleProvider) ProvidePassword(context.Context) (string, error) {
	if p.password == "" {
		return "", fmt.Errorf(
			"%w: TELECLI_TDLIB_PASSWORD is required for 2FA",
			ErrInvalidAuthParameters,
		)
	}
	return p.password, nil
}

// Compile-time assertion.
var _ AuthProvider = (*sendConsoleProvider)(nil)

func requireSendIntegrationEnv(t *testing.T) (
	apiID int,
	apiHash string,
	phone string,
	password string,
	libraryPath string,
	chatID ChatID,
	text string,
) {
	t.Helper()

	if os.Getenv("TELECLI_TDLIB_INTEGRATION") != "1" {
		t.Skip("TELECLI_TDLIB_INTEGRATION != 1")
	}

	if os.Getenv("TELECLI_TDLIB_SEND_CONFIRM") != "1" {
		t.Skip("TELECLI_TDLIB_SEND_CONFIRM != 1; this test sends a real message")
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

	chatIDValue := os.Getenv("TELECLI_TDLIB_SEND_CHAT_ID")
	if chatIDValue == "" {
		t.Fatal("TELECLI_TDLIB_SEND_CHAT_ID is required")
	}
	chatIDInt, err := strconv.ParseInt(chatIDValue, 10, 64)
	if err != nil {
		t.Fatalf("TELECLI_TDLIB_SEND_CHAT_ID invalid: %q", chatIDValue)
	}
	if chatIDInt == 0 {
		t.Fatal("TELECLI_TDLIB_SEND_CHAT_ID must not be zero")
	}
	chatID = ChatID(chatIDInt)

	text = os.Getenv("TELECLI_TDLIB_SEND_TEXT")
	if strings.TrimSpace(text) == "" {
		t.Fatal("TELECLI_TDLIB_SEND_TEXT must contain non-whitespace characters")
	}

	return apiID, apiHash, phone, password, libraryPath, chatID, text
}

// listAvailableChats logs the first GetChats page for diagnostics.
//
// Privacy: only the page size and whether the target chat ID is
// present are logged. Chat titles, last message previews, and other
// chat IDs are not printed.
//
// Semantics: absence of the target ID from the page is not a failure.
// The chat list may be paged, filtered, or ordered differently, so the
// caller always proceeds to sendMessage.
func listAvailableChats(
	t *testing.T,
	ctx context.Context,
	session *AuthorizedSession,
	want ChatID,
) {
	t.Helper()

	snapshot, err := session.GetChats(ctx, 100)
	if err != nil {
		t.Logf("GetChats before send failed: %v", err)
		return
	}

	found := false
	for _, chat := range snapshot.Chats {
		if chat.ID == want {
			found = true
			break
		}
	}

	t.Logf(
		"chat snapshot contains %d chats; target chat_id present: %t",
		len(snapshot.Chats),
		found,
	)

	if !found {
		t.Logf(
			"chat_id=%d was not present in the first %d chats; "+
				"attempting sendMessage directly",
			want,
			len(snapshot.Chats),
		)
	}
}

func TestSendTextMessageAccountManual(t *testing.T) {
	apiID, apiHash, phone, password, libraryPath, chatID, text :=
		requireSendIntegrationEnv(t)

	dir := t.TempDir()
	params := TdlibParameters{
		APIID:              apiID,
		APIHash:            apiHash,
		DatabaseDirectory:  filepath.Join(dir, "database"),
		FilesDirectory:     filepath.Join(dir, "files"),
		SystemLanguageCode: "en",
		DeviceModel:        "telecli-send-manual",
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

	// Until NewRuntime succeeds, this test owns the native handle.
	runtimeOwnsNative := false

	t.Cleanup(func() {
		if !runtimeOwnsNative {
			_ = native.Close()
		}
	})

	// Reduce TDLib log noise; level 1 keeps errors only.
	if _, err := native.Execute(
		RawMessage(
			`{"@type":"setLogVerbosityLevel",` +
				`"new_verbosity_level":1}`,
		),
	); err != nil {
		t.Fatalf("setLogVerbosityLevel: %v", err)
	}

	cfg := DefaultConfig()
	cfg.ReceiveTimeout = 100 * time.Millisecond
	cfg.ShutdownTimeout = 30 * time.Second
	cfg.UpdateBuffer = 4096

	rt, err := NewRuntime(cfg, native, recorder.NewNoop())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	runtimeOwnsNative = true

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

	// go test does not attach os.Stdin to the terminal. Open /dev/tty
	// directly so the code prompt works under both go test and a
	// direct test-binary run.
	terminal, err := os.Open("/dev/tty")
	if err != nil {
		t.Fatalf("open /dev/tty: %v", err)
	}
	defer terminal.Close()

	provider := &sendConsoleProvider{
		phone:    phone,
		password: password,
		input:    bufio.NewReader(terminal),
		output:   os.Stdout,
	}

	session, err := Authorize(ctx, rt, params, provider)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if session.ClientID() <= 0 {
		t.Fatalf("ClientID = %d, want > 0", session.ClientID())
	}

	sessionClosed := false

	t.Cleanup(func() {
		if sessionClosed {
			return
		}
		closeContext, cancel := context.WithTimeout(
			context.Background(),
			cfg.ShutdownTimeout,
		)
		defer cancel()
		_ = session.Close(closeContext)
	})

	// Diagnostics only; the send is always attempted.
	listCtx, cancelList := context.WithTimeout(
		context.Background(),
		60*time.Second,
	)
	defer cancelList()

	listAvailableChats(t, listCtx, session, chatID)

	t.Logf(
		"sending to chat_id=%d, text_length=%d",
		chatID,
		len([]rune(text)),
	)

	sendCtx, cancelSend := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelSend()

	message, err := session.SendTextMessage(sendCtx, chatID, text)
	if err != nil {
		t.Fatalf("SendTextMessage: %v", err)
	}

	if message.ID == 0 {
		t.Fatal("message.ID = 0, want non-zero TDLib message id")
	}
	if message.ChatID != chatID {
		t.Fatalf("message.ChatID = %d, want %d", message.ChatID, chatID)
	}
	if !message.Outgoing {
		t.Fatal("message.Outgoing = false, want true for a sent message")
	}
	if message.Text != text {
		t.Fatalf("message.Text = %q, want %q", message.Text, text)
	}
	if message.Timestamp.IsZero() {
		t.Fatal("message.Timestamp is zero")
	}

	t.Logf(
		"sendMessage returned message id=%d chat_id=%d date=%s",
		message.ID,
		message.ChatID,
		message.Timestamp.Format(time.RFC3339),
	)

	closeContext, cancelClose := context.WithTimeout(
		context.Background(),
		cfg.ShutdownTimeout,
	)
	defer cancelClose()

	if err := session.Close(closeContext); err != nil {
		t.Fatalf("session.Close: %v", err)
	}
	sessionClosed = true

	if got := rt.State(); got != LifecycleClosed {
		t.Fatalf("runtime state = %s, want closed", got)
	}
}
