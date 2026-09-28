package tui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// The screen snapshots of PR-10A.7: what every screen of the interface
// looks like, kept next to the code that draws it.
//
// A snapshot is a golden file under testdata/snapshots, one file per
// screen, and each file holds two views of the same screen: the text
// without its escape sequences, so that a reviewer can read a change in a
// review, and the escape sequences themselves, one line at a time and
// %q-escaped, so that a change of colour is a diff like any other. With
// only the first view a theme could be replaced and not one test would
// notice, because the words on the screen would be the same words.
//
// The snapshots are rewritten only when a test is asked to:
//
//	go test ./internal/tui -run Snapshot -update
//
// and a run without the flag fails on a difference and prints both sides of
// it. That is the whole point of a golden file: a change of the way the
// interface looks is a change somebody meant to make, and the file is where
// they are told about it.
//
// Everything on these screens is invented here. The chat names, the
// messages, the times and the delivery states are written in this file and
// nowhere else, and no snapshot may ever be drawn from a real account
// (§19): a golden file is committed, and a committed file outlives the
// machine it was made on, travels to a CI machine and is read by whoever
// reviews the change.

// snapshotUpdate rewrites the golden files instead of comparing with them.
//
// It is a flag and not an environment variable because the answer to "are
// these snapshots still true" is asked once per run, and an environment
// variable would survive a run that forgot to clean it up.
var snapshotUpdate = flag.Bool(
	"update",
	false,
	"rewrite the golden screen snapshots under testdata/snapshots",
)

// snapshotDir is where the golden files live, under the directory Go
// reserves for the data of a test.
const snapshotDir = "testdata/snapshots"

// The layout of every snapshot is one of the three of §10.1, at a size that
// is fixed here rather than taken from the terminal that runs the test.
const (
	snapshotWideWidth   = 120
	snapshotWideHeight  = 30
	snapshotMediumWidth = 80
	snapshotNarrowWidth = 60
	snapshotHeight      = 24

	// snapshotConversationWidth is the width of the screens that show what
	// is under a message: a delivery state is a second line under the text,
	// and a state with a sentence under it needs a third. At the wide width
	// a state is a line between two other lines, and a snapshot of it says
	// less about the state.
	snapshotConversationWidth = 100
)

var (
	// snapshotZone is the zone every model reads times in.
	//
	// A screen that says "Retrying at 12:10" or "last seen at 14:09" is a
	// statement about a time zone, and a test that read the zone of the
	// machine it runs on would pass in one country and fail in another. A
	// fixed zone is what makes the bytes of a snapshot the same on a Mac
	// and on a Linux runner.
	snapshotZone = time.FixedZone("UTC+3", 3*60*60)

	// snapshotClock is the moment every snapshot is drawn at.
	snapshotClock = time.Date(2026, 3, 14, 12, 9, 0, 0, snapshotZone)
)

// The identity of the account in the fixtures. It is a word, not a key: a
// snapshot is a drawing, and the account it is drawn for is never a real
// one (§19).
const (
	snapshotAccountKey   = "account-fixture"
	snapshotChatID       = int64(1)
	snapshotEntryID      = "entry-fixture"
	snapshotEntryVersion = 3
)

// The synthetic screen content. Every string below is written for these
// tests; none of them is a chat, a message or a name of anybody.
const (
	snapshotOutgoingText = "Postponing the release to Friday"
	snapshotDraft        = "The changelog is in the release notes"
)

// snapshotChats is the chat list every snapshot is drawn over. It has a
// name in each case §3.3 has to cut or wrap, so a snapshot shows what a
// long title and a Cyrillic one look like.
func snapshotChats() []Chat {
	return []Chat{
		{ID: 1, Title: "Anna Example", Unread: 2, Preview: "the build is green again", Time: "12:07"},
		{ID: 2, Title: "Release Room", Preview: "the tag is pushed", Time: "12:05", Kind: ChatKindGroup},
		{ID: 3, Title: "Notes", Unread: 1, Preview: "milk, bread, coffee", Time: "11:58"},
		{ID: 4, Title: "Команда Разработки", Preview: "созвон в 15:00", Time: "11:40", Kind: ChatKindChannel},
	}
}

// snapshotWidthChats is the list a screen drawn in either width rule is
// made of.
//
// Every name here carries a character the two rules of
// internal/tui/termwidth count differently: a hand with the emoji selector
// behind it is two columns to one of them and one to the other, and a name
// that lands on the width of the pane in one rule and over it in the other
// is cut in one screen and whole in the other. That difference is the whole
// reason the rules exist, and a snapshot that did not have it would be a
// snapshot of two screens that happen to look alike.
//
// The names are long enough to be cut, because a chat title is the one
// piece of text on this screen that nobody controls.
func snapshotWidthChats() []Chat {
	return []Chat{
		{ID: 1, Title: "Xiaomi News Channel ✌️", Unread: 3, Preview: "прошивка вышла ✌️"},
		{ID: 2, Title: "ТФУЮАНЬ 🇨🇳 САХ ЦЕНЫ ✌️", Preview: "сахар подорожал 🇨🇳"},
		{ID: 3, Title: "🏃 Секция новостей 🏃‍♂️", Preview: "пробег в семь утра 🏃‍♂️"},
		{ID: 4, Title: "Команда 👍🏽 СПАСИБО", Unread: 1, Preview: "за помощь с переводом"},
		{ID: 5, Title: "Китай 中文 club", Preview: "новый канал про 中文"},
		{ID: 6, Title: "Café Déjâ Vu ✌️", Preview: "кофе в девять"},
	}
}

