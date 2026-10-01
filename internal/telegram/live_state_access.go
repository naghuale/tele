package telegram

import (
	"encoding/json"
	"fmt"
)

// This file keeps the rights of a chat up to date, in the same store the
// chat list is projected into and for the same reason: the session pump is
// the only writer, and a consumer reads a snapshot rather than being handed
// an update. It is what makes a change of rights visible without a
// restart — somebody is made an administrator of a channel, or a mute is
// lifted, and the field the interface refused to draw is there within a
// poll cycle.
//
// Two updates carry a change of rights, and both are read here:
//
//   - updateChatPermissions (td_api.tl:10501) carries the chat's own
//     permissions, which decide a basic group and a supergroup;
//   - updateSupergroup (td_api.tl:10739) carries the supergroup, and a
//     supergroup carries this account's own member status in it
//     (td_api.tl:2747), which is what decides a channel.
//
// updateChatMember is deliberately not read, and the reason is the whole
// point of reading the other two. It carries the status of one member of a
// chat (td_api.tl:2526) and does not say that the member is this account,
// so a promotion of somebody else would be read as this account's own
// promotion and the interface would draw a field for a channel Telegram
// still refuses to post in. TDLib sends updateSupergroup and
// updateChatPermissions for a change that is about this account, and those
// two are the ones that can be placed without knowing the account.

const (
	updateChatPermissionsType = "updateChatPermissions"
	updateSupergroupType      = "updateSupergroup"
)

// chatAccessFacts is what the store knows about writing in one chat.
//
// It is a value and not a pointer so that a comparison is a comparison, and
// the two things it holds are compared together because they are read
// together: the decision asks for a chat and a member status at once.
type chatAccessFacts struct {
	write  ChatWrite
	member MemberStatus
}

// equal reports whether two records are the same.
func (f chatAccessFacts) equal(other chatAccessFacts) bool {
	return f.write == other.write && f.member == other.member
}

// accessUpdateChatPermissionsJSON is the update of the permissions of a chat
// (td_api.tl:10501).
type accessUpdateChatPermissionsJSON struct {
	Type        string               `json:"@type"`
	ChatID      tdInt                `json:"chat_id"`
	Permissions *chatPermissionsJSON `json:"permissions"`
}

// accessUpdateSupergroupJSON is the update of a supergroup or a channel
// (td_api.tl:10739). The update names the supergroup and not the chat, so
// the chat is found through the supergroup the chat itself named.
type accessUpdateSupergroupJSON struct {
	Type       string               `json:"@type"`
	Supergroup supergroupAccessJSON `json:"supergroup"`
}

