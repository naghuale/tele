package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// The recorded answers of this file are what a real account answered with,
// in the shapes the pinned schema gives them: a `chat` with a `type` and a
// `permissions` (td_api.tl:3628), a `user` with a `have_access`
// (td_api.tl:2403) and a `supergroup` with a `status` (td_api.tl:2747).
//
// They are written out rather than built from the shapes of the decoders on
// purpose: a fixture assembled out of the same struct tags as the code under
// it agrees with that code whatever TDLib does. Every one of them is named
// with `Rights` because the package already holds a recorded channel and a
// recorded basic group for the view, and a second fixture under the same
// name would be one answer for two claims.

const (
	recordedRightsChannelID     = ChatID(-1001234567890)
	recordedRightsChannelSuper  = int64(1234567890)
	recordedRightsGroupID       = ChatID(-424242)
	recordedRightsPrivateChatID = ChatID(777)
	recordedRightsPrivatePeerID = int64(888)
)

// recordedRightsChannelChat is the answer of getChat for a channel this
// account is only subscribed to.
//
// The permissions are written as TDLib writes them for a member of a
// channel — everything off — and the reading does not depend on that: the
// member status decides, and the test says so.
const recordedRightsChannelChat = `{"@type":"chat","id":-1001234567890,` +
	`"title":"Release Notes","unread_count":3,` +
	`"permissions":{"can_send_basic_messages":false,"can_change_info":false,` +
	`"can_invite_users":false,"can_pin_messages":false},` +
	`"type":{"@type":"chatTypeSupergroup","supergroup_id":1234567890,` +
	`"is_channel":true}}`

// recordedRightsSubscribed is the same channel read as a supergroup: the
// status of the account in it is a plain membership.
const recordedRightsSubscribed = `{"id":1234567890,"member_count":4242,` +
	`"status":{"@type":"chatMemberStatusMember","member_until_date":0},` +
	`"is_channel":true}`

// recordedRightsAdminPosts is a channel the account administers with the
// right to post (td_api.tl:2500).
const recordedRightsAdminPosts = `{"id":1234567890,"member_count":4242,` +
	`"status":{"@type":"chatMemberStatusAdministrator","can_be_edited":true,` +
	`"rights":{"can_manage_chat":true,"can_post_messages":true,` +
	`"can_delete_messages":true,"can_edit_messages":true}},` +
	`"is_channel":true}`

// recordedRightsAdminCannotPost is a channel the account administers without
// the right to post: an administrator of a channel is not a poster of it
// unless the right is in the rights.
const recordedRightsAdminCannotPost = `{"id":1234567890,"member_count":4242,` +
	`"status":{"@type":"chatMemberStatusAdministrator","can_be_edited":true,` +
	`"rights":{"can_manage_chat":true,"can_post_messages":false,` +
	`"can_delete_messages":true}},` +
	`"is_channel":true}`

// recordedRightsCreator is a channel the account owns, which the schema gives
// every administrator privilege.
const recordedRightsCreator = `{"id":1234567890,"member_count":4242,` +
	`"status":{"@type":"chatMemberStatusCreator","is_anonymous":false,` +
	`"is_member":true},"is_channel":true}`

// recordedRightsLeft is a channel the account is not in any more.
const recordedRightsLeft = `{"id":1234567890,` +
	`"status":{"@type":"chatMemberStatusLeft"},"is_channel":true}`

// recordedRightsGroupWritable and recordedRightsGroupMuted are two basic
// groups that differ in one permission.
const recordedRightsGroupWritable = `{"@type":"chat","id":-424242,` +
	`"title":"Team","unread_count":0,` +
	`"permissions":{"can_send_basic_messages":true,"can_change_info":false,` +
	`"can_invite_users":true,"can_pin_messages":false},` +
	`"type":{"@type":"chatTypeBasicGroup","basic_group_id":424242}}`

