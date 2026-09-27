package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/tui/theme"
)

// The action sheet of §13: what a user can do with a message that has not
// been delivered yet, and what they cannot.
//
// The list of items is the whole decision of this feature. A queued message
// can be cancelled, a failed one can be written again, an uncertain one asks
// a question before it becomes a copy, and there is no Retry anywhere: a
// retry is what the queue does on its own schedule, and a button for it
// would be a second dispatcher with a user behind it.

// actionSpecLabels is every item word of §13. The tests below compare the
// items of every state against it, so a wording change has to be made here
// on purpose.
var actionSpecLabels = []string{
	"Cancel message",
	"Copy text",
	"Create new message",
	"Create a new message",
	"Keep as uncertain",
	"Cancel record",
	"Close",
}

func TestTheItemsOfEveryStateAreExactlyTheOnesOfTheSpec(t *testing.T) {
	for name, testCase := range map[string]struct {
		state MessageDeliveryState
		items []string
	}{
		"queued": {
			state: MessageDeliveryQueued,
			items: []string{"Cancel message", "Copy text", "Close"},
		},
		"sending": {
			state: MessageDeliverySending,
			items: []string{"Copy text", "Close"},
		},
		"retrying": {
			state: MessageDeliveryRetrying,
			items: []string{"Cancel message", "Copy text", "Close"},
		},
		"failed": {
			state: MessageDeliveryFailed,
			items: []string{"Create new message", "Copy text", "Close"},
		},
		"uncertain": {
			state: MessageDeliveryUncertain,
			items: []string{
				"Create a new message",
				"Copy text",
				"Keep as uncertain",
				"Cancel record",
				"Close",
			},
		},
		"canceled": {
			state: MessageDeliveryCanceled,
			items: []string{"Copy text", "Close"},
		},
		"sent": {
			state: MessageDeliverySent,
			items: []string{"Copy text", "Close"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			sheet := actionSheetFor(testCase.state, false)
			if got := sheetLabels(sheet); !sameStrings(got, testCase.items) {
				t.Fatalf("items = %q, want %q", got, testCase.items)
			}
		})
	}
}

// A message Telegram already has is not a delivery problem any more, and
// the only thing a user can do with one is take its text.
func TestAMessageOfTheHistoryHasTwoItems(t *testing.T) {
	sheet := actionSheetFor(MessageDeliverySent, true)
	if got := sheetLabels(sheet); !sameStrings(got, []string{"Copy text", "Close"}) {
		t.Fatalf("items = %q", got)
	}
}

// Retry is not an item anywhere, and every item is a word §13 names: a
// sheet that grows an item nobody specified is a sheet nobody reviewed.
func TestEveryItemIsAWordOfTheSpecAndNoneIsARetry(t *testing.T) {
	for _, state := range []MessageDeliveryState{
		MessageDeliveryQueued,
		MessageDeliverySending,
		MessageDeliveryRetrying,
		MessageDeliveryFailed,
		MessageDeliveryUncertain,
		MessageDeliveryCanceled,
		MessageDeliverySent,
	} {
		for _, item := range sheetLabels(actionSheetFor(state, false)) {
			if !containsString(actionSpecLabels, item) {
				t.Fatalf("%s offers %q, which §13 does not name", state, item)
			}
			if strings.Contains(strings.ToLower(item), "retry") {
				t.Fatalf("%s offers %q", state, item)
			}
		}
	}
}

// ---- Opening and closing ----

func TestAOpensTheSheetOverTheSelectedMessage(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	if model.actionSheet.open {
		t.Fatal("the sheet is open before a key was pressed")
	}

	model, _ = updateModel(t, model, pressRunes("a"))
	if !model.actionSheet.open {
		t.Fatal("a did not open the action sheet")
	}
	if len(model.actionSheet.items) == 0 {
		t.Fatal("the sheet opened with no items")
	}
}

// A message of the history opens the same sheet, with the two items it has.
func TestAOpensTheSheetOverAMessageOfTheHistory(t *testing.T) {
	model, _ := actionModel(t, MessageDeliverySent, true)

	model, _ = updateModel(t, model, pressRunes("a"))
	if !model.actionSheet.open {
		t.Fatal("a did not open the action sheet over a message of the history")
	}
	if got := sheetLabels(model.actionSheet); !sameStrings(
		got,
		[]string{"Copy text", "Close"},
	) {
		t.Fatalf("items = %q", got)
	}
}

