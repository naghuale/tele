package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fixtures below follow the pinned TDLib schema,
// td/generate/scheme/td_api.tl at commit ea97bcdd (TDLib 1.8.67):
//
//	updateNewMessage message:message = Update;                      // :10401
//	updateMessageSendSucceeded message:message old_message_id:int53
//	    = Update;                                                   // :10413
//	updateMessageSendFailed message:message old_message_id:int53
//	    error:error = Update;                                       // :10419
//	updateDeleteMessages chat_id:int53 message_ids:vector<int53>
//	    is_permanent:Bool from_cache:Bool = Update;                 // :10701
//
// The shapes that are easy to get wrong and are therefore pinned here:
// updateNewMessage has no chat_id of its own, the chat comes from
// message.chat_id, and updateDeleteMessages is the only one of the four
// with a top-level chat_id and a plain array of int53.

// reviewMessageBody is the body of the message every fixture carries. A
// test that must prove no body text escapes does so against this value.
const reviewMessageBody = "review-message-body-must-not-escape"

// ---- One event per update type ----

func TestNewMessageBecomesMessageAdded(t *testing.T) {
	state := NewLiveState()

	changed, err := state.apply(RawMessage(newMessageUpdate(
		7, 501, false, reviewMessageBody,
	)))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("a message event must report a change")
	}

	events, next, resync := state.MessageEventsSince(7, 0)
	if next != 1 {
		t.Fatalf("next = %d, want 1", next)
	}
	if resync {
		t.Fatal("a consumer that asked from 0 must not be told to resync")
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	added, ok := events[0].(MessageAdded)
	if !ok {
		t.Fatalf("event = %T, want MessageAdded", events[0])
	}
	if added.Message.ID != 501 {
		t.Fatalf("Message.ID = %d, want 501", added.Message.ID)
	}
	if added.Message.ChatID != 7 {
		t.Fatalf("Message.ChatID = %d, want 7", added.Message.ChatID)
	}
	if added.Message.Text != reviewMessageBody {
		t.Fatalf("Message.Text = %q, want %q", added.Message.Text, reviewMessageBody)
	}
	if added.Message.Outgoing {
		t.Fatal("Message.Outgoing = true, want false for an incoming message")
	}
	if added.Message.Timestamp.IsZero() {
		t.Fatal("Message.Timestamp is zero")
	}
}