const recordedRightsGroupMuted = `{"@type":"chat","id":-424242,` +
	`"title":"Team","unread_count":0,` +
	`"permissions":{"can_send_basic_messages":false,"can_change_info":false,` +
	`"can_invite_users":false,"can_pin_messages":false},` +
	`"type":{"@type":"chatTypeBasicGroup","basic_group_id":424242}}`

// recordedRightsPrivateChat is a chat with one other person.
const recordedRightsPrivateChat = `{"@type":"chat","id":777,` +
	`"title":"Anna Example","unread_count":1,` +
	`"permissions":{"can_send_basic_messages":true,"can_change_info":true,` +
	`"can_invite_users":true,"can_pin_messages":true},` +
	`"type":{"@type":"chatTypePrivate","user_id":888}}`

const recordedRightsUserReachable = `{"@type":"user","id":888,` +
	`"first_name":"Anna","last_name":"Example","have_access":true,` +
	`"is_contact":true,"type":{"@type":"userTypeRegular"}}`

// recordedRightsUserGone is the same person after they deleted their account,
// or after this account blocked them: the schema says an inaccessible
// account is one whose identifier "can't be passed to any method".
const recordedRightsUserGone = `{"@type":"user","id":888,"first_name":"Anna",` +
	`"last_name":"Example","have_access":false,` +
	`"type":{"@type":"userTypeRegular"}}`

// recordedRightsUserSilent is a user answer with no `have_access` at all. The
// field is in the schema of this build, so an answer without it is not an
// answer about the account, and reading it as an inaccessible one would put
// a read-only line over every personal chat of a TDLib that had stopped
// writing the field.
const recordedRightsUserSilent = `{"@type":"user","id":888,` +
	`"first_name":"Anna","last_name":"Example",` +
	`"type":{"@type":"userTypeRegular"}}`

// recordedRightsChatWithoutPermissions is a chat whose answer carried no
// permissions at all, which is a chat nobody was told anything about rather
// than a chat with nothing permitted.
const recordedRightsChatWithoutPermissions = `{"@type":"chat","id":-424242,` +
	`"title":"Team","unread_count":0,` +
	`"type":{"@type":"chatTypeBasicGroup","basic_group_id":424242}}`

// recordedRightsChatOfALostType is a chat whose `type` is a name this
// build's schema does not have. It is what a basic group used to be called,
// and a decoder that answers an unknown type with a personal one is how a
// group of another version of TDLib became a chat with a single person.
const recordedRightsChatOfALostType = `{"@type":"chat","id":-424242,` +
	`"title":"Team","unread_count":0,` +
	`"permissions":{"can_send_basic_messages":false},` +
	`"type":{"@type":"chatTypeGroup","basic_group_id":1}}`

// recordedPayload turns a recorded answer into a payload the fake transport
// can send back.
//
// The decoder is told to keep the numbers as they were written: a payload
// that went through float64 writes a channel identifier as
// -1.00123456789e+12, which is a shape TDLib does not send and one this
// package cannot read.
func recordedPayload(t *testing.T, raw string) map[string]any {
	t.Helper()

	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()

	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("decode recorded answer: %v", err)
	}

	return payload
}

// ---- The decision ----

