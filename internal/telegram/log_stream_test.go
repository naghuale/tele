package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"telecli/internal/telemetry/recorder"
)

// The library's own journal is a door of its own, and the defect this
// replaces was that door standing open onto the terminal the interface
// owns: a line about a poll appeared over a running interface on 03.10
// (owner, task 5, review-tele5, SHA 6a77c6e). So the door is closed
// before anything goes through it, and these tests hold it closed.

// journalPath is the file a test points the library's journal at.
func journalPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "telecli-journal", "tdlib.log")
}

// startedRuntimeWithJournal starts a runtime whose journal is named, and
// closes it when the test ends.
func startedRuntimeWithJournal(
	t *testing.T,
	native Native,
	cfg Config,
) *Runtime {
	t.Helper()

	cfg.ReceiveTimeout = time.Millisecond
	runtime, err := NewRuntime(cfg, native, recorder.NewNoop())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	return runtime
}

// defaultJournalConfig is the configuration of a runtime that writes its
// journal where the test says.
func defaultJournalConfig(path string) Config {
	cfg := DefaultConfig()
	cfg.LogFilePath = path

	return cfg
}

// logStreamRequest decodes the setLogStream this runtime sent.
func logStreamRequest(t *testing.T, native *recordingNative) map[string]any {
	t.Helper()

	raw := native.sentRequestsOfType("setLogStream")
	if len(raw) == 0 {
		t.Fatal("setLogStream was never executed")
	}
	if len(raw) != 1 {
		t.Fatalf("setLogStream calls = %d, want 1", len(raw))
	}

	var request struct {
		Type      string         `json:"@type"`
		LogStream map[string]any `json:"log_stream"`
	}
	if err := json.Unmarshal(raw[0], &request); err != nil {
		t.Fatalf("decode setLogStream: %v", err)
	}
	if request.Type != "setLogStream" {
		t.Fatalf("@type = %q, want setLogStream", request.Type)
	}

	return request.LogStream
}

// TestTheLibraryJournalIsNamedBeforeTheFirstRequest pins the order that
// keeps the library's lines off the terminal: the journal is named and
// the verbosity lowered before the first logical client and before the
// first request of the program. td_receive itself writes log lines at
// TDLib's default verbosity, so a receive before that is a line over a
// running interface.
func TestTheLibraryJournalIsNamedBeforeTheFirstRequest(t *testing.T) {
	native := newRecordingNative()
	scriptHappyPath(native, false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runtime := startedRuntimeWithJournal(
		t,
		native,
		defaultJournalConfig(journalPath(t)),
	)

	if _, err := Authorize(
		ctx,
		runtime,
		validAuthParams(),
		&fakeProvider{
			phone:    "+10000000000",
			code:     "12345",
			password: "pw",
		},
	); err != nil {
		t.Fatalf(
			"Authorize: %v; events = %v",
			err,
			native.recordedEvents(),
		)
	}

	events := native.recordedEvents()
	streamAt := indexOfEvent(events, "execute:setLogStream")
	verbosityAt := indexOfEvent(events, "execute:setLogVerbosityLevel")
	clientAt := indexOfEvent(events, "create_client_id")
	stateAt := indexOfEvent(events, "send:getAuthorizationState")
	paramsAt := indexOfEvent(events, "send:setTdlibParameters")

	if streamAt < 0 || verbosityAt < 0 {
		t.Fatalf(
			"the library's journal was not configured; events = %v",
			events,
		)
	}
	if clientAt < 0 || stateAt < 0 || paramsAt < 0 {
		t.Fatalf(
			"missing startup events (client=%d state=%d params=%d); events = %v",
			clientAt, stateAt, paramsAt, events,
		)
	}
	if !(streamAt < clientAt &&
		clientAt < stateAt &&
		stateAt < paramsAt) {
		t.Fatalf(
			"wrong order: stream=%d client=%d state=%d params=%d; events = %v",
			streamAt, clientAt, stateAt, paramsAt, events,
		)
	}
	if verbosityAt >= clientAt {
		t.Fatalf(
			"the verbosity was lowered after the client existed "+
				"(stream=%d verbosity=%d client=%d); events = %v",
			streamAt, verbosityAt, clientAt, events,
		)
	}
}

// TestTheLibraryJournalNamesTheFileAndItsRotation pins the payload TDLib
// is given: the file this program named, rotated at the same size as the
// interface's own reasons, and without the process's standard error
// dragged into a journal nobody will read.
func TestTheLibraryJournalNamesTheFileAndItsRotation(t *testing.T) {
	native := newRecordingNative()
	path := journalPath(t)

	startedRuntimeWithJournal(t, native, defaultJournalConfig(path))

	stream := logStreamRequest(t, native)
	if stream["@type"] != "logStreamFile" {
		t.Fatalf("@type = %v, want logStreamFile", stream["@type"])
	}
	if stream["path"] != path {
		t.Fatalf("path = %v, want %s", stream["path"], path)
	}
	if got, want := stream["max_file_size"], float64(TDLibLogMaxBytes); got != want {
		t.Fatalf("max_file_size = %v, want %v", got, want)
	}
	if got, want := stream["redirect_stderr"], false; got != want {
		t.Fatalf("redirect_stderr = %v, want %v", got, want)
	}
}

// TestTheJournalFileIsCreatedWithPrivateRightsBeforeTDLibWritesToIt
// pins that the file exists, and is this program's to read only, by the
// time the library is told to write to it. A file the library creates
// itself takes the library's permissions, and the journal is telecli's own
// record of what it did with a Telegram account.
func TestTheJournalFileIsCreatedWithPrivateRightsBeforeTDLibWritesToIt(
	t *testing.T,
) {
	path := journalPath(t)
	inner := newRecordingNative()
	native := &journalWatchingNative{recordingNative: inner, path: path}

	startedRuntimeWithJournal(t, native, defaultJournalConfig(path))

	if !native.sawFileAtStream {
		t.Fatal("the journal file did not exist when setLogStream went out")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat the journal file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("journal file mode = %#o, want 0600", got)
	}

	if got := parentMode(t, path); got != 0o700 {
		t.Fatalf("journal folder mode = %#o, want 0700", got)
	}
}

// journalWatchingNative answers the log requests like TDLib and remembers
// whether the journal file was already there when it was asked for.
type journalWatchingNative struct {
	*recordingNative
	path string

	sawFileAtStream bool
}

func (n *journalWatchingNative) Execute(request []byte) ([]byte, error) {
	if requestType(request) == "setLogStream" {
		_, err := os.Stat(n.path)
		n.sawFileAtStream = err == nil
	}

	return n.recordingNative.Execute(request)
}

// parentMode is the permissions of the folder holding the journal.
func parentMode(t *testing.T, path string) os.FileMode {
	t.Helper()

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat the journal folder: %v", err)
	}

	return info.Mode().Perm()
}

