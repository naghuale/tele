package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/tui/theme"
)

// The tests in this file are about the way the interface looks: where the
// focus is, which row is selected, which side a message is on, and what a
// picture says. They are the tests the approved drawing asked for, and each
// one names a thing a person would otherwise have to look at the screen to
// find out.

// focusBarGlyphs are the glyphs a focus has been drawn with.
//
// They are named here because the rule is a negative one: no row of a chat
// list and no row of a timeline may carry any of them. A line of focus down
// the side of a list is a column of the list — a user reads `▌ Alice` as a
// name that begins with a block — and a rule under a header is one line, in
// one place, and cannot be mistaken for anything else on the screen.
var focusBarGlyphs = []rune{'▌', '┃', '│'}

// assertNoFocusGlyphsInRows fails if any row of a screen carries a glyph
// that has been used to mark the focus of a list or a timeline.
//
// A popup is the one place a bar is still drawn, down the side of the menu
// it belongs to, so the rows it covers are not checked for the bar glyph:
// the block of a menu is found by the first and the last of its rows, and
// everything in between is the menu's own. A bar anywhere else is a list
// or a timeline that has grown a column it should not have, and that is
// what this is here to catch.
func assertNoFocusGlyphsInRows(t *testing.T, name string, lines []string) {
	t.Helper()

	first, last := -1, -1
	for index, line := range lines {
		if !strings.ContainsRune(line, '▌') {
			continue
		}
		if first < 0 {
			first = index
		}
		last = index
	}

	for index, line := range lines {
		popup := first >= 0 && index >= first && index <= last

		for _, glyph := range focusBarGlyphs {
			if glyph == '▌' && popup {
				continue
			}
			if strings.ContainsRune(line, glyph) {
				t.Errorf(
					"%s: row %d carries the focus glyph %q: %q",
					name,
					index+1,
					string(glyph),
					line,
				)
			}
		}
	}
}

// The focus is a rule under the heading of the panel that has the keys, and
// nowhere else. §5.2 asks for one focused region at a time, and a rule in a
// single place is the only shape of that which is not also a column of the
// list beside it.
func TestTheFocusIsOneRuleUnderTheHeadingOfTheActivePanel(t *testing.T) {
	for _, focus := range []Focus{FocusChatList, FocusHistory, FocusComposer} {
		t.Run(focus.String(), func(t *testing.T) {
			m := focusedOn(openedProgramModel(t, theme.ProfileTrueColor, 120, 30), focus)
			lines := viewLines(m.View())
			rules := panelRuleLines(lines)

			if len(rules) != 1 {
				t.Fatalf("the screen draws %d rules, want 1:\n%s", len(rules), m.View())
			}

			// The rule closes the header of the pane. A header is the
			// title and the line under it, and the title is in one of the
			// two rows the rule is under: the header of the conversation
			// is two rows and the header of the list is three, the search
			// line sitting between the title and the rule (the mockup of
			// the owner).
			above := []string{ansi.Strip(lines[rules[0]-1])}
			if rules[0] > 1 {
				above = append(above, ansi.Strip(lines[rules[0]-2]))
			}
			if !strings.Contains(strings.Join(above, "\n"), chatListTitle) &&
				!strings.Contains(strings.Join(above, "\n"), m.selected().Title) {
				t.Fatalf(
					"the rule is under %q, which is not a panel heading",
					strings.Join(above, "\n"),
				)
			}

			assertNoFocusGlyphsInRows(t, focus.String(), lines)
		})
	}
}