// TestChatAccessOfAChannelIsTheOwnMemberStatus walks the states of a channel
// this account can and cannot post in, from recorded answers.
func TestChatAccessOfAChannelIsTheOwnMemberStatus(t *testing.T) {
	write := mustDecodeRightsChat(t, recordedRightsChannelChat, recordedRightsChannelID)

	if write.Kind != ChatKindSupergroup || !write.IsChannel {
		t.Fatalf(
			"decoded chat = kind %q channel %v, want a channel",
			write.Kind, write.IsChannel,
		)
	}
	if write.SupergroupID != recordedRightsChannelSuper {
		t.Fatalf(
			"supergroup = %d, want %d: an update about the supergroup has "+
				"to find the chat that named it",
			write.SupergroupID, recordedRightsChannelSuper,
		)
	}

	cases := []struct {
		name       string
		supergroup string
		canSend    bool
		reason     ChatAccessReason
	}{
		{
			name:       "a subscriber cannot post",
			supergroup: recordedRightsSubscribed,
			canSend:    false,
			reason:     ChatAccessChannelReadOnly,
		},
		{
			name:       "an administrator with the right can post",
			supergroup: recordedRightsAdminPosts,
			canSend:    true,
			reason:     ChatAccessNone,
		},
		{
			name:       "an administrator without the right cannot post",
			supergroup: recordedRightsAdminCannotPost,
			canSend:    false,
			reason:     ChatAccessChannelReadOnly,
		},
		{
			name:       "the owner of a channel can post",
			supergroup: recordedRightsCreator,
			canSend:    true,
			reason:     ChatAccessNone,
		},
		{
			name:       "an account that left the channel says so",
			supergroup: recordedRightsLeft,
			canSend:    false,
			reason:     ChatAccessNotAMember,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			member := mustDecodeSupergroupMemberStatus(t, testCase.supergroup)

			access, ok := ChatAccessOf(write, member)
			if !ok {
				t.Fatal("a channel with a member status has an answer")
			}
			if access.CanSend != testCase.canSend {
				t.Errorf(
					"CanSend = %v, want %v", access.CanSend, testCase.canSend,
				)
			}
			if access.Reason != testCase.reason {
				t.Errorf("Reason = %q, want %q", access.Reason, testCase.reason)
			}
		})
	}
}

// TestChatAccessOfAChannelWithoutAMemberStatusSaysNothing is the case a
// reading of a channel's permissions gets wrong: a member status nobody has
// reported is not a subscriber.
func TestChatAccessOfAChannelWithoutAMemberStatusSaysNothing(t *testing.T) {
	write := mustDecodeRightsChat(t, recordedRightsChannelChat, recordedRightsChannelID)

	if _, ok := ChatAccessOf(write, MemberStatus{}); ok {
		t.Fatal(
			"a channel with no member status was decided: a read-only line " +
				"over a channel this account may post in",
		)
	}
}

// TestChatAccessOfAChannelIgnoresThePermissionsOfTheChat is the same
// question the other way round: the permissions of a channel say nothing
// about posting, because the schema gives the right to the rights of the
// administrator.
func TestChatAccessOfAChannelIgnoresThePermissionsOfTheChat(t *testing.T) {
	write := mustDecodeRightsChat(t, recordedRightsChannelChat, recordedRightsChannelID)
	write.PermissionsKnown = true
	write.CanSendBasicMessages = true

	access, ok := ChatAccessOf(write, MemberStatus{State: MemberStateMember})
	if !ok {
		t.Fatal("a channel with a member status has an answer")
	}
	if access.CanSend {
		t.Fatal(
			"a subscriber of a channel was given a field: " +
				"chat.permissions is not the right to post in a channel",
		)
	}
}

// TestChatAccessOfAGroupIsTheRightToSend walks a group with the right and a
// group without it, from recorded answers.
func TestChatAccessOfAGroupIsTheRightToSend(t *testing.T) {
	cases := []struct {
		name    string
		chat    string
		canSend bool
		reason  ChatAccessReason
	}{
		{
			name:    "a group with the right to send",
			chat:    recordedRightsGroupWritable,
			canSend: true,
			reason:  ChatAccessNone,
		},
		{
			name:    "a group without the right to send",
			chat:    recordedRightsGroupMuted,
			canSend: false,
			reason:  ChatAccessGroupRestricted,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			write := mustDecodeRightsChat(t, testCase.chat, recordedRightsGroupID)
			if write.Kind != ChatKindGroup {
				t.Fatalf("kind = %q, want a basic group", write.Kind)
			}

			access, ok := ChatAccessOf(write, MemberStatus{})
			if !ok {
				t.Fatal("a group with permissions has an answer")
			}
			if access.CanSend != testCase.canSend {
				t.Errorf(
					"CanSend = %v, want %v", access.CanSend, testCase.canSend,
				)
			}
			if access.Reason != testCase.reason {
				t.Errorf("Reason = %q, want %q", access.Reason, testCase.reason)
			}
		})
	}
}