func TestEnterAndIStillGoToTheComposer(t *testing.T) {
	for name, key := range map[string]tea.KeyMsg{
		"enter": press(tea.KeyEnter),
		"i":     pressRunes("i"),
	} {
		t.Run(name, func(t *testing.T) {
			model, _ := actionModel(t, MessageDeliveryQueued, false)

			model, _ = updateModel(t, model, key)
			if model.actionSheet.open {
				t.Fatal("the action sheet opened instead of the composer")
			}
			if model.focus != FocusComposer {
				t.Fatalf("focus = %v, want the composer", model.focus)
			}
		})
	}
}

// §4.6: the hint bar names the keys that work, and a key that does nothing
// is a promise the interface cannot keep.
func TestTheTimelineHintNamesTheActionKey(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model.focus = FocusHistory

	hint := model.hintText(LayoutFor(model.width, model.height))
	if !strings.Contains(hint, "a actions") {
		t.Fatalf("the timeline hint = %q, want the action key", hint)
	}
}

func TestEscClosesTheSheetWithoutDoingAnything(t *testing.T) {
	for _, state := range []MessageDeliveryState{
		MessageDeliveryQueued,
		MessageDeliveryFailed,
		MessageDeliveryCanceled,
	} {
		t.Run(string(state), func(t *testing.T) {
			canceller := &recordingCanceller{}
			model, _ := actionModel(t, state, false)
			model.canceller = canceller

			model, _ = updateModel(t, model, pressRunes("a"))
			model, _ = updateModel(t, model, press(tea.KeyEsc))

			if model.actionSheet.open {
				t.Fatal("Esc did not close the sheet")
			}
			if len(canceller.canceled) != 0 {
				t.Fatalf("Esc canceled %v", canceller.canceled)
			}
		})
	}
}

// §13: in uncertain, Esc is the safe default, and the safe default is
// keeping the record. A user who opens a sheet and closes it without
// reading must not have decided anything.
func TestEscInUncertainKeepsTheRecord(t *testing.T) {
	canceller := &recordingCanceller{}
	model, _ := actionModel(t, MessageDeliveryUncertain, false)
	model.canceller = canceller

	model, _ = updateModel(t, model, pressRunes("a"))
	model, _ = updateModel(t, model, press(tea.KeyEsc))

	if model.actionSheet.open {
		t.Fatal("Esc did not close the sheet")
	}
	if len(canceller.canceled) != 0 {
		t.Fatalf("Esc canceled the record: %v", canceller.canceled)
	}
	if model.modal.open {
		t.Fatal("Esc asked a question instead of keeping the record")
	}
}

func TestTheItemsMoveWithTheKeysAndStayInsideTheList(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryUncertain, false)
	model, _ = updateModel(t, model, pressRunes("a"))
	items := len(model.actionSheet.items)

	for _, key := range []tea.KeyMsg{pressRunes("j"), press(tea.KeyDown)} {
		model, _ = updateModel(t, model, key)
	}
	if model.actionSheet.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 after two downs", model.actionSheet.cursor)
	}
	for _, key := range []tea.KeyMsg{pressRunes("k"), press(tea.KeyUp)} {
		model, _ = updateModel(t, model, key)
	}
	if model.actionSheet.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 after two ups", model.actionSheet.cursor)
	}

	for range items + 3 {
		model, _ = updateModel(t, model, pressRunes("j"))
	}
	if model.actionSheet.cursor != items-1 {
		t.Fatalf("cursor = %d, want the last item %d", model.actionSheet.cursor, items-1)
	}
	for range items + 3 {
		model, _ = updateModel(t, model, pressRunes("k"))
	}
	if model.actionSheet.cursor != 0 {
		t.Fatalf("cursor = %d, want the first item", model.actionSheet.cursor)
	}
}

// The keys of a sheet are the keys of a sheet, not the keys of the
// timeline: j walks the items and does not scroll the conversation behind.
func TestTheSheetKeysDoNotMoveTheTimeline(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model, _ = updateModel(t, model, pressRunes("a"))
	before := model.selectedMsg

	model, _ = updateModel(t, model, pressRunes("j"))
	if model.selectedMsg != before {
		t.Fatalf("the cursor moved to %d while a sheet was open", model.selectedMsg)
	}
}

// ---- Cancel ----

// A cancel names the entry and the version the screen read, because the
// queue refuses a cancel of a record that has moved on since.
func TestCancelNamesTheEntryAndTheVersion(t *testing.T) {
	canceller := &recordingCanceller{}
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model.canceller = canceller

	model = act(t, model, "Cancel message")

	if len(canceller.canceled) != 1 {
		t.Fatalf("cancels = %d, want 1", len(canceller.canceled))
	}
	if got := canceller.canceled[0]; got.entryID != "entry-1" || got.version != 7 {
		t.Fatalf("cancel = %+v, want entry-1 at version 7", got)
	}
	if model.actionSheet.open {
		t.Fatal("the sheet stayed open over a cancel")
	}
}