// ChatAccess returns whether this account can write in a chat, and whether
// the store knows enough to say.
//
// The second result is false for a chat the store has never heard of, and
// for a chat whose rights no update has ever carried. A caller that is told
// false keeps the answer it had, which is the answer of a read: an update
// that arrives before the first read is not lost, because the read asks
// TDLib, whose answer is at least as new as the update was.
func (l *LiveState) ChatAccess(chatID ChatID) (ChatAccess, bool) {
	if l == nil {
		return ChatAccess{}, false
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	facts, known := l.access[chatID]
	if !known {
		return ChatAccess{}, false
	}

	return l.chatAccessOf(facts)
}

// chatAccessOf decides a record the caller already holds the lock for.
func (l *LiveState) chatAccessOf(facts chatAccessFacts) (ChatAccess, bool) {
	write := facts.write

	// Whether the person on the other side of a personal chat is reachable
	// is a fact about that person and not about the chat, so it is read
	// from the users the store keeps and not from the chat's own record.
	if write.Kind == ChatKindPrivate && !write.PeerKnown {
		reachable, read := l.userReachableLocked(write.PeerUserID)
		write.PeerReachable = reachable
		write.PeerKnown = read
	}

	return ChatAccessOf(write, facts.member)
}

// chatMemberStatus returns the own member status the store holds for a
// supergroup, and whether it holds one.
//
// A supergroup is asked with its own identifier and not with a chat, so
// this is what a read uses to avoid a round trip for an answer the store
// was given while the program was running.
func (l *LiveState) chatMemberStatus(
	supergroupID int64,
) (MemberStatus, bool) {
	if l == nil || supergroupID == 0 {
		return MemberStatus{}, false
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	chatID, known := l.bySupergroup[supergroupID]
	if !known {
		return MemberStatus{}, false
	}

	facts, known := l.access[chatID]
	if !known || facts.member.State == MemberStateUnknown {
		return MemberStatus{}, false
	}

	return facts.member, true
}

// noteChatAccess records what a read found out about a chat.
//
// It is the only writer of the chat's supergroup index, and it is how a
// supergroup update finds the chat it belongs to: TDLib names the
// supergroup in updateSupergroup and the chat in every other update, and
// the chat is the one that says which supergroup it is.
func (l *LiveState) noteChatAccess(
	chatID ChatID,
	write ChatWrite,
	member MemberStatus,
) {
	if l == nil || chatID == 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.noteChatAccessLocked(chatID, write, member)
}

func (l *LiveState) noteChatAccessLocked(
	chatID ChatID,
	write ChatWrite,
	member MemberStatus,
) {
	if write.Kind == ChatKindSupergroup && write.SupergroupID != 0 {
		l.bySupergroup[write.SupergroupID] = chatID
	}

	// A read knows the member status of a supergroup and of nothing else,
	// and it must not write an empty one over what an update has put
	// there: the read was asked about the supergroup, and the update
	// carried the status.
	if write.Kind != ChatKindSupergroup {
		member = MemberStatus{}
	}

	after := chatAccessFacts{write: write, member: member}
	if before, exists := l.access[chatID]; exists && before.equal(after) {
		return
	}

	l.access[chatID] = after
	l.signalChanged()
}

// notePeerAccess records whether a person's account is reachable.
//
// The two writers are the read of that person and TDLib's own update about
// them, and the last one to be applied is the one that is current: both are
// answers to the same question about the same object, which is the argument
// above about a read and an update not racing.
func (l *LiveState) notePeerAccess(userID int64, reachable, known bool) {
	if l == nil || userID == 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	record := l.users[userID]
	if record.reachable == reachable && record.reachableKnown == known {
		return
	}

	record.reachable = reachable
	record.reachableKnown = known
	l.users[userID] = record
	l.signalChanged()
}

// userReachableLocked returns whether the store has been told that a person's
// account is reachable.
func (l *LiveState) userReachableLocked(
	userID int64,
) (reachable, known bool) {
	if userID == 0 {
		return false, false
	}

	record, known := l.users[userID]

	return record.reachable, known && record.reachableKnown
}

// applyChatPermissions applies a change of the permissions of a chat.
//
// An update for a chat the store has no rights for is dropped, and that is
// not a loss: the store cannot decide anything about a chat it does not
// know the kind of, and the read that will decide this chat asks TDLib,
// whose answer already carries the new permissions.
func (l *LiveState) applyChatPermissions(raw RawMessage) (bool, error) {
	var update accessUpdateChatPermissionsJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return false, fmt.Errorf("decode %s: %w", updateChatPermissionsType, err)
	}
	if update.ChatID == 0 {
		return false, nil
	}
	if update.Permissions == nil {
		return false, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	facts, known := l.access[ChatID(update.ChatID)]
	if !known {
		return false, nil
	}

	after := facts
	after.write.PermissionsKnown = true
	after.write.CanSendBasicMessages = update.Permissions.CanSendBasicMessages
	if after.equal(facts) {
		return false, nil
	}

	l.access[ChatID(update.ChatID)] = after
	l.signalChanged()

	return true, nil
}

// applySupergroup applies a change of a supergroup or a channel, which
// carries this account's own member status in it.
//
// A supergroup the store has not been told the chat of is dropped, for the
// same reason a chat it has no rights for is.
func (l *LiveState) applySupergroup(raw RawMessage) (bool, error) {
	var update accessUpdateSupergroupJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return false, fmt.Errorf("decode %s: %w", updateSupergroupType, err)
	}
	if update.Supergroup.ID == 0 {
		return false, nil
	}

	status, err := decodeMemberStatusObject(update.Supergroup.Status)
	if err != nil {
		return false, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	chatID, known := l.bySupergroup[int64(update.Supergroup.ID)]
	if !known {
		return false, nil
	}

	facts, known := l.access[chatID]
	if !known {
		return false, nil
	}

	after := facts
	after.member = status
	if after.equal(facts) {
		return false, nil
	}

	l.access[chatID] = after
	l.signalChanged()

	return true, nil
}