// TestWithoutAJournalFileTheLibraryIsToldToWriteNowhere pins the answer a
// program with no data folder gives. "Nowhere" is not a missing line and
// not a fallback to the terminal: the journal is dropped, because the
// interface owns the terminal and there is no folder to write to.
func TestWithoutAJournalFileTheLibraryIsToldToWriteNowhere(t *testing.T) {
	native := newRecordingNative()

	cfg := DefaultConfig()
	cfg.ReceiveTimeout = time.Millisecond
	runtime, err := NewRuntime(cfg, native, recorder.NewNoop())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	stream := logStreamRequest(t, native)
	if stream["@type"] != "logStreamEmpty" {
		t.Fatalf("@type = %v, want logStreamEmpty", stream["@type"])
	}
	if len(stream) != 1 {
		t.Fatalf(
			"logStreamEmpty carries fields it does not have: %v", stream,
		)
	}
}

// TestStartupFailsClosedWhenTheLibraryRefusesTheJournal pins that a
// library whose journal could not be moved stops the program. A refusal
// means the library keeps writing to the terminal the interface owns,
// which is the defect this replaces.
func TestStartupFailsClosedWhenTheLibraryRefusesTheJournal(t *testing.T) {
	inner := newRecordingNative()
	native := &refusingLogStreamNative{
		recordingNative: inner,
		response:        []byte(`{"@type":"error","code":400,"message":"no"}`),
	}

	cfg := defaultJournalConfig(journalPath(t))
	cfg.ReceiveTimeout = time.Millisecond
	runtime, err := NewRuntime(cfg, native, recorder.NewNoop())
	if err != nil {
		t.Fatal(err)
	}

	if err := runtime.Start(context.Background()); !errors.Is(
		err,
		ErrTDLibLogConfiguration,
	) {
		t.Fatalf("Start error = %v, want ErrTDLibLogConfiguration", err)
	}
	time.Sleep(20 * time.Millisecond)

	if state := runtime.State(); state == LifecycleRunning {
		t.Fatal("the runtime runs although the journal could not be named")
	}
	for _, event := range inner.recordedEvents() {
		if strings.HasPrefix(event, "send:") {
			t.Fatalf(
				"%q went out although the journal could not be named; events = %v",
				event,
				inner.recordedEvents(),
			)
		}
	}

	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// refusingLogStreamNative refuses the journal request and answers every
// other one, so a test can tell which of the two log requests failed.
type refusingLogStreamNative struct {
	*recordingNative
	response []byte
}

func (n *refusingLogStreamNative) Execute(request []byte) ([]byte, error) {
	if requestType(request) == "setLogStream" {
		n.record("execute:setLogStream", request)

		return append([]byte(nil), n.response...), nil
	}

	return n.recordingNative.Execute(request)
}

// TestTheConfiguredVerbosityIsTheOneSent pins that the level in the
// configuration is the level TDLib is asked for, and not a constant of
// the binding.
func TestTheConfiguredVerbosityIsTheOneSent(t *testing.T) {
	for _, level := range []int{0, MaxLogVerbosity} {
		native := newRecordingNative()
		cfg := defaultJournalConfig(journalPath(t))
		cfg.LogVerbosity = level

		startedRuntimeWithJournal(t, native, cfg)

		sent := verbosityLevel(t, native)
		if sent != level {
			t.Fatalf("new_verbosity_level = %d, want %d", sent, level)
		}
	}
}

// TestAVerbosityThatWouldDumpRequestsIsRefused pins the ceiling. From
// level 2 up the library writes the requests it receives into its journal,
// and a request carries the api_hash of setTdlibParameters and the text of
// every message: a configuration that asked for that would put both into
// a file on the user's disk.
func TestAVerbosityThatWouldDumpRequestsIsRefused(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogVerbosity = MaxLogVerbosity + 1

	if err := cfg.Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Config.Validate error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewRuntime(cfg, newRecordingNative(), recorder.NewNoop()); !errors.Is(
		err,
		ErrInvalidConfig,
	) {
		t.Fatalf("NewRuntime error = %v, want ErrInvalidConfig", err)
	}
}

// TestTheJournalNamesAreTheOnesThePinnedSchemaDeclares pins the request
// against the scheme of the pinned TDLib rather than against memory: the
// class, and every field of it, is read out of testdata/td_api.tl.
//
// A name this schema does not have is a request TDLib throws away without
// saying so, and a field it does not have is a value it ignores — the
// journal would then stay on the terminal with the program believing it
// had moved it.
func TestTheJournalNamesAreTheOnesThePinnedSchemaDeclares(t *testing.T) {
	declared := schemaFields(t, "setLogStream")
	if want := []string{"log_stream"}; !sameFields(declared, want) {
		t.Fatalf(
			"the pinned schema declares setLogStream %v, want %v",
			declared, want,
		)
	}
	// The request this package builds must name exactly the fields the
	// schema declares: one more is a field TDLib ignores, one less is a
	// request it cannot read.
	native := newRecordingNative()
	startedRuntimeWithJournal(t, native, defaultJournalConfig(journalPath(t)))

	var request map[string]any
	for _, raw := range native.sentRequestsOfType("setLogStream") {
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatalf("decode setLogStream: %v", err)
		}
	}
	// @type is how the JSON interface names the class, and the TL scheme
	// has no such field: the class name is the declaration itself.
	delete(request, "@type")
	if got := keysOf(request); !sameFields(got, declared) {
		t.Fatalf(
			"setLogStream carries %v, the pinned schema declares %v",
			got, declared,
		)
	}

	fileFields := schemaFields(t, "logStreamFile")
	if want := []string{"path", "max_file_size", "redirect_stderr"}; !sameFields(
		fileFields,
		want,
	) {
		t.Fatalf(
			"the pinned schema declares logStreamFile %v, want %v",
			fileFields, want,
		)
	}
	stream := logStreamRequest(t, native)
	carried := make([]string, 0, len(stream))
	for key := range stream {
		if key == "@type" {
			continue
		}
		carried = append(carried, key)
	}
	if !sameFields(carried, fileFields) {
		t.Fatalf(
			"logStreamFile carries %v, the pinned schema declares %v",
			carried, fileFields,
		)
	}
	if !names(t)["logStreamFile"] || !names(t)["logStreamEmpty"] {
		t.Fatal("the pinned schema has no logStreamFile or logStreamEmpty")
	}
}