// The record moved while the sheet was open and the queue refuses. The user
// is told in words that are true that nothing was canceled, and the record
// is read again: the screen is showing a state the user did not act on.
func TestAVersionConflictSaysNothingWasCanceled(t *testing.T) {
	canceller := &recordingCanceller{err: ErrMessageStateChanged}
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model.canceller = canceller

	model = act(t, model, "Cancel message")

	view := plain(model.View())
	if !strings.Contains(view, "This message changed state. Nothing was canceled.") {
		t.Fatalf("the screen does not say what happened: %q", viewLines(view))
	}
	if state := model.pendingMessageOf("entry-1"); state != nil {
		t.Fatalf(
			"the record is still on the screen as %v, and it was not canceled",
			*state,
		)
	}
}

// A cancel that fails for any other reason says so in the same place and
// keeps the record: a queue that is locked must not look like a queue that
// changed its mind.
func TestAFailedCancelSaysTheMessageIsStillThere(t *testing.T) {
	canceller := &recordingCanceller{err: ErrMessageCancelUnavailable}
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model.canceller = canceller

	model = act(t, model, "Cancel message")

	if !strings.Contains(plain(model.View()), "This message was not canceled.") {
		t.Fatalf("the screen does not say what happened: %q", viewLines(plain(model.View())))
	}
	if state := model.pendingMessageOf("entry-1"); state == nil ||
		*state != MessageDeliveryQueued {
		t.Fatalf("the record is %v, want it still queued", state)
	}
}

// ---- Create a new message ----

// A failed message becomes a draft and nothing else: the text lands in the
// composer and the user sends it, because a program that re-queues a
// message on a keypress is a program that sends messages nobody confirmed.
func TestCreateNewMessagePutsTheTextInTheComposer(t *testing.T) {
	submitter := &recordingSubmitter{}
	model, _ := actionModel(t, MessageDeliveryFailed, false)
	model.submitter = submitter

	model = act(t, model, "Create new message")

	if got := string(model.composer); got != actionFixtureText {
		t.Fatalf("the draft = %q, want the text of the message", got)
	}
	if model.focus != FocusComposer {
		t.Fatalf("focus = %v, want the composer", model.focus)
	}
	if submitter.calls != 0 {
		t.Fatalf("the submitter was called %d times, want none", submitter.calls)
	}
	if model.actionSheet.open {
		t.Fatal("the sheet stayed open")
	}
}

// The draft replaces what was in the composer: a copy of a message is a
// new thing to send, and two drafts in one composer is a conversation with
// itself.
func TestACopyReplacesTheDraftAndPutsTheCursorInIt(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryFailed, false)
	model.composer = []rune("старый черновик")
	model.composerCursor = len(model.composer)

	model = act(t, model, "Create new message")

	if got := string(model.composer); got != actionFixtureText {
		t.Fatalf("the draft = %q, want the new one", got)
	}
	if model.composerCursor != len([]rune(actionFixtureText)) {
		t.Fatalf("the cursor is at %d, want the end of the draft", model.composerCursor)
	}
}

// ---- The uncertain question ----

// §12.3: the only modal confirmation in the interface, and it asks before
// the one action that can put a duplicate into a conversation.
func TestUncertainCreateAsksFirst(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryUncertain, false)

	model, _ = updateModel(t, model, pressRunes("a"))
	model, _ = chooseItem(t, model, "Create a new message")

	if !model.modal.open {
		t.Fatal("the uncertain sheet created a copy without asking")
	}
	if got := string(model.composer); got != "" {
		t.Fatalf("the draft = %q, want the composer untouched until the answer", got)
	}

	question := plain(strings.Join(model.confirmModalRows(), "\n"))
	for _, want := range []string{
		"Send a new copy?",
		"may already have succeeded",
		"Create new copy",
		"Keep uncertain",
		"Cancel",
	} {
		if !strings.Contains(question, want) {
			t.Fatalf("the question does not say %q:\n%s", want, question)
		}
	}
}

// The safe answer is the default: a user who opens the sheet, reads the
// question and presses Enter has not chosen to send anything.
func TestTheModalStartsOnKeepUncertain(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryUncertain, false)
	model, _ = updateModel(t, model, pressRunes("a"))
	model, _ = chooseItem(t, model, "Create a new message")

	if model.modal.cursor != modalKeepUncertain {
		t.Fatalf(
			"the modal cursor = %d, want the keep item %d",
			model.modal.cursor,
			modalKeepUncertain,
		)
	}

	model = applyKey(t, model, press(tea.KeyEnter))
	if model.modal.open {
		t.Fatal("Enter did not answer the question")
	}
	if got := string(model.composer); got != "" {
		t.Fatalf("the draft = %q, want nothing created", got)
	}
}

