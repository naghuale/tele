package tui

// Chat is a mock chat entry used by PR-03.
type Chat struct {
	ID       int64
	Title    string
	Unread   int
	Preview  string
	Messages []Message
}

// Message is a mock message entry used by PR-03.
type Message struct {
	ID       int64
	Outgoing bool
	Text     string
	Time     string
}

// mockChats returns deterministic mock data. No time.Now().
func mockChats() []Chat {
	return []Chat{
		{
			ID:      1,
			Title:   "Alice",
			Unread:  2,
			Preview: "How are you?",
			Messages: []Message{
				{ID: 1, Outgoing: false, Text: "Hello", Time: "10:00"},
				{ID: 2, Outgoing: true, Text: "Hi", Time: "10:01"},
				{ID: 3, Outgoing: false, Text: "How are you?", Time: "10:02"},
			},
		},
		{
			ID:      2,
			Title:   "Dev Team",
			Unread:  0,
			Preview: "PR merged",
			Messages: []Message{
				{ID: 1, Outgoing: false, Text: "PR-02 is ready", Time: "09:00"},
				{ID: 2, Outgoing: true, Text: "Проверка Unicode", Time: "09:05"},
			},
		},
		{
			ID:      3,
			Title:   "Saved Messages",
			Unread:  5,
			Preview: "Note",
			Messages: []Message{
				{ID: 1, Outgoing: true, Text: "Привет", Time: "08:00"},
				{ID: 2, Outgoing: true, Text: "Note", Time: "08:01"},
			},
		},
	}
}