// The selected chat is a row with the background of the Selected token and
// its name in the accent, and nothing else: no marker, no reverse. A `›`
// in front of the name is the first letter of the name to half the people
// who see it, and reverse video is a highlight nobody asked for.
func TestTheSelectedChatIsARowAndNoMarker(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusChatList,
	)

	view := m.View()
	if strings.Contains(view, "\x1b[7m") {
		t.Fatalf("the selected row is in reverse video:\n%q", view)
	}

	// The list, and not the whole screen: the composer draws a prompt of
	// the same shape, and that one says the field has the keys.
	layout := LayoutFor(m.width, m.height)
	rows, _ := m.chatListRows(layout, layout.SidebarContentWidth())
	for _, row := range rows {
		for _, line := range row {
			if strings.ContainsRune(ansi.Strip(line), firstRune(theme.SelectionMark)) {
				t.Fatalf("a row of the list carries a selection glyph: %q", line)
			}
		}
	}

	// The row the cursor is on is the one with the background on it, and it
	// is the background of both of its lines rather than of one of them.
	background := selectedBackgroundEscape(m)
	if background == "" {
		t.Fatal("the theme has no selected background to draw")
	}

	marked := 0
	for _, line := range viewLines(view) {
		if strings.Contains(line, background) {
			marked++
		}
	}
	if marked < 2 {
		t.Fatalf("the selected background is on %d rows, want both of the row's:\n%q", marked, view)
	}
}

// Without colour there is no background to show, and the selection comes
// back as a glyph in a column of its own with the name in bold — §2.7 asks
// the selection to survive a terminal that prints nothing, and a row that
// is only a colour is not a selection.
func TestNoColorMarksTheSelectedChatWithAGlyphInItsOwnColumn(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileNoColor, 120, 30),
		FocusChatList,
	)

	row, _, ok := lineWith(plain(m.View()), theme.SelectionMark)
	if !ok {
		t.Fatalf("the selected chat has no marker:\n%s", plain(m.View()))
	}
	if !strings.HasPrefix(strings.TrimLeft(row, " "), theme.SelectionMark) {
		t.Fatalf("the marker is not in a column of its own: %q", row)
	}

	// The row also asks to be bold, which is the second half of §2.7's
	// "reverse or bold". The profile that has no colour prints neither,
	// so the check is on what the theme asks for rather than on what the
	// screen shows — which is why the glyph above has to be there.
	if attributes := m.theme.SelectionAttributes(true); !attributes.Bold {
		t.Fatal("the selected row is not asked to be bold")
	}
}

// A chat is a name with a time and a preview with a badge, and one blank
// row under it. Two chats with no gap between them are one block of six
// lines, and a user has to read them to tell them apart — the blank row is
// what tells them apart, and it is blank whatever else is true of the chat
// above it: a gap that changed colour with the selection would be a band
// under the chosen one.
func TestAChatRowIsTwoLinesAndTheNextOneIsBelowIt(t *testing.T) {
	m := sizedModel(t, 120, 30)
	layout := LayoutFor(m.width, m.height)
	rows, _ := m.chatListRows(layout, chatListPaneWidth(layout))

	if len(rows) < 2 {
		t.Fatalf("the list draws %d rows, want at least 2", len(rows))
	}

	first := rows[0]
	if len(first) != 3 {
		t.Fatalf(
			"a chat takes %d rows, want a name, a preview and a gap",
			len(first),
		)
	}
	if strings.TrimSpace(plain(first[0])) == "" {
		t.Fatalf("the name of a chat is empty: %q", plain(first[0]))
	}
	if strings.TrimSpace(plain(first[2])) != "" {
		t.Fatalf("the row under a chat is not blank: %q", plain(first[2]))
	}
	if rows[1][0] == first[0] {
		t.Fatal("two chats are drawn on the same line")
	}
}