func TestTheModalCreatesTheCopyOnRequest(t *testing.T) {
	submitter := &recordingSubmitter{}
	model, _ := actionModel(t, MessageDeliveryUncertain, false)
	model.submitter = submitter

	model, _ = updateModel(t, model, pressRunes("a"))
	model, _ = chooseItem(t, model, "Create a new message")
	model = applyKey(t, model, press(tea.KeyUp))
	model = applyKey(t, model, press(tea.KeyEnter))

	if model.modal.open {
		t.Fatal("the modal stayed open")
	}
	if got := string(model.composer); got != actionFixtureText {
		t.Fatalf("the draft = %q, want the text of the message", got)
	}
	if submitter.calls != 0 {
		t.Fatalf("the submitter was called %d times, want none", submitter.calls)
	}
	// The old record is still uncertain: the copy is a draft, and the
	// record stays until the user decides about it on its own.
	if state := model.pendingMessageOf("entry-1"); state == nil ||
		*state != MessageDeliveryUncertain {
		t.Fatalf("the record is %v, want it still uncertain", state)
	}
}

// Esc in the modal is Cancel: nothing happens, and nothing was asked of the
// user that they did not answer.
func TestEscInTheModalDoesNothing(t *testing.T) {
	canceller := &recordingCanceller{}
	model, _ := actionModel(t, MessageDeliveryUncertain, false)
	model.canceller = canceller

	model, _ = updateModel(t, model, pressRunes("a"))
	model, _ = chooseItem(t, model, "Create a new message")
	model = applyKey(t, model, press(tea.KeyEsc))

	if model.modal.open {
		t.Fatal("Esc did not close the question")
	}
	if got := string(model.composer); got != "" {
		t.Fatalf("Esc created a draft: %q", got)
	}
	if len(canceller.canceled) != 0 {
		t.Fatalf("Esc canceled the record: %v", canceller.canceled)
	}
	if state := model.pendingMessageOf("entry-1"); state == nil ||
		*state != MessageDeliveryUncertain {
		t.Fatalf("the record is %v, want it still uncertain", state)
	}
}

// ---- Copy ----

// OSC 52 puts the text in the clipboard of the terminal itself, which is
// the copy that works over ssh and in a browser tab. The text goes to the
// terminal and nowhere else: never to a log, never to a file.
func TestCopyWritesTheTextToTheTerminalAndSaysSo(t *testing.T) {
	terminal := &recordingWriter{}
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model.clipboard = newTerminalOutput(terminal, true)

	model = act(t, model, "Copy text")

	written := terminal.String()
	encoded := base64.StdEncoding.EncodeToString([]byte(actionFixtureText))
	if !strings.Contains(written, encoded) {
		t.Fatalf("the terminal got %q, want the base64 of the text", written)
	}
	if !isOSC52(written) {
		t.Fatalf("what the terminal got is not an OSC 52 sequence: %q", written)
	}
	if !strings.Contains(plain(model.View()), "Copied") {
		t.Fatalf(
			"the screen does not say it was copied: %q",
			viewLines(plain(model.View())),
		)
	}
}

// A program with nowhere to write an escape sequence says so rather than
// claiming a copy that did not happen.
func TestCopyWithoutATerminalSaysNothingWasCopied(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model.clipboard = nil

	model = act(t, model, "Copy text")

	view := plain(model.View())
	if strings.Contains(view, "Copied") {
		t.Fatalf("the screen says Copied with no terminal: %q", viewLines(view))
	}
	if !strings.Contains(view, "Could not copy") {
		t.Fatalf("the screen does not say the copy failed: %q", viewLines(view))
	}
}

// ---- The popup itself ----

// §12.3 and §24: a raised background and a shadow, and not one line of a
// frame. A frame around a menu says "this is a box" where the interface
// has to say what is in it.
func TestPopupUsesBackgroundAndShadowWithoutFrame(t *testing.T) {
	for name, profile := range map[string]theme.Profile{
		"true color": theme.ProfileTrueColor,
		"no color":   theme.ProfileNoColor,
	} {
		t.Run(name, func(t *testing.T) {
			model, _ := actionModel(t, MessageDeliveryQueued, false)
			model = withProfile(model, profile)
			model, _ = updateModel(t, model, pressRunes("a"))

			view := model.View()
			for _, glyph := range boxDrawing {
				if strings.ContainsRune(view, glyph) {
					t.Fatalf("the sheet contains the box glyph %q:\n%q", glyph, view)
				}
			}

			rows := model.actionSheetRows()
			if len(rows) == 0 {
				t.Fatalf("the sheet drew nothing:\n%q", view)
			}
			if !strings.Contains(strings.Join(rows, "\n"), "Cancel message") {
				t.Fatalf("the sheet has no items:\n%q", rows)
			}

			if !popupIsSetOff(rows, profile == theme.ProfileTrueColor) {
				t.Fatalf("the sheet is not drawn over the screen:\n%q", rows)
			}
		})
	}
}

