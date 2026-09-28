package telegram

import (
	"encoding/json"
	"testing"
	"time"
)

// The send results TDLib delivers, in the shape it delivers them.
//
// The three fixtures below are the recorded payloads of one real send: the
// answer to sendMessage, and the two updates that can follow it. They are
// what the durable queue has to make sense of, and they are pinned here so
// that what the live store decodes them into is a fact rather than a
// reading of the schema — the queue's reconciler is proved against these
// decoded values in internal/application, and this file is where the
// decoding is proved against the bytes.

const (
	// sendTemporaryMessageID is what sendMessage answers with. TDLib
	// allocates temporary identifiers far above anything a history page
	// contains, and replaces them with the final one.
	sendTemporaryMessageID = 1_000_000_042

	// sendFinalMessageID is the identifier Telegram assigned.
	sendFinalMessageID = 501
)

// recordedSendMessageResponse is the sendMessage answer for the message
// every fixture below is about.
const recordedSendMessageResponse = `{
  "@type": "message",
  "id": 1000000042,
  "sender_user_id": 7,
  "chat_id": 7,
  "is_outgoing": true,
  "date": 1759100000,
  "sending_state": {
    "@type": "messageSendingStatePending"
  },
  "content": {
    "@type": "messageText",
    "text": {
      "@type": "formattedText",
      "text": "проверяю сборку",
      "entities": []
    }
  }
}`

// recordedSendSucceeded is the update Telegram sends when the message is
// on its way. old_message_id is the temporary identifier and message.id is
// the final one.
const recordedSendSucceeded = `{
  "@type": "updateMessageSendSucceeded",
  "message": {
    "@type": "message",
    "id": 501,
    "sender_user_id": 7,
    "chat_id": 7,
    "is_outgoing": true,
    "date": 1759100000,
    "content": {
      "@type": "messageText",
      "text": {
        "@type": "formattedText",
        "text": "проверяю сборку",
        "entities": []
      }
    }
  },
  "old_message_id": 1000000042
}`

// recordedSendFailed is the update Telegram sends when it refuses the
// message. The temporary identifier comes back the same way, and the error
// carries TDLib's own code.
const recordedSendFailed = `{
  "@type": "updateMessageSendFailed",
  "message": {
    "@type": "message",
    "id": 1000000042,
    "sender_user_id": 7,
    "chat_id": 7,
    "is_outgoing": true,
    "date": 1759100000,
    "content": {
      "@type": "messageText",
      "text": {
        "@type": "formattedText",
        "text": "проверяю сборку",
        "entities": []
      }
    }
  },
  "old_message_id": 1000000042,
  "error": {
    "@type": "error",
    "code": 400,
    "message": "Can't send message"
  }
}`

// The sendMessage answer decodes to a message whose identifier is the
// temporary one, and whose sending state is pending. That is the whole
// reason the queue cannot treat the answer as a delivery receipt: the
// message is not on its way until a later update says so.
//
// The decode is the one SendTextMessage performs on the answer, so the
// assertion is about the recorded bytes and not about a reading of them.
func TestRecordedSendMessageResponseCarriesATemporaryIdentifier(t *testing.T) {
	t.Parallel()

	var response sendMessageResponse
	if err := json.Unmarshal(
		[]byte(recordedSendMessageResponse), &response,
	); err != nil {
		t.Fatalf("decode sendMessage response: %v", err)
	}
	if response.Type != "message" {
		t.Fatalf("@type = %q, want message", response.Type)
	}
	if int64(response.ID) != sendTemporaryMessageID {
		t.Fatalf("message id = %d, want the temporary one", int64(response.ID))
	}
	if int64(response.ChatID) != 7 || !response.IsOutgoing {
		t.Fatalf(
			"chat = %d, outgoing = %v; want an outgoing message of chat 7",
			int64(response.ChatID), response.IsOutgoing,
		)
	}

	// The state TDLib sends with the answer is the reason the answer is
	// not a receipt: it is still pending.
	var pending struct {
		SendingState struct {
			Type string `json:"@type"`
		} `json:"sending_state"`
	}
	if err := json.Unmarshal(
		[]byte(recordedSendMessageResponse), &pending,
	); err != nil {
		t.Fatalf("decode sending state: %v", err)
	}
	if pending.SendingState.Type != "messageSendingStatePending" {
		t.Fatalf(
			"sending_state = %q, want messageSendingStatePending: the "+
				"answer is not a delivery receipt",
			pending.SendingState.Type,
		)
	}
}