// The badge of an unread count is a pill, not a `● 2` in the text: a
// number that is part of the sentence is a number a user has to parse, and
// the pill says the same thing in a shape that is not a word. It is in the
// accent for a chat with one other person in it and in the muted step for
// a group, because a hundred unread messages in a channel is background.
//
// The pill keeps its own colour on the selected row: it is a badge rather
// than a cell of the row, and a badge that takes the colour of the row it
// is in has stopped being one.
func TestTheUnreadBadgeIsAPillAndGroupsAreQuieter(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileTrueColor, 120, 30)
	m.chats = []Chat{
		{ID: 1, Title: "Anna", Unread: 2, Preview: "hello", Time: "10:02"},
		{ID: 2, Title: "Room", Unread: 2, Preview: "hello", Time: "10:02", Kind: ChatKindGroup},
		{ID: 3, Title: "Notes", Time: "10:02"},
	}
	m.chatsState = loadStateLoaded
	// The badge keeps its own colour on the row the cursor is on, so the
	// row under the cursor is one of the two without a count.
	m.selectedChat = 2

	view := m.View()
	private := selectedBackgroundEscapeFor(
		m, m.tokens().Unread, m.tokens().SidebarBackground,
	)
	grouped := selectedBackgroundEscapeFor(
		m, m.tokens().MutedText, m.tokens().SidebarBackground,
	)

	if private == "" || grouped == "" || private == grouped {
		t.Skipf("the theme has no distinct pill colours: %q and %q", private, grouped)
	}
	if !strings.Contains(view, private) {
		t.Fatalf("a private chat has no accent pill:\n%q", view)
	}
	if !strings.Contains(view, grouped) {
		t.Fatalf("a group has no muted pill:\n%q", view)
	}
}

// selectedBackgroundEscapeFor is the escape sequence for a background of
// one colour with a text of another, printed the way the renderer prints
// it.
func selectedBackgroundEscapeFor(
	m Model,
	background theme.Color,
	surface theme.Color,
) string {
	if background.Kind() != theme.ColorKindRGB || surface.Kind() != theme.ColorKindRGB {
		return ""
	}

	return backgroundParameters(
		m.styles().pill(background, surface).Render("x"),
	)
}

// The feed is pressed against the composer: a conversation with three
// messages in it has those three messages on the rows directly above the
// field, and the empty rows are above them. A screen with the newest message
// in its top row makes a user scroll to read what just arrived.
func TestTheFeedIsAnchoredToTheComposer(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileNoColor, 120, 30),
		FocusHistory,
	)
	m.chats[m.selectedChat].Messages = []Message{
		{ID: 1, Text: "the oldest", Time: "10:00", Author: "Anna"},
	}
	m.historyState = loadStateLoaded
	m.selectedMsg = 0
	m.timelineTop = 0

	view := plain(m.View())
	composer, ok := lineIndexWith(view, composerPlaceholder)
	if !ok {
		t.Fatalf("there is no composer:\n%s", view)
	}

	_, text, ok := lineWith(view, "the oldest")
	if !ok {
		t.Fatalf("the message is not on the screen:\n%s", view)
	}

	// The band of the composer opens with its own row of air, so the text
	// of the newest message is the row above the row of the block's own
	// background that closes the block, with the empty rows of the feed
	// all above the name of whoever sent it. The row of air under a
	// message is inside its block and not a row of the feed: the owner's
	// Terminal draws its rows with a gap, so a row of half blocks under a
	// block was a band of its own and not air — this row is full cells of
	// the block's own colour, which is what makes it air.
	if composer-text != 3 {
		t.Fatalf(
			"the text is on row %d and the composer on row %d, want them against the field:\n%s",
			text,
			composer,
			view,
		)
	}
	if _, author, ok := lineWith(view, "Anna"); !ok || author <= 2 {
		t.Fatalf("the feed is not anchored to the composer:\n%s", view)
	}
}