// snapshotMessages is one page of history as TDLib answers it: newest
// first.
//
// The order is the one a real page arrives in and not the one it is
// drawn in. The model reverses a page where it lands (TDLib answers
// newest first and a conversation is read oldest first, divergence 1 of
// the specification), and a golden that handed the model an already
// chronological list would be a golden of a program nobody runs: the
// screen would show 12:07 at the top and 12:02 above the composer, which
// is exactly the mistake this fixture exists to catch.
//
// The times are strings on purpose: they are part of the fixture rather
// than a moment, so a snapshot never depends on when it was drawn.
func snapshotMessages() []Message {
	return []Message{
		{
			ID: 3, Text: "Спасибо, посмотрю после обеда", Time: "12:07",
			Author: "Anna Example", AuthorID: 5,
		},
		{ID: 2, Outgoing: true, Text: "Shipped the release notes", Time: "12:05"},
		{
			ID: 1, Text: "The build is green again", Time: "12:02",
			Author: "Anna Example", AuthorID: 5,
		},
	}
}

// snapshotFixture is everything about a screen that is not what is on it:
// the size, the theme and the profile. All three are given by the test
// rather than found by the program, because a snapshot of a screen drawn
// with whatever the environment happened to say is a snapshot of that
// environment.
type snapshotFixture struct {
	width   int
	height  int
	theme   string
	profile theme.Profile

	// widthMode is the rule the screen is drawn in. It is the zero value
	// on every screen that is not about widths, which is the rule a
	// terminal nobody could ask is drawn with.
	widthMode termwidth.Mode

	// chats is the list the screen is drawn over, and nil for the list
	// every other snapshot is drawn over.
	chats []Chat
}

// wide is the two-pane layout of §10.2 at the size the snapshots use.
func wide(profile theme.Profile) snapshotFixture {
	return snapshotFixture{
		width:   snapshotWideWidth,
		height:  snapshotWideHeight,
		profile: profile,
	}
}

// medium is the two narrow panes of §10.3.
func medium(profile theme.Profile) snapshotFixture {
	return snapshotFixture{
		width:   snapshotMediumWidth,
		height:  snapshotHeight,
		profile: profile,
	}
}

// narrow is the single pane of §10.4.
func narrow(profile theme.Profile) snapshotFixture {
	return snapshotFixture{
		width:   snapshotNarrowWidth,
		height:  snapshotHeight,
		profile: profile,
	}
}

// conversation is a screen with a little less room than wide, which is
// where a delivery state is drawn with the sentence §6 puts under it.
func conversation(profile theme.Profile) snapshotFixture {
	return snapshotFixture{
		width:   snapshotConversationWidth,
		height:  snapshotHeight,
		profile: profile,
	}
}

// snapshotModel builds a model the way the composition root builds one,
// with the size, the theme, the profile, the clock and the zone of the
// fixture, and hands it the fixture's chat list.
func snapshotModel(
	t *testing.T,
	f snapshotFixture,
	deps Dependencies,
) Model {
	t.Helper()

	if deps.MessageSubmitter == nil {
		deps.MessageSubmitter = &recordingSubmitter{}
	}
	if deps.Source == nil {
		deps.Source = &fakeChatSource{}
	}

	built := theme.DefaultTheme()
	if f.theme != "" {
		var err error

		built, err = theme.ThemeFor(f.theme)
		if err != nil {
			t.Fatalf("theme.ThemeFor(%q): %v", f.theme, err)
		}
	}
	deps.Theme = built.ForProfile(f.profile)
	deps.ColorProfile = f.profile
	// The rule is given rather than measured: a snapshot of a screen drawn
	// with whatever the terminal of the machine that made it happened to
	// say is a snapshot of that terminal.
	deps.WidthMode = f.widthMode

	m, err := NewModelWithDependencies(context.Background(), deps)
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	// The clock and the zone are set here and not read: a screen that says
	// when a retry is due is a screen whose bytes are the bytes of the
	// machine that drew it unless they are pinned.
	m.now = func() time.Time { return snapshotClock }
	m.location = snapshotZone

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: f.width, Height: f.height})

	chats := f.chats
	if chats == nil {
		chats = snapshotChats()
	}
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: chats})

	return m
}

// openSnapshotChat opens the first chat of the fixture and puts its history
// on the screen, which is the state the other screens here are built on.
func openSnapshotChat(t *testing.T, m Model) Model {
	t.Helper()

	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:    snapshotChatID,
		operation: m.historyOperation,
		page:      HistoryPage{Messages: snapshotMessages()},
	})

	return m.scrollToNewest()
}

// snapshotConversation is the screen of §3.1: the chat list with a
// conversation beside it, the focus on the list, which is the state a user
// is in while they are choosing a chat.
func snapshotConversation(t *testing.T, f snapshotFixture) Model {
	t.Helper()

	m := openSnapshotChat(t, snapshotModel(t, f, Dependencies{
		AccountKey: snapshotAccountKey,
	}))
	m.focus = FocusChatList

	return m
}

// snapshotChatList is the screen of §3.3 with no chat open: the list on its
// own, which is all a narrow terminal has room for (§10.4).
func snapshotChatList(t *testing.T, f snapshotFixture) Model {
	t.Helper()

	return snapshotModel(t, f, Dependencies{AccountKey: snapshotAccountKey})
}

