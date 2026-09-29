package application

import (
	"context"
	stdLog "log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"telecli/internal/config"
	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// While the interface is on the screen, the terminal belongs to the
// renderer.
//
// The owner found this the hard way: statuses started resolving, and then
// every confirmed message shifted the screen down by a line. The cause was
// a log line written by a reconciler that was working perfectly — one line
// per confirmed message is not an error condition, it is the program doing
// its job. So this cannot wait for something to go wrong. It drives a send
// and its confirmation, which is the ordinary case, and checks that the
// ordinary case says nothing to the terminal.
//
// The renderer's own frames are what the terminal is for, and those are
// covered by the snapshot suite. What is checked here is everything the
// renderer does not own: the queue, the dispatcher, the reconciler and the
// counters, over the same wiring `telecli tui` builds.

// A send and its confirmation write nothing to the terminal, and the
// reason goes to the log file.
func TestASendAndItsConfirmationWriteNothingToTheTerminal(t *testing.T) {
	terminal := captureTerminal(t)

	// The installation the composition root does for `telecli tui`.
	dataDir := terminalDataDir(t)
	uiLog := openUILog(dataDir, time.Now)
	t.Cleanup(func() { _ = uiLog.Close() })
	installUILog(uiLog)
	t.Cleanup(restoreProcessLog)

	path := newSendPathWithLogger(t, uiLog.Logger)

	// A message goes out and Telegram confirms it: the real dispatcher,
	// the real reconciler, the real store.
	entered, release := path.sender.hold(-1000000042)
	if _, err := path.queue.QueueMessage(
		context.Background(), 7, "проверяю сборку",
	); err != nil {
		t.Fatalf("queue message: %v", err)
	}
	<-entered

	path.deliverConfirmation(501, -1000000042, 7)
	close(release)

	if _, err := path.waitForState(
		"e0", outbox.StateSent, 10*time.Second,
	); err != nil {
		t.Fatalf("the confirmation was not applied: %v", err)
	}

	stdout, stderr := terminal.text()
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf(
			"standard error received this while a message was being "+
				"confirmed:\n\n%s\n\nThe renderer owns that terminal. "+
				"Anything else written there shifts every row of the "+
				"running screen by one.",
			stderr,
		)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf(
			"standard output received this while a message was being "+
				"confirmed; the renderer is its only writer:\n\n%s",
			stdout,
		)
	}

	// And the reason really went to the file, so this is not passing
	// because the program said nothing at all. A guard that passes
	// because the component was quiet proves nothing about a component
	// that is not.
	written, err := os.ReadFile(filepath.Join(dataDir, TUILogFileName))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(written), "outbox send result applied") {
		t.Fatalf(
			"the log file has no line about the confirmation:\n%s",
			written,
		)
	}
}

// A component that was not given a logger is silent.
//
// This is the exact shape of the defect the owner reported. The durable
// runtime was built without a logger, the reconciler asked for the
// process default, and the process default writes to the terminal the
// interface owns — so every confirmed message shifted the screen. A
// component with no logger has to say nothing at all: quiet is a small
// loss, a broken screen is not a small one.
func TestAComponentWithNoLoggerSaysNothingToTheTerminal(t *testing.T) {
	terminal := captureTerminal(t)

	// The process default is a terminal, which is what it is in the
	// program that shipped this defect.
	//
	// It is set explicitly rather than left alone because slog's default
	// logger holds the stderr it was built with: replacing the os.Stderr
	// variable does not redirect it, so a test that only swapped the
	// variable would watch an empty pipe and pass over the very thing it
	// is here to catch.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	stdLog.SetOutput(os.Stderr)
	stdLog.SetFlags(0)
	t.Cleanup(restoreProcessLog)

	path := newSendPathWithLogger(t, nil)

	entered, release := path.sender.hold(-1000000042)
	if _, err := path.queue.QueueMessage(
		context.Background(), 7, "проверяю сборку",
	); err != nil {
		t.Fatalf("queue message: %v", err)
	}
	<-entered
	path.deliverConfirmation(501, -1000000042, 7)
	close(release)

	if _, err := path.waitForState(
		"e0", outbox.StateSent, 10*time.Second,
	); err != nil {
		t.Fatalf("the confirmation was not applied: %v", err)
	}

	stdout, stderr := terminal.text()
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf(
			"a component with no logger wrote to the terminal:\n\n%s\n\n"+
				"This is the defect the owner reported: the reconciler "+
				"asked for the process default, and the process default "+
				"is the terminal the interface is drawing on.",
			stderr,
		)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("standard output received this:\n\n%s", stdout)
	}
}

