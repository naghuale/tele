package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// The screen of the owner's check on 01.10.
const (
	ownerWidth  = 176
	ownerHeight = 43
)

// ownerChats is a list like the owner's: names of a length a real account
// has, previews that are empty and long, counts and times, and one chat with
// no name at all.
func ownerChats() []Chat {
	names := []string{
		"Дмитрий С", "Избранное", "Елена Батрашина", "Работа — релиз",
		"Команда Разработки", "Мама", "", "Анна Example", "Брат",
		"Фотографии", "Договоры", "Служебный чат", "Елена ✌️ Батрашина",
	}
	previews := []string{
		"Собираемся в 15:00", "", "проверяю сборку",
		"https://example.com/very/long/link/that/does/not/fit/at/all",
		"🖼 фото", "ок", "Мама: перезвони", "ok", "👍",
		"Договор подписан", "", "Ой", "❤️",
	}

	chats := make([]Chat, 0, 40)
	for index := range 40 {
		chats = append(chats, Chat{
			ID:      int64(index + 1),
			Title:   names[index%len(names)],
			Preview: previews[index%len(previews)],
			Unread:  index % 5,
			At:      mockMoment(fmt.Sprintf("12:%02d", index%60)),
			Kind:    ChatKind(index % 3),
		})
	}

	return chats
}

// ownerModel is the screen the owner's report is about: a list of real
// names, the first chat open with messages in it, and the status line
// saying what the account is doing.
func ownerModel(t *testing.T, mode termwidth.Mode) Model {
	t.Helper()

	model := programModel(t, theme.ProfileTrueColor, ownerWidth, ownerHeight)
	model.widths = termwidth.Unmeasured(mode)
	model.chats = ownerChats()
	model.chatsState = loadStateLoaded
	model.selectedChat = 0

	model.chats[0].Messages = []Message{
		{ID: 100, Author: "Дмитрий С", Text: "Привет", At: mockMoment("12:00")},
		{ID: 101, Outgoing: true, Text: "Привет!", At: mockMoment("12:01")},
		{ID: 102, Author: "Дмитрий С", Text: "Как сборка?", At: mockMoment("12:02")},
		{ID: 103, Outgoing: true, Text: "Зелёная", At: mockMoment("12:03")},
	}

	model.summary = StatusSummary{
		Connection: ConnectionReady,
		Presence:   Presence{Kind: PresenceGroup, OnlineMembers: 2},
		Queue:      QueueSummary{Known: true},
	}

	return model
}

// The walk of the owner's report: the selection down the list, the
// conversation opened and left, the draft grown with Alt+Enter.
func ownerWalk() []struct{ name, keys string } {
	type step = struct{ name, keys string }

	steps := make([]step, 0, 16)
	for index := range 6 {
		steps = append(steps, step{
			fmt.Sprintf("down %d", index+1), keyDown,
		})
	}
	steps = append(steps,
		step{"enter", keyEnter},
		step{"down 7", keyDown},
		step{"alt+enter 1", "\x1b\r"},
		step{"alt+enter 2", "\x1b\r"},
		step{"alt+enter 3", "\x1b\r"},
		step{"esc", keyEsc},
		step{"down 8", keyDown},
		step{"up 1", keyUp},
	)

	return steps
}

// The screen must be a rectangle: as many rows as the window has, and no
// row of it wider than the window under either rule of counting.
//
// A row this program counts as the width of the window and the renderer
// counts a column wider is a row the renderer cuts: the last cell of that
// row keeps what was drawn there before, and a Bubble Tea renderer that
// repaints only the rows that changed leaves it. That is the chat list of
// the owner's screen with "/ search" twice, the preview of one chat under
// the name of another, and a cell of an old background at the left edge of
// the row.
//
// A row the terminal itself counts wider than the window is worse: it wraps,
// everything under the wrap moves down a row, and the rows above the wrap
// keep the tail of the row before them.
//
// The renderer decides with charmbracelet/x/ansi, so that is the count the
// row has to satisfy as well as ours. A row may be a column short of the
// width under our rule — that is the air the disagreement is paid for in —
// but not under either of them.
func TestEveryRowOfTheScreenIsTheWidthOfTheWindowToBothRules(t *testing.T) {
	for name, mode := range map[string]termwidth.Mode{
		"grapheme":  termwidth.ModeGrapheme,
		"codepoint": termwidth.ModeCodepoint,
	} {
		t.Run(name, func(t *testing.T) {
			model := ownerModel(t, mode)

			lines := strings.Split(model.View(), "\n")
			if len(lines) != ownerHeight {
				t.Fatalf(
					"the frame is %d rows, want %d",
					len(lines), ownerHeight,
				)
			}

			for row, line := range lines {
				if ours := model.widths.StringWidth(line); ours > ownerWidth {
					t.Errorf(
						"row %d is %d columns for this program, want at most %d:\n%s",
						row+1, ours, ownerWidth, plain(line),
					)
				}
			}
		})
	}
}

