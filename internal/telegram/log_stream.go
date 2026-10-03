package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Where the journal of the library itself goes.
//
// TDLib prints its internal log to stderr on its own, and lowering the
// verbosity does not silence it: the line the owner saw on 03.10 over a
// running interface (task 5, review-tele5, SHA 6a77c6e) was
//
//	[ 1][t 4][…][PollManager.cpp:853][#1][!MessagesManager] Fix total voter ...
//
// which is a level-1 warning, and level 1 is what the program asks for.
// A terminal belongs to the renderer while the interface is on it, so the
// library is told where to write instead of being asked to be quieter: its
// journal goes to a file beside the interface's own, or nowhere at all
// when there is no data folder to put it in. Both are answers that cost
// the user nothing and keep a library's account of itself off a screen.

// TDLibLogMaxBytes is how large the library's journal may grow before it is
// rotated by the library itself.
//
// It is the same limit the interface's own reasons are rotated at
// (application.tuiLogMaxBytes): two journals in one folder that rolled
// over at different sizes would leave a user with two rules to remember.
// TestTheTwoJournalsRollOverAtTheSameSize in internal/application fails
// when the two ever disagree.
const TDLibLogMaxBytes = 4 << 20 // 4 MiB

// setLogStreamRequest is the TDLib request that decides where the library
// writes its internal log.
//
// testdata/td_api.tl:16234: setLogStream log_stream:LogStream = Ok;
// The answer is `ok`, the same answer setLogVerbosityLevel returns, so
// both requests are checked the same way.
type setLogStreamRequest struct {
	Type      string          `json:"@type"`
	LogStream json.RawMessage `json:"log_stream"`
}

// logStreamFile is the LogStream that names a file.
//
// testdata/td_api.tl:11253: logStreamFile path:string max_file_size:int53 redirect_stderr:Bool = LogStream;
//
// RedirectStderr is false on purpose. It would hand the whole process's
// standard error to TDLib's journal, which is where telecli's own reasons
// to the user go when the program fails to start: an exit with nothing on
// the terminal and a line nobody will ever open. The observed line is in
// the format of this stream — a level, a thread, an ellipsis, the source
// file and line, and the tag — so naming the stream is enough to take it
// off the screen.
type logStreamFile struct {
	Type           string `json:"@type"`
	Path           string `json:"path"`
	MaxFileSize    int64  `json:"max_file_size"`
	RedirectStderr bool   `json:"redirect_stderr"`
}

// logStreamEmpty is the LogStream that names no destination at all.
//
// testdata/td_api.tl:11256: logStreamEmpty = LogStream;
type logStreamEmpty struct {
	Type string `json:"@type"`
}

// buildSetLogStreamFileRequest renders setLogStream for the journal at
// path, rotated by the library at maxBytes.
func buildSetLogStreamFileRequest(
	path string,
	maxBytes int64,
) (RawMessage, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%w: empty log file path", ErrTDLibLogConfiguration)
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf(
			"%w: log file path must be absolute, got %q",
			ErrTDLibLogConfiguration,
			path,
		)
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf(
			"%w: log file size must be > 0, got %d",
			ErrTDLibLogConfiguration,
			maxBytes,
		)
	}

	stream, err := json.Marshal(logStreamFile{
		Type:           "logStreamFile",
		Path:           path,
		MaxFileSize:    maxBytes,
		RedirectStderr: false,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"%w: build log stream: %w",
			ErrTDLibLogConfiguration,
			err,
		)
	}

	return buildSetLogStreamRequest(stream)
}

// buildSetLogStreamEmptyRequest renders setLogStream for a program with no
// file to write the journal to.
func buildSetLogStreamEmptyRequest() (RawMessage, error) {
	stream, err := json.Marshal(logStreamEmpty{Type: "logStreamEmpty"})
	if err != nil {
		return nil, fmt.Errorf(
			"%w: build empty log stream: %w",
			ErrTDLibLogConfiguration,
			err,
		)
	}

	return buildSetLogStreamRequest(stream)
}

// buildSetLogStreamRequest wraps a rendered LogStream in setLogStream.
func buildSetLogStreamRequest(stream json.RawMessage) (RawMessage, error) {
	raw, err := json.Marshal(setLogStreamRequest{
		Type:      "setLogStream",
		LogStream: stream,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"%w: build request: %w",
			ErrTDLibLogConfiguration,
			err,
		)
	}

	return RawMessage(raw), nil
}

// prepareTDLibLogFile creates the journal file before TDLib is told to
// write to it.
//
// It is created by this program and not by the library, because a file the
// library creates itself takes the library's permissions. The journal is
// telecli's own record of what it did with a Telegram account, so it is
// 0600, and the folder that holds it is 0700 for the same reason: a file
// the library creates while rotating its journal is inside a folder
// nobody else can open.
//
// An existing file is opened, never truncated: a journal is append-only,
// and a second telecli may have written to it already.
func prepareTDLibLogFile(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf(
				"%w: create log folder: %w",
				ErrTDLibLogConfiguration,
				err,
			)
		}
	}

	file, err := os.OpenFile(
		path,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: create log file: %w",
			ErrTDLibLogConfiguration,
			err,
		)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf(
			"%w: close log file: %w",
			ErrTDLibLogConfiguration,
			err,
		)
	}

	return nil
}

// redirectTDLibLog points the library's journal at path, or nowhere when
// there is no path.
//
// It fails closed. A library whose journal could not be moved keeps
// writing to the terminal the interface owns, which is the defect this
// replaces, so a refused stream is a refused startup rather than a quiet
// fallback.
func redirectTDLibLog(native Native, path string) error {
	if native == nil {
		return fmt.Errorf("%w: native is required", ErrTDLibLogConfiguration)
	}

	path = strings.TrimSpace(path)
	if path == "" {
		request, err := buildSetLogStreamEmptyRequest()
		if err != nil {
			return err
		}

		return executeLogStream(native, request)
	}

	if err := prepareTDLibLogFile(path); err != nil {
		return err
	}
	request, err := buildSetLogStreamFileRequest(path, TDLibLogMaxBytes)
	if err != nil {
		return err
	}

	return executeLogStream(native, request)
}

// executeLogStream sends setLogStream and proves the library took it.
func executeLogStream(native Native, request RawMessage) error {
	response, err := native.Execute(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTDLibLogConfiguration, err)
	}

	return requireOKResponse(response)
}