// An outgoing message arrives twice: once with the temporary ID the send
// returned, once replaced. Both must be events, or a sent message would
// never appear in a live chat.
func TestOutgoingNewMessageIsAdded(t *testing.T) {
	state := NewLiveState()

	if _, err := state.apply(RawMessage(
		newMessageUpdate(7, 99, true, "temporary body"),
	)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	events, _, _ := state.MessageEventsSince(7, 0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	added, ok := events[0].(MessageAdded)
	if !ok {
		t.Fatalf("event = %T, want MessageAdded", events[0])
	}
	if !added.Message.Outgoing {
		t.Fatal("Message.Outgoing = false, want true")
	}
	if added.Message.ID != 99 {
		t.Fatalf("Message.ID = %d, want the temporary id 99", added.Message.ID)
	}
}

func TestSendSucceededBecomesMessageReplaced(t *testing.T) {
	state := NewLiveState()

	_, err := state.apply(RawMessage(
		`{"@type":"updateMessageSendSucceeded","old_message_id":99,"message":` +
			messageJSON(7, 1234, true, "final body") + `}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	events, _, _ := state.MessageEventsSince(7, 0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	replaced, ok := events[0].(MessageReplaced)
	if !ok {
		t.Fatalf("event = %T, want MessageReplaced", events[0])
	}
	if replaced.OldID != 99 {
		t.Fatalf("OldID = %d, want the temporary id 99", replaced.OldID)
	}
	if replaced.Message.ID != 1234 {
		t.Fatalf("Message.ID = %d, want 1234", replaced.Message.ID)
	}
	if replaced.Message.ChatID != 7 {
		t.Fatalf("Message.ChatID = %d, want 7", replaced.Message.ChatID)
	}
}

func TestSendFailedBecomesMessageFailed(t *testing.T) {
	state := NewLiveState()

	_, err := state.apply(RawMessage(
		`{"@type":"updateMessageSendFailed","old_message_id":99,"message":` +
			messageJSON(7, 99, true, reviewMessageBody) +
			`,"error":{"@type":"error","code":420,"message":"Message can't be sent"}}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	events, _, _ := state.MessageEventsSince(7, 0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	failed, ok := events[0].(MessageFailed)
	if !ok {
		t.Fatalf("event = %T, want MessageFailed", events[0])
	}
	if failed.OldID != 99 {
		t.Fatalf("OldID = %d, want 99", failed.OldID)
	}
	if failed.Message.ID != 99 {
		t.Fatalf("Message.ID = %d, want 99", failed.Message.ID)
	}
	if failed.Error.Code != 420 {
		t.Fatalf("Error.Code = %d, want 420", failed.Error.Code)
	}
	if failed.Error.Description != "Message can't be sent" {
		t.Fatalf(
			"Error.Description = %q, want TDLib's own text",
			failed.Error.Description,
		)
	}
}

// A failed send is the one event an error string is derived from, and an
// error string is what reaches a log line. The body of the message must
// not be in it, and neither must TDLib's description.
func TestMessageFailedCarriesNoBodyInItsError(t *testing.T) {
	state := NewLiveState()

	_, err := state.apply(RawMessage(
		`{"@type":"updateMessageSendFailed","old_message_id":99,"message":` +
			messageJSON(7, 99, true, reviewMessageBody) +
			`,"error":{"@type":"error","code":420,"message":"Message can't be sent"}}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	events, _, _ := state.MessageEventsSince(7, 0)
	failed, ok := events[0].(MessageFailed)
	if !ok {
		t.Fatalf("event = %T, want MessageFailed", events[0])
	}

	// Everything a log line could get from this event: the error string,
	// and the code and description the event carries. The body must be in
	// none of them.
	safe := []string{
		failed.Error.Error(),
		failed.Error.Reason(),
		strconv.Itoa(failed.Error.Code),
		failed.Error.Description,
		fmt.Sprintf("%+v", failed.Error),
	}
	for _, text := range safe {
		if strings.Contains(text, reviewMessageBody) {
			t.Fatalf("the message body leaked into %q", text)
		}
	}

	if !strings.Contains(failed.Error.Reason(), "code=420") {
		t.Fatalf(
			"Reason = %q, want the code and nothing transport-provided",
			failed.Error.Reason(),
		)
	}
}

func TestPermanentDeleteBecomesMessagesDeleted(t *testing.T) {
	state := NewLiveState()

	changed, err := state.apply(RawMessage(
		`{"@type":"updateDeleteMessages","chat_id":7,"message_ids":[11,12],` +
			`"is_permanent":true,"from_cache":false}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("a permanent deletion must report a change")
	}

	events, _, _ := state.MessageEventsSince(7, 0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	deleted, ok := events[0].(MessagesDeleted)
	if !ok {
		t.Fatalf("event = %T, want MessagesDeleted", events[0])
	}
	if len(deleted.IDs) != 2 || deleted.IDs[0] != 11 || deleted.IDs[1] != 12 {
		t.Fatalf("IDs = %v, want [11 12]", deleted.IDs)
	}
}

// TDLib: is_permanent = false only removes the messages from its cache.
// They are still there for the user, so the store must say nothing: a
// recorded deletion would make a message disappear from the interface
// and come back on the next history load.
func TestNonPermanentDeleteIsIgnored(t *testing.T) {
	state := NewLiveState()
	drainChanged(state)

	changed, err := state.apply(RawMessage(
		`{"@type":"updateDeleteMessages","chat_id":7,"message_ids":[11],` +
			`"is_permanent":false,"from_cache":true}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if changed {
		t.Fatal("a cache-only deletion must not report a change")
	}

	events, next, resync := state.MessageEventsSince(7, 0)
	if len(events) != 0 || next != 0 || resync {
		t.Fatalf(
			"events = %v, next = %d, resync = %v, want no event at all",
			events, next, resync,
		)
	}

	select {
	case <-state.Changed():
		t.Fatal("a cache-only deletion must not signal")
	case <-time.After(100 * time.Millisecond):
	}
}

// A deletion with no ids is an event that says nothing, and it must not
// spend a sequence number either.
func TestDeleteWithoutIDsIsIgnored(t *testing.T) {
	state := NewLiveState()

	changed, err := state.apply(RawMessage(
		`{"@type":"updateDeleteMessages","chat_id":7,"message_ids":[],` +
			`"is_permanent":true,"from_cache":false}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if changed {
		t.Fatal("a deletion of nothing must not report a change")
	}
	if _, next, _ := state.MessageEventsSince(7, 0); next != 0 {
		t.Fatalf("next = %d, want 0", next)
	}
}

// ---- Parsing failures ----

// A payload the store cannot read is reported and changes nothing, as in
// the chat-list path: the event would be filed under no chat at all.
func TestMalformedMessageUpdateIsReportedAndKeepsState(t *testing.T) {
	cases := map[string]string{
		"no message":      `{"@type":"updateNewMessage"}`,
		"message is null": `{"@type":"updateNewMessage","message":null}`,
		"message has no id": `{"@type":"updateNewMessage","message":` +
			`{"@type":"message","chat_id":7,"id":0,"date":1700000000,` +
			`"content":{"@type":"messageText","text":{"@type":"formattedText","text":"x"}}}}`,
		"message has no chat": `{"@type":"updateNewMessage","message":` +
			`{"@type":"message","id":5,"chat_id":0,"date":1700000000,` +
			`"content":{"@type":"messageText","text":{"@type":"formattedText","text":"x"}}}}`,
		"message is not an object":         `{"@type":"updateNewMessage","message":7}`,
		"send succeeded without a message": `{"@type":"updateMessageSendSucceeded","old_message_id":1}`,
		"send failed with a non-error error": `{"@type":"updateMessageSendFailed",` +
			`"old_message_id":1,"message":` + messageJSON(7, 1, true, "x") +
			`,"error":{"@type":"chat","id":7}}`,
		"delete with a string id": `{"@type":"updateDeleteMessages","chat_id":7,` +
			`"message_ids":["11"],"is_permanent":true,"from_cache":false}`,
		"delete without a chat": `{"@type":"updateDeleteMessages","chat_id":0,` +
			`"message_ids":[11],"is_permanent":true,"from_cache":false}`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			state := NewLiveState()

			changed, err := state.apply(RawMessage(raw))
			if err == nil {
				t.Fatalf("apply(%s) error = nil, want a reported failure", raw)
			}
			if changed {
				t.Fatal("a failed decode must not report a change")
			}
			if _, next, _ := state.MessageEventsSince(7, 0); next != 0 {
				t.Fatalf("next = %d, want 0 (state must be unchanged)", next)
			}
		})
	}
}

// A failed send without an error object still says which message failed,
// so it is an event and not a failure.
func TestSendFailedWithoutErrorObjectKeepsTheEvent(t *testing.T) {
	state := NewLiveState()

	_, err := state.apply(RawMessage(
		`{"@type":"updateMessageSendFailed","old_message_id":99,"message":` +
			messageJSON(7, 99, true, "body") + `}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	events, _, _ := state.MessageEventsSince(7, 0)
	failed, ok := events[0].(MessageFailed)
	if !ok {
		t.Fatalf("event = %T, want MessageFailed", events[0])
	}
	if failed.Error != (MessageError{}) {
		t.Fatalf("Error = %+v, want the zero value", failed.Error)
	}
	if failed.Error.Reason() != "telegram send error" {
		t.Fatalf("Reason = %q, want the generic text", failed.Error.Reason())
	}
}

// ---- The window and its sequence ----

func TestMessageEventsSinceReportsTheSequence(t *testing.T) {
	state := NewLiveState()

	for i := 1; i <= 3; i++ {
		mustApplyNewMessage(t, state, 7, int64(100+i), "body")
	}

	events, next, resync := state.MessageEventsSince(7, 0)
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if next != 3 {
		t.Fatalf("next = %d, want 3", next)
	}
	if resync {
		t.Fatal("resync = true, want false")
	}

	// The events come in the order TDLib sent them.
	for i, event := range events {
		added, ok := event.(MessageAdded)
		if !ok {
			t.Fatalf("event %d = %T, want MessageAdded", i, event)
		}
		if want := MessageID(100 + i + 1); added.Message.ID != want {
			t.Fatalf("event %d = message %d, want %d", i, added.Message.ID, want)
		}
	}
}

func TestMessageEventsSinceFromTheEndIsEmpty(t *testing.T) {
	state := NewLiveState()
	for i := 1; i <= 3; i++ {
		mustApplyNewMessage(t, state, 7, int64(100+i), "body")
	}

	events, next, resync := state.MessageEventsSince(7, 3)
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0", len(events))
	}
	if next != 3 {
		t.Fatalf("next = %d, want 3", next)
	}
	if resync {
		t.Fatal("a cursor that is up to date must not be told to resync")
	}
}

func TestMessageEventsSinceFromTheMiddleReturnsTheRest(t *testing.T) {
	state := NewLiveState()
	for i := 1; i <= 3; i++ {
		mustApplyNewMessage(t, state, 7, int64(100+i), "body")
	}

	events, next, resync := state.MessageEventsSince(7, 1)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if next != 3 || resync {
		t.Fatalf("next = %d, resync = %v, want 3 and false", next, resync)
	}
	if added := events[0].(MessageAdded); added.Message.ID != 102 {
		t.Fatalf("first event = %d, want 102", added.Message.ID)
	}
}

// A window that has moved past the cursor cannot answer for it, and the
// answer is a reload, never a partial list that looks complete.
func TestMessageEventsSinceOverflowAsksForResync(t *testing.T) {
	state := NewLiveState()

	for i := 1; i <= 300; i++ {
		mustApplyNewMessage(t, state, 7, int64(1000+i), "body")
	}

	events, next, resync := state.MessageEventsSince(7, 10)
	if !resync {
		t.Fatal("resync = false, want true for a cursor older than the window")
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0 next to a resync", len(events))
	}
	if next != 300 {
		t.Fatalf("next = %d, want the current sequence 300", next)
	}

	// A cursor the window can still answer for, one before its oldest
	// event: the window holds 45..300, so 44 has seen everything.
	events, next, resync = state.MessageEventsSince(7, 290)
	if resync {
		t.Fatal("resync = true, want false for a cursor inside the window")
	}
	if len(events) != 10 {
		t.Fatalf("events = %d, want 10", len(events))
	}
	if next != 300 {
		t.Fatalf("next = %d, want 300", next)
	}
	if added := events[0].(MessageAdded); added.Message.ID != 1291 {
		t.Fatalf("first event = %d, want 1291", added.Message.ID)
	}
	if added := events[9].(MessageAdded); added.Message.ID != 1300 {
		t.Fatalf("last event = %d, want 1300", added.Message.ID)
	}
}

// The boundary one before the oldest event is still answerable: the
// window holds everything after it, so nothing is missing.
func TestMessageEventsSinceAtTheOldestBoundaryIsServed(t *testing.T) {
	state := NewLiveState()

	for i := 1; i <= messageWindowSize+10; i++ {
		mustApplyNewMessage(t, state, 7, int64(1000+i), "body")
	}

	oldest := uint64(messageWindowSize + 10 - messageWindowSize + 1)
	events, _, resync := state.MessageEventsSince(7, oldest-1)
	if resync {
		t.Fatalf("resync = true, want false at the boundary %d", oldest-1)
	}
	if len(events) != messageWindowSize {
		t.Fatalf("events = %d, want the whole window %d", len(events), messageWindowSize)
	}
}

// A cursor ahead of the store is one telecli never issued. Answering it
// with a resync is the only safe reading: the store cannot know what the
// consumer missed.
func TestMessageEventsSinceAheadOfTheStoreAsksForResync(t *testing.T) {
	state := NewLiveState()
	for i := 1; i <= 3; i++ {
		mustApplyNewMessage(t, state, 7, int64(100+i), "body")
	}

	events, next, resync := state.MessageEventsSince(7, 99)
	if !resync {
		t.Fatal("resync = false, want true for a cursor from the future")
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0", len(events))
	}
	if next != 3 {
		t.Fatalf("next = %d, want 3", next)
	}
}

func TestMessageEventsSinceUnknownChatIsEmpty(t *testing.T) {
	state := NewLiveState()
	mustApplyNewMessage(t, state, 7, 501, "body")

	events, next, resync := state.MessageEventsSince(99, 0)
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0", len(events))
	}
	if next != 0 {
		t.Fatalf("next = %d, want 0 for a chat with no events", next)
	}
	if resync {
		t.Fatal("a chat that has produced nothing must not ask for a resync")
	}
}

func TestWindowsOfDifferentChatsAreIndependent(t *testing.T) {
	state := NewLiveState()

	for i := 1; i <= 2; i++ {
		mustApplyNewMessage(t, state, 7, int64(100+i), "body")
	}
	mustApplyNewMessage(t, state, 8, 201, "body")

	seven, nextSeven, _ := state.MessageEventsSince(7, 0)
	eight, nextEight, _ := state.MessageEventsSince(8, 0)

	if len(seven) != 2 || nextSeven != 2 {
		t.Fatalf("chat 7: %d events, next %d, want 2 and 2", len(seven), nextSeven)
	}
	if len(eight) != 1 || nextEight != 1 {
		t.Fatalf("chat 8: %d events, next %d, want 1 and 1", len(eight), nextEight)
	}

	// Overflowing one chat must not move the other one.
	for i := 1; i <= messageWindowSize+1; i++ {
		mustApplyNewMessage(t, state, 7, int64(2000+i), "body")
	}
	if _, _, resync := state.MessageEventsSince(7, 1); !resync {
		t.Fatal("chat 7 must resync after an overflow")
	}
	eight, nextEight, resync := state.MessageEventsSince(8, 0)
	if len(eight) != 1 || nextEight != 1 || resync {
		t.Fatalf(
			"chat 8 after the overflow: %d events, next %d, resync %v",
			len(eight), nextEight, resync,
		)
	}
}

// A chat that never receives a message must cost nothing: no window is
// created for it.
func TestNoWindowIsCreatedWithoutAnEvent(t *testing.T) {
	state := NewLiveState()
	mustApplyLiveChat(t, state, 7, "Alice", 100)

	if len(state.messages) != 0 {
		t.Fatalf(
			"windows = %d, want none for a chat that never sent a message",
			len(state.messages),
		)
	}

	mustApplyNewMessage(t, state, 7, 501, "body")
	if len(state.messages) != 1 {
		t.Fatalf("windows = %d, want 1", len(state.messages))
	}
}

func TestMessageWindowIsBounded(t *testing.T) {
	state := NewLiveState()

	for i := 1; i <= messageWindowSize*4; i++ {
		mustApplyNewMessage(t, state, 7, int64(1000+i), "body")
	}

	window := state.messages[7]
	if window == nil {
		t.Fatal("no window for a chat that produced events")
	}
	if len(window.events) != messageWindowSize {
		t.Fatalf("events kept = %d, want %d", len(window.events), messageWindowSize)
	}
	// The capacity is the size class append grew the slice to. It stops
	// there: the append path is closed once the window is full and the
	// slide reuses the same array, so a chat that talks for hours costs
	// the same memory as one that sent three messages.
	if cap(window.events) > 2*messageWindowSize {
		t.Fatalf(
			"capacity = %d, want a small multiple of %d",
			cap(window.events),
			messageWindowSize,
		)
	}
}

// ---- Copies and concurrency ----

func TestMessageEventsSinceReturnsCopies(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(RawMessage(
		`{"@type":"updateDeleteMessages","chat_id":7,"message_ids":[11,12],` +
			`"is_permanent":true,"from_cache":false}`,
	)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	events, _, _ := state.MessageEventsSince(7, 0)
	deleted := events[0].(MessagesDeleted)
	deleted.IDs[0] = 999

	again, _, _ := state.MessageEventsSince(7, 0)
	if got := again[0].(MessagesDeleted).IDs[0]; got != 11 {
		t.Fatalf("IDs[0] = %d, want 11: the store handed out its own slice", got)
	}
}

func TestConcurrentMessageReadersAndWriterAreRaceFree(t *testing.T) {
	state := NewLiveState()

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
				events, _, _ := state.MessageEventsSince(7, 0)
				for _, event := range events {
					switch typed := event.(type) {
					case MessageAdded:
						_ = typed.Message.Text
					case MessageReplaced:
						_ = typed.Message.ID
					case MessageFailed:
						_ = typed.Error.Reason()
					case MessagesDeleted:
						_ = typed.IDs
					}
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			raw := newMessageUpdate(7, int64(1000+i), false, "body")
			if _, err := state.apply(RawMessage(raw)); err != nil {
				return
			}
		}
		close(stop)
	}()

	wg.Wait()
}

// ---- Change signal ----

// A message event signals the same channel: the consumer decides what to
// re-read, and a chat-list consumer re-reads a list that did not move.
func TestMessageEventSignalsChanged(t *testing.T) {
	state := NewLiveState()
	drainChanged(state)

	if _, err := state.apply(RawMessage(
		newMessageUpdate(7, 501, false, "body"),
	)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	select {
	case <-state.Changed():
	case <-time.After(time.Second):
		t.Fatal("no change signal after a message event")
	}
}

func TestTenMessageEventsCoalesceIntoOneSignal(t *testing.T) {
	state := NewLiveState()
	drainChanged(state)

	for i := 0; i < 10; i++ {
		if _, err := state.apply(RawMessage(
			newMessageUpdate(7, int64(1000+i), false, "body"),
		)); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}

	select {
	case <-state.Changed():
	case <-time.After(time.Second):
		t.Fatal("no signal after ten message events")
	}

	select {
	case <-state.Changed():
		t.Fatal("the channel must coalesce ten events into one signal")
	case <-time.After(100 * time.Millisecond):
	}
}

// ---- Not held during authorization ----

// The TUI starts from the first page of history, so a message update
// during the login wait is a gap the first page already closes. Holding
// them would make the held slice grow with the traffic of a busy account
// while the user is typing a code.
func TestMessageUpdatesAreNotHeldDuringAuth(t *testing.T) {
	for _, raw := range []string{
		newMessageUpdate(7, 501, false, "body"),
		`{"@type":"updateMessageSendSucceeded","old_message_id":99,"message":` +
			messageJSON(7, 1234, true, "body") + `}`,
		`{"@type":"updateMessageSendFailed","old_message_id":99,"message":` +
			messageJSON(7, 99, true, "body") +
			`,"error":{"@type":"error","code":420,"message":"nope"}}`,
		`{"@type":"updateDeleteMessages","chat_id":7,"message_ids":[11],` +
			`"is_permanent":true,"from_cache":false}`,
	} {
		if isLiveStateUpdate(RawMessage(raw)) {
			t.Fatalf("%s must not be held during authorization", raw)
		}
	}

	// They are still applied once the session is running.
	state := NewLiveState()
	if _, err := state.apply(RawMessage(
		newMessageUpdate(7, 501, false, "body"),
	)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if events, _, _ := state.MessageEventsSince(7, 0); len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestNilLiveStateMessageEventsAreSafe(t *testing.T) {
	var state *LiveState

	events, next, resync := state.MessageEventsSince(7, 0)
	if events != nil || next != 0 || resync {
		t.Fatalf(
			"events = %v, next = %d, resync = %v, want nil, 0 and false",
			events, next, resync,
		)
	}
}

// ---- The chat list is untouched ----

// A message event must not move the chat list, and a chat update must not
// spend a sequence number: the two projections are independent.
func TestMessageEventsDoNotDisturbTheChatList(t *testing.T) {
	state := seededLiveState(t)

	if _, err := state.apply(RawMessage(
		newMessageUpdate(7, 501, false, "body"),
	)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	chats := state.ChatList()
	if len(chats) != 1 {
		t.Fatalf("ChatList() = %d chats, want 1", len(chats))
	}
	if chats[0].LastMessage == nil || chats[0].LastMessage.ID != 1000 {
		t.Fatalf(
			"LastMessage = %+v, want the seeded message untouched",
			chats[0].LastMessage,
		)
	}

	if _, err := state.apply(RawMessage(
		`{"@type":"updateChatTitle","chat_id":7,"title":"Renamed"}`,
	)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, next, _ := state.MessageEventsSince(7, 1); next != 1 {
		t.Fatalf("next = %d, want 1: a chat update spent no sequence", next)
	}
}

// The preview of a message must be built by the same code whichever path
// saw it, so a message cannot look different in the list and in the
// window.
func TestEventPreviewMatchesTheChatListPreview(t *testing.T) {
	state := NewLiveState()
	mustApplyLiveChat(t, state, 7, "Alice", 100)

	// A photo, so the placeholder path is exercised rather than a plain
	// text body.
	_, err := state.apply(RawMessage(
		`{"@type":"updateNewMessage","message":{"@type":"message","id":501,` +
			`"chat_id":7,"is_outgoing":false,"date":1700000000,` +
			`"content":{"@type":"messagePhoto","photo":{"@type":"photo",` +
			`"sizes":[]}}}}`,
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	events, _, _ := state.MessageEventsSince(7, 0)
	added := events[0].(MessageAdded)
	if added.Message.Text != "[photo]" {
		t.Fatalf("Text = %q, want the placeholder [photo]", added.Message.Text)
	}

	_, listPreview, _ := parseLastMessage(json.RawMessage(
		`{"@type":"message","id":501,"chat_id":7,"date":1700000000,` +
			`"content":{"@type":"messagePhoto","photo":{"@type":"photo","sizes":[]}}}`,
	))
	if added.Message.Text != listPreview {
		t.Fatalf(
			"event preview %q differs from the list preview %q",
			added.Message.Text,
			listPreview,
		)
	}
}

// ---- Pump wiring ----

// The pump is the only writer, so a message update has to reach the store
// through it: an event that only the tests can produce would never exist
// in a running client.
func TestPumpRecordsMessageEventToLiveState(t *testing.T) {
	session, _, _, client := newSessionWithFakes(t)
	state := session.LiveState()

	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(newMessageUpdate(7, 501, false, "body")),
	}

	event := waitForMessageEvent(t, state, 7)
	added, ok := event.(MessageAdded)
	if !ok {
		t.Fatalf("event = %T, want MessageAdded", event)
	}
	if added.Message.ID != 501 {
		t.Fatalf("Message.ID = %d, want 501", added.Message.ID)
	}
}

// A malformed message update is reported on the session error stream, as
// a malformed chat update is, and the window it would have gone into is
// left alone.
func TestPumpRecordsMalformedMessageError(t *testing.T) {
	session, _, _, client := newSessionWithFakes(t)
	state := session.LiveState()

	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"updateNewMessage"}`),
	}

	select {
	case err := <-session.Errors():
		if err == nil {
			t.Fatal("a malformed message update must be reported")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the malformed update was not reported on session.Errors()")
	}

	if _, next, _ := state.MessageEventsSince(7, 0); next != 0 {
		t.Fatalf("next = %d, want 0 (state must be unchanged)", next)
	}
}

// ---- Fixtures ----

// mustApplyNewMessage files one incoming message in a chat.
func mustApplyNewMessage(
	t *testing.T,
	state *LiveState,
	chatID ChatID,
	messageID int64,
	body string,
) {
	t.Helper()

	_, err := state.apply(RawMessage(newMessageUpdate(
		chatID, messageID, false, body,
	)))
	if err != nil {
		t.Fatalf("apply new message: %v", err)
	}
}

// waitForMessageEvent waits until the chat's window holds an event.
func waitForMessageEvent(
	t *testing.T,
	state *LiveState,
	chatID ChatID,
) MessageEvent {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, _, _ := state.MessageEventsSince(chatID, 0)
		if len(events) > 0 {
			return events[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no message event for chat %d", chatID)

	return nil
}

// newMessageUpdate builds an updateNewMessage in the pinned shape.
func newMessageUpdate(
	chatID ChatID,
	messageID int64,
	outgoing bool,
	body string,
) string {
	return `{"@type":"updateNewMessage","message":` +
		messageJSON(chatID, messageID, outgoing, body) + `}`
}

// messageJSON builds a message in the pinned shape. The content is a
// messageText, which is the only type whose preview is the body itself.
func messageJSON(
	chatID ChatID,
	messageID int64,
	outgoing bool,
	body string,
) string {
	return `{"@type":"message","id":` + strconv.FormatInt(messageID, 10) +
		`,"chat_id":` + strconv.FormatInt(int64(chatID), 10) +
		`,"is_outgoing":` + strconv.FormatBool(outgoing) +
		`,"date":1700000000,"content":{"@type":"messageText",` +
		`"text":{"@type":"formattedText","text":` + strconv.Quote(body) + `}}}`
}