// A modal is a popup too, and a frame around a question would be the
// loudest thing on the screen.
func TestTheModalHasNoFrame(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryUncertain, false)
	model, _ = updateModel(t, model, pressRunes("a"))
	model, _ = chooseItem(t, model, "Create a new message")

	view := model.View()
	for _, glyph := range boxDrawing {
		if strings.ContainsRune(view, glyph) {
			t.Fatalf("the modal contains the box glyph %q:\n%q", glyph, view)
		}
	}
}

// §14: the selected item is marked by a symbol where there is no colour,
// because a menu whose selection is a colour is a menu nobody can read on
// a terminal that shows none.
func TestTheSelectedItemIsMarkedWithoutColour(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model = uncolored(model)
	model, _ = updateModel(t, model, pressRunes("a"))

	rows := model.actionSheetRows()
	marked := 0
	for _, row := range rows {
		text := plain(row)
		switch {
		case strings.ContainsRune(text, firstRune(theme.SelectionMark)):
			marked++
		case strings.ContainsRune(text, firstRune(theme.SelectionNone)):
		default:
			t.Fatalf("the item %q carries no marker at all", text)
		}
	}
	if marked != 1 {
		t.Fatalf("%d items are marked, want 1", marked)
	}
}

// ---- privacy ----

// A version is not personal data and a text is: the printing of a pending
// message still leaves the text out, because the sheet is the only place
// that reads it and a value on its way to a log is not the sheet.
func TestAPendingMessageWithAVersionStillPrintsWithoutText(t *testing.T) {
	message := PendingMessage{
		EntryID:   "entry-1",
		ChatID:    7,
		Text:      actionFixtureText,
		State:     MessageDeliveryQueued,
		Version:   7,
		CreatedAt: actionFixtureMoment,
	}

	for name, printed := range map[string]string{
		"v":    message.String(),
		"plus": fmt.Sprintf("%+v", message),
		"go":   message.GoString(),
	} {
		if strings.Contains(printed, actionFixtureText) {
			t.Fatalf("%s printed the text: %q", name, printed)
		}
	}
	if !strings.Contains(message.String(), "7") {
		t.Fatalf("String() = %q, want the version in it", message.String())
	}
}

// ---- helpers ----

// actionFixtureText is the text of the message these tests act on.
const actionFixtureText = "текст сообщения"

// actionFixtureMoment is when that message was queued.
var actionFixtureMoment = time.Date(2026, 9, 28, 14, 35, 0, 0, time.UTC)

// withProfile rebuilds a model for a colour profile, the way the
// composition root builds one: the theme is degraded to the profile and the
// renderer is built from the profile, not from the terminal.
func withProfile(model Model, profile theme.Profile) Model {
	model.colorProfile = profile
	model.theme = theme.DefaultTheme().ForProfile(profile)
	model.rendererForProfile = nil

	return model
}

// recordingCanceller records the cancels it was asked for.
type recordingCanceller struct {
	canceled []cancelCall
	err      error
}

type cancelCall struct {
	entryID string
	version uint64
}

func (c *recordingCanceller) CancelMessage(
	_ context.Context,
	entryID string,
	version uint64,
) error {
	c.canceled = append(c.canceled, cancelCall{entryID: entryID, version: version})

	return c.err
}

// actionModel is a conversation with one message in one delivery state, the
// cursor on it, and a canceller ready to be used.
//
// The state is the state of a pending message, because a queued message is
// what has actions; the same model with history=true is a message Telegram
// already has.
func actionModel(
	t *testing.T,
	state MessageDeliveryState,
	history bool,
) (Model, *pendingSource) {
	t.Helper()

	pending := &pendingSource{}
	created := actionFixtureMoment

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		PendingMessages:  pending,
		AccountKey:       "account-1",
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{
		chats: []Chat{{ID: 7, Title: "A"}},
	})
	model, _ = updateModel(t, model, press(tea.KeyEnter))

	message := Message{
		ID:       501,
		Outgoing: true,
		Text:     actionFixtureText,
		Time:     "14:35",
	}

	if history {
		model, _ = updateModel(t, model, historyLoadedMsg{
			chatID:    7,
			operation: model.historyOperation,
			page:      HistoryPage{Messages: []Message{message}},
		})
		model = model.scrollToNewest()
		model.focus = FocusHistory

		return model, pending
	}

	pending.messages = []PendingMessage{{
		EntryID:   "entry-1",
		ChatID:    7,
		Text:      actionFixtureText,
		State:     state,
		Version:   7,
		CreatedAt: created,
	}}
	model, _ = updateModel(t, model, deliveryRefresh(t, model, pending))
	model = model.scrollToNewest()
	model.focus = FocusHistory

	return model, pending
}

