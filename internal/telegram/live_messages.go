package telegram

import (
	"encoding/json"
	"fmt"
)

// The message event window is ADR-0003 §4: the store keeps events per
// chat, not a copy of the history, and overflow degrades into a reload of
// the first history page instead of silent loss.
//
// The field and type names below follow the pinned TDLib schema,
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
// Two shapes are easy to get wrong here:
//
//   - updateNewMessage carries no chat_id. The chat comes from the
//     message, and message.chat_id is an int53, which TDLib carries as
//     a JSON number. Only updateDeleteMessages has a top-level chat_id.
//   - updateDeleteMessages is the only one of the four that is not about
//     a message object: it carries a vector of int53 as a plain JSON
//     array, and it says whether the deletion is permanent. A
//     non-permanent deletion only drops TDLib's own cache, so the user
//     still sees the messages and the store must say nothing.

// MessageEvent is one change to a chat's messages.
//
// The set is closed. The unexported method keeps a consumer from
// inventing an event the store never produces, which makes a type switch
// on the four types below exhaustive by construction.
type MessageEvent interface {
	// isMessageEvent is unexported: only this package implements
	// MessageEvent.
	isMessageEvent()
}

// MessageAdded is a message that appeared in a chat.
//
// It carries incoming and outgoing messages alike. An outgoing message
// arrives here first with the temporary ID that sendMessage returned,
// and is replaced by MessageReplaced once Telegram assigns the real one.
type MessageAdded struct {
	Message Message
}

// MessageReplaced is an outgoing message whose temporary ID became its
// final one.
//
// OldID is the temporary ID the consumer already holds, so it can
// replace its row in place instead of appending a second copy.
type MessageReplaced struct {
	OldID   MessageID
	Message Message
}

// MessageFailed is an outgoing message TDLib refused to send.
//
// OldID is the temporary ID the consumer already holds. The Message is
// the final form of the failed send, used to update the row the consumer
// already shows; Error says why.
type MessageFailed struct {
	OldID   MessageID
	Message Message
	Error   MessageError
}

// MessagesDeleted removes messages from a chat.
//
// The IDs are in the order TDLib sent them and are never empty: an
// update with no IDs changes nothing and is not recorded.
type MessagesDeleted struct {
	IDs []MessageID
}

func (MessageAdded) isMessageEvent()    {}
func (MessageReplaced) isMessageEvent() {}
func (MessageFailed) isMessageEvent()   {}
func (MessagesDeleted) isMessageEvent() {}

// MessageError is why TDLib refused a send.
//
// Description is TDLib's own error text. It is kept for the interface
// and is deliberately left out of Error(): an error string is what ends
// up in a log line or a status block, and a transport-provided string
// must never be the reason user content reaches one. Reason is the form
// that is safe to log, and it mirrors outbox.SafeReason.
type MessageError struct {
	// Code is TDLib's error code, or 0 when the update carried none.
	Code int

	// Description is the transport's own text for the failure. It never
	// contains the message being sent: that is TDLib's contract, and the
	// field exists so a consumer can show more than a number if it ever
	// needs to.
	Description string
}

// Reason returns a short description that is safe to log and to show.
func (e MessageError) Reason() string {
	if e.Code != 0 {
		return fmt.Sprintf("telegram send error code=%d", e.Code)
	}
	return "telegram send error"
}

// Error implements error with the same safe text as Reason, so a
// description cannot leak through a stray err.Error().
func (e MessageError) Error() string { return e.Reason() }

// messageWindowSize is how many events one chat keeps.
//
// ADR-0003 §4 fixes it at 256. A window is created by the first event of
// a chat, so a chat that never receives a message costs nothing.
const messageWindowSize = 256

// messageEventRecord is one stored event with the sequence number it was
// given.
//
// The number is not part of the event a consumer sees: a consumer keeps
// one cursor per chat, and MessageEventsSince hands it the cursor back.
type messageEventRecord struct {
	seq   uint64
	event MessageEvent
}

// chatMessages is the window of one chat: the most recent events and the
// sequence the next event gets.
//
// next is the sequence of the next event, which is also the count of
// events the chat ever produced. It is 0 while the chat has no window, so
// a chat without events reports next == 0.
//
// The slice is grown by append until it holds messageWindowSize records
// and is then reused in place, so the allocation a chat costs stops
// growing with its traffic.
type chatMessages struct {
	events []messageEventRecord
	next   uint64
}

// record files one event under the next sequence number.
func (w *chatMessages) record(event MessageEvent) {
	w.next++

	record := messageEventRecord{seq: w.next, event: event}

	if len(w.events) < messageWindowSize {
		w.events = append(w.events, record)
		return
	}

	// The window is full, so the oldest event falls out. Sliding the
	// slice costs one 256-entry move, which is cheaper than the two-slice
	// ring a wrap-around index would need, and what matters more is that
	// the allocation stops growing here: a chat that talks for hours
	// costs the same memory as one that sent three messages.
	copy(w.events, w.events[1:])
	w.events[len(w.events)-1] = record
}