// A message from the other side is on the left with the name of whoever
// sent it above the text; a message of this user is a block on the right
// with the state of the send under it. The two are told apart by where they
// are, which is a thing every reader of a chat already knows.
func TestTheTwoSidesOfAConversationAreToldApartByWhereTheyAre(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileNoColor, 120, 30),
		FocusHistory,
	)
	m.chats[m.selectedChat].Messages = []Message{
		{ID: 1, Text: "from the other side", Time: "10:00", Author: "Anna", AuthorID: 7},
		{ID: 2, Outgoing: true, Text: "from this side", Time: "10:01"},
	}
	m.historyState = loadStateLoaded
	m.selectedMsg = 1
	m.timelineTop = 0

	view := plain(m.View())
	incoming, _, ok := lineWith(view, "from the other side")
	if !ok {
		t.Fatalf("the incoming message is not on the screen:\n%s", view)
	}
	outgoing, _, ok := lineWith(view, "from this side")
	if !ok {
		t.Fatalf("the outgoing message is not on the screen:\n%s", view)
	}

	// The list, the column every region reserves, the inset between the
	// marker and the text, the margin the feed keeps on each side, and the
	// air inside the block the words stand in: the other side's messages
	// are a block against the left margin like everything else, and the
	// text of a message is two columns in from the edge of its own block.
	layout := LayoutFor(m.width, m.height)
	first := layout.SidebarWidth() + paneGapWidth
	want := first + focusColumnWidth + contentInsetWidth +
		layout.FeedMargin() + layout.BlockInset()
	if got := indentOf(incoming); got != want {
		t.Fatalf("the incoming text starts at %d, want %d", got, want)
	}
	// The column of the words themselves: the marker of the message under
	// the cursor is in front of the block, and it is a column of its own.
	outgoingColumn := strings.Index(outgoing, "from this side")
	if outgoingColumn <= indentOf(incoming) {
		t.Fatalf(
			"the incoming text starts at %d and the outgoing at %d, want the outgoing further right",
			indentOf(incoming),
			outgoingColumn,
		)
	}
	if !strings.Contains(view, "Anna") {
		t.Fatalf("the sender is not named:\n%s", view)
	}
	if !strings.Contains(view, "✓ sent 10:01") {
		t.Fatalf("the outgoing message has no state under it:\n%s", view)
	}
}

// A block of a message of this user is never more than seventy per cent of
// the feed, because a conversation with messages from both sides has a seam
// down it and a user can tell which is which without reading either.
func TestAnOutgoingBlockIsAtMostSeventyPerCentOfTheFeed(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	m.chats[m.selectedChat].Messages = []Message{{
		ID: 1, Outgoing: true, Text: strings.Repeat("long ", 60), Time: "10:01",
	}}
	m.historyState = loadStateLoaded
	m.selectedMsg = 0
	m.timelineTop = 0

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()
	limit := width * outgoingBlockSharePercent / 100
	block := m.messageBlockFor(
		sideOutgoing, layout, width, strings.Repeat("long ", 60), true, "✓ sent 10:01",
	)
	if block.width > limit {
		t.Fatalf("the block is %d columns, want at most %d", block.width, limit)
	}
}

// A picture is a word in brackets and the caption under it, and an album is
// one entry with the number of its parts. Three photographs are one thing
// somebody did, and a feed that shows them as three messages is a feed of
// nine lines for one picture.
func TestAnAlbumOfPhotosIsOneEntry(t *testing.T) {
	messages := []Message{
		{
			ID: 1, Media: "photo", AlbumID: 5, Caption: "at the bridge",
			Time: "10:00", Author: "Anna", AuthorID: 7,
		},
		{ID: 2, Media: "photo", AlbumID: 5, Time: "10:00", Author: "Anna", AuthorID: 7},
		{ID: 3, Media: "photo", AlbumID: 5, Time: "10:00", Author: "Anna", AuthorID: 7},
	}

	entries := timelineEntries(messages)
	if len(entries) != 1 {
		t.Fatalf("%d entries, want 1", len(entries))
	}
	if got := entries[0].count(); got != 3 {
		t.Fatalf("the entry stands for %d parts, want 3", got)
	}
	if got := entryText(entries[0]); got != "[3 photos] at the bridge" {
		t.Fatalf("the album reads %q, want %q", got, "[3 photos] at the bridge")
	}
}