// The same rows, counted the way the renderer counts them.
func TestEveryRowOfTheScreenIsTheWidthOfTheWindowToTheRenderer(t *testing.T) {
	for name, mode := range map[string]termwidth.Mode{
		"grapheme":  termwidth.ModeGrapheme,
		"codepoint": termwidth.ModeCodepoint,
	} {
		t.Run(name, func(t *testing.T) {
			model := ownerModel(t, mode)

			// A row may be a column short of the window to the renderer:
			// a symbol with text presentation by default is drawn two
			// cells wide out of the emoji font and counted as one by the
			// renderer, and the row that states two cells for it is a row
			// with one column of air at its right edge. What a row may
			// not be is over the width to either of them: the renderer
			// cuts such a row and the terminal wraps it.
			for row, line := range strings.Split(model.View(), "\n") {
				if got := rendererWidth(line); got > ownerWidth {
					t.Errorf(
						"row %d is %d columns to the renderer, want at most %d:\n%s",
						row+1, got, ownerWidth, plain(line),
					)
				}
			}
		})
	}
}

// The feed must fill the rows it has, whatever height the composer has
// grown to, and stand on the last of them.
//
// The owner typed Alt+Enter a few times and everything above the newest
// message went blank: the window was placed against the rows the feed had
// when the draft was one line, the composer took rows from it, and nothing
// placed the window again. A window placed against rows the feed no longer
// has is a window whose messages are above the top of it.
func TestTheFeedFillsTheRowsAboveAGrownComposer(t *testing.T) {
	for _, lines := range []int{1, 2, 3, 5} {
		model := ownerModel(t, termwidth.ModeCodepoint)
		model.chats[0].Messages = manyMessages(60)
		model = model.scrollToNewest()

		for range lines {
			model, _ = updateModel(t, model, tea.KeyMsg{
				Type: tea.KeyEnter,
				Alt:  true,
			})
		}

		layout := LayoutFor(model.width, model.height)
		width := layout.ChatContentWidth()
		feed := model.historyFeedRows(layout, width)
		rows := strings.Split(plain(model.View()), "\n")

		// The feed starts under the header: the name of the chat, the
		// status and the rule that says this pane has the keys.
		first := 1 + len(model.statusBlockLines(layout, width)) + 1

		// A blank row between two messages is a gap and belongs to the feed,
		// so the feed is full when its own first row holds a message: that
		// is the top row the window was placed to fill.
		if strings.TrimSpace(rows[first]) == "" {
			t.Errorf(
				"a %d-line draft: the first row of the feed is empty:\n%s",
				lines, strings.Join(rows[first:first+feed], "\n"),
			)
		}

		// And it stands on the composer: the newest message is on the last
		// row of the feed, with as many messages above it as the rows hold.
		if last := rows[first+feed-1]; !strings.Contains(last, newestText) {
			t.Errorf(
				"a %d-line draft: the last row of the feed is %q",
				lines, strings.TrimRight(last, " "),
			)
		}

		visible := 0
		for row := first; row < first+feed; row++ {
			if strings.Contains(rows[row], "сообщение ") {
				visible++
			}
		}
		if visible < 5 {
			t.Errorf(
				"a %d-line draft: %d of the feed's %d rows hold a message",
				lines, visible, feed,
			)
		}
	}
}

// newestText is what the newest message of manyMessages says.
const newestText = "сообщение 59"