// snapshotDelivery is a conversation with one outgoing message in the given
// state of §6, with the cursor still on the history below it.
//
// The cursor is where the user left it: a poll brings the message in and
// does not move what was being read, so the state is drawn under a
// conversation that is still there.
func snapshotDelivery(
	t *testing.T,
	f snapshotFixture,
	state MessageDeliveryState,
) Model {
	t.Helper()

	source := &pendingSource{messages: []PendingMessage{{
		EntryID:       snapshotEntryID,
		ChatID:        snapshotChatID,
		Text:          snapshotOutgoingText,
		State:         state,
		Version:       snapshotEntryVersion,
		CreatedAt:     snapshotClock.Add(-2 * time.Minute),
		NextAttemptAt: snapshotClock.Add(time.Minute),
	}}}

	m := openSnapshotChat(t, snapshotModel(t, f, Dependencies{
		AccountKey:      snapshotAccountKey,
		PendingMessages: source,
	}))
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))
	m.focus = FocusHistory

	return m
}

// snapshotActionSheet is the sheet of §13 over a message whose delivery is
// uncertain: the decision a user has to make, and the one screen that says
// what can be done about a message that may already have gone out.
func snapshotActionSheet(t *testing.T, f snapshotFixture) Model {
	t.Helper()

	m := snapshotDelivery(t, f, MessageDeliveryUncertain)

	// The cursor walks onto the message that is leaving and the sheet
	// opens over it, which is the whole of §13 as a user meets it.
	m, _ = updateModel(t, m, pressRunes("j"))
	m, _ = updateModel(t, m, pressRunes("a"))

	return m
}

// snapshotSearch is the search of §9 over the chat list with a query in it.
func snapshotSearch(t *testing.T, f snapshotFixture, query string) Model {
	t.Helper()

	m := snapshotConversation(t, f)
	m, _ = updateModel(t, m, pressRunes("/"))
	for _, letter := range query {
		m, _ = updateModel(t, m, pressRunes(string(letter)))
	}

	return m
}

// snapshotStatus is a conversation with one read of the status block of
// §4.3 delivered, which is where the presence, the connection and the queue
// counts are said.
func snapshotStatus(
	t *testing.T,
	f snapshotFixture,
	summary StatusSummary,
) Model {
	t.Helper()

	source := &summarySource{summary: summary}
	m := openSnapshotChat(t, snapshotModel(t, f, Dependencies{
		AccountKey:      snapshotAccountKey,
		StatusSummaries: source,
	}))
	m, _ = updateModel(t, m, m.statusRefresh(t, source))

	return m
}

// snapshotEnqueueError is the inline error of §12.1: a message the queue
// refused, with the draft still in the composer and the reason that stays
// out of the screen.
func snapshotEnqueueError(t *testing.T, f snapshotFixture) Model {
	t.Helper()

	m := openSnapshotChat(t, snapshotModel(t, f, Dependencies{
		AccountKey: snapshotAccountKey,
		MessageSubmitter: &recordingSubmitter{
			err: errors.New("queue message: the outbox is closed"),
		},
	}))
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes(snapshotDraft))

	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Enter did not submit anything")
	}
	m, _ = updateModel(t, m, cmd())

	if m.sendState != sendStateError {
		t.Fatalf("sendState = %v, want the error state", m.sendState)
	}

	return m
}

// snapshotSendingPaused is the composer whose delivery cannot work, which
// is how a user meets telecli on a machine with no keychain (§18 and
// decision 3): the reason is on the screen before anything is pressed.
func snapshotSendingPaused(t *testing.T, f snapshotFixture) Model {
	t.Helper()

	return openSnapshotChat(t, snapshotModel(t, f, Dependencies{
		AccountKey: snapshotAccountKey,
		SendError:  errSendingPausedFixture,
	}))
}

// ---- the screens ----

// snapshotScreen is one screen of the interface and the test that owns its
// golden file. The name is the name of the test, so the file in testdata
// and the test that checks it cannot drift apart without a test noticing.
type snapshotScreen struct {
	test  string
	build func(t *testing.T) Model
}