// TestChatAccessOfAGroupWhoseMemberStatusIsGone covers a chat this account
// was removed from while it was open: the permissions of such a chat say
// what the group allows, not what this account may do in it.
func TestChatAccessOfAGroupWhoseMemberStatusIsGone(t *testing.T) {
	write := mustDecodeRightsChat(t, recordedRightsGroupWritable, recordedRightsGroupID)

	for member, reason := range map[MemberState]ChatAccessReason{
		MemberStateLeft:   ChatAccessNotAMember,
		MemberStateBanned: ChatAccessBanned,
	} {
		access, ok := ChatAccessOf(write, MemberStatus{State: member})
		if !ok {
			t.Fatalf("member %q: a group with a status has an answer", member)
		}
		if access.CanSend || access.Reason != reason {
			t.Errorf(
				"member %q: access = %+v, want the reason %q",
				member, access, reason,
			)
		}
	}
}

// TestChatAccessOfAPrivateChatIsWhetherThePersonIsThere walks a chat with a
// person and a chat with an account that is gone, from recorded answers.
func TestChatAccessOfAPrivateChatIsWhetherThePersonIsThere(t *testing.T) {
	write := mustDecodeRightsChat(t, recordedRightsPrivateChat, recordedRightsPrivateChatID)
	if write.Kind != ChatKindPrivate ||
		write.PeerUserID != recordedRightsPrivatePeerID {
		t.Fatalf(
			"decoded chat = kind %q peer %d, want a personal chat of %d",
			write.Kind, write.PeerUserID, recordedRightsPrivatePeerID,
		)
	}

	cases := []struct {
		name     string
		user     string
		canSend  bool
		deciding bool
		reason   ChatAccessReason
	}{
		{
			name:     "a person who is there",
			user:     recordedRightsUserReachable,
			canSend:  true,
			deciding: true,
		},
		{
			name:     "an account that is gone",
			user:     recordedRightsUserGone,
			canSend:  false,
			deciding: true,
			reason:   ChatAccessPeerUnreachable,
		},
		{
			name: "an answer that says nothing about the account",
			user: recordedRightsUserSilent,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			peer := write
			peer.PeerReachable, peer.PeerKnown = decodeRightsReachability(
				t, testCase.user,
			)

			access, ok := ChatAccessOf(peer, MemberStatus{})
			if ok != testCase.deciding {
				t.Fatalf("decided = %v, want %v", ok, testCase.deciding)
			}
			if !ok {
				return
			}
			if access.CanSend != testCase.canSend {
				t.Errorf(
					"CanSend = %v, want %v", access.CanSend, testCase.canSend,
				)
			}
			if access.Reason != testCase.reason {
				t.Errorf("Reason = %q, want %q", access.Reason, testCase.reason)
			}
		})
	}
}

// mustDecodeRightsChat decodes a recorded `chat` answer for the identifier
// that answer carries.
func mustDecodeRightsChat(t *testing.T, raw string, chatID ChatID) ChatWrite {
	t.Helper()

	return mustDecodeChatWrite(t, raw, chatID)
}

func mustDecodeSupergroupMemberStatus(t *testing.T, raw string) MemberStatus {
	t.Helper()

	member, err := decodeSupergroupMemberStatus(RawMessage(raw))
	if err != nil {
		t.Fatalf("decode supergroup answer: %v", err)
	}

	return member
}

func decodeRightsReachability(t *testing.T, raw string) (reachable, known bool) {
	t.Helper()

	reachable, known, err := decodeUserReachability(
		RawMessage(raw), recordedRightsPrivatePeerID,
	)
	if err != nil {
		t.Fatalf("decode user answer: %v", err)
	}

	return reachable, known
}

// ---- What the answer does not say ----

