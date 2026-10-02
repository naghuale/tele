// Package livewatch is the diagnostic of the live chat list.
//
// The live list has three layers — the store that TDLib's updates are
// applied to, the adapter the composition root builds over it, and the
// interface that draws it — and each of them is proved by its own tests. A
// defect that lives between the three is therefore proved by none of them,
// which is exactly what a report from a real account is (#47, 02.10: the
// list did not move, every test green).
//
// So this package is one file, off unless the environment names it, that
// every layer writes one line into as it goes. The lines are the steps of
// one change, from the update that arrived to the frame that changed, and
// the step that is missing is the answer.
//
// What a line may hold is the rule the whole repository works by: no
// message text, no names, no identifiers beyond a short hash of a chat id.
// A chat id is a small number of one account, so the hash is there to
// correlate the lines of one chat and not to hide it from somebody who
// already has it.
package livewatch

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync"
	"time"
)

// EnvVar names the file the diagnostic is appended to.
//
// It is off unless it is set: nothing is created, nothing is written and no
// file is opened, so a program that does not set it behaves exactly as it
// did before. The file is opened once, in append mode, and is never
// truncated — a second run adds to the first.
const EnvVar = "TELECLI_DEBUG_LIVE"

// FileMode is the mode of the file the diagnostic writes, and it is the
// mode of a file a person does not want read by another account on the
// machine.
const FileMode os.FileMode = 0o600

// Field is one key=value of a line.
//
// The pairs are values rather than a map so that a line reads in the order
// the steps happen in, which is the whole reason the file exists.
type Field struct {
	key   string
	value string
}

// Int returns a field for a number.
func Int(key string, value int) Field {
	return Field{key: key, value: fmt.Sprintf("%d", value)}
}

// Text returns a field for a word of a fixed vocabulary.
func Text(key, value string) Field {
	if value == "" {
		value = "-"
	}

	return Field{key: key, value: value}
}

// Bool returns a field for a yes or a no.
func Bool(key string, value bool) Field {
	if value {
		return Field{key: key, value: "yes"}
	}

	return Field{key: key, value: "no"}
}

// Chat returns a field for the hash of a chat identifier.
//
// Zero is written as a dash and not as a hash: it is not a chat, and a
// reader of the file has to be able to tell the two apart.
func Chat(key string, id int64) Field {
	if id == 0 {
		return Field{key: key, value: "-"}
	}

	hash := fnv.New32a()
	_, _ = fmt.Fprintf(hash, "%d", id)

	return Field{key: key, value: fmt.Sprintf("%08x", hash.Sum32())}
}

// Update types as the diagnostic names them.
//
// The five updates the store acts on are named and everything else the wire
// carries is one bucket.
//
// The four are the ones TDLib sends for a message that arrived in a chat,
// and the fifth is the update that creates a chat in the store: a chat that
// was never created cannot have its row moved, and a file that said only
// "other" for it could not tell a store nobody filled from a store that is
// full of traffic nobody asked about.
const (
	UpdateNewMessage      = "updateNewMessage"
	UpdateNewChat         = "updateNewChat"
	UpdateChatLastMessage = "updateChatLastMessage"
	UpdateChatPosition    = "updateChatPosition"
	UpdateChatReadInbox   = "updateChatReadInbox"
	UpdateOther           = "other"
	UpdateUnreadable      = "unreadable"
)

// The steps the diagnostic writes, one name per layer and per moment.
//
// A line is a step, and the step that is missing between two lines that are
// there is the answer to a report that a list did not move.
const (
	// StepProgram is the model with the live state behind it, or without.
	StepProgram = "program.start"

	// StepStoreUpdate is one raw update the live store was given.
	StepStoreUpdate = "store.update"

	// StepStoreSignal is one change signal the store posted.
	StepStoreSignal = "store.signal"

	// StepWait is the adapter's wait for a change returning.
	StepWait = "live.wait"

	// StepWaitArmed is the interface arming the next wait.
	StepWaitArmed = "tui.wait_armed"

	// StepLiveChanged is the interface told that the state changed.
	StepLiveChanged = "tui.live_changed"

	// StepRepaintDue is the redraw a burst of changes was owed.
	StepRepaintDue = "tui.repaint_due"

	// StepChatList is where the window of the chat list was before and
	// after a redraw.
	StepChatList = "tui.chat_list"

	// StepRepaintDrawn is a redraw that read the state.
	StepRepaintDrawn = "tui.repaint_drawn"

	// StepRepaint is the full repaint a redraw asked the program for.
	StepRepaint = "tui.repaint"

	// StepFrameChanged is the answer to the one question the whole file
	// is opened for: did the frame on the screen change.
	StepFrameChanged = "tui.frame"
)

