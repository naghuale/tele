package livewatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNothingIsWrittenWithoutTheVariable(t *testing.T) {
	t.Setenv(EnvVar, "")

	if Enabled() {
		t.Fatal("the diagnostic is on with no file named")
	}

	Log("store.update", Text("type", UpdateNewMessage))

	path := filepath.Join(t.TempDir(), "live.log")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the diagnostic wrote a file that nobody named")
	}
}

func TestALineIsAppendedWithTheStepAndItsFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.log")
	t.Setenv(EnvVar, path)

	Log("store.update",
		Text("type", UpdateChatLastMessage),
		Chat("chat", 4242),
		Bool("signalled", true),
	)
	Log("live.wait", Text("reason", "signal"))

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the diagnostic: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(written), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the diagnostic has %d lines, want 2:\n%s", len(lines), written)
	}
	if !strings.Contains(lines[0], "store.update") ||
		!strings.Contains(lines[0], "type=updateChatLastMessage") ||
		!strings.Contains(lines[0], "signalled=yes") {
		t.Fatalf("the first line is %q", lines[0])
	}
	if strings.Contains(lines[0], "4242") {
		t.Fatalf("the first line carries the chat id itself: %q", lines[0])
	}
	if !strings.Contains(lines[0], "chat=") {
		t.Fatalf("the first line carries no chat at all: %q", lines[0])
	}
	if !strings.Contains(lines[1], "live.wait reason=signal") {
		t.Fatalf("the second line is %q", lines[1])
	}

	// A second run adds to the first rather than replacing it, because the
	// file is what a run of the program leaves behind.
	Log("store.update", Text("type", UpdateOther))

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the diagnostic: %v", err)
	}
	if len(strings.Split(strings.TrimRight(string(after), "\n"), "\n")) != 3 {
		t.Fatalf("the second run did not append:\n%s", after)
	}
}

func TestTheFileIsNotReadableByAnotherAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.log")
	t.Setenv(EnvVar, path)

	Log("store.update", Text("type", UpdateOther))

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the diagnostic wrote nothing: %v", err)
	}
	if mode := info.Mode().Perm(); mode != FileMode {
		t.Fatalf("the mode of the file is %o, want %o", mode, FileMode)
	}
}

func TestAFileThatCannotBeOpenedDoesNotStopTheProgram(t *testing.T) {
	t.Setenv(EnvVar, filepath.Join(t.TempDir(), "no-such-directory", "live.log"))

	Log("store.update", Text("type", UpdateOther))
	Log("live.wait", Text("reason", "signal"))
}

func TestTheChatFieldIsAHashAndZeroIsNoChat(t *testing.T) {
	if Chat("chat", 0).value != "-" {
		t.Fatalf("the field of no chat is %q, want a dash", Chat("chat", 0).value)
	}

	first := Chat("chat", 7).value
	if len(first) != 8 {
		t.Fatalf("the hash of a chat is %q, want eight columns", first)
	}
	if first == Chat("chat", 8).value {
		t.Fatal("two chats have one hash")
	}
	if first != Chat("chat", 7).value {
		t.Fatal("the hash of a chat is not the same twice")
	}
}

func TestTheUpdateTypesAreTheOnesTheStoreActsOnAndTheRest(t *testing.T) {
	for _, name := range []string{
		UpdateNewMessage,
		UpdateNewChat,
		UpdateChatLastMessage,
		UpdateChatPosition,
		UpdateChatReadInbox,
	} {
		if got := UpdateType(name); got != name {
			t.Errorf("UpdateType(%q) = %q, want the name itself", name, got)
		}
	}

	for _, name := range []string{
		"updateChatTitle",
		"updateOption",
		"updateUserStatus",
		"updateDeleteMessages",
		"",
	} {
		if got := UpdateType(name); got != UpdateOther {
			t.Errorf("UpdateType(%q) = %q, want %q", name, got, UpdateOther)
		}
	}
}

func TestAValueWithASpaceInItStaysOnOneLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.log")
	t.Setenv(EnvVar, path)

	Log("store.update", Text("reason", "two words"))

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the diagnostic: %v", err)
	}
	if len(strings.Split(strings.TrimRight(string(written), "\n"), "\n")) != 1 {
		t.Fatalf("a value with a space broke the line:\n%s", written)
	}
	if !strings.Contains(string(written), "reason=two_words") {
		t.Fatalf("the line is %q", written)
	}
}