// chooseItem opens the sheet, walks to an item and activates it, the way a
// user walks down a list and presses Enter.
func chooseItem(t *testing.T, model Model, label string) (Model, tea.Cmd) {
	t.Helper()

	found := false
	for index, item := range model.actionSheet.items {
		if item.label != label {
			continue
		}

		for range index {
			model, _ = updateModel(t, model, pressRunes("j"))
		}
		found = true

		break
	}
	if !found {
		t.Fatalf("the sheet has no item %q", label)
	}

	return updateModel(t, model, press(tea.KeyEnter))
}

// act opens the sheet, chooses an item and applies everything the action
// produced, including the messages its commands answer with.
func act(t *testing.T, model Model, label string) Model {
	t.Helper()

	model, _ = updateModel(t, model, pressRunes("a"))
	keys := actionSheetItemTo(model, label)
	if len(keys) == 0 {
		t.Fatalf("the sheet has no item %q", label)
	}

	return applyKey(t, model, keys[0], keys[1:]...)
}

// actionSheetItemTo returns the keys that walk a sheet to an item.
func actionSheetItemTo(model Model, label string) []tea.KeyMsg {
	for index, item := range model.actionSheet.items {
		if item.label == label {
			keys := make([]tea.KeyMsg, 0, index+1)
			for range index {
				keys = append(keys, pressRunes("j"))
			}

			// Enter is the last key of the walk: the sheet is activated by
			// pressing Enter on an item, not by arriving at it.
			return append(keys, press(tea.KeyEnter))
		}
	}

	return nil
}

// applyKey presses keys and feeds every message the commands they return
// back into the model, the way the program does.
func applyKey(
	t *testing.T,
	model Model,
	first tea.KeyMsg,
	rest ...tea.KeyMsg,
) Model {
	t.Helper()

	keys := append([]tea.KeyMsg{first}, rest...)
	for _, key := range keys {
		var cmd tea.Cmd
		model, cmd = updateModel(t, model, key)
		for _, produced := range runAll(t, cmd) {
			model, _ = updateModel(t, model, produced)
		}
	}

	return model
}

// runAll runs a command and returns the messages it produced, following
// batches.
func runAll(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	if cmd == nil {
		return nil
	}

	answered := runSoon(cmd)
	if len(answered) == 0 {
		return nil
	}

	batch, isBatch := answered[0].(tea.BatchMsg)
	if !isBatch {
		return answered
	}

	var produced []tea.Msg
	for _, member := range batch {
		produced = append(produced, runSoon(member)...)
	}

	return produced
}

// runSoon runs one command and returns what it answered in a moment.
//
// A command that sleeps is a timer - the notice that takes a sentence away,
// the poll of the queue - and a test is not waiting for a timer. What a
// test wants from a command is its answer, and an answer that takes three
// seconds is not one.
func runSoon(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	answered := make(chan tea.Msg, 1)
	go func() { answered <- cmd() }()

	select {
	case msg := <-answered:
		if msg == nil {
			return nil
		}

		return []tea.Msg{msg}

	case <-time.After(commandAnswerTimeout):
		return nil
	}
}

// commandAnswerTimeout is how long a test waits for a command to answer.
const commandAnswerTimeout = 250 * time.Millisecond

// popupIsSetOff reports whether the popup is drawn over the conversation
// rather than as another column of it: every row starts after an indent, and
// where there is colour to make a shadow with there is one.
func popupIsSetOff(rows []string, colour bool) bool {
	if len(rows) == 0 {
		return false
	}

	for _, row := range rows {
		text := plain(row)
		if len(strings.TrimLeft(text, " ")) == len(text) {
			return false
		}
		if colour && !strings.Contains(row, "48;") {
			return false
		}
	}

	return true
}

func sheetLabels(sheet actionSheet) []string {
	labels := make([]string, 0, len(sheet.items))
	for _, item := range sheet.items {
		labels = append(labels, item.label)
	}

	return labels
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}

	return true
}