// since returns the records that follow seq, the current sequence, and
// whether the window can answer at all.
//
// resync is reported when the events after seq are no longer all here,
// which happens when the window has moved past seq, and when seq is
// ahead of the store, which no cursor telecli issued should ever be.
func (w *chatMessages) since(
	seq uint64,
) (records []messageEventRecord, next uint64, resync bool) {
	if w == nil {
		return nil, 0, false
	}

	next = w.next

	if seq > next {
		// A cursor from the future: the store cannot know what the
		// consumer missed.
		return nil, next, true
	}

	if len(w.events) == 0 {
		// Only seq == 0, and next == 0, reaches here without a
		// resync: there is nothing this chat has produced yet.
		return nil, next, false
	}

	// The window holds oldest..next, so a cursor of oldest-1 still sees
	// every event after it. Anything lower missed events that have since
	// fallen out.
	if seq+1 < w.events[0].seq {
		return nil, next, true
	}

	for _, record := range w.events {
		if record.seq > seq {
			records = append(records, record)
		}
	}

	return records, next, false
}

// MessageEventsSince returns the message events of a chat that follow
// seq, together with the cursor to continue from.
//
// The three results say what a consumer has to do:
//
//   - resync false: events is every event after seq, and next is the
//     cursor to ask with next time;
//   - resync true: the window can no longer answer for seq, either
//     because events were pushed out of it or because seq is ahead of
//     the store. The consumer reloads the first history page, exactly
//     like opening the chat, and continues from next.
//
// A chat with no events reports next == 0 and no resync, so a consumer
// that just loaded a history page is not told to reload it.
//
// The returned events are copies: later updates do not change them, and
// two readers cannot see each other's changes.
func (l *LiveState) MessageEventsSince(
	chatID ChatID,
	seq uint64,
) (events []MessageEvent, next uint64, resync bool) {
	if l == nil {
		return nil, 0, false
	}

	l.mu.RLock()
	records, next, resync := l.messages[chatID].since(seq)
	l.mu.RUnlock()

	events = make([]MessageEvent, 0, len(records))
	for _, record := range records {
		events = append(events, copyMessageEvent(record.event))
	}

	return events, next, resync
}

// copyMessageEvent returns an event the caller owns.
//
// Only MessagesDeleted holds a reference, and the store owns the one it
// decoded; handing it out would let one reader's sort reorder another
// reader's events.
func copyMessageEvent(event MessageEvent) MessageEvent {
	if deleted, ok := event.(MessagesDeleted); ok {
		deleted.IDs = append([]MessageID(nil), deleted.IDs...)
		return deleted
	}
	return event
}

// recordMessageUpdate files one decoded event under its chat. The caller
// must hold the write lock.
//
// Every recorded event is a change, so the store signals after each one.
// A consumer of the chat list is told about a change it does not care
// about, and simply re-reads a snapshot that did not move: that is the
// price of one signal channel, and it is cheaper than a second one to
// keep in step.
func (l *LiveState) recordMessageUpdate(update messageUpdate) bool {
	window, exists := l.messages[update.chatID]
	if !exists {
		// The window is created by the first event, so a chat that never
		// receives a message costs nothing.
		window = &chatMessages{}
		l.messages[update.chatID] = window
	}

	window.record(update.event)
	l.signalChanged()

	return true
}

// messageUpdateTypes are the update @types that become message events.
//
// They are deliberately not part of liveStateUpdateTypes: message
// updates are not held during authorization, because the TUI starts from
// the first page of history and that page already closes the gap. Holding
// them would make the held slice grow with the message traffic of a busy
// account while the user is typing a login code.
var messageUpdateTypes = map[string]struct{}{
	"updateNewMessage":           {},
	"updateMessageSendSucceeded": {},
	"updateMessageSendFailed":    {},
	"updateDeleteMessages":       {},
}

// messageUpdate is a decoded event that has not been recorded yet.
//
// The chat is carried apart from the event because two of the four update
// types have no chat_id of their own.
type messageUpdate struct {
	chatID ChatID
	event  MessageEvent
}

// updateNewMessageJSON mirrors updateNewMessage (td_api.tl:10401).
type updateNewMessageJSON struct {
	Message json.RawMessage `json:"message"`
}

// updateSendResultJSON mirrors updateMessageSendSucceeded and
// updateMessageSendFailed (td_api.tl:10413, :10419).
type updateSendResultJSON struct {
	Message      json.RawMessage `json:"message"`
	OldMessageID int64           `json:"old_message_id"`
	Error        json.RawMessage `json:"error"`
}

// updateDeleteMessagesJSON mirrors updateDeleteMessages
// (td_api.tl:10701).
type updateDeleteMessagesJSON struct {
	ChatID      int64   `json:"chat_id"`
	MessageIDs  []int64 `json:"message_ids"`
	IsPermanent bool    `json:"is_permanent"`
}