// snapshotScreens is every screen a snapshot is kept of: the ones §21
// reserves a name for, and the ones the other tasks of PR-10A added.
func snapshotScreens() []snapshotScreen {
	return []snapshotScreen{
		{"TestSnapshotWideChatListAndConversation", func(t *testing.T) Model {
			return snapshotConversation(t, wide(theme.ProfileTrueColor))
		}},
		{"TestSnapshotMediumChatListAndConversation", func(t *testing.T) Model {
			return snapshotConversation(t, medium(theme.ProfileTrueColor))
		}},
		{"TestSnapshotNarrowChatList", func(t *testing.T) Model {
			return snapshotChatList(t, narrow(theme.ProfileTrueColor))
		}},
		{"TestSnapshotNarrowConversation", func(t *testing.T) Model {
			m := snapshotConversation(t, narrow(theme.ProfileTrueColor))
			m.focus = FocusComposer

			return m
		}},
		{"TestSnapshotDeliveryQueued", func(t *testing.T) Model {
			return snapshotDelivery(t, conversation(theme.ProfileTrueColor), MessageDeliveryQueued)
		}},
		{"TestSnapshotDeliverySending", func(t *testing.T) Model {
			return snapshotDelivery(t, conversation(theme.ProfileTrueColor), MessageDeliverySending)
		}},
		{"TestSnapshotDeliveryRetrying", func(t *testing.T) Model {
			return snapshotDelivery(t, conversation(theme.ProfileTrueColor), MessageDeliveryRetrying)
		}},
		{"TestSnapshotDeliverySent", func(t *testing.T) Model {
			return snapshotDelivery(t, conversation(theme.ProfileTrueColor), MessageDeliverySent)
		}},
		{"TestSnapshotDeliveryFailed", func(t *testing.T) Model {
			return snapshotDelivery(t, conversation(theme.ProfileTrueColor), MessageDeliveryFailed)
		}},
		{"TestSnapshotDeliveryUncertain", func(t *testing.T) Model {
			return snapshotDelivery(t, conversation(theme.ProfileTrueColor), MessageDeliveryUncertain)
		}},
		{"TestSnapshotDeliveryCanceled", func(t *testing.T) Model {
			return snapshotDelivery(t, conversation(theme.ProfileTrueColor), MessageDeliveryCanceled)
		}},
		{"TestSnapshotActionSheetUncertain", func(t *testing.T) Model {
			return snapshotActionSheet(t, wide(theme.ProfileTrueColor))
		}},
		{"TestSnapshotEnqueueErrorKeepsDraft", func(t *testing.T) Model {
			return snapshotEnqueueError(t, conversation(theme.ProfileTrueColor))
		}},
		{"TestSnapshotThemeCatppuccin", func(t *testing.T) Model {
			return snapshotThemed(t, theme.ThemeCatppuccinMocha)
		}},
		{"TestSnapshotThemeTokyoNight", func(t *testing.T) Model {
			return snapshotThemed(t, theme.ThemeTokyoNightStorm)
		}},
		{"TestSnapshotThemeGruvbox", func(t *testing.T) Model {
			return snapshotThemed(t, theme.ThemeGruvboxDark)
		}},
		{"TestSnapshotNoColor", func(t *testing.T) Model {
			return snapshotThemed(t, "")
		}},
		{"TestSnapshotANSI256", func(t *testing.T) Model {
			return snapshotFallback(t, theme.ProfileANSI256)
		}},
		{"TestSnapshotANSI16", func(t *testing.T) Model {
			return snapshotFallback(t, theme.ProfileANSI16)
		}},
		{"TestSnapshotSearchMatches", func(t *testing.T) Model {
			return snapshotSearch(t, wide(theme.ProfileTrueColor), "release")
		}},
		{"TestSnapshotSearchWithoutMatches", func(t *testing.T) Model {
			return snapshotSearch(t, wide(theme.ProfileTrueColor), "quarterly")
		}},
		{"TestSnapshotStatusWithPresence", func(t *testing.T) Model {
			return snapshotStatus(t, wide(theme.ProfileTrueColor), StatusSummary{
				Connection: ConnectionReady,
				Queue:      QueueSummary{Known: true, Queued: 2, Retrying: 1},
				Presence: Presence{
					Kind:      PresenceUser,
					ExpiresAt: snapshotClock.Add(2 * time.Hour),
				},
			})
		}},
		{"TestSnapshotSendingPaused", func(t *testing.T) Model {
			return snapshotSendingPaused(t, wide(theme.ProfileTrueColor))
		}},
		{"TestSnapshotWidthGrapheme", func(t *testing.T) Model {
			return snapshotDifficultNames(t, termwidth.ModeGrapheme)
		}},
		{"TestSnapshotWidthCodepoint", func(t *testing.T) Model {
			return snapshotDifficultNames(t, termwidth.ModeCodepoint)
		}},
		{"TestSnapshotGroupWithAuthors", func(t *testing.T) Model {
			return snapshotGroup(t, wide(theme.ProfileTrueColor))
		}},
		{"TestSnapshotChannelAlbum", func(t *testing.T) Model {
			return snapshotChannel(t, wide(theme.ProfileTrueColor))
		}},
		{"TestSnapshotShortFeedAboveComposer", func(t *testing.T) Model {
			return snapshotShortFeed(t, wide(theme.ProfileTrueColor))
		}},
	}
}

// snapshotGroup is a conversation with several people in it: every message
// names whoever sent it, and two of them are in different colours. It is a
// screen the other snapshots cannot stand in for, because a private chat
// has one author colour and says nothing about the others.
func snapshotGroup(t *testing.T, f snapshotFixture) Model {
	t.Helper()

	f.chats = []Chat{{
		ID:      snapshotChatID,
		Title:   "Release Room",
		Kind:    ChatKindGroup,
		Preview: "the tag is pushed",
		Time:    "12:09",
		// A page as TDLib answers it, newest first; the model reverses
		// it. See snapshotMessages.
		Messages: []Message{
			{
				ID: 3, Outgoing: true, Text: "Thank you both",
				Time: "12:06",
			},
			{
				ID: 2, Text: "I will take the release notes",
				Time: "12:04", Author: "Boris", AuthorID: 34,
			},
			{
				ID: 1, Text: "the build is green again", Time: "12:02",
				Author: "Marta", AuthorID: 21,
			},
		},
	}}

	m := snapshotModel(t, f, Dependencies{AccountKey: snapshotAccountKey})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:    snapshotChatID,
		operation: m.historyOperation,
		page:      HistoryPage{Messages: f.chats[0].Messages},
	})

	return m.scrollToNewest()
}