// The numbers `telecli doctor` prints are written to a file beside the
// queue, not to the terminal.
func TestTheSendResultCountersAreNotWrittenToTheTerminal(t *testing.T) {
	terminal := captureTerminal(t)

	dataDir := terminalDataDir(t)
	uiLog := openUILog(dataDir, time.Now)
	t.Cleanup(func() { _ = uiLog.Close() })
	installUILog(uiLog)
	t.Cleanup(restoreProcessLog)

	reconciler := &sendResultReconciler{
		counters: &sendResultCounters{},
		sink:     newSendResultCounterFile(dataDir),
	}
	reconciler.flushCounters()

	stdout, stderr := terminal.text()
	if strings.TrimSpace(stderr) != "" || strings.TrimSpace(stdout) != "" {
		t.Fatalf(
			"writing the counters touched the terminal "+
				"(stdout=%q stderr=%q)", stdout, stderr,
		)
	}

	report, ok := readSendResultCounters(dataDir)
	if !ok {
		t.Fatal("no counters were written for doctor to read")
	}
	if report.Seen != 0 || report.Matched != 0 {
		t.Fatalf("report = %+v, want the counters as they are", report)
	}
}

// A log that cannot be opened is discarded, and the interface still runs.
//
// A program that refuses to start because it could not write a
// diagnostic file has made its diagnostics a dependency of its job, and
// the one thing a user must never lose is the ability to send a message.
func TestAnUnopenableLogIsDiscardedAndTheInterfaceStillRuns(t *testing.T) {
	t.Parallel()

	// A path that is a file, so nothing can be created inside it.
	blocker := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	uiLog := openUILog(blocker, time.Now)
	t.Cleanup(func() { _ = uiLog.Close() })

	if uiLog.Path != "" {
		t.Fatalf(
			"path = %q, want the log to report that it is not written",
			uiLog.Path,
		)
	}
	if uiLog.Writer == nil || uiLog.Logger == nil {
		t.Fatal("a log that cannot be opened leaves nothing to write to")
	}

	// Writing to it is a no-op rather than a panic or a line on the
	// terminal.
	uiLog.Logger.Info("this must not reach a terminal")
	uiLog.Logger.Warn("nor this")
	if _, err := uiLog.Writer.Write([]byte("nor this\n")); err != nil {
		t.Fatalf("write to the discarded log: %v", err)
	}
}

// The file is 0600, and the previous one is kept beside it.
func TestTheLogFileIsPrivateAndRotated(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	rotating, err := openRotatingFile(
		filepath.Join(dataDir, TUILogFileName), 512,
	)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = rotating.Close() })

	info, err := os.Stat(rotating.path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf(
			"mode = %o, want 600: this is the program's own record of "+
				"what it did with an account", perm,
		)
	}

	for i := 0; i < 8; i++ {
		if _, err := rotating.Write(
			[]byte(strings.Repeat("x", 200)),
		); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	if _, err := os.Stat(rotating.path + ".1"); err != nil {
		t.Fatalf(
			"the previous log was not kept beside the current one: %v", err,
		)
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) > 2 {
		t.Fatalf(
			"the folder holds %d files: the log, one beside it, and "+
				"nothing else. A directory of logs nobody asked for is "+
				"its own problem.", len(entries),
		)
	}
}

// telecli doctor says where the log is, because a quiet log is what
// creates the question.
func TestDoctorSaysWhereTheLogIs(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	writeUILogPath(&out, config.Config{
		DataDir: "/tmp/example-telecli",
	})

	want := filepath.Join("/tmp/example-telecli", TUILogFileName)
	if !strings.Contains(out.String(), want) {
		t.Fatalf(
			"doctor printed %q, want the path %q: a user whose interface "+
				"runs quietly has to be able to find the file that does "+
				"not", out.String(), want,
		)
	}

	empty := &strings.Builder{}
	writeUILogPath(empty, config.Config{})
	if !strings.Contains(empty.String(), "none") {
		t.Fatalf(
			"doctor printed %q for a program with no data folder, want "+
				"it to say there is none", empty,
		)
	}
}

// capturedTerminal is the process's own output, replaced by pipes.
type capturedTerminal struct {
	mu     sync.Mutex
	stdout strings.Builder
	stderr strings.Builder

	stop func()
}