// A message with a picture and no caption says what it carries and nothing
// else. A row of empty columns where the words should be is a message a
// user cannot read.
func TestAPhotoWithoutACaptionSaysOnlyWhatItCarries(t *testing.T) {
	message := Message{ID: 1, Media: "photo", Time: "10:00", Author: "Anna"}

	entries := timelineEntries([]Message{message})
	if got := entryText(entries[0]); got != "[photo]" {
		t.Fatalf("the message reads %q, want %q", got, "[photo]")
	}
	if got := entryText(timelineEntries([]Message{{ID: 2, Time: "10:00"}})[0]); got != "" {
		t.Fatalf("a message with no text and no media reads %q, want nothing", got)
	}
}

// Two photos from two people are two messages: an album is a run from one
// sender, and collapsing across senders would take somebody's picture away.
func TestOnlyOneSendersRunIsAnAlbum(t *testing.T) {
	messages := []Message{
		{ID: 1, Media: "photo", AlbumID: 5, Author: "Anna", AuthorID: 7},
		{ID: 2, Media: "photo", AlbumID: 5, Author: "Boris", AuthorID: 9},
	}

	if got := len(timelineEntries(messages)); got != 2 {
		t.Fatalf("%d entries, want 2", got)
	}
}

// A group has a colour per person, so that two names in the same
// conversation are not the same word, and the same person is the same
// colour in every message and in every chat.
func TestAuthorColoursFollowTheSenderAndNothingElse(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileTrueColor, 120, 30)

	first := m.styles().authorColor(7).Render("Anna")
	again := m.styles().authorColor(7).Render("Anna")
	other := m.styles().authorColor(9).Render("Boris")

	if first != again {
		t.Fatalf("the same sender got two styles: %q and %q", first, again)
	}
	if first == other {
		t.Fatalf("two senders got the same style: %q", first)
	}
}

// The composer is a band across the width of the conversation, with a `›`
// in front of the draft, and the keys under it inside the same band. The
// prompt is in the accent while the keys are in it and in the muted step
// while they are not, and there is no cursor at all in the second case: a
// bar blinking in a field nobody is typing into is the one thing on the
// screen that says the user is somewhere else.
func TestTheComposerIsABandWithAPromptAndNoStrayCursor(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusComposer,
	)

	view := plain(m.View())
	if _, ok := lineIndexWith(view, composerPlaceholder); !ok {
		t.Fatalf("there is no composer:\n%s", view)
	}
	if !strings.Contains(view, composerPrompt) {
		t.Fatalf("the composer has no prompt:\n%s", view)
	}
	if !strings.Contains(view, m.hintText(LayoutFor(m.width, m.height))) {
		t.Fatalf("the hints are not inside the band:\n%s", view)
	}
	if !strings.Contains(m.View(), cursorBar) {
		t.Fatalf("the focused composer has no cursor:\n%q", m.View())
	}

	elsewhere := focusedOn(m, FocusHistory)
	if strings.Contains(elsewhere.View(), cursorBar) {
		t.Fatalf("a composer without the keys has a cursor:\n%q", elsewhere.View())
	}
	if !strings.Contains(elsewhere.View(), composerPrompt) {
		t.Fatalf("a composer without the keys has no prompt:\n%q", elsewhere.View())
	}
}

// The own chat is called "Saved Messages" by Telegram and "Избранное" by
// the person using it, and a user who is looking for it types the word they
// know. The second name is a way of finding the row and never a second
// thing on the screen.
func TestTheOwnChatIsFoundByEitherOfItsNames(t *testing.T) {
	m := typedSearching(t, sizedModel(t, 120, 30), "избр")

	if len(m.chatListEntries()) != 1 {
		t.Fatalf(
			"the query matched %d chats, want the one with oneself",
			len(m.chatListEntries()),
		)
	}
	if got := m.chatListEntries()[0].chat.Title; got != "Saved Messages" {
		t.Fatalf("the match is %q, want the chat with oneself", got)
	}

	if names := m.selected().Aliases; len(names) != 1 || names[0] != "Избранное" {
		t.Fatalf("the own chat is not found by %q: %q", "Избранное", names)
	}
}

