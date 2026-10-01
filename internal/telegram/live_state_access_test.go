package telegram

import (
	"testing"
)

// This file is the live half of the rights of a chat: the updates TDLib
// sends about them, and what a consumer reads afterwards.
//
// The fixtures are the same recorded answers the read is proved against,
// wrapped in the updates that carry them.

// recordedRightsAdminPostsUpdate is the promotion of this account to an
// administrator of a channel with the right to post, as TDLib sends it
// (td_api.tl:10739).
const recordedRightsAdminPostsUpdate = `{"@type":"updateSupergroup",` +
	`"supergroup":{"id":1234567890,"member_count":4243,` +
	`"status":{"@type":"chatMemberStatusAdministrator","can_be_edited":true,` +
	`"rights":{"can_post_messages":true}}}}`

// recordedRightsAdminLosesPostUpdate is the same channel with the right taken
// away again.
const recordedRightsAdminLosesPostUpdate = `{"@type":"updateSupergroup",` +
	`"supergroup":{"id":1234567890,"member_count":4243,` +
	`"status":{"@type":"chatMemberStatusAdministrator","can_be_edited":true,` +
	`"rights":{"can_post_messages":false}}}}`

// recordedRightsPermissionsUpdate is a change of the right to send in a group
// (td_api.tl:10501).
const recordedRightsPermissionsUpdate = `{"@type":"updateChatPermissions",` +
	`"chat_id":-424242,"permissions":{"can_send_basic_messages":true,` +
	`"can_change_info":false,"can_invite_users":false}}`

// recordedRightsPermissionsTakenUpdate is the mute that took the right away
// again.
const recordedRightsPermissionsTakenUpdate = `{"@type":"updateChatPermissions",` +
	`"chat_id":-424242,"permissions":{"can_send_basic_messages":false,` +
	`"can_change_info":false,"can_invite_users":false}}`

// recordedRightsUserGoneUpdate is the account on the other side of a personal chat
// becoming inaccessible.
const recordedRightsUserGoneUpdate = `{"@type":"updateUser",` +
	`"user":{"id":888,"have_access":false,` +
	`"type":{"@type":"userTypeRegular"}}}`

// recordedRightsAnotherMemberUpdate is the status of somebody else in a group
// changing. The store must not read it as a change of this account's own
// rights, because nothing in it says which account that member is.
const recordedRightsAnotherMemberUpdate = `{"@type":"updateChatMember",` +
	`"chat_id":-424242,"actor_user_id":1,"date":1700000000,` +
	`"old_chat_member":{"member_id":{"@type":"messageSenderUser","user_id":1},` +
	`"status":{"@type":"chatMemberStatusMember"}},` +
	`"new_chat_member":{"member_id":{"@type":"messageSenderUser","user_id":1},` +
	`"status":{"@type":"chatMemberStatusAdministrator","rights":` +
	`{"can_post_messages":true}}}}`

func mustApplyRightsUpdate(t *testing.T, state *LiveState, raw string) {
	t.Helper()

	applied, err := state.ApplyUpdate(RawMessage(raw))
	if err != nil {
		t.Fatalf("apply %s: %v", raw, err)
	}
	if !applied {
		t.Fatalf("apply %s: the store says nothing changed", raw)
	}
}

// TestTheRightsOfAChannelFollowTheOwnMemberStatus is the promise of the
// whole file: a promotion is visible without a restart and without a second
// read, and taking the right away is as visible as having it.
func TestTheRightsOfAChannelFollowTheOwnMemberStatus(t *testing.T) {
	state := NewLiveState()
	state.noteChatAccess(
		recordedRightsChannelID,
		mustDecodeRightsChat(t, recordedRightsChannelChat, recordedRightsChannelID),
		MemberStatus{State: MemberStateMember},
	)

	mustApplyRightsUpdate(t, state, recordedRightsAdminPostsUpdate)

	access, known := state.ChatAccess(recordedRightsChannelID)
	if !known || !access.CanSend {
		t.Fatalf(
			"after the promotion the store says %+v (known=%v), want a field",
			access, known,
		)
	}

	mustApplyRightsUpdate(t, state, recordedRightsAdminLosesPostUpdate)

	access, known = state.ChatAccess(recordedRightsChannelID)
	if !known || access.CanSend ||
		access.Reason != ChatAccessChannelReadOnly {
		t.Fatalf(
			"after the right was taken away the store says %+v (known=%v), "+
				"want a read-only channel",
			access, known,
		)
	}
}