// firstRune is the first glyph of a string from the theme, which is a
// marker of one column.
func firstRune(text string) rune {
	for _, glyph := range text {
		return glyph
	}

	return 0
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

// §4.6: a popup names its own keys while it is open, because the keys of
// the timeline do nothing underneath it.
func TestAPopupNamesItsOwnKeys(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	layout := LayoutFor(model.width, model.height)

	// The timeline names its own keys while no popup is open, including
	// the key that opens one.
	if hint := model.hintText(layout); !strings.Contains(hint, "a actions") {
		t.Fatalf("the timeline hint = %q, want the action key", hint)
	}

	model, _ = updateModel(t, model, pressRunes("a"))
	if hint := model.hintText(layout); !strings.Contains(hint, "Enter run") {
		t.Fatalf("the sheet hint = %q, want its own keys", hint)
	}
	if strings.Contains(model.hintText(layout), "a actions") {
		t.Fatal("the sheet names the key that opens it")
	}

	model, _ = updateModel(t, model, press(tea.KeyEsc))
	if hint := model.hintText(layout); !strings.Contains(hint, "a actions") {
		t.Fatalf("the timeline hint did not come back: %q", hint)
	}
}

// ---- the production path ----

// The copy has to work in the program and not only in a test that injects
// a writer: the program builds its own output, hands it to the model and
// writes its frames through the same one, so a copy cannot land in the
// middle of a frame. A test that sets model.clipboard itself proves nothing
// about that wiring, so this one goes through the program.
func TestCopyOnTheProgramPathReachesTheProgramsWriter(t *testing.T) {
	terminal := &recordingWriter{}
	model := programModelWithOutput(t, terminal, true)

	model = openedConversationWith(t, model, MessageDeliveryQueued)
	model = act(t, model, "Copy text")

	written := terminal.String()
	if !isOSC52(written) {
		t.Fatalf("the program's writer got %q, want an OSC 52 sequence", written)
	}
	if !strings.Contains(written, base64.StdEncoding.EncodeToString([]byte(actionFixtureText))) {
		t.Fatalf("the program's writer got %q, want the text of the message", written)
	}
	if !strings.Contains(plain(model.View()), noticeCopied) {
		t.Fatalf(
			"the screen does not say it was copied: %q",
			viewLines(plain(model.View())),
		)
	}
}

// A pipe is not a terminal: OSC 52 asks a program on the other end of the
// connection, and there is nobody to ask. The interface says the copy did
// not happen instead of writing a sequence into a log file.
func TestCopyIntoSomethingThatIsNotATerminalRefuses(t *testing.T) {
	terminal := &recordingWriter{}
	model := programModelWithOutput(t, terminal, false)

	model = openedConversationWith(t, model, MessageDeliveryQueued)
	model = act(t, model, "Copy text")

	if written := terminal.String(); written != "" {
		t.Fatalf("a non-terminal was sent %q", written)
	}
	if !strings.Contains(plain(model.View()), "Could not copy") {
		t.Fatalf(
			"the screen does not say the copy failed: %q",
			viewLines(plain(model.View())),
		)
	}
}

// ---- the shape of the block ----

// A menu whose items step to the right as they get shorter is a menu
// nobody can scan: every row of the block starts its text in the same
// column, and the block is that wide.
func TestEveryPopupRowStartsItsTextInTheSameColumn(t *testing.T) {
	for name, profile := range map[string]theme.Profile{
		"true color": theme.ProfileTrueColor,
		"no color":   theme.ProfileNoColor,
	} {
		t.Run(name, func(t *testing.T) {
			for _, open := range []string{"sheet", "modal"} {
				model, _ := actionModel(t, MessageDeliveryUncertain, false)
				model = withProfile(model, profile)
				model, _ = updateModel(t, model, pressRunes("a"))
				if open == "modal" {
					for _, key := range actionSheetItemTo(
						model,
						"Create a new message",
					) {
						model, _ = updateModel(t, model, key)
					}
				}

				rows := popupRowsOf(t, model)
				column := -1
				for _, row := range rows {
					text := strings.TrimLeft(plain(row), " ")
					at := len(plain(row)) - len(text)
					if column < 0 {
						column = at
						continue
					}
					if at != column {
						t.Fatalf(
							"%s row %q starts its text at %d, the others at %d",
							open,
							plain(row),
							at,
							column,
						)
					}
				}
			}
		})
	}
}

// The popup belongs to the conversation: a menu drawn over the chat list
// would cover the chats a user reaches for next, and a menu that is not
// next to the message it acts on is a menu about nothing.
func TestThePopupIsDrawnOverTheConversation(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model = withProfile(model, theme.ProfileTrueColor)
	model, _ = updateModel(t, model, pressRunes("a"))

	layout := LayoutFor(model.width, model.height)
	rows, firstRow := m_popupRowsAndRowFor(model, layout)
	if len(rows) == 0 {
		t.Fatal("the sheet drew nothing")
	}

	want := model.popupFirstColumn(layout)
	got := firstTextColumn(plain(m_popupRowAt(model.View(), firstRow)))
	if got < want {
		t.Fatalf(
			"the sheet starts at column %d, the conversation at %d: it is over the chat list",
			got,
			want,
		)
	}
}

// §5: one focused region at a time. While a popup is open it is the popup,
// and the timeline gives its accent line up: two regions marked at once is a
// screen where the user cannot tell which one has the keys.
func TestThePopupTakesTheFocusLineFromTheTimeline(t *testing.T) {
	model, _ := actionModel(t, MessageDeliveryQueued, false)
	model = withProfile(model, theme.ProfileTrueColor)
	model.focus = FocusHistory
	model, _ = updateModel(t, model, pressRunes("a"))

	view := model.View()
	bar := firstRune(theme.FocusBar)
	inConversation := 0
	for _, line := range viewLines(ansi.Strip(view)) {
		if !strings.ContainsRune(line, bar) {
			continue
		}
		inConversation++
	}

	// The popup has one focus line per row and the timeline has none, so
	// the rows that carry a bar are the rows of the popup: its own width,
	// indented from the conversation's column.
	popupRows, _ := m_popupRowsAndRowFor(model, LayoutFor(model.width, model.height))
	if inConversation > len(popupRows) {
		t.Fatalf(
			"%d rows carry a focus line and the popup has %d: the timeline is marked too",
			inConversation,
			len(popupRows),
		)
	}
	if inConversation == 0 {
		t.Fatal("the popup has no focus line of its own")
	}

	// The timeline alone is the opposite.
	closed, _ := actionModel(t, MessageDeliveryQueued, false)
	closed = withProfile(closed, theme.ProfileTrueColor)
	closed.focus = FocusHistory
	marked := 0
	for _, line := range viewLines(ansi.Strip(closed.View())) {
		if strings.ContainsRune(line, bar) {
			marked++
		}
	}
	if marked == 0 {
		t.Fatal("the timeline has no focus line while it is the focused region")
	}
}

// ---- helpers ----

// programModelWithOutput is a model built the way the program builds one,
// with the program's own output in it.
//
// It is the composition root of the program: the theme is resolved for the
// profile, the model is built from the dependencies, and the output the
// program writes its frames through is the one the model copies into.
func programModelWithOutput(
	t *testing.T,
	terminal *recordingWriter,
	isTerminal bool,
) Model {
	t.Helper()

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		MessageSubmitter: &recordingSubmitter{},
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model = uncolored(model)
	model.clipboard = terminalOutputOf(terminal, isTerminal)
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})

	return model
}

