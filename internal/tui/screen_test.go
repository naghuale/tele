package tui

import "testing"

func TestScreenValid(t *testing.T) {
	if !ScreenChats.Valid() {
		t.Fatal("ScreenChats must be valid")
	}
	if !ScreenConversation.Valid() {
		t.Fatal("ScreenConversation must be valid")
	}
	if Screen(42).Valid() {
		t.Fatal("unknown Screen must be invalid")
	}
}

func TestScreenString(t *testing.T) {
	if got := ScreenChats.String(); got != "chats" {
		t.Fatalf("ScreenChats.String() = %q", got)
	}
	if got := ScreenConversation.String(); got != "conversation" {
		t.Fatalf("ScreenConversation.String() = %q", got)
	}
	if got := Screen(42).String(); got != "unknown" {
		t.Fatalf("Screen(42).String() = %q", got)
	}
}

func TestFocusValid(t *testing.T) {
	for _, f := range []Focus{FocusChatList, FocusHistory, FocusComposer} {
		if !f.Valid() {
			t.Fatalf("%v must be valid", f)
		}
	}
	if Focus(42).Valid() {
		t.Fatal("unknown Focus must be invalid")
	}
}

func TestFocusString(t *testing.T) {
	cases := map[Focus]string{
		FocusChatList: "chat_list",
		FocusHistory:  "history",
		FocusComposer: "composer",
		Focus(42):     "unknown",
	}
	for f, want := range cases {
		if got := f.String(); got != want {
			t.Fatalf("Focus(%d).String() = %q, want %q", f, got, want)
		}
	}
}