// snapshotChannel is a channel: the author of every message is the channel
// itself, and the unread badge of the row is the muted one rather than the
// accent, because a hundred unread messages in a channel is background.
func snapshotChannel(t *testing.T, f snapshotFixture) Model {
	t.Helper()

	f.chats = []Chat{{
		ID:      snapshotChatID,
		Title:   "Xiaomi News",
		Kind:    ChatKindChannel,
		Unread:  12,
		Preview: "[2 photos] the new wallpaper",
		Time:    "12:07",
		// A page as TDLib answers it, newest first; the model reverses
		// it. See snapshotMessages.
		Messages: []Message{
			{
				ID: 2, Media: "photo", AlbumID: 9, Time: "12:05",
				Author: "Xiaomi News", AuthorID: 900,
			},
			{
				ID: 1, Media: "photo", AlbumID: 9, Time: "12:05",
				Author: "Xiaomi News", AuthorID: 900,
			},
		},
	}}

	m := snapshotModel(t, f, Dependencies{AccountKey: snapshotAccountKey})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:    snapshotChatID,
		operation: m.historyOperation,
		page:      HistoryPage{Messages: f.chats[0].Messages},
	})

	return m.scrollToNewest()
}

// snapshotShortFeed is a conversation with fewer messages than the feed has
// rows for: they are on the rows directly above the composer and the empty
// rows are above them.
func snapshotShortFeed(t *testing.T, f snapshotFixture) Model {
	t.Helper()

	m := snapshotModel(t, f, Dependencies{AccountKey: snapshotAccountKey})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:    snapshotChatID,
		operation: m.historyOperation,
		page: HistoryPage{Messages: []Message{{
			ID: 1, Text: "the build is green again", Time: "12:02",
			Author: "Anna", AuthorID: 5,
		}}},
	})
	m = m.scrollToNewest()
	m.focus = FocusHistory

	return m
}

// snapshotDifficultNames is the same screen drawn in each of the two rules.
//
// They are two screens and not one screen drawn twice: a title that is cut
// in one of them and whole in the other is the difference the rules make,
// and a golden file of each is the only way a reviewer can see it without a
// terminal that behaves like the one it was made on.
func snapshotDifficultNames(t *testing.T, mode termwidth.Mode) Model {
	t.Helper()

	f := wide(theme.ProfileTrueColor)
	f.widthMode = mode
	f.chats = snapshotWidthChats()

	m := openSnapshotChat(t, snapshotModel(t, f, Dependencies{
		AccountKey: snapshotAccountKey,
	}))
	m.focus = FocusChatList

	return m
}

// snapshotThemed is the wide conversation in a named theme, which is the
// only thing that differs between the theme snapshots.
//
// An empty name is the screen without colour rather than a theme called
// nothing: that is the golden this file has always been named for, and it
// was being drawn in full colour and labelled as if it were not.
func snapshotThemed(t *testing.T, name string) Model {
	t.Helper()

	profile := theme.ProfileTrueColor
	if name == "" {
		profile = theme.ProfileNoColor
	}

	return snapshotConversation(t, snapshotFixture{
		width:   snapshotWideWidth,
		height:  snapshotWideHeight,
		theme:   name,
		profile: profile,
	})
}

// snapshotFallback is the wide conversation on a terminal that cannot show
// the colours of the theme, which is the whole of §2.7: the same words, the
// same markers, fewer colours.
func snapshotFallback(t *testing.T, profile theme.Profile) Model {
	t.Helper()

	return snapshotConversation(t, wide(profile))
}

// snapshotScreenByName returns the screen a test owns, and fails rather
// than returning a zero screen if the name is not one of them: a test that
// checks nothing is worse than a test that fails.
func snapshotScreenByName(t *testing.T, test string) snapshotScreen {
	t.Helper()

	for _, screen := range snapshotScreens() {
		if screen.test == test {
			return screen
		}
	}

	t.Fatalf("no snapshot screen is named %q", test)

	return snapshotScreen{}
}

// ---- the tests §21 reserves the names for ----

// The two panes of a wide screen, which is the shape most of the interface
// is seen in.
func TestSnapshotWideChatListAndConversation(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotWideChatListAndConversation"))
}

// The same screen with the narrow list of §10.3: the same words in less
// room, and a snapshot is the only way to see what that cost.
func TestSnapshotMediumChatListAndConversation(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotMediumChatListAndConversation"))
}

// A narrow screen shows one region, so the chat list is the whole screen
// and the conversation is what the way back in the header is for.
func TestSnapshotNarrowChatList(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotNarrowChatList"))
}

func TestSnapshotNarrowConversation(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotNarrowConversation"))
}

// Every state of §6 as it is drawn under the text of its message, with the
// symbol and the word the state has. A state that loses its word, or gains
// a neighbouring one, is a state a user cannot read.
func TestSnapshotDeliveryQueued(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotDeliveryQueued"))
}

func TestSnapshotDeliverySending(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotDeliverySending"))
}

func TestSnapshotDeliveryRetrying(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotDeliveryRetrying"))
}

func TestSnapshotDeliverySent(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotDeliverySent"))
}

func TestSnapshotDeliveryFailed(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotDeliveryFailed"))
}

func TestSnapshotDeliveryUncertain(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotDeliveryUncertain"))
}

func TestSnapshotDeliveryCanceled(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotDeliveryCanceled"))
}

// The decision of §13, and the one screen where a message may already have
// been delivered has to be said before anything else.
func TestSnapshotActionSheetUncertain(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotActionSheetUncertain"))
}

// A refused message keeps its text, and the cause that would have a phone
// number in it stays off the screen.
func TestSnapshotEnqueueErrorKeepsDraft(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotEnqueueErrorKeepsDraft"))
}

// Three themes, one token model: the same words, painted three ways.
func TestSnapshotThemeCatppuccin(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotThemeCatppuccin"))
}