// The update that confirms a send carries the temporary identifier as
// old_message_id and the final one as message.id. The queue correlates the
// two on exactly that field and on nothing else.
func TestRecordedSendSucceededCarriesBothIdentifiers(t *testing.T) {
	t.Parallel()

	state := NewLiveState()
	changed, err := state.apply(RawMessage(recordedSendSucceeded))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("the confirmation must report a change")
	}

	events, _, resync := state.MessageEventsSince(7, 0)
	if resync {
		t.Fatal("a window read from 0 must not resync")
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	replaced, ok := events[0].(MessageReplaced)
	if !ok {
		t.Fatalf("event = %#v, want a MessageReplaced", events[0])
	}
	if replaced.OldID != sendTemporaryMessageID {
		t.Fatalf(
			"old message id = %d, want the temporary one the sendMessage "+
				"answer carried", replaced.OldID,
		)
	}
	if replaced.Message.ID != sendFinalMessageID {
		t.Fatalf(
			"message id = %d, want the final one", replaced.Message.ID,
		)
	}
	if replaced.Message.ChatID != 7 {
		t.Fatalf("chat id = %d, want 7", replaced.Message.ChatID)
	}
}

// The update that refuses a send carries the same temporary identifier and
// an error with TDLib's own code. The queue ends the entry as a permanent
// failure with that code, and never writes TDLib's text anywhere: the
// error description stays out of the record and out of the log.
func TestRecordedSendFailedCarriesTheTemporaryIdentifierAndACode(t *testing.T) {
	t.Parallel()

	state := NewLiveState()
	if _, err := state.apply(RawMessage(recordedSendFailed)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	events, _, resync := state.MessageEventsSince(7, 0)
	if resync {
		t.Fatal("a window read from 0 must not resync")
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	failed, ok := events[0].(MessageFailed)
	if !ok {
		t.Fatalf("event = %#v, want a MessageFailed", events[0])
	}
	if failed.OldID != sendTemporaryMessageID {
		t.Fatalf("old message id = %d, want the temporary one", failed.OldID)
	}
	if failed.Error.Code != 400 {
		t.Fatalf("error code = %d, want 400", failed.Error.Code)
	}
	if failed.Error.Reason() == failed.Error.Description {
		t.Fatal(
			"the safe reason must not be TDLib's own text: a string that " +
				"reaches a log line or a status block is a way out for " +
				"whatever the transport wrote",
		)
	}
	if reason := failed.Error.Reason(); reason != "telegram send error code=400" {
		t.Fatalf("reason = %q, want the code and nothing else", reason)
	}
}

// A confirmation that arrives before the queue has finished recording the
// acceptance it belongs to is the ordinary order of things: the update
// follows TDLib's own answer, and the queue writes the answer afterwards.
// The window therefore holds the confirmation from the first read, which
// is what lets a consumer that starts looking later still find it.
func TestAConfirmationHeldBeforeAnyoneLooksIsStillThere(t *testing.T) {
	t.Parallel()

	state := NewLiveState()
	if _, err := state.apply(RawMessage(recordedSendSucceeded)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// A consumer that only starts looking now still reads it.
	events, next, resync := state.MessageEventsSince(7, 0)
	if resync || next != 1 || len(events) != 1 {
		t.Fatalf(
			"events = %d, next = %d, resync = %v; a window read from 0 "+
				"must answer with everything it holds",
			len(events), next, resync,
		)
	}
}

// The message a confirmation carries is a normal outgoing message of its
// chat, dated by Telegram rather than by this program.
func TestConfirmedMessageCarriesItsOwnDate(t *testing.T) {
	t.Parallel()

	state := NewLiveState()
	if _, err := state.apply(RawMessage(recordedSendSucceeded)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	events, _, _ := state.MessageEventsSince(7, 0)

	replaced := events[0].(MessageReplaced)
	want := time.Unix(1759100000, 0).UTC()
	if !replaced.Message.Timestamp.Equal(want) {
		t.Fatalf(
			"timestamp = %s, want %s", replaced.Message.Timestamp, want,
		)
	}
}