// tdlibErrorJSON mirrors the fields of a TDLib error object.
type tdlibErrorJSON struct {
	Type    string `json:"@type"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// decodeMessageUpdate decodes one message update into the event it
// expresses.
//
// The second result is false for an update the store does not consume as
// a message event, including a non-permanent deletion, which only drops
// TDLib's cache and leaves nothing for the user to see change.
//
// Decoding completes before the store is involved, so a malformed
// payload is reported and the window is left exactly as it was. That is
// the same rule the chat-list path follows, and it is why a malformed
// update is a reported error rather than a silently skipped event.
func decodeMessageUpdate(raw RawMessage) (messageUpdate, bool, error) {
	var envelope updateEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return messageUpdate{}, false, fmt.Errorf("decode live update: %w", err)
	}

	if _, ok := messageUpdateTypes[envelope.Type]; !ok {
		return messageUpdate{}, false, nil
	}

	switch envelope.Type {
	case "updateNewMessage":
		return decodeNewMessageUpdate(raw)
	case "updateMessageSendSucceeded":
		return decodeSendSucceededUpdate(raw)
	case "updateMessageSendFailed":
		return decodeSendFailedUpdate(raw)
	case "updateDeleteMessages":
		return decodeDeleteMessagesUpdate(raw)
	default:
		return messageUpdate{}, false, nil
	}
}

func decodeNewMessageUpdate(
	raw RawMessage,
) (messageUpdate, bool, error) {
	var update updateNewMessageJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return messageUpdate{}, false, fmt.Errorf(
			"decode updateNewMessage: %w",
			err,
		)
	}

	message, err := parseLiveMessage(update.Message)
	if err != nil {
		return messageUpdate{}, false, fmt.Errorf(
			"decode updateNewMessage: %w",
			err,
		)
	}

	return messageUpdate{
		chatID: message.ChatID,
		event:  MessageAdded{Message: message},
	}, true, nil
}

func decodeSendSucceededUpdate(
	raw RawMessage,
) (messageUpdate, bool, error) {
	var update updateSendResultJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return messageUpdate{}, false, fmt.Errorf(
			"decode updateMessageSendSucceeded: %w",
			err,
		)
	}

	message, err := parseLiveMessage(update.Message)
	if err != nil {
		return messageUpdate{}, false, fmt.Errorf(
			"decode updateMessageSendSucceeded: %w",
			err,
		)
	}

	return messageUpdate{
		chatID: message.ChatID,
		event: MessageReplaced{
			OldID:   MessageID(update.OldMessageID),
			Message: message,
		},
	}, true, nil
}

func decodeSendFailedUpdate(
	raw RawMessage,
) (messageUpdate, bool, error) {
	var update updateSendResultJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return messageUpdate{}, false, fmt.Errorf(
			"decode updateMessageSendFailed: %w",
			err,
		)
	}

	message, err := parseLiveMessage(update.Message)
	if err != nil {
		return messageUpdate{}, false, fmt.Errorf(
			"decode updateMessageSendFailed: %w",
			err,
		)
	}

	messageErr, err := parseMessageError(update.Error)
	if err != nil {
		return messageUpdate{}, false, fmt.Errorf(
			"decode updateMessageSendFailed: %w",
			err,
		)
	}

	return messageUpdate{
		chatID: message.ChatID,
		event: MessageFailed{
			OldID:   MessageID(update.OldMessageID),
			Message: message,
			Error:   messageErr,
		},
	}, true, nil
}

// parseMessageError reads the error object of a failed send.
//
// An absent error is not a protocol surprise: the event still says which
// message failed, and MessageError is then only the zero value. An error
// that is present but is not a TDLib error object is.
func parseMessageError(raw json.RawMessage) (MessageError, error) {
	if isAbsentJSON(raw) {
		return MessageError{}, nil
	}

	var failure tdlibErrorJSON
	if err := json.Unmarshal(raw, &failure); err != nil {
		return MessageError{}, fmt.Errorf("decode error: %w", err)
	}
	if failure.Type != "error" {
		return MessageError{}, fmt.Errorf(
			"decode error: @type = %q, want error",
			failure.Type,
		)
	}

	return MessageError{
		Code:        failure.Code,
		Description: failure.Message,
	}, nil
}

func decodeDeleteMessagesUpdate(
	raw RawMessage,
) (messageUpdate, bool, error) {
	var update updateDeleteMessagesJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return messageUpdate{}, false, fmt.Errorf(
			"decode updateDeleteMessages: %w",
			err,
		)
	}

	if update.ChatID == 0 {
		// TDLib does not use chat id 0, and a deletion without a chat
		// could not be filed under one.
		return messageUpdate{}, false, fmt.Errorf(
			"decode updateDeleteMessages: no chat id",
		)
	}

	// TDLib: "is_permanent = false" means the messages are only removed
	// from its cache. They are still there for the user, so recording
	// them as deleted would make a message vanish from the interface and
	// come back on the next history load.
	if !update.IsPermanent {
		return messageUpdate{}, false, nil
	}

	ids := make([]MessageID, 0, len(update.MessageIDs))
	for _, id := range update.MessageIDs {
		ids = append(ids, MessageID(id))
	}

	// An update that removes nothing would be an event that says
	// nothing, and it would still spend a sequence number.
	if len(ids) == 0 {
		return messageUpdate{}, false, nil
	}

	return messageUpdate{
		chatID: ChatID(update.ChatID),
		event:  MessagesDeleted{IDs: ids},
	}, true, nil
}