func TestSnapshotThemeTokyoNight(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotThemeTokyoNight"))
}

func TestSnapshotThemeGruvbox(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotThemeGruvbox"))
}

// Without colour the interface still has to say where the focus is and
// what the states are: this is the screen of a user with NO_COLOR set, and
// it is the reason the markers are characters and not attributes.
func TestSnapshotNoColor(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotNoColor"))
}

// The two fallbacks of §2.7, which are the same screen with the palette
// reduced to what the terminal has.
func TestSnapshotANSI256(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotANSI256"))
}

func TestSnapshotANSI16(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotANSI16"))
}

// The search of §9 with a query that found something, and with one that did
// not: the second is a screen of its own and it is not an error.
func TestSnapshotSearchMatches(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotSearchMatches"))
}

func TestSnapshotSearchWithoutMatches(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotSearchWithoutMatches"))
}

// The status line with the presence of the other side, the connection and
// the queue counts in one row, which is the whole of §4.3.
func TestSnapshotStatusWithPresence(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotStatusWithPresence"))
}

// A composer that cannot send says so before anything is pressed, and says
// it in the place the user is looking.
func TestSnapshotSendingPaused(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotSendingPaused"))
}

// The same screen with the names the two width rules disagree about, drawn
// in each of them. Every line of both is a whole number of columns in the
// rule that screen was drawn in, which is what §20 asks of a screen and
// what the rules exist to make possible.
func TestSnapshotWidthGrapheme(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotWidthGrapheme"))
}

func TestSnapshotWidthCodepoint(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotWidthCodepoint"))
}

// A group names every message by whoever sent it, and two senders are in
// two colours. A screen that cannot show that is a screen where a reader
// has to guess who said what.
func TestSnapshotGroupWithAuthors(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotGroupWithAuthors"))
}

// A channel, with an album of two photographs drawn as one entry and its
// unread badge in the muted step rather than the accent.
func TestSnapshotChannelAlbum(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotChannelAlbum"))
}

// A conversation with fewer messages than the feed has rows for: the
// messages sit on the rows above the composer and the empty rows are above
// them.
func TestSnapshotShortFeedAboveComposer(t *testing.T) {
	assertSnapshot(t, snapshotScreenByName(t, "TestSnapshotShortFeedAboveComposer"))
}

// ---- what every snapshot has to satisfy ----

// The invariants of §20, checked over every snapshot at once rather than
// one test at a time: a screen that grew a frame, or a second focus, or a
// line too wide for the terminal, is a screen the goldens would happily
// accept, and the goldens are not the place where that is noticed.
func TestSnapshotInvariants(t *testing.T) {
	for _, screen := range snapshotScreens() {
		t.Run(screen.test, func(t *testing.T) {
			m := screen.build(t)
			view := snapshotViewOf(t, screen, m)

			assertNoFrame(t, screen.test, view.plain)
			assertOnePanelRule(t, screen.test, view.plain)
			assertNoFocusGlyphsInRows(t, screen.test, view.plain)
			assertNoFaint(t, screen.test, view.profile, view.escaped)
			assertLinesFit(t, screen.test, view.widths, view.width, view.plain)
		})
	}
}

// A golden file nobody checks is a file that will drift and then be
// believed. A file with no test is the other kind of mistake: a screen
// whose test was renamed, leaving a golden behind that a new test would
// have to copy by hand.
func TestEverySnapshotFileBelongsToAScreen(t *testing.T) {
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		t.Fatalf("read %s: %v", snapshotDir, err)
	}

	known := map[string]bool{}
	for _, screen := range snapshotScreens() {
		known[snapshotFileName(screen.test)] = true
	}

	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("%s holds a directory: %s", snapshotDir, entry.Name())
		}
		if !known[entry.Name()] {
			t.Errorf("%s/%s is not the golden file of a screen", snapshotDir, entry.Name())
		}
	}
}

// The bytes of a snapshot are a property of the code and not of the shell
// the test runs under. A terminal that says it is a different one, or that
// refuses colour altogether, must not change a single byte of what the
// interface draws, or the goldens would only hold on the machine that wrote
// them.
func TestSnapshotsIgnoreTheTerminalEnvironment(t *testing.T) {
	for _, variable := range []string{
		"TERM",
		"COLORTERM",
		"NO_COLOR",
		"CLICOLOR",
		"CLICOLOR_FORCE",
	} {
		t.Setenv(variable, "dumb")
	}

	for _, name := range []string{
		"TestSnapshotWideChatListAndConversation",
		"TestSnapshotNoColor",
		"TestSnapshotANSI256",
	} {
		screen := snapshotScreenByName(t, name)
		view := snapshotViewOf(t, screen, screen.build(t))
		want := readSnapshotLines(t, name)
		got := snapshotFileLines(view)
		if sameSnapshotLines(want, got) {
			continue
		}

		t.Fatalf(
			"%s changed with the environment:\n%s",
			name,
			snapshotDiff(t, view, want, got),
		)
	}
}

// ---- the harness ----

// snapshotView is one screen as a snapshot keeps it: the size it was drawn
// at, the text of it without escape sequences, and the escape sequences
// themselves line by line.
type snapshotView struct {
	test    string
	width   int
	height  int
	theme   string
	profile string

	// widths is the rule the screen was drawn in. It travels with the
	// view because the invariants below are about columns: a line that is
	// too wide in the rule the screen was drawn in is a line the terminal
	// will wrap, and a line that is too wide in the other one is a line
	// that looks broken to nobody.
	widths  termwidth.WidthModel
	plain   []string
	escaped []string
}

