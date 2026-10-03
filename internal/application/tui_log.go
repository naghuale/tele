package application

import (
	"fmt"
	"io"
	stdLog "log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"telecli/internal/config"
	"telecli/internal/telegram"
)

// Where everything the interface must not show goes while the interface
// is on the screen.
//
// The terminal belongs to the renderer. A log line written over a running
// interface shifts every row below it by one, so a user watching a message
// change from "sending" to "sent" sees the screen break instead of the
// state — and the component that wrote it is not the component that is at
// fault, which is what makes this so hard to see from the inside. A
// reconciler that logs one line per confirmed message is the worst case:
// it is not an error condition, it is the program working.
//
// So while `telecli tui` runs, every reason goes to a file in the data
// folder. Outside the interface nothing changes: the same reasons go to
// the terminal, where there is a user waiting to read them and no rows to
// disturb.

// TUILogFileName is the file the reasons go to while the interface runs.
//
// It is in the data folder because that is the one path the user has
// already been told about and can already find, and because it is the one
// directory telecli owns: the log is not a thing to be cleaned up by hand
// next to the queue.
const TUILogFileName = "telecli.log"

// tuiLogMaxBytes is how large the log may grow before it is rotated.
//
// A program that logs a line per confirmed message and runs for a week
// would otherwise leave a file nobody will ever open, on the user's disk,
// for the sake of diagnostics nobody reads. A few megabytes is more than
// any single support report needs, because the lines that matter are the
// recent ones.
const tuiLogMaxBytes = 4 << 20 // 4 MiB

// resolveDataDir is the folder the program keeps its files in: the
// delivery folder when one is set, and the general one otherwise.
//
// It is one function because the queue, the log and `telecli doctor` all
// have to agree on it. A log written beside one folder while the queue
// lives in another is a log nobody finds.
func resolveDataDir(cfg config.Config) string {
	dataDir := strings.TrimSpace(cfg.MessageDelivery.DataDir)
	if dataDir == "" {
		dataDir = strings.TrimSpace(cfg.DataDir)
	}

	return dataDir
}

// TUILogPath is the file the interface's reasons go to, or "" when no
// data folder is configured.
//
// It is exported because `telecli doctor` prints it: a user who is told
// that the interface is running quietly has to be able to find the file
// that is not quiet.
func TUILogPath(cfg config.Config) string {
	dataDir := resolveDataDir(cfg)
	if dataDir == "" {
		return ""
	}

	return filepath.Join(dataDir, TUILogFileName)
}

// rotatingFile is an append-only file that rolls over when it grows past a
// size, keeping the previous file beside it.
//
// It is written through a mutex because the things logging into it run in
// the dispatcher's and the reconciler's goroutines, and a size check and a
// write have to be one step or the file is rotated twice for one line.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	file *os.File
	size int64
}

// openRotatingFile opens the log file for appending, rotating it first if
// what is already there is at the limit.
//
// The file is created with 0600 because it is the program's own record of
// what it did with a Telegram account: paths, error codes and the reasons
// the interface could not show. It is not anybody else's to read.
func openRotatingFile(path string, max int64) (*rotatingFile, error) {
	rotating := &rotatingFile{path: path, max: max}
	if err := rotating.rotateIfFull(); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	rotating.file = file

	if info, err := file.Stat(); err == nil {
		rotating.size = info.Size()
	}

	return rotating, nil
}

// rotateIfFull moves the current file aside when it is at the limit.
func (r *rotatingFile) rotateIfFull() error {
	info, err := os.Stat(r.path)
	if err != nil {
		// No file yet, or it cannot be read: either way there is nothing
		// to roll over and the append below creates what is missing.
		return nil
	}
	if info.Size() < r.max {
		return nil
	}

	previous := r.path + ".1"
	_ = os.Remove(previous)
	if err := os.Rename(r.path, previous); err != nil {
		return fmt.Errorf("rotate log file: %w", err)
	}

	return nil
}

// Write appends one record, rolling the file over first if this record
// would take it past the limit.
func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil {
		return 0, os.ErrClosed
	}

	// The size is checked before the write rather than after: a file that
	// is already over the limit is rotated on the next line, and one that
	// crosses it on this line is rotated on the one after, which is the
	// same result with one fewer rename.
	if r.size+int64(len(p)) > r.max {
		if err := r.rollLocked(); err != nil {
			return 0, err
		}
	}

	n, err := r.file.Write(p)
	r.size += int64(n)

	return n, err
}

// rollLocked closes the current file and starts a new one. The caller
// holds the lock.
func (r *rotatingFile) rollLocked() error {
	if r.file != nil {
		_ = r.file.Close()
		r.file = nil
	}
	if err := r.rotateIfFull(); err != nil {
		return err
	}

	file, err := os.OpenFile(
		r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600,
	)
	if err != nil {
		return fmt.Errorf("reopen log file: %w", err)
	}
	r.file = file
	r.size = 0

	return nil
}

// Close releases the file.
func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil

	return err
}

// uiLog is the log the interface runs on.
type uiLog struct {
	// Writer is where the reasons go. It is both an io.Writer and the
	// sink of the logger, so that the TUI's own diagnostics and the
	// components' structured logs end up in one file in one order.
	Writer io.Writer

	// Logger is the structured logger every component is given.
	Logger *slog.Logger

	// Path is the file, empty when the reasons are being discarded.
	Path string

	closer io.Closer
}

// Close releases the file, if there is one.
func (l *uiLog) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	return l.closer.Close()
}

// discardLogger is what a component uses when it was not given one.
//
// It discards rather than falling back to slog.Default(), because the
// default writes to the terminal the interface owns. A component with no
// logger is a component that says nothing, which is quiet; a component
// that reaches for the default breaks the screen of a user who is watching
// a message go out.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// openUILog opens the log the interface runs on.
//
// A log that cannot be opened is not a reason to refuse to start: the
// interface works without it, and a program that will not open because it
// could not write a diagnostic file is a program whose diagnostics have
// become a dependency of its job. So the reasons are discarded and the
// path is empty, and `telecli doctor` says the file is not being written.
func openUILog(dataDir string, now func() time.Time) *uiLog {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" || now == nil {
		return &uiLog{Writer: io.Discard, Logger: discardLogger()}
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return &uiLog{Writer: io.Discard, Logger: discardLogger()}
	}

	path := filepath.Join(dataDir, TUILogFileName)
	rotating, err := openRotatingFile(path, tuiLogMaxBytes)
	if err != nil {
		return &uiLog{Writer: io.Discard, Logger: discardLogger()}
	}

	handler := slog.NewTextHandler(rotating, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	return &uiLog{
		Writer: rotating,
		Logger: slog.New(handler),
		Path:   path,
		closer: rotating,
	}
}

// installUILog points the process-wide log destinations at the file.
//
// Three doors are redirected. slog.SetDefault and log.SetOutput are the
// two a component can walk through without being handed anything, and the
// authorization trace is the third: it is opt-in through an environment
// variable, and a user who asked for it while the interface was running
// would have had it written over their conversation.
//
// None of the three is needed by any code this repository owns, which is
// the point: they are set here so that nothing that does reach for one can
// reach the terminal. TDLib writes through a door of its own — its log
// stream — and that one is named in the runtime configuration
// (telegramRuntimeConfig) rather than here, because it is the library's
// own journal and not this program's reasons.
func installUILog(log *uiLog) {
	if log == nil {
		return
	}
	slog.SetDefault(log.Logger)
	stdLog.SetOutput(log.Writer)
	stdLog.SetFlags(0)
	telegram.SetAuthTraceOutput(log.Writer)
}
