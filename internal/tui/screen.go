package tui

// Screen is the active top-level screen.
type Screen uint8

const (
	ScreenChats Screen = iota
	ScreenConversation
	ScreenAuth
)

// Valid reports whether s is a known Screen.
func (s Screen) Valid() bool {
	switch s {
	case ScreenChats, ScreenConversation, ScreenAuth:
		return true
	}
	return false
}

// String returns a stable lowercase name.
func (s Screen) String() string {
	switch s {
	case ScreenChats:
		return "chats"
	case ScreenConversation:
		return "conversation"
	case ScreenAuth:
		return "auth"
	default:
		return "unknown"
	}
}

// Focus is the active focus region within the current screen.
type Focus uint8

const (
	FocusChatList Focus = iota
	FocusHistory
	FocusComposer

	// FocusSearch is the search line above the chat list (§9). It is a
	// focus region of its own rather than a mode of the list: §5 counts
	// the search among the regions exactly one of which is focused, and a
	// line that is being typed into deserves to be the thing whose accent
	// says so.
	FocusSearch
)

// Valid reports whether f is a known Focus.
func (f Focus) Valid() bool {
	switch f {
	case FocusChatList, FocusHistory, FocusComposer, FocusSearch:
		return true
	}
	return false
}

// String returns a stable lowercase name.
func (f Focus) String() string {
	switch f {
	case FocusChatList:
		return "chat_list"
	case FocusHistory:
		return "history"
	case FocusComposer:
		return "composer"
	case FocusSearch:
		return "search"
	default:
		return "unknown"
	}
}

// AuthPromptKind identifies the value requested by an authorization
// prompt.
type AuthPromptKind uint8

const (
	AuthPromptNone AuthPromptKind = iota
	AuthPromptPhone
	AuthPromptCode
	AuthPromptPassword
)

// Valid reports whether k is a known authorization prompt kind.
func (k AuthPromptKind) Valid() bool {
	switch k {
	case AuthPromptNone,
		AuthPromptPhone,
		AuthPromptCode,
		AuthPromptPassword:
		return true
	}
	return false
}

// String returns a stable lowercase name.
func (k AuthPromptKind) String() string {
	switch k {
	case AuthPromptNone:
		return "none"
	case AuthPromptPhone:
		return "phone"
	case AuthPromptCode:
		return "code"
	case AuthPromptPassword:
		return "password"
	default:
		return "unknown"
	}
}

// Title returns the human-readable prompt title.
func (k AuthPromptKind) Title() string {
	switch k {
	case AuthPromptPhone:
		return "Phone number"
	case AuthPromptCode:
		return "Code"
	case AuthPromptPassword:
		return "Password"
	default:
		return ""
	}
}