// ChatID is the identifier a hash is taken of.
//
// It is an int64 rather than telegram.ChatID because the diagnostic is
// below every layer of the program: the store, the adapter and the
// interface all write into the same file, and none of them may be known to
// the others.
type ChatID = int64

// StoreUpdate writes the line of one raw update the live store was given.
//
// The changed field is the store's own answer to whether the update moved
// anything, and the error field is there because a decode error is the one
// reason an update about a chat arrives and does nothing.
func StoreUpdate(kind string, chat ChatID, changed bool, err error) {
	fields := []Field{
		Text("type", kind),
		Bool("changed", changed),
		Bool("err", err != nil),
	}
	if chat != 0 {
		fields = append(fields, Chat("chat", chat))
	}

	Log(StepStoreUpdate, fields...)
}

// StoreSignal writes the line of one change signal the store posted.
func StoreSignal() {
	Log(StepStoreSignal)
}

// LogChatList writes one line of where the window of the chat list was.
//
// phase is before or after the state was read, and the three numbers are
// the offset of the window, the chat under the cursor and the row of it
// inside the window. A change that moved the list and a window that did not
// follow it is the difference between a moving list and a still one.
func LogChatList(phase string, offset, selected, row int) {
	Log(StepChatList,
		Text("phase", phase),
		Int("offset", offset),
		Int("selected", selected),
		Int("row", row),
	)
}

// WaitReturned writes the line of the adapter's wait for a change returning,
// and says what woke it: a change, or the end of the program's context.
func WaitReturned(reason string) {
	Log(StepWait, Text("reason", reason))
}

// UpdateType returns the name the diagnostic gives a TDLib update type.
func UpdateType(name string) string {
	switch name {
	case UpdateNewMessage,
		UpdateNewChat,
		UpdateChatLastMessage,
		UpdateChatPosition,
		UpdateChatReadInbox:
		return name
	default:
		return UpdateOther
	}
}

// The state of the diagnostic file: the path it was opened from and the
// handle itself, under one lock with the write, so that a line is never
// written to a file that was closed under it.
//
// The path is read from the environment on every line rather than once, so
// that the sink follows the variable: a file that is renamed between two
// lines of one run is followed, and a test that names another file gets a
// file of its own instead of the one the test before it opened.
var (
	mu     sync.Mutex
	opened string
	file   *os.File
)

// Enabled reports whether the diagnostic is on.
//
// It is asked on the paths that are hot in a running program — before a
// frame is rendered twice to be compared — so it reads the environment and
// nothing else, and answers about the environment as it is now.
func Enabled() bool {
	return os.Getenv(EnvVar) != ""
}

// Log appends one line to the diagnostic file.
//
// It returns without writing when the diagnostic is off, and when the file
// cannot be opened: a diagnostic that cannot be written is not a reason to
// stop the program it is watching, and there is nothing the caller can do
// about it but carry on.
func Log(step string, fields ...Field) {
	path := os.Getenv(EnvVar)
	if path == "" {
		return
	}

	line := time.Now().UTC().Format(time.RFC3339Nano) + " " + step
	for _, field := range fields {
		line += " " + field.key + "=" + strings.ReplaceAll(field.value, " ", "_")
	}
	line += "\n"

	mu.Lock()
	defer mu.Unlock()

	handle := openFor(path)
	if handle == nil {
		return
	}
	if _, err := handle.WriteString(line); err != nil {
		// A file that stopped accepting lines is closed here and tried
		// again on the next line, because the reason is usually a full
		// disk rather than a wrong path.
		_ = handle.Close()
		file = nil
		opened = ""
	}
}

// openFor returns the handle of the diagnostic file of the path, opening it
// if it is not open yet and closing the one of a path that is no longer
// named. It returns nil when the file cannot be opened.
//
// The caller holds the lock.
func openFor(path string) *os.File {
	if file != nil {
		if opened == path {
			return file
		}

		_ = file.Close()
		file = nil
		opened = ""
	}

	handle, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, FileMode)
	if err != nil {
		return nil
	}

	file, opened = handle, path

	return handle
}