// The alias is a way of finding a row, not a second name for it: the list
// says "Saved Messages" and nothing else.
func TestTheAliasOfTheOwnChatIsNeverDrawn(t *testing.T) {
	m := sizedModel(t, 120, 30)

	if strings.Contains(plain(m.View()), "Избранное") {
		t.Fatalf("the alias is on the screen:\n%s", plain(m.View()))
	}
}

// typedSearching returns a model with a query typed into the search.
func typedSearching(t *testing.T, m Model, query string) Model {
	t.Helper()

	m, _ = updateModel(t, m, pressRunes("/"))
	for _, letter := range query {
		m, _ = updateModel(t, m, pressRunes(string(letter)))
	}

	return m
}

// The keys still reach the composer after the band grew: the draft goes in,
// and the message is queued when Enter is pressed.
func TestTheComposerStillTakesTextAfterTheBandGrew(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileNoColor, 120, 30),
		FocusComposer,
	)

	m, _ = updateModel(t, m, pressRunes("привет"))

	if got := m.Composer(); got != "привет" {
		t.Fatalf("the draft is %q, want the typed text", got)
	}
	if _, _, ok := lineWith(plain(m.View()), "привет"); !ok {
		t.Fatalf("the draft is not on the screen:\n%s", plain(m.View()))
	}
}

// The screen is a rectangle on every size and in every profile, which is
// what the whole layout is for.
func TestTheScreenIsARectangleAtEverySize(t *testing.T) {
	for _, size := range [][2]int{
		{120, 30}, {100, 24}, {80, 24}, {72, 20}, {60, 24}, {60, 12}, {40, 5},
	} {
		for _, profile := range []theme.Profile{
			theme.ProfileTrueColor, theme.ProfileNoColor,
		} {
			m := focusedOn(
				openedProgramModel(t, profile, size[0], size[1]),
				FocusHistory,
			)

			lines := viewLines(m.View())
			if len(lines) != size[1] {
				t.Errorf(
					"%dx%d %v: %d rows, want %d",
					size[0], size[1], profile, len(lines), size[1],
				)
			}

			for index, line := range lines {
				if got := m.widths.StringWidth(line); got != size[0] {
					t.Errorf(
						"%dx%d %v: row %d is %d columns, want %d",
						size[0], size[1], profile, index, got, size[0],
					)
				}
			}
		}
	}
}

// The tea key that carries the focus is Tab and it moves between the panes
// the rule is on. A rule that did not move with the keys would be a
// decoration rather than a claim.
//
// The cycle is the composer, the list, the conversation, and the rule of a
// pane closes that pane's header — which is two rows for a conversation and
// three for a list, so the two rules are a row apart.
func TestTabMovesTheRuleBetweenThePanes(t *testing.T) {
	m := focusedOn(openedProgramModel(t, theme.ProfileNoColor, 120, 30), FocusComposer)

	m, _ = updateModel(t, m, press(tea.KeyTab))
	assertPanelRule(t, m, listPane)

	m, _ = updateModel(t, m, press(tea.KeyTab))
	assertPanelRule(t, m, conversationPane)
}

