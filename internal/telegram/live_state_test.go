package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The JSON fixtures below follow the pinned TDLib schema,
// td/generate/scheme/td_api.tl at commit ea97bcdd (TDLib 1.8.67):
//
//	chatPosition list:ChatList order:int64 is_pinned:Bool source:ChatSource
//	    = ChatPosition;                                              // :3545
//	chat ... last_message:message positions:vector<chatPosition> ...;  // :3628
//	updateNewChat chat:chat = Update;                              // :10483
//	updateChatLastMessage chat_id:int53 last_message:message
//	    positions:vector<chatPosition> = Update;                    // :10507
//	updateChatPosition chat_id:int53 position:chatPosition
//	    = Update;                                                  // :10512
//	updateChatDraftMessage chat_id:int53 draft_message:draftMessage
//	    positions:vector<chatPosition> = Update;                    // :10539
//
// positions is therefore a bare JSON array of chatPosition, and the chat
// list is the position's `list` field. `source` is a ChatSource and must
// stay null in these fixtures. Fixtures must not be shaped to match the
// implementation: a fixture written to the parser keeps both wrong.

// ---- Applying updates ----

func TestApplyNewChatAddsChat(t *testing.T) {
	state := NewLiveState()

	changed, err := state.apply(RawMessage(reviewNewChat))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("updateNewChat must report a change")
	}

	chats := state.ChatList()
	if len(chats) != 1 {
		t.Fatalf("ChatList() = %d chats, want 1", len(chats))
	}
	got := chats[0]
	if got.ID != 10 {
		t.Fatalf("ID = %d, want 10", got.ID)
	}
	if got.Title != "A" {
		t.Fatalf("Title = %q, want A", got.Title)
	}
	if got.Order != 500 {
		t.Fatalf("Order = %d, want 500", got.Order)
	}
	if got.UnreadCount != 2 {
		t.Fatalf("UnreadCount = %d, want 2", got.UnreadCount)
	}
	if got.LastMessage == nil {
		t.Fatal("LastMessage is nil")
	}
	if got.LastMessage.ID != 900 || got.LastMessage.Text != "hey" {
		t.Fatalf("LastMessage = %+v, want id=900 text=hey", got.LastMessage)
	}
	if !got.LastMessage.Outgoing {
		t.Fatal("LastMessage.Outgoing = false, want true")
	}
}

// reviewNewChat is a real-format updateNewChat: positions is an array and
// the list is in `list`.
const reviewNewChat = `{"@type":"updateNewChat","chat":{"@type":"chat","id":10,"title":"A","unread_count":2,` +
	`"last_message":{"@type":"message","id":900,"chat_id":10,"is_outgoing":true,"date":1700000000,` +
	`"content":{"@type":"messageText","text":{"@type":"formattedText","text":"hey"}}},` +
	`"positions":[{"@type":"chatPosition","list":{"@type":"chatListMain"},"order":"500",` +
	`"is_pinned":false,"source":null}]}}`