// snapshotViewOf draws the screen and takes it apart into what a golden file
// holds.
func snapshotViewOf(t *testing.T, screen snapshotScreen, m Model) snapshotView {
	t.Helper()

	view := m.View()
	lines := strings.Split(view, "\n")

	text := make([]string, 0, len(lines))
	escaped := make([]string, 0, len(lines))
	for _, line := range lines {
		text = append(text, plain(line))
		escaped = append(escaped, fmt.Sprintf("%q", line))
	}

	return snapshotView{
		test:    screen.test,
		width:   m.width,
		height:  m.height,
		theme:   m.theme.Name,
		profile: m.colorProfile.String(),
		widths:  m.widths,
		plain:   text,
		escaped: escaped,
	}
}

// assertSnapshot compares the screen with its golden file, or rewrites the
// file when the run was asked to.
func assertSnapshot(t *testing.T, screen snapshotScreen) {
	t.Helper()

	view := snapshotViewOf(t, screen, screen.build(t))
	path := filepath.Join(snapshotDir, snapshotFileName(screen.test))
	lines := snapshotFileLines(view)

	if *snapshotUpdate {
		if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
			t.Fatalf("create %s: %v", snapshotDir, err)
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}

		return
	}

	want := readSnapshotLines(t, screen.test)
	if sameSnapshotLines(want, lines) {
		return
	}

	t.Fatalf(
		"%s\n\nto accept the change:\n\n\tgo test ./internal/tui -run Snapshot -update\n",
		snapshotDiff(t, view, want, lines),
	)
}

// snapshotFileLines is the content of a golden file: what the screen is,
// what it looked like, and how it was drawn.
func snapshotFileLines(view snapshotView) []string {
	lines := snapshotFileHeader(view)
	lines = append(lines, view.plain...)
	lines = append(lines,
		"#",
		"# Escaped: the same screen with its escape sequences, one line at a time.",
	)

	return append(lines, view.escaped...)
}

// snapshotFileHeader is what a golden file says about itself before it
// shows anything: which test owns it, and the size, the theme and the
// profile it was drawn at. A reader who wants to know why two snapshots of
// different screens are not the same file starts here.
func snapshotFileHeader(view snapshotView) []string {
	return []string{
		"# golden screen snapshot",
		"#",
		"# test:    " + view.test,
		fmt.Sprintf("# size:    %dx%d", view.width, view.height),
		"# theme:   " + view.theme,
		"# profile: " + view.profile,
		"# width:   " + view.widths.Mode().String(),
		"#",
		"# Plain text: the screen as a user reads it, without escape sequences.",
	}
}

// snapshotFileName is the golden file of a test. The name of the test is
// the name of the file, so that the file and the test that owns it are
// found by the same search.
func snapshotFileName(test string) string {
	return test + ".txt"
}

// readSnapshotLines returns the golden file of a test, and says how to
// write one when there is none.
func readSnapshotLines(t *testing.T, test string) []string {
	t.Helper()

	path := filepath.Join(snapshotDir, snapshotFileName(test))
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf(
				"%s does not exist; write it with:\n\n\tgo test ./internal/tui -run %s -update",
				path,
				test,
			)
		}

		t.Fatalf("read %s: %v", path, err)

		return nil
	}

	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

// sameSnapshotLines reports whether two golden files are the same bytes.
func sameSnapshotLines(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}

	for index := range want {
		if want[index] != got[index] {
			return false
		}
	}

	return true
}

// snapshotDiff says which lines of a golden file differ, with both sides.
//
// A golden file is read by a person deciding whether to accept a change, so
// the report is a list of the lines that moved, which part of the file they
// are in, and what they say now. The plain half of the file is quoted so
// that a stray escape sequence and a lost trailing space are both visible;
// the escaped half is already one quoted string per line, and quoting it
// twice would bury the one escape sequence that changed.
func snapshotDiff(
	t *testing.T,
	view snapshotView,
	want []string,
	got []string,
) string {
	t.Helper()

	var report strings.Builder
	fmt.Fprintf(
		&report,
		"%s/%s does not match the screen:\n",
		snapshotDir,
		snapshotFileName(view.test),
	)

	differing := 0
	longest := maxInt(len(want), len(got))
	for index := 0; index < longest; index++ {
		if index < len(want) && index < len(got) && want[index] == got[index] {
			continue
		}

		differing++
		if differing > maxSnapshotDiffLines {
			fmt.Fprintf(
				&report,
				"  ... and %d more lines differ\n",
				longest-index,
			)

			break
		}

		fmt.Fprintf(&report, "  %s line %d\n", snapshotSectionOf(index, view), index+1)
		fmt.Fprintf(&report, "    - %s\n", snapshotLine(want, index, view))
		fmt.Fprintf(&report, "    + %s\n", snapshotLine(got, index, view))
	}

	if differing == 0 {
		return report.String() + "  the two are the same lines\n"
	}

	fmt.Fprintf(&report, "  %d of %d lines differ\n", differing, longest)

	return report.String()
}

// maxSnapshotDiffLines is how many differences a report names. A screen
// that changed entirely has one difference per line, and thirty of them say
// everything a reader needs; the rest is noise in a terminal.
const maxSnapshotDiffLines = 30