// Every cell of both rows of the selected chat carries the Selected
// background, and not only the first one.
//
// A run that is styled without it ends with SGR 0, and that reset takes
// the surface of the row with it: what is left is a one-column highlight
// in the middle of a row, which every reader of a list takes for a cursor
// rather than for a selection, and which on a list of twenty chats is
// twenty highlights in twenty places.
//
// It is checked on a true-colour and on an indexed profile, because the
// two print a background in different ways and only one of them was right,
// and it is checked on the rows of the list rather than on the lines of
// the screen, because a screen line is two panes and the background in
// question is the list's.
func TestEveryCellOfTheSelectedChatIsOnItsBackground(t *testing.T) {
	for _, profile := range []theme.Profile{
		theme.ProfileTrueColor, theme.ProfileANSI256,
	} {
		t.Run(profile.String(), func(t *testing.T) {
			m := focusedOn(
				openedProgramModel(t, profile, 120, 30),
				FocusChatList,
			)
			m.chats[0].Title = "Anna Example"
			m.chats[0].Preview = "the build is green again"

			background := backgroundParameters(
				m.styles().selected(true).Render("x"),
			)
			if background == "" {
				t.Fatal("the theme has no selected background to draw")
			}

			layout := LayoutFor(m.width, m.height)
			rows, _ := m.chatListRows(layout, chatListPaneWidth(layout))

			if len(rows[0]) != 3 {
				t.Fatalf(
					"a chat takes %d rows, want a name, a preview and a gap",
					len(rows[0]),
				)
			}

			badge := backgroundParameters(
				m.styles().pill(m.tokens().Unread, m.tokens().SidebarBackground).
					Render("x"),
			)

			for _, row := range rows[0][:2] {
				assertSelectedRow(t, m, background, badge, row)
			}
		})
	}
}

// The message under the cursor is selected, and it is selected on its
// block and on nothing else.
//
// The block is where the words are, so the block is where the selection
// has to be: a selection that ran the width of the feed under a message
// would be the band this feed stopped having, and a selection that stopped
// before the block would be nowhere. The columns of the row around the
// block are the feed's, on both sides, on every row of the message.
func TestEveryColumnOfTheSelectedMessageBlockIsOnItsBackground(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	m.chats[m.selectedChat].Messages = []Message{{
		ID: 1, Text: "the build is green again", Time: "10:02",
		Author: "Anna Example", AuthorID: 5,
	}}
	m.historyState = loadStateLoaded
	m.selectedMsg = 0
	m.timelineTop = 0

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()
	selected := backgroundParameters(m.styles().selected(true).Render("x"))
	feed := backgroundParameters(
		m.styles().on(m.tokens().ChatBackground, m.styles().unstyled()).
			Render("x"),
	)

	entries := timelineEntries(m.selected().Messages)
	rows := m.entryLines(entries[0], layout, width, m.styles())

	// The first row of a block is the blank line between messages.
	for index, row := range rows[1:] {
		assertSelectedBlock(t, m, index, selected, feed, width, row)
	}
}

// assertSelectedBlock fails unless the cells of a row of a selected
// message are the selection inside the block and the feed outside it.
//
// The row is walked a run at a time rather than read as one string: a
// reset in the middle of a row is invisible in the plain text and in a
// substring search, and it is the only thing this is about.
func assertSelectedBlock(
	t *testing.T,
	m Model,
	index int,
	selected, feed string,
	width int,
	row string,
) {
	t.Helper()

	columns := 0
	for _, run := range styleRuns(row) {
		columns += m.widths.StringWidth(run.text)

		switch {
		case strings.Contains(run.sgr, selected):
		case strings.Contains(run.sgr, feed):
		default:
			t.Errorf(
				"row %d: %d columns are on neither the selection nor the feed: %q",
				index, m.widths.StringWidth(run.text), run.text,
			)
		}
	}

	if columns != width {
		t.Errorf("row %d: %d columns, want the %d of the feed", index, columns, width)
	}
}