// terminalOutputOf builds the program's output over a writer.
func terminalOutputOf(terminal *recordingWriter, isTTY bool) *terminalOutput {
	return newTerminalOutput(terminal, isTTY)
}

// openedConversationWith opens a chat with one pending message in a state.
func openedConversationWith(
	t *testing.T,
	model Model,
	state MessageDeliveryState,
) Model {
	t.Helper()

	created := actionFixtureMoment
	pending := &pendingSource{messages: []PendingMessage{{
		EntryID:   "entry-1",
		ChatID:    7,
		Text:      actionFixtureText,
		State:     state,
		Version:   7,
		CreatedAt: created,
	}}}
	model.pendingMessages = pending

	model, _ = updateModel(t, model, chatsLoadedMsg{
		chats: []Chat{{ID: 7, Title: "A"}},
	})
	model, _ = updateModel(t, model, press(tea.KeyEnter))
	model, _ = updateModel(t, model, deliveryRefresh(t, model, pending))
	model = model.scrollToNewest()
	model.focus = FocusHistory

	return model
}

// popupRowsOf returns the rows of the open popup, whichever it is.
func popupRowsOf(t *testing.T, model Model) []string {
	t.Helper()

	if model.modal.open {
		return model.confirmModalRows()
	}

	return model.actionSheetRows()
}

// m_popupRowsAndRowFor is the placement of the open popup.
func m_popupRowsAndRowFor(
	model Model,
	layout Layout,
) ([]string, int) {
	rows, firstRow := model.popupRowsAndRow(layout)

	return rows, firstRow
}

// m_popupRowAt returns one screen row of a rendered screen.
func m_popupRowAt(view string, row int) string {
	lines := strings.Split(view, "\n")
	if row < 0 || row >= len(lines) {
		return ""
	}

	return lines[row]
}

// firstTextColumn returns the column the first visible character of a row is
// in.
func firstTextColumn(row string) int {
	return len(row) - len(strings.TrimLeft(row, " "))
}