// names is the set of class names of the pinned schema.
func names(t *testing.T) map[string]bool {
	t.Helper()

	return tdlibNames(t)
}

// schemaFields reads the fields a class of the pinned schema declares,
// the way the TL scheme writes them: `name:Type` between the class name and
// the `= Type;` it returns.
func schemaFields(t *testing.T, class string) []string {
	t.Helper()

	prefix := class + " "
	for _, line := range readSchemaLines(t) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}

		head, _, found := strings.Cut(line, " = ")
		if !found {
			continue
		}

		var fields []string
		for _, field := range strings.Fields(head) {
			name, _, isField := strings.Cut(field, ":")
			if !isField {
				continue
			}
			fields = append(fields, name)
		}

		return fields
	}

	t.Fatalf("the pinned schema has no %s", class)

	return nil
}

// keysOf returns the keys of a decoded request.
func keysOf(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

// sameFields compares two field lists without regard to order.
func sameFields(got, want []string) bool {
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)

	other := append([]string(nil), want...)
	sort.Strings(other)

	if len(sorted) != len(other) {
		return false
	}
	for index := range sorted {
		if sorted[index] != other[index] {
			return false
		}
	}

	return true
}

// verbosityLevel is the level of the single setLogVerbosityLevel request
// the runtime sent.
func verbosityLevel(t *testing.T, native *recordingNative) int {
	t.Helper()

	raw := native.sentRequestsOfType("setLogVerbosityLevel")
	if len(raw) != 1 {
		t.Fatalf("setLogVerbosityLevel calls = %d, want 1", len(raw))
	}
	var payload struct {
		Level int `json:"new_verbosity_level"`
	}
	if err := json.Unmarshal(raw[0], &payload); err != nil {
		t.Fatalf("decode setLogVerbosityLevel: %v", err)
	}

	return payload.Level
}