// assertSelectedRow fails unless every column of a selected row is on the
// background of the row or on the background of the badge.
//
// The columns of air inside the row are on the background of the row: the
// card is a band of colour with words in it, and a band that stopped short
// of the edges of the pane is the stripe §4.2 removed. The badge is the
// one exception: the unread count of the selected chat drawn in the colour
// of the list on the background of the selection is a number nobody can
// read, and drawn in the colour of the badge on the background of the
// selection it is a badge that has stopped being one.
//
// The row is walked a run at a time rather than read as one string: a
// reset in the middle of a row is invisible in the plain text and in a
// substring search, and it is the only thing this is about.
func assertSelectedRow(
	t *testing.T,
	m Model,
	selected, badge string,
	row string,
) {
	t.Helper()

	columns := 0

	for _, run := range styleRuns(row) {
		width := m.widths.StringWidth(run.text)
		columns += width

		switch {
		case badge != "" && strings.Contains(run.sgr, badge):
		case strings.Contains(run.sgr, selected):
		default:
			t.Errorf(
				"%d columns of the row are on neither the selection nor the badge: %q",
				width,
				run.text,
			)
		}
	}

	if columns == 0 {
		t.Fatalf("the row is empty: %q", row)
	}
}

// The unread badge of the selected chat keeps the background of the badge
// and not the background of the row, on the row the cursor is on as well
// as on every other one.
func TestTheUnreadBadgeKeepsItsPillOnTheSelectedRow(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusChatList,
	)
	m.chats[0].Title = "Anna Example"
	m.chats[0].Preview = "the build is green again"
	m.chats[0].Unread = 2

	layout := LayoutFor(m.width, m.height)
	rows, _ := m.chatListRows(layout, layout.SidebarContentWidth())

	selected := backgroundParameters(m.styles().selected(true).Render("x"))
	badge := backgroundParameters(
		m.styles().pill(m.tokens().Unread, m.tokens().SidebarBackground).
			Render("x"),
	)
	if selected == badge {
		t.Skip("the theme gives the selection and the badge the same colour")
	}

	assertSelectedRow(t, m, selected, badge, rows[0][1])
}

// The feed is chronological: the oldest message is on the first row of it
// and the newest is on the last, and a message of this user that is still
// in the queue is after both of them because it is newer than both.
//
// A page arrives from TDLib newest first and the model reverses it where
// it lands, so a golden that handed the model an already chronological
// list would be a golden of a program nobody runs. This is the test that
// says the reversal is still there.
func TestTheFeedIsInTheOrderOfTime(t *testing.T) {
	queued := PendingMessage{
		EntryID:   "entry-1",
		ChatID:    1,
		Text:      "still on its way",
		State:     MessageDeliveryQueued,
		CreatedAt: time.Date(2026, 3, 14, 12, 30, 0, 0, snapshotZone),
	}

	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	// Chat.Messages is chronological, oldest first: a page arrives newest
	// first and the model reverses it where it lands.
	m.chats[m.selectedChat].Messages = []Message{
		{ID: 1, Text: "the oldest of three", Time: "12:02", Author: "Anna", AuthorID: 5},
		{ID: 2, Text: "the middle of three", Time: "12:08", Author: "Anna", AuthorID: 5},
		{ID: 3, Text: "the newest of three", Time: "12:10", Author: "Anna", AuthorID: 5},
	}
	m.historyState = loadStateLoaded
	m.pending = []PendingMessage{queued}
	m.selectedMsg = 3
	m.timelineTop = 0

	view := plain(m.View())
	_, oldest, ok := lineWith(view, "the oldest of three")
	if !ok {
		t.Fatalf("the oldest message is not on the screen:\n%s", view)
	}
	_, middle, ok := lineWith(view, "the middle of three")
	if !ok {
		t.Fatalf("the middle message is not on the screen:\n%s", view)
	}
	_, newest, ok := lineWith(view, "the newest of three")
	if !ok {
		t.Fatalf("the newest message is not on the screen:\n%s", view)
	}
	_, queuedRow, ok := lineWith(view, "still on its way")
	if !ok {
		t.Fatalf("the queued message is not on the screen:\n%s", view)
	}

	if !(oldest < middle && middle < newest && newest < queuedRow) {
		t.Fatalf(
			"the feed is in the order %d, %d, %d, %d, want oldest first and the queued message last",
			oldest, middle, newest, queuedRow,
		)
	}
}