// snapshotSectionOf names the part of the golden file a line belongs to, so
// that a difference is reported against the half of the file a reader is
// looking at.
//
// The parts are counted from the screen that was drawn, which is the one
// whose shape is known. A golden file that has drifted that far is a file
// that has to be written again, and the header names the difference.
func snapshotSectionOf(index int, view snapshotView) string {
	switch {
	case index < len(snapshotFileHeader(view)):
		return "header"
	case index < len(snapshotFileHeader(view))+len(view.plain):
		return "plain"
	default:
		return "escaped"
	}
}

// snapshotLine is one side of a difference: the line as the golden file has
// it, quoted where the file does not quote it already, or a marker where
// the file has no such line.
func snapshotLine(lines []string, index int, view snapshotView) string {
	if index >= len(lines) {
		return "(no such line)"
	}

	if snapshotSectionOf(index, view) == "escaped" {
		return lines[index]
	}

	return fmt.Sprintf("%q", lines[index])
}

// ---- the invariants of §20, over every snapshot ----

// assertNoFrame fails if the screen draws a box around anything.
//
// §1 forbids the frames outright, and §20 asks for two of the shapes by
// name: an ASCII table border and a box-drawn one. A frame is a line made
// of nothing but frame characters, which is what a border is and is not
// what a message is, so the check cannot be fooled by a message that
// happens to contain a dash.
func assertNoFrame(t *testing.T, test string, lines []string) {
	t.Helper()

	for index, line := range lines {
		for _, glyph := range boxDrawing {
			if strings.ContainsRune(line, glyph) {
				t.Errorf("%s: line %d draws the box glyph %q", test, index+1, glyph)
			}
		}

		if isFrameLine(line) {
			t.Errorf("%s: line %d is a frame: %q", test, index+1, line)
		}
	}
}

// asciiFrame is every character a plain text table is drawn with.
const asciiFrame = "+-|="

// isFrameLine reports whether a line is made of nothing but frame
// characters and spaces, which is what a border is and nothing else.
func isFrameLine(line string) bool {
	marked := strings.TrimSpace(line)
	if marked == "" {
		return false
	}

	for _, glyph := range marked {
		if !strings.ContainsRune(asciiFrame, glyph) {
			return false
		}
	}

	return true
}

// assertOnePanelRule fails unless the screen marks its focus in exactly one
// place, and that place is a rule under a heading.
//
// §5.2 is one focused region at a time, and the mark of a region is a
// rule under its header. A line of focus down the side of a list and a
// line of focus down the side of a timeline are two columns of the screen
// that a user reads as text, and a screen with both is a screen where
// nothing says where the keys are.
func assertOnePanelRule(t *testing.T, test string, lines []string) {
	t.Helper()

	rules := 0
	for _, line := range lines {
		if strings.Contains(line, focusRuleGlyph) {
			rules++
		}
	}

	switch {
	case rules > 1:
		t.Errorf("%s: %d panel rules, want at most one", test, rules)
	case rules == 0:
		// A composer-only screen has no header to put a rule under, and a
		// screen with a popup over it has given the mark to the popup.
		if strings.Contains(strings.Join(lines, "\n"), composerPlaceholder) {
			return
		}

		t.Errorf("%s: no rule under any heading", test)
	}
}

// assertNoFaint fails if a screen with colour asks the terminal to make a
// run faint.
//
// Faint is the terminal multiplying a colour it was given by something of
// its own, and by how much is not a number anybody chose: the Apple
// Terminal takes a muted colour down to about 2:1. The theme package
// measures the contrast of a colour against a background and the
// contrast test holds the muted tier to 4.5:1, and a terminal that dims
// it afterwards has undone exactly that. A tier is dimmer because its
// colour is dimmer.
//
// The no-colour profile is exempt, and always was: there is no colour
// there to be faint about, and termenv prints no attributes under it in
// any case.
func assertNoFaint(t *testing.T, test, profile string, escaped []string) {
	t.Helper()

	if profile == theme.ProfileNoColor.String() {
		return
	}

	for index, line := range escaped {
		if !asksForFaint(line) {
			continue
		}

		t.Errorf(
			"%s: row %d asks the terminal for a faint run: %s",
			test,
			index+1,
			line,
		)
	}
}

// faintSGR is the SGR parameter that makes a run faint.
const faintSGR = "2"

// asksForFaint reports whether a rendered line asks the terminal for a
// faint run anywhere in it.
//
// The SGR sequences are read one at a time rather than searched for as
// text: "38;2;..." is a true-colour foreground and not a faint, and a
// search for ";2;" says otherwise about every coloured run on the screen.
func asksForFaint(line string) bool {
	for rest := line; rest != ""; {
		start := strings.Index(rest, "\x1b[")
		if start < 0 {
			return false
		}

		rest = rest[start+len("\x1b["):]

		end := strings.Index(rest, "m")
		if end < 0 {
			return false
		}

		for _, parameter := range strings.Split(rest[:end], ";") {
			if parameter == faintSGR {
				return true
			}
		}

		rest = rest[end+1:]
	}

	return false
}

// assertLinesFit fails if any line of the screen is wider than the terminal
// it is drawn for.
//
// A line one column over wraps where it should not and pushes the composer
// off the screen, and on a narrow screen it is the difference between a
// message a user can read and a message cut in half.
func assertLinesFit(
	t *testing.T,
	test string,
	widths termwidth.WidthModel,
	width int,
	lines []string,
) {
	t.Helper()

	for index, line := range lines {
		if got := widths.StringWidth(line); got > width {
			t.Errorf("%s: line %d is %d columns, want at most %d: %q", test, index+1, got, width, line)
		}
	}
}