func TestApplyChatTitleUpdatesTitle(t *testing.T) {
	state := seededLiveState(t)

	changed, err := state.apply(RawMessage(
		`{"@type":"updateChatTitle","chat_id":7,"title":"Renamed"}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("a new title must report a change")
	}
	if got := state.ChatList()[0].Title; got != "Renamed" {
		t.Fatalf("Title = %q, want Renamed", got)
	}
}

func TestApplyChatPositionSetsOrder(t *testing.T) {
	state := seededLiveState(t)

	changed, err := state.apply(RawMessage(`{"@type":"updateChatPosition","chat_id":7,` +
		`"position":{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"500","is_pinned":false,"source":null}}`))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("a new order must report a change")
	}
	if got := state.ChatList()[0].Order; got != 500 {
		t.Fatalf("Order = %d, want 500", got)
	}
}

// TDLib: "If new order is 0, then the chat needs to be removed from the
// list" (td_api.tl:10511). The list is the one named in the position,
// which for a removal is chatListMain.
func TestApplyChatPositionZeroOrderRemovesChatFromMainList(t *testing.T) {
	state := seededLiveState(t)

	changed, err := state.apply(RawMessage(`{"@type":"updateChatPosition","chat_id":7,` +
		`"position":{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"0","is_pinned":false,"source":null}}`))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("order 0 must report a change")
	}
	if chats := state.ChatList(); len(chats) != 0 {
		t.Fatalf("ChatList() = %d chats, want 0", len(chats))
	}
}

// A chat can be in the main list and in a folder at once. updateChatPosition
// names one list, so a position for any other list says nothing about the
// main list and must leave it alone.
func TestApplyChatPositionForAnotherListDoesNotAffectMainList(t *testing.T) {
	for _, list := range []string{
		`{"@type":"chatListArchive"}`,
		`{"@type":"chatListFolder","chat_folder_id":2}`,
	} {
		state := seededLiveState(t)
		drainChanged(state)

		changed, err := state.apply(RawMessage(`{"@type":"updateChatPosition","chat_id":7,` +
			`"position":{"@type":"chatPosition","list":` + list + `,` +
			`"order":"900","is_pinned":false,"source":null}}`))
		if err != nil {
			t.Fatalf("apply %s: %v", list, err)
		}
		if changed {
			t.Fatalf("a position in %s must not change the main list", list)
		}

		chats := state.ChatList()
		if len(chats) != 1 {
			t.Fatalf("ChatList() = %d chats, want 1 for %s", len(chats), list)
		}
		if chats[0].Order != 100 {
			t.Fatalf("Order = %d, want 100 (unchanged for %s)", chats[0].Order, list)
		}

		select {
		case <-state.Changed():
			t.Fatalf("a position in %s must not signal a change", list)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// A chatSource is not a chat list, so a position whose list is unknown
// must not be treated as main.
func TestApplyChatPositionWithChatSourceIsNotMainList(t *testing.T) {
	state := seededLiveState(t)

	changed, err := state.apply(RawMessage(`{"@type":"updateChatPosition","chat_id":7,` +
		`"position":{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"500","is_pinned":false,"source":{"@type":"chatSourcePublic"}}}`))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("a main-list position applies regardless of source")
	}
	if got := state.ChatList()[0].Order; got != 500 {
		t.Fatalf("Order = %d, want 500", got)
	}
}

func TestApplyChatLastMessageUpdatesMessageAndPositions(t *testing.T) {
	state := seededLiveState(t)

	changed, err := state.apply(RawMessage(`{"@type":"updateChatLastMessage","chat_id":7,` +
		`"last_message":{"@type":"message","id":950,"chat_id":7,"is_outgoing":true,` +
		`"date":1700000500,"content":{"@type":"messageText",` +
		`"text":{"@type":"formattedText","text":"newest"}}},` +
		`"positions":[{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"900","is_pinned":false,"source":null}]}`))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("a new last message must report a change")
	}

	got := state.ChatList()[0]
	if got.Order != 900 {
		t.Fatalf("Order = %d, want 900 (positions must be applied)", got.Order)
	}
	if got.LastMessage == nil || got.LastMessage.ID != 950 || !got.LastMessage.Outgoing {
		t.Fatalf("LastMessage = %+v, want outgoing id=950", got.LastMessage)
	}
	if got.LastMessage.Text != "newest" {
		t.Fatalf("LastMessage.Text = %q, want newest", got.LastMessage.Text)
	}
}

// A positions vector with no chatListMain entry means the chat left the
// main list.
func TestApplyChatLastMessageDropsChatLeftFromMainList(t *testing.T) {
	state := seededLiveState(t)

	changed, err := state.apply(RawMessage(`{"@type":"updateChatLastMessage","chat_id":7,` +
		`"last_message":{"@type":"message","id":950,"chat_id":7,"date":1700000500,` +
		`"content":{"@type":"messageText","text":{"@type":"formattedText","text":"newest"}}},` +
		`"positions":[{"@type":"chatPosition","list":{"@type":"chatListArchive"},` +
		`"order":"900","is_pinned":false,"source":null}]}`))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("losing the main-list position must report a change")
	}
	if chats := state.ChatList(); len(chats) != 0 {
		t.Fatalf("ChatList() = %d chats, want 0", len(chats))
	}
}

func TestApplyChatDraftMessageAppliesOnlyPositions(t *testing.T) {
	state := seededLiveState(t)
	before := state.ChatList()[0]

	changed, err := state.apply(RawMessage(`{"@type":"updateChatDraftMessage","chat_id":7,` +
		`"draft_message":{"@type":"draftMessage","date":1700000000,"is_ephemeral":false,` +
		`"input_message_text":{"@type":"inputMessageText",` +
		`"text":{"@type":"formattedText","text":"unsent"}}},` +
		`"positions":[{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"750","is_pinned":false,"source":null}]}`))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("new positions must report a change")
	}

	after := state.ChatList()[0]
	if after.Order != 750 {
		t.Fatalf("Order = %d, want 750", after.Order)
	}
	if after.Title != before.Title {
		t.Fatalf("Title = %q, want %q (drafts must not change it)", after.Title, before.Title)
	}
	if after.UnreadCount != before.UnreadCount {
		t.Fatalf("UnreadCount = %d, want %d", after.UnreadCount, before.UnreadCount)
	}
}

func TestApplyChatReadInboxUpdatesUnreadCount(t *testing.T) {
	state := seededLiveState(t)

	// The seeded chat has unread_count 0, so 5 is a change and 0 is a
	// change back.
	changed, err := state.apply(RawMessage(
		`{"@type":"updateChatReadInbox","chat_id":7,"unread_count":5}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("a new unread count must report a change")
	}
	if got := state.ChatList()[0].UnreadCount; got != 5 {
		t.Fatalf("UnreadCount = %d, want 5", got)
	}

	changed, err = state.apply(RawMessage(
		`{"@type":"updateChatReadInbox","chat_id":7,"unread_count":0}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("clearing the unread count must report a change")
	}
	if got := state.ChatList()[0].UnreadCount; got != 0 {
		t.Fatalf("UnreadCount = %d, want 0", got)
	}
}

// The owner's channel had 89 unread and stayed at 89. The recorded answer
// TDLib sends after a read is the whole shape of updateChatReadInbox, with
// the identifier the read reached beside the count, and the count of the
// list is the count in it — the read pointer is TDLib's own bookkeeping and
// nothing in the projection has to understand it to show 0.
//
// The payload is the real one (td_api.tl: updateChatReadInbox chat_id:int53
// last_read_inbox_message_id:int53 unread_count:int32), so a field this
// decoder has never heard of and still has to ignore is part of the test.
func TestARecordedReadInboxTakesAChannelFromEightyNineToZero(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(RawMessage(
		`{"@type":"updateNewChat","chat":{"@type":"chat","id":` +
			`-1001234567890,"title":"Release Notes","unread_count":89,` +
			`"type":{"@type":"chatTypeSupergroup","is_channel":true},"positions":[` +
			`{"@type":"chatPosition","list":{"@type":"chatListMain"},"order":"700",` +
			`"is_pinned":false,"source":null}]}}`,
	)); err != nil {
		t.Fatalf("apply updateNewChat: %v", err)
	}

	changed, err := state.apply(RawMessage(
		`{"@type":"updateChatReadInbox","chat_id":-1001234567890,` +
			`"last_read_inbox_message_id":991,"unread_count":0}`,
	))
	if err != nil {
		t.Fatalf("apply updateChatReadInbox: %v", err)
	}
	if !changed {
		t.Fatal("clearing 89 unread must report a change")
	}

	chats := state.ChatList()
	if len(chats) != 1 {
		t.Fatalf("ChatList() = %d chats, want 1", len(chats))
	}
	if got := chats[0].UnreadCount; got != 0 {
		t.Fatalf("UnreadCount = %d, want 0", got)
	}
	if got := chats[0].Title; got != "Release Notes" {
		t.Fatalf("Title = %q, want the title the read did not touch", got)
	}
}

func TestApplyUnknownChatDoesNotCreateEntry(t *testing.T) {
	for _, raw := range []string{
		`{"@type":"updateChatTitle","chat_id":404,"title":"Ghost"}`,
		`{"@type":"updateChatPosition","chat_id":404,"position":{"@type":"chatPosition",` +
			`"list":{"@type":"chatListMain"},"order":"10","is_pinned":false,"source":null}}`,
		`{"@type":"updateChatLastMessage","chat_id":404,"last_message":null,"positions":[]}`,
		`{"@type":"updateChatReadInbox","chat_id":404,"unread_count":5}`,
	} {
		changed, err := NewLiveState().apply(RawMessage(raw))
		if err != nil {
			t.Fatalf("apply(%s): %v", raw, err)
		}
		if changed {
			t.Fatalf("apply(%s) reported a change for an unknown chat", raw)
		}
	}
}

// An update the store does not model at all is ignored, not reported.
// The message updates are not in this list: they became events in
// ADR-0003 step 2 and are covered in live_messages_test.go.
func TestApplyUnsupportedUpdateTypeIsIgnored(t *testing.T) {
	for _, raw := range []string{
		`{"@type":"updateUser","user":{"@type":"user","id":1}}`,
		`{"@type":"updateOption","name":"x","value":{"@type":"optionValueBoolean","value":true}}`,
		`{"@type":"updateCall","call":{"@type":"call","id":1}}`,
	} {
		state := seededLiveState(t)
		changed, err := state.apply(RawMessage(raw))
		if err != nil {
			t.Fatalf("apply(%s): %v", raw, err)
		}
		if changed {
			t.Fatalf("apply(%s) must not report a change", raw)
		}
	}
}

// liveStateUpdateFixtures is one real-shaped update per held type.
//
// A held type without a fixture here would be a type whose application is
// not checked at all, and the check is the whole point of holding it.
var liveStateUpdateFixtures = map[string]RawMessage{
	"updateNewChat": RawMessage(`{"@type":"updateNewChat","chat":{"@type":"chat","id":10,` +
		`"title":"A","unread_count":0,"positions":[]}}`),
	"updateChatTitle": RawMessage(`{"@type":"updateChatTitle","chat_id":10,"title":"B"}`),
	"updateChatPosition": RawMessage(`{"@type":"updateChatPosition","chat_id":10,"position":` +
		`{"@type":"chatPosition","list":{"@type":"chatListMain"},"order":"500",` +
		`"is_pinned":false,"source":null}}`),
	"updateChatLastMessage": RawMessage(`{"@type":"updateChatLastMessage","chat_id":10,` +
		`"last_message":{"@type":"message","id":900,"chat_id":10,"is_outgoing":false,` +
		`"date":1700000000,"content":{"@type":"messageText",` +
		`"text":{"@type":"formattedText","text":"hey"}}},"positions":[]}`),
	// The draft text itself is not modelled, so the position it carries is
	// what an updateChatDraftMessage changes.
	"updateChatDraftMessage": RawMessage(`{"@type":"updateChatDraftMessage","chat_id":10,` +
		`"draft_message":{"@type":"draftMessage","date":0,"position":null},"positions":[` +
		`{"@type":"chatPosition","list":{"@type":"chatListMain"},"order":"500",` +
		`"is_pinned":false,"source":null}]}`),
	"updateChatReadInbox": RawMessage(`{"@type":"updateChatReadInbox","chat_id":10,` +
		`"last_read_inbox_message_id":0,"unread_count":1}`),
	updateConnectionStateType: RawMessage(`{"@type":"updateConnectionState",` +
		`"state":{"@type":"connectionStateReady"}}`),
}

// Every type the coordinator holds must be one the store actually applies,
// otherwise an update would be preserved and then dropped.
func TestLiveStateUpdateTypesAreAllApplied(t *testing.T) {
	for updateType := range liveStateUpdateTypes {
		raw, hasFixture := liveStateUpdateFixtures[updateType]
		if !hasFixture {
			t.Fatalf("%s is held but has no fixture to apply", updateType)
		}
		if !isLiveStateUpdate(raw) {
			t.Fatalf("%s: isLiveStateUpdate = false", updateType)
		}

		// The chat updates need a chat to apply to, so a chat is seeded
		// first; the connection state needs nothing. The seed has another
		// title, so the updateNewChat fixture is still a change.
		state := NewLiveState()
		mustApplyLiveChatWithoutPositions(t, state, 10, "Seed")

		changed, err := state.apply(raw)
		if err != nil {
			t.Fatalf("%s: apply: %v", updateType, err)
		}
		if !changed {
			t.Fatalf("%s: a held update changed nothing", updateType)
		}
	}
}

func TestApplyMalformedOrderReportsErrorAndKeepsState(t *testing.T) {
	state := seededLiveState(t)
	before := state.ChatList()

	_, err := state.apply(RawMessage(`{"@type":"updateChatPosition","chat_id":7,` +
		`"position":{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"not-a-number","is_pinned":false,"source":null}}`))
	if err == nil {
		t.Fatal("an unparsable order must be reported as an error")
	}
	if !errors.Is(err, ErrLiveStateOrder) {
		t.Fatalf("err = %v, want ErrLiveStateOrder", err)
	}

	after := state.ChatList()
	if len(after) != len(before) {
		t.Fatalf("chat count = %d, want %d", len(after), len(before))
	}
	if after[0].Order != before[0].Order {
		t.Fatalf("Order = %d, want %d (state must be unchanged)", after[0].Order, before[0].Order)
	}
}

// ---- Ordering ----

func TestChatListSortsByOrderThenIDDescending(t *testing.T) {
	state := NewLiveState()
	mustApplyLiveChat(t, state, 1, "one", 100)
	mustApplyLiveChat(t, state, 2, "two", 300)
	mustApplyLiveChat(t, state, 3, "three", 300)
	mustApplyLiveChat(t, state, 4, "four", 200)

	got := liveIDs(state.ChatList())
	want := []ChatID{3, 2, 4, 1}
	if len(got) != len(want) {
		t.Fatalf("ChatList() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ChatList() = %v, want %v (equal orders break by ID descending)", got, want)
		}
	}
}

// Telegram draws a pinned chat above the rest of the list however old its
// last message is (td_api.tl:3545), and the pinned chats keep the order of
// their own positions among themselves.
func TestChatListPutsPinnedChatsFirstInTheirOwnOrder(t *testing.T) {
	state := NewLiveState()
	mustApplyLiveChat(t, state, 1, "newest", 900)
	mustApplyLiveChat(t, state, 2, "middle", 500)
	mustApplyLiveChat(t, state, 3, "pinned, newer of the two", 400)
	mustApplyLiveChat(t, state, 4, "oldest", 100)
	mustApplyLiveChat(t, state, 5, "pinned, older of the two", 200)

	mustPinLiveChat(t, state, 5, 200)
	mustPinLiveChat(t, state, 3, 400)

	got := liveIDs(state.ChatList())
	want := []ChatID{3, 5, 1, 2, 4}
	if len(got) != len(want) {
		t.Fatalf("ChatList() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf(
				"ChatList() = %v, want %v (pinned first, then by order)",
				got, want,
			)
		}
	}
}

// A chat that is pinned in a folder and not in the main list is not pinned
// in the list the interface draws: is_pinned belongs to one position, and
// that position names the list it is in.
func TestChatListIgnoresPinnedOutsideTheMainList(t *testing.T) {
	state := NewLiveState()
	mustApplyLiveChat(t, state, 1, "newer", 900)
	mustApplyLiveChatInList(t, state, 2, "in a folder", 100, `{"@type":"chatListFolder","chat_folder_id":2}`)

	raw := `{"@type":"updateChatPosition","chat_id":2,"position":{"@type":"chatPosition",` +
		`"list":{"@type":"chatListFolder","chat_folder_id":2},"order":"100",` +
		`"is_pinned":true,"source":null}}`
	if _, err := state.apply(RawMessage(raw)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got := liveIDs(state.ChatList())
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("ChatList() = %v, want [1]", got)
	}
}

func TestChatListExcludesZeroOrderAndForeignLists(t *testing.T) {
	state := NewLiveState()
	mustApplyLiveChat(t, state, 1, "listed", 100)
	mustApplyLiveChat(t, state, 2, "zero order", 0)
	mustApplyLiveChatInList(t, state, 3, "archived", 900, `{"@type":"chatListArchive"}`)
	mustApplyLiveChatWithoutPositions(t, state, 4, "no position")
	mustApplyLiveChatInList(t, state, 5, "in a folder", 800, `{"@type":"chatListFolder","chat_folder_id":2}`)

	got := liveIDs(state.ChatList())
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("ChatList() = %v, want [1]", got)
	}
}

// ---- Changed() coalescing ----

func TestChangedSignalsOncePerChange(t *testing.T) {
	state := seededLiveState(t)
	drainChanged(state)

	if _, err := state.apply(RawMessage(
		`{"@type":"updateChatTitle","chat_id":7,"title":"A"}`,
	)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	select {
	case <-state.Changed():
	case <-time.After(time.Second):
		t.Fatal("no change signal after an update that changed state")
	}
}

func TestChangedCoalescesManyChangesIntoOneSignal(t *testing.T) {
	state := seededLiveState(t)
	drainChanged(state)

	// Ten changes with no reader in between. The pump must never block and
	// the channel must hold exactly one pending signal.
	for i := 0; i < 10; i++ {
		raw := `{"@type":"updateChatTitle","chat_id":7,"title":"t` +
			strconv.Itoa(i) + `"}`
		if _, err := state.apply(RawMessage(raw)); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}

	select {
	case <-state.Changed():
	case <-time.After(time.Second):
		t.Fatal("no signal after ten changes")
	}

	select {
	case <-state.Changed():
		t.Fatal("channel must coalesce ten changes into a single signal")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestChangedSilentForUpdateWithoutChange(t *testing.T) {
	state := seededLiveState(t)
	drainChanged(state)

	// The same title again: nothing changed, so nothing is signalled.
	changed, err := state.apply(RawMessage(
		`{"@type":"updateChatTitle","chat_id":7,"title":"Alice"}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if changed {
		t.Fatal("apply must report no change for an identical update")
	}

	select {
	case <-state.Changed():
		t.Fatal("an update that changed nothing must not signal")
	case <-time.After(100 * time.Millisecond):
	}
}

// ---- Snapshot isolation ----

func TestChatListReturnsCopyUnaffectedByLaterUpdates(t *testing.T) {
	state := seededLiveState(t)

	snapshot := state.ChatList()
	if _, err := state.apply(RawMessage(
		`{"@type":"updateChatTitle","chat_id":7,"title":"Mutated"}`,
	)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if snapshot[0].Title != "Alice" {
		t.Fatalf("snapshot Title = %q, want Alice", snapshot[0].Title)
	}
	if state.ChatList()[0].Title != "Mutated" {
		t.Fatalf("live Title = %q, want Mutated", state.ChatList()[0].Title)
	}
}

func TestLastMessageIsCopied(t *testing.T) {
	state := NewLiveState()
	mustApplyLiveChat(t, state, 1, "one", 100)

	first := state.ChatList()
	if first[0].LastMessage == nil {
		t.Fatal("LastMessage is nil")
	}
	first[0].LastMessage.Text = "mutated by caller"

	second := state.ChatList()
	if second[0].LastMessage == nil {
		t.Fatal("LastMessage is nil on the second snapshot")
	}
	if second[0].LastMessage.Text == "mutated by caller" {
		t.Fatal("the store handed out its own Message; callers must get copies")
	}
}

func TestConcurrentReadersAndWriterAreRaceFree(t *testing.T) {
	state := NewLiveState()
	mustApplyLiveChat(t, state, 1, "one", 100)
	mustApplyLiveChat(t, state, 2, "two", 200)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, chat := range state.ChatList() {
					_ = chat.Title
					_ = chat.Order
					if chat.LastMessage != nil {
						_ = chat.LastMessage.Text
					}
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			raw := `{"@type":"updateChatTitle","chat_id":1,"title":"t` +
				strconv.Itoa(i) + `"}`
			if _, err := state.apply(RawMessage(raw)); err != nil {
				return
			}
		}
		close(stop)
	}()

	wg.Wait()
}

// ---- Invalid input ----

func TestApplyRejectsMalformedJSON(t *testing.T) {
	if _, err := NewLiveState().apply(RawMessage(`{"@type":`)); err == nil {
		t.Fatal("malformed JSON must be reported as an error")
	}
}

// A positions object instead of an array must be reported, not silently
// dropped: that was the shape this store got wrong once.
func TestApplyRejectsChatPositionsWrapperObject(t *testing.T) {
	_, err := NewLiveState().apply(RawMessage(`{"@type":"updateNewChat","chat":{"@type":"chat",` +
		`"id":10,"title":"A","unread_count":0,` +
		`"positions":{"@type":"chatPositions","positions":[]}}}`))
	if err == nil {
		t.Fatal("a chatPositions wrapper object must be reported as an error")
	}
}

func TestNilLiveStateAccessorsAreSafe(t *testing.T) {
	var state *LiveState
	if chats := state.ChatList(); chats != nil {
		t.Fatalf("ChatList() = %v, want nil", chats)
	}
	if state.Changed() != nil {
		t.Fatal("Changed() must be nil for a nil store")
	}
}

// ---- Pump wiring ----

func TestPumpAppliesUpdateNewChatToLiveState(t *testing.T) {
	session, _, _, client := newSessionWithFakes(t)

	client.updates <- Update{ClientID: client.id, Raw: RawMessage(reviewNewChat)}

	waitForLiveChat(t, session.LiveState(), 10)
}

func TestPumpKeepsQueryRepliesWorking(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	// An update that changes the store, then a query reply.
	client.updates <- Update{ClientID: client.id, Raw: RawMessage(reviewNewChat)}
	waitForLiveChat(t, session.LiveState(), 10)

	type result struct {
		raw RawMessage
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		raw, err := session.Query(
			context.Background(),
			RawMessage(`{"@type":"getMe"}`),
		)
		resultCh <- result{raw: raw, err: err}
	}()

	request := waitForSentRequest(t, sender)
	extra := extractExtra(t, request)

	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"user","id":42,"@extra":"` + extra + `"}`),
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("Query: %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Query did not return after the update was applied")
	}
}

func TestPumpRecordsOrderErrorAndKeepsState(t *testing.T) {
	session, _, _, client := newSessionWithFakes(t)
	state := session.LiveState()
	mustApplyLiveChat(t, state, 7, "Alice", 100)

	client.updates <- Update{ClientID: client.id, Raw: RawMessage(
		`{"@type":"updateChatPosition","chat_id":7,` +
			`"position":{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
			`"order":"oops","is_pinned":false,"source":null}}`,
	)}

	select {
	case err := <-session.Errors():
		if !errors.Is(err, ErrLiveStateOrder) {
			t.Fatalf("err = %v, want ErrLiveStateOrder", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a bad order was not reported on session.Errors()")
	}

	if got := state.ChatList()[0].Order; got != 100 {
		t.Fatalf("Order = %d, want 100 (state must be unchanged)", got)
	}
}

// ---- Held updates from the authorization phase ----

func TestUpdatesHeldDuringAuthAreAppliedBeforePumpUpdates(t *testing.T) {
	sender := &fakeSender{}
	closer := &fakeCloser{}
	client := newTestClient(t)

	held := []RawMessage{
		RawMessage(`{"@type":"updateNewChat","chat":{"@type":"chat","id":11,` +
			`"title":"During auth","unread_count":0,` +
			`"positions":[{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
			`"order":"300","is_pinned":false,"source":null}]}}`),
		RawMessage(`{"@type":"updateChatTitle","chat_id":11,"title":"After auth rename"}`),
	}

	session := newAuthorizedSession(sender, closer, client, 100*time.Millisecond, held)
	t.Cleanup(func() {
		session.cancel()
		select {
		case <-session.pumpDone:
		case <-time.After(time.Second):
			t.Error("session pump did not exit during cleanup")
		}
	})

	chat := waitForLiveChat(t, session.LiveState(), 11)
	if chat.Title != "After auth rename" {
		t.Fatalf("Title = %q, want %q (held updates must be applied in order)",
			chat.Title, "After auth rename")
	}
	if chat.Order != 300 {
		t.Fatalf("Order = %d, want 300", chat.Order)
	}
}

// ---- LoadChats ----

func TestLoadChatsOKMeansMoreChatsRemain(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		complete bool
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		complete, err := session.LoadChats(context.Background(), 20)
		resultCh <- result{complete: complete, err: err}
	}()

	request := waitForSentRequest(t, sender)
	assertLoadChatsRequest(t, request, 20)
	extra := extractExtra(t, request)

	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"ok","@extra":"` + extra + `"}`),
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("LoadChats: %v", r.err)
		}
		if r.complete {
			t.Fatal("complete = true, want false for an ok response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LoadChats did not return")
	}
}

func TestLoadChatsNotFoundMeansComplete(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		complete bool
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		complete, err := session.LoadChats(context.Background(), 20)
		resultCh <- result{complete: complete, err: err}
	}()

	request := waitForSentRequest(t, sender)
	extra := extractExtra(t, request)

	client.updates <- Update{
		ClientID: client.id,
		Raw: RawMessage(`{"@type":"error","code":404,` +
			`"message":"Chat list is already loaded","@extra":"` + extra + `"}`),
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("a 404 is not a failure, got %v", r.err)
		}
		if !r.complete {
			t.Fatal("complete = false, want true for a 404 response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LoadChats did not return")
	}
}

func TestLoadChatsOtherErrorIsReturned(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		complete bool
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		complete, err := session.LoadChats(context.Background(), 20)
		resultCh <- result{complete: complete, err: err}
	}()

	request := waitForSentRequest(t, sender)
	extra := extractExtra(t, request)

	client.updates <- Update{
		ClientID: client.id,
		Raw: RawMessage(`{"@type":"error","code":400,` +
			`"message":"Chat list can't be loaded","@extra":"` + extra + `"}`),
	}

	select {
	case r := <-resultCh:
		if r.err == nil {
			t.Fatal("a 400 must be returned as an error")
		}
		if !errors.Is(r.err, ErrTDLibResponse) {
			t.Fatalf("err = %v, want ErrTDLibResponse", r.err)
		}
		if r.complete {
			t.Fatal("complete = true on a failing response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LoadChats did not return")
	}
}

func TestLoadChatsRejectsInvalidLimitWithoutRequest(t *testing.T) {
	session, sender, _, _ := newSessionWithFakes(t)

	for _, limit := range []int{0, -1, maxChatListLimit + 1} {
		complete, err := session.LoadChats(context.Background(), limit)
		if err == nil {
			t.Fatalf("limit %d: expected an error", limit)
		}
		if !errors.Is(err, ErrInvalidChatLimit) {
			t.Fatalf("limit %d: err = %v, want ErrInvalidChatLimit", limit, err)
		}
		if complete {
			t.Fatalf("limit %d: complete = true, want false", limit)
		}
	}

	if requests := sender.count(); requests != 0 {
		t.Fatalf("sent %d requests, want 0 for an invalid limit", requests)
	}
}

func TestLoadChatsAcceptsMaxLimit(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		complete bool
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		complete, err := session.LoadChats(context.Background(), maxChatListLimit)
		resultCh <- result{complete: complete, err: err}
	}()

	request := waitForSentRequest(t, sender)
	assertLoadChatsRequest(t, request, maxChatListLimit)
	extra := extractExtra(t, request)

	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"ok","@extra":"` + extra + `"}`),
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("LoadChats(max): %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LoadChats did not return for the maximum limit")
	}
}

// ---- helpers ----

// newChatRaw builds a real-format updateNewChat for one listed chat, with
// an optional extra field spliced in after the @type.
//
// Fixtures are built from the schema rather than hand-written per test, so
// a wrong shape cannot hide behind a parser that agrees with it.
func newChatRaw(id ChatID, title string, order int64, extra string) RawMessage {
	position := `{"@type":"chatPosition","list":{"@type":"chatListMain"},"order":"` +
		strconv.FormatInt(order, 10) + `","is_pinned":false,"source":null}`
	if extra != "" {
		extra = "," + extra
	}
	return RawMessage(`{"@type":"updateNewChat"` + extra + `,"chat":{"@type":"chat","id":` +
		strconv.Itoa(int(id)) + `,"title":"` + title + `","unread_count":0,` +
		`"positions":[` + position + `]}}`)
}

// seededLiveState returns a store holding one listed chat, id 7.
func seededLiveState(t *testing.T) *LiveState {
	t.Helper()
	state := NewLiveState()
	mustApplyLiveChat(t, state, 7, "Alice", 100)
	drainChanged(state)
	return state
}

// mustApplyLiveChat adds a listed chat with one text last message.
func mustApplyLiveChat(t *testing.T, state *LiveState, id ChatID, title string, order int64) {
	t.Helper()
	mustApplyLiveChatInList(
		t, state, id, title, order, `{"@type":"chatListMain"}`,
	)
}

// mustApplyLiveChatInList adds a chat positioned in the given list.
func mustApplyLiveChatInList(
	t *testing.T,
	state *LiveState,
	id ChatID,
	title string,
	order int64,
	list string,
) {
	t.Helper()
	raw := `{"@type":"updateNewChat","chat":{"@type":"chat","id":` + strconv.Itoa(int(id)) +
		`,"title":"` + title + `","unread_count":0,` +
		`"last_message":{"@type":"message","id":1000,"chat_id":` + strconv.Itoa(int(id)) +
		`,"date":1700000000,"content":{"@type":"messageText",` +
		`"text":{"@type":"formattedText","text":"hi"}}},` +
		`"positions":[{"@type":"chatPosition","list":` + list + `,"order":"` +
		strconv.FormatInt(order, 10) + `","is_pinned":false,"source":null}]}}`
	if _, err := state.apply(RawMessage(raw)); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
}

// mustPinLiveChat pins a chat in the main list at the given order, the way
// TDLib says it with updateChatPosition: the flag and the order arrive
// together in one position.
func mustPinLiveChat(t *testing.T, state *LiveState, id ChatID, order int64) {
	t.Helper()
	raw := `{"@type":"updateChatPosition","chat_id":` + strconv.Itoa(int(id)) +
		`,"position":{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"` + strconv.FormatInt(order, 10) +
		`","is_pinned":true,"source":null}}`
	if _, err := state.apply(RawMessage(raw)); err != nil {
		t.Fatalf("pin apply: %v", err)
	}
}

// mustApplyLiveChatWithoutPositions adds a chat with no positions at all.
func mustApplyLiveChatWithoutPositions(
	t *testing.T,
	state *LiveState,
	id ChatID,
	title string,
) {
	t.Helper()
	raw := `{"@type":"updateNewChat","chat":{"@type":"chat","id":` + strconv.Itoa(int(id)) +
		`,"title":"` + title + `","unread_count":0}}`
	if _, err := state.apply(RawMessage(raw)); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
}

// drainChanged consumes any pending change signal.
func drainChanged(state *LiveState) {
	for {
		select {
		case <-state.Changed():
		default:
			return
		}
	}
}

// liveIDs extracts chat IDs for readable failure output.
func liveIDs(chats []LiveChat) []ChatID {
	ids := make([]ChatID, 0, len(chats))
	for _, chat := range chats {
		ids = append(ids, chat.ID)
	}
	return ids
}

// waitForLiveChat waits until chatID appears in the store's list.
func waitForLiveChat(t *testing.T, state *LiveState, chatID ChatID) LiveChat {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, chat := range state.ChatList() {
			if chat.ID == chatID {
				return chat
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("chat %d did not appear in LiveState", chatID)
	return LiveChat{}
}

// assertLoadChatsRequest checks the request shape sent for loadChats.
func assertLoadChatsRequest(t *testing.T, request RawMessage, limit int) {
	t.Helper()

	var decoded struct {
		Type     string `json:"@type"`
		ChatList struct {
			Type string `json:"@type"`
		} `json:"chat_list"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode loadChats request %s: %v", request, err)
	}
	if decoded.Type != "loadChats" {
		t.Fatalf("@type = %q, want loadChats", decoded.Type)
	}
	if decoded.ChatList.Type != "chatListMain" {
		t.Fatalf("chat_list.@type = %q, want chatListMain", decoded.ChatList.Type)
	}
	if decoded.Limit != limit {
		t.Fatalf("limit = %d, want %d", decoded.Limit, limit)
	}
}