func TestDecodeChatWriteKeepsWhatTheAnswerDoesNotSay(t *testing.T) {
	write := mustDecodeRightsChat(t, recordedRightsChatWithoutPermissions, recordedRightsGroupID)
	if write.PermissionsKnown {
		t.Fatal(
			"a chat whose answer carried no permissions was read as one " +
				"that permits nothing",
		)
	}
	if _, ok := ChatAccessOf(write, MemberStatus{}); ok {
		t.Fatal("a chat with no permissions in its answer was decided")
	}
}

func TestDecodeChatWriteKeepsAnUnknownTypeEmpty(t *testing.T) {
	write := mustDecodeRightsChat(t, recordedRightsChatOfALostType, recordedRightsGroupID)
	if write.Kind != "" {
		t.Fatalf("kind = %q, want an empty kind", write.Kind)
	}
	if _, ok := ChatAccessOf(write, MemberStatus{}); ok {
		t.Fatal("a chat of a type this build does not know was decided")
	}
}

func TestDecodeChatWriteRejectsAnAnswerAboutAnotherChat(t *testing.T) {
	_, err := decodeChatWrite(
		RawMessage(recordedRightsChannelChat), ChatID(1),
	)
	if !errors.Is(err, ErrUnexpectedChatResponse) {
		t.Fatalf("err = %v, want ErrUnexpectedChatResponse", err)
	}
}

func TestDecodeChatWriteRejectsAnAnswerThatIsNotAChat(t *testing.T) {
	_, err := decodeChatWrite(
		RawMessage(`{"@type":"notChat","id":1}`), ChatID(1),
	)
	if !errors.Is(err, ErrUnexpectedChatResponse) {
		t.Fatalf("err = %v, want ErrUnexpectedChatResponse", err)
	}
}

func TestDecodeMemberStatusRefusesAConstructorItDoesNotKnow(t *testing.T) {
	_, err := decodeSupergroupMemberStatus(
		RawMessage(`{"id":1,"status":{"@type":"chatMemberStatusOwner"}}`),
	)
	if !errors.Is(err, ErrChatAccessUnavailable) {
		t.Fatalf("err = %v, want ErrChatAccessUnavailable", err)
	}
}

// ---- The read ----

func TestGetChatAccessRejectsTheZeroChatID(t *testing.T) {
	session, _, _, _ := newSessionWithFakes(t)

	_, err := session.GetChatAccess(context.Background(), 0)
	if !errors.Is(err, ErrInvalidChatID) {
		t.Fatalf("err = %v, want ErrInvalidChatID", err)
	}
}

// TestGetChatAccessAsksForWhatTheChatCannotAnswer is the wire of the read:
// one getChat, and one more read for the two answers a chat cannot give on
// its own.
func TestGetChatAccessAsksForWhatTheChatCannotAnswer(t *testing.T) {
	cases := []struct {
		name     string
		chat     string
		chatID   ChatID
		answer   string
		answerOf string
		canSend  bool
		reason   ChatAccessReason
	}{
		{
			name:     "a channel asks for the member status of the account",
			chat:     recordedRightsChannelChat,
			chatID:   recordedRightsChannelID,
			answer:   recordedRightsSubscribed,
			answerOf: "getSupergroup",
			canSend:  false,
			reason:   ChatAccessChannelReadOnly,
		},
		{
			name:    "a group is answered by the chat alone",
			chat:    recordedRightsGroupWritable,
			chatID:  recordedRightsGroupID,
			canSend: true,
		},
		{
			name:     "a personal chat asks whether the person is there",
			chat:     recordedRightsPrivateChat,
			chatID:   recordedRightsPrivateChatID,
			answer:   recordedRightsUserReachable,
			answerOf: "getUser",
			canSend:  true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			session, sender, _, client := newSessionWithFakes(t)

			access := readRights(
				t, session, sender, client, testCase.chatID, testCase.chat,
				1, testCase.answerOf, testCase.answer,
			)
			if access.CanSend != testCase.canSend {
				t.Errorf(
					"CanSend = %v, want %v", access.CanSend, testCase.canSend,
				)
			}
			if access.Reason != testCase.reason {
				t.Errorf(
					"Reason = %q, want %q", access.Reason, testCase.reason,
				)
			}

			types := sender.types(t)
			want := []string{"getChat"}
			if testCase.answerOf != "" {
				want = append(want, testCase.answerOf)
			}
			if len(types) != len(want) {
				t.Fatalf("requests = %v, want %v", types, want)
			}
			for index, name := range want {
				if types[index] != name {
					t.Errorf("request %d = %q, want %q", index, types[index], name)
				}
			}
		})
	}
}