// captureTerminal replaces the process's standard output and standard
// error with pipes for the duration of a test.
//
// The process's own, not an injected writer: the point is to catch the
// component that reaches for a destination nobody handed it. An injected
// writer would prove the components use the writer they were given, and
// say nothing about the one that goes around it — which is the defect.
func captureTerminal(t *testing.T) *capturedTerminal {
	t.Helper()

	captured := &capturedTerminal{}

	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}

	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite

	// Each pipe is drained by its own goroutine, so a writer that fills
	// the buffer cannot deadlock whatever is under test.
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		captured.copy(&captured.stdout, outRead)
	}()
	go func() {
		defer readers.Done()
		captured.copy(&captured.stderr, errRead)
	}()

	captured.stop = func() {
		os.Stdout, os.Stderr = savedOut, savedErr
		_ = outWrite.Close()
		_ = errWrite.Close()
		readers.Wait()
		_ = outRead.Close()
		_ = errRead.Close()
	}
	t.Cleanup(captured.stop)

	return captured
}

// copy moves everything a pipe receives into a buffer.
//
// The process's log is redirected too, so the two do not race for the
// same text: a line that the log swallowed cannot also appear here, which
// would make the test fail for the wrong reason.
func (c *capturedTerminal) copy(
	into *strings.Builder, file *os.File,
) {
	buffer := make([]byte, 4096)
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			c.mu.Lock()
			into.Write(buffer[:n])
			c.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// settle is how long a quiet period means "nothing more is coming".
const settle = 50 * time.Millisecond

// text is what the terminal received, once the pipes have stopped.
//
// The copy out of a pipe runs in its own goroutine, so reading the buffer
// the instant a state changed would race it: a log line written before
// the state moved may not have been copied yet, and a test asserting that
// the terminal is empty would pass on a program that did write to it.
// So this waits for the writes to stop arriving rather than for a fixed
// time, which is both faster when nothing was written and correct when
// something was.
func (c *capturedTerminal) text() (stdout, stderr string) {
	deadline := time.Now().Add(3 * time.Second)
	previous := ""
	for {
		current := c.snapshot()
		if current == previous || time.Now().After(deadline) {
			stdout, stderr, _ := strings.Cut(current, "\x00")
			return stdout, stderr
		}
		previous = current
		time.Sleep(settle)
	}
}

// snapshot is both streams as one string, for change detection.
func (c *capturedTerminal) snapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.stdout.String() + "\x00" + c.stderr.String()
}

// restoreProcessLog puts the process-wide destinations back, so that one
// test's installation cannot make a later one quiet.
func restoreProcessLog() {
	slog.SetDefault(savedSlogDefault)
	stdLog.SetOutput(savedStdLogWriter)
}

var (
	savedSlogDefault  = slog.Default()
	savedStdLogWriter = stdLog.Writer()
)

// terminalDataDir is a data folder under the test's own temporary
// directory.
//
// The real queue, the real configuration and the real Keychain are never
// touched: the folder is made by t.TempDir and the queue inside it uses a
// test key provider.
func terminalDataDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}

	return dir
}

// A trace asked for while the interface runs goes to the file, not to the
// screen.
//
// The authorization trace is opt-in through TELECLI_AUTH_TRACE, and it is
// the one printer in the program a user can turn on without knowing it
// exists. A user who set it to debug a login and then ran the interface
// would have had the handshake written over their conversation.
func TestTheAuthorizationTraceFollowsTheRedirect(t *testing.T) {
	t.Setenv("TELECLI_AUTH_TRACE", "1")

	var out strings.Builder
	restore := telegram.SetAuthTraceOutput(&out)
	t.Cleanup(restore)

	telegram.NewEnvironmentAuthDiagnostics(nil).State(
		telegram.AuthDiagnosticStateWaitCode,
	)

	if !strings.Contains(out.String(), "telecli auth trace") {
		t.Fatalf(
			"the trace went nowhere: %q. It is opt-in, so nothing else "+
				"catches a user who turned it on during an interface run.",
			out.String(),
		)
	}
}

// The composition root redirects the trace along with the other two
// destinations.
func TestTheCompositionRootRedirectsTheAuthorizationTrace(t *testing.T) {
	t.Parallel()

	installer, err := os.ReadFile("tui_log.go")
	if err != nil {
		t.Fatalf("read tui_log.go: %v", err)
	}
	if !strings.Contains(string(installer), "telegram.SetAuthTraceOutput(") {
		t.Fatal(
			"the installer does not redirect the authorization trace: a " +
				"user who asked for it would have it written over the " +
				"running interface",
		)
	}
}