// TestTheRightsOfAGroupFollowThePermissionsOfTheChat is the other half: a
// mute lifted in a group brings the field back.
func TestTheRightsOfAGroupFollowThePermissionsOfTheChat(t *testing.T) {
	state := NewLiveState()
	state.noteChatAccess(
		recordedRightsGroupID,
		mustDecodeRightsChat(t, recordedRightsGroupMuted, recordedRightsGroupID),
		MemberStatus{},
	)

	access, known := state.ChatAccess(recordedRightsGroupID)
	if !known || access.CanSend ||
		access.Reason != ChatAccessGroupRestricted {
		t.Fatalf("the store says %+v, want a group to watch", access)
	}

	mustApplyRightsUpdate(t, state, recordedRightsPermissionsUpdate)

	access, _ = state.ChatAccess(recordedRightsGroupID)
	if !access.CanSend {
		t.Fatalf(
			"after the mute was lifted the store says %+v, want a field",
			access,
		)
	}

	mustApplyRightsUpdate(t, state, recordedRightsPermissionsTakenUpdate)

	access, _ = state.ChatAccess(recordedRightsGroupID)
	if access.CanSend {
		t.Fatalf("after the mute the store says %+v, want no field", access)
	}
}

// TestTheRightsOfAPersonalChatFollowTheAccountOnTheOtherSide covers the
// case of a chat with somebody who is no longer there.
func TestTheRightsOfAPersonalChatFollowTheAccountOnTheOtherSide(t *testing.T) {
	state := NewLiveState()
	write := mustDecodeRightsChat(t, recordedRightsPrivateChat, recordedRightsPrivateChatID)
	state.noteChatAccess(
		recordedRightsPrivateChatID, write, MemberStatus{},
	)
	state.notePeerAccess(recordedRightsPrivatePeerID, true, true)

	access, known := state.ChatAccess(recordedRightsPrivateChatID)
	if !known || !access.CanSend {
		t.Fatalf("the store says %+v, want a field", access)
	}

	mustApplyRightsUpdate(t, state, recordedRightsUserGoneUpdate)

	access, _ = state.ChatAccess(recordedRightsPrivateChatID)
	if access.CanSend || access.Reason != ChatAccessPeerUnreachable {
		t.Fatalf(
			"after the account was gone the store says %+v, want the reason "+
				"of an unreachable account",
			access,
		)
	}
}

// TestTheRightsOfAChatTheStoreWasNeverToldAboutChangeNothing is the shape
// of the gap the store leaves on purpose: an update about a chat it has
// never heard of is dropped, and the read that follows asks TDLib, whose
// answer is at least as new as the update was.
func TestTheRightsOfAChatTheStoreWasNeverToldAboutChangeNothing(t *testing.T) {
	state := NewLiveState()

	if _, known := state.ChatAccess(recordedRightsChannelID); known {
		t.Fatal("the store decided a chat it has never heard of")
	}

	applied, err := state.ApplyUpdate(
		RawMessage(recordedRightsPermissionsUpdate),
	)
	if err != nil {
		t.Fatalf("apply the update: %v", err)
	}
	if applied {
		t.Fatal(
			"an update about an unknown chat was recorded as a change: it " +
				"cannot be placed, and the read will ask TDLib anyway",
		)
	}
}

// TestTheStatusOfAnotherMemberIsNotTheStatusOfThisAccount is why
// updateChatMember is not read: it carries the status of one member and does
// not say that the member is the account this program runs as.
func TestTheStatusOfAnotherMemberIsNotTheStatusOfThisAccount(t *testing.T) {
	state := NewLiveState()
	state.noteChatAccess(
		recordedRightsGroupID,
		mustDecodeRightsChat(t, recordedRightsGroupWritable, recordedRightsGroupID),
		MemberStatus{State: MemberStateMember},
	)

	applied, err := state.ApplyUpdate(
		RawMessage(recordedRightsAnotherMemberUpdate),
	)
	if err != nil {
		t.Fatalf("apply the update: %v", err)
	}
	if applied {
		t.Fatal("the promotion of another member changed this account's rights")
	}

	access, _ := state.ChatAccess(recordedRightsGroupID)
	if !access.CanSend {
		t.Fatalf("the store says %+v, want the rights it had", access)
	}
}

// TestAReadOfOneChatDoesNotDecideAnother guards the index that places a
// supergroup update: it maps a supergroup to the chat that named it, and
// nothing else.
func TestAReadOfOneChatDoesNotDecideAnother(t *testing.T) {
	state := NewLiveState()
	state.noteChatAccess(
		recordedRightsChannelID,
		mustDecodeRightsChat(t, recordedRightsChannelChat, recordedRightsChannelID),
		MemberStatus{State: MemberStateMember},
	)

	if _, known := state.ChatAccess(9999); known {
		t.Fatal("the store decided a chat nobody read")
	}

	mustApplyRightsUpdate(t, state, recordedRightsAdminPostsUpdate)

	if _, known := state.ChatAccess(9999); known {
		t.Fatal("a supergroup update reached a chat that is not its own")
	}
}