// TestTheStoreKeepsWhatAReadFound is why a read is not a question on every
// poll: what it found is in the store, the next read of the same chat asks
// TDLib about the chat alone, and a promotion that arrives afterwards is
// answered from the update rather than from a second round trip.
func TestTheStoreKeepsWhatAReadFound(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	readRights(t, session, sender, client, recordedRightsChannelID,
		recordedRightsChannelChat, 1, "getSupergroup", recordedRightsSubscribed)

	access, known := session.LiveState().ChatAccess(recordedRightsChannelID)
	if !known {
		t.Fatal("the store does not know a chat a read has just described")
	}
	if access.CanSend || access.Reason != ChatAccessChannelReadOnly {
		t.Fatalf("the store says %+v, want a channel to watch", access)
	}

	// The next read of the same chat asks the chat and nothing else: the
	// member status is in the store.
	before := len(sender.types(t))
	readRights(t, session, sender, client, recordedRightsChannelID,
		recordedRightsChannelChat, 2, "", "")

	for _, name := range sender.types(t)[before:] {
		if name != "getChat" {
			t.Fatalf(
				"the second read asked for %q: the store had the answer", name,
			)
		}
	}

	mustApplyRightsUpdate(t, session.LiveState(), recordedRightsAdminPostsUpdate)

	access, known = session.LiveState().ChatAccess(recordedRightsChannelID)
	if !known || !access.CanSend {
		t.Fatalf(
			"the store says %+v (known=%v), want a field after the promotion",
			access, known,
		)
	}
}

func TestGetChatAccessReportsAChatItCannotDecide(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		access ChatAccess
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		access, err := session.GetChatAccess(
			context.Background(), recordedRightsGroupID,
		)
		resultCh <- result{access: access, err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChat", 1)
	feedResponse(
		t, client, recordedPayload(t, recordedRightsChatOfALostType), extra,
	)

	select {
	case got := <-resultCh:
		if !errors.Is(got.err, ErrChatAccessUnavailable) {
			t.Fatalf("err = %v, want ErrChatAccessUnavailable", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatAccess did not return")
	}
}

// readRights runs one read of a chat's rights against the fake transport and
// answers whatever else the read asks for.
//
// nth is the getChat request to answer, counted from one, so that a second
// read of the same chat in one test is not answered with the first one's
// extra.
func readRights(
	t *testing.T,
	session *AuthorizedSession,
	sender *fakeSender,
	client *Client,
	chatID ChatID,
	chat string,
	nth int,
	answerOf string,
	answer string,
) ChatAccess {
	t.Helper()

	type result struct {
		access ChatAccess
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		access, err := session.GetChatAccess(context.Background(), chatID)
		resultCh <- result{access: access, err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChat", nth)
	feedResponse(t, client, recordedPayload(t, chat), extra)

	if answerOf != "" {
		next := waitForRequestTypeN(t, sender, answerOf, 1)
		feedResponse(t, client, recordedPayload(t, answer), next)
	}

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("GetChatAccess: %v", got.err)
		}

		return got.access
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatAccess did not return")
	}

	return ChatAccess{}
}

// mustDecodeChatWrite decodes a recorded `chat` answer for the identifier the
// answer itself carries, which is what a test of the decoder needs: the
// decoder's own job is to refuse an answer about another chat.
func mustDecodeChatWrite(
	t *testing.T,
	raw string,
	chatID ChatID,
) ChatWrite {
	t.Helper()

	write, err := decodeChatWrite(RawMessage(raw), chatID)
	if err != nil {
		t.Fatalf("decode chat answer: %v", err)
	}

	return write
}