func manyMessages(count int) []Message {
	messages := make([]Message, 0, count)
	for index := range count {
		messages = append(messages, Message{
			ID:       int64(100 + index),
			Outgoing: index%3 == 0,
			Author:   "Дмитрий С",
			Text:     fmt.Sprintf("сообщение %d", index),
			At:       mockMoment("12:00"),
		})
	}

	return messages
}

// The owner's report of 01.10: two messages of the feed and one preview in
// the chat list said `[unsupported message]`, and there was no way to tell
// what had been sent (#17).
//
// A service message is what happened in the chat rather than something
// somebody wrote, and it is now a row of the feed of its own with the words
// of what happened on it. The two rows of the report are the ones this test
// draws: a member who joined and a message that was pinned.
func TestWhatHappenedInTheChatIsSaidAsARowOfItsOwn(t *testing.T) {
	model := ownerModel(t, termwidth.ModeCodepoint)
	model.screen = ScreenConversation
	model.focus = FocusComposer
	model.chats[0].Messages = []Message{
		{ID: 1, Author: "Дмитрий С", At: mockMoment("11:00"), Service: "joined the chat"},
		{ID: 2, Outgoing: true, Text: "вижу", At: mockMoment("11:01")},
		{
			ID: 3, Author: "Дмитрий С", At: mockMoment("11:02"),
			Service: "pinned a message",
		},
	}

	view := plain(model.View())
	for _, phrase := range []string{"joined the chat", "pinned a message"} {
		if !strings.Contains(view, phrase) {
			t.Fatalf("the feed does not say %q:\n%s", phrase, view)
		}
	}
	if strings.Contains(view, "unsupported") {
		t.Fatalf("the feed says something about what it cannot read:\n%s", view)
	}
	// A service message is not somebody's message: nobody is named above
	// it, because its sender is the chat and the chat is named at the top
	// of the screen.
	if strings.Contains(view, "Дмитрий С  11:00") {
		t.Fatalf("a service message is signed with a name:\n%s", view)
	}
}

// The connection is said once.
//
// The owner read "connected" on one row of the header and "2 online ·
// connected" on the row under it. The frame says the whole status on one row
// in every state this walk reaches, so what was on the screen was the
// header of an earlier frame with the header of the new one under it — a
// row the terminal was not told to write over, which is the same kind of
// leftover as the list rows of the first defect and is gone with it.
//
// The test is the rule rather than the report: one word, one row.
func TestTheConnectionIsSaidOnOneRow(t *testing.T) {
	for name, mode := range map[string]termwidth.Mode{
		"grapheme":  termwidth.ModeGrapheme,
		"codepoint": termwidth.ModeCodepoint,
	} {
		t.Run(name, func(t *testing.T) {
			model := ownerModel(t, mode)

			saidOn := 0
			for _, line := range strings.Split(plain(model.View()), "\n") {
				if strings.Contains(line, "connected") {
					saidOn++
				}
			}

			if saidOn != 1 {
				t.Errorf("the connection is said on %d rows, want 1", saidOn)
			}
		})
	}
}

// The screen the terminal holds is a fresh render of the state, after every
// key of the walk and not only where the program believes it drew.
func TestTheTerminalHoldsAFreshRenderAfterEveryKey(t *testing.T) {
	for name, mode := range map[string]termwidth.Mode{
		"grapheme":  termwidth.ModeGrapheme,
		"codepoint": termwidth.ModeCodepoint,
	} {
		t.Run(name, func(t *testing.T) {
			widths := termwidth.Unmeasured(mode)
			harness := startProgram(
				t, ownerModel(t, mode), widths, ownerWidth, ownerHeight,
			)

			harness.send(t, tea.WindowSizeMsg{
				Width: ownerWidth, Height: ownerHeight,
			})

			for _, step := range ownerWalk() {
				view := harness.key(t, step.name, step.keys)

				assertScreenIsTheFrame(t, harness.emulator, view)
			}
		})
	}
}

// rendererWidth is how many columns the renderer of Bubble Tea counts a
// rendered row in.
//
// It is not our width model and it is not the terminal's: it is the rule the
// renderer itself truncates a row with and decides whether to erase the rest
// of the line with, so a row that disagrees with it is a row the terminal is
// told one width about and the program believes another.
func rendererWidth(line string) int {
	return termwidth.RendererWidth(line)
}
