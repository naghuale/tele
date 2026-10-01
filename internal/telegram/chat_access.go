package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// This file is the answer to one question: can this account write in this
// chat.
//
// The interface used to ask it in no way at all. It drew a field, took text
// into it and put the text in the queue, and Telegram refused the send: a
// channel this account was only subscribed to took two messages, and both of
// them stayed on the screen with `! failed` under them. A field Telegram is
// going to refuse is a promise the program cannot keep, so the rights are
// read before the field is drawn.
//
// Every part of the answer is TDLib's own, and it is read from the object
// that owns it:
//
//   - a basic group and a supergroup: chat.permissions.can_send_basic_messages
//     (td_api.tl:1070), which is the right this account holds in that chat;
//   - a channel: this account's own member status, because the schema
//     describes a channel as a chat "where only administrators can post"
//     (td_api.tl:2741) and the right to post is
//     chatAdministratorRights.can_post_messages (td_api.tl:1092) — a field
//     of the member status and not of the permissions;
//   - a chat with one other person: user.have_access (td_api.tl:2403), which
//     the schema defines as false for an account that is inaccessible, and an
//     account that deleted itself or was blocked is inaccessible.
//
// The decision is one pure function over three small values, so that a
// recorded answer of TDLib is what decides it and nothing else is.

// ErrChatAccessUnavailable is returned when the rights of a chat could not
// be read at all.
//
// It is not the same answer as "this chat cannot be written in": an
// unavailable read leaves the interface with nothing to say, and a field
// drawn from nothing is a field that offers an action nobody has asked
// Telegram about.
var ErrChatAccessUnavailable = errors.New("telegram chat access unavailable")

// MemberState is this account's own status in a chat, in the words of this
// package rather than TDLib's.
//
// The constructors of the schema are chatMemberStatusCreator,
// chatMemberStatusAdministrator, chatMemberStatusMember,
// chatMemberStatusRestricted, chatMemberStatusLeft and chatMemberStatusBanned
// (td_api.tl:2493 onward). They are the same six states under other names
// here, and the names are the program's own so that a value which travels to
// a diagnostic reads in one vocabulary.
type MemberState string

const (
	// MemberStateUnknown is a status nobody has been told about.
	MemberStateUnknown MemberState = ""

	// MemberStateCreator is the owner of a chat, who holds every
	// administrator privilege (td_api.tl:2486).
	MemberStateCreator MemberState = "creator"

	// MemberStateAdministrator is a member with some administrator rights.
	MemberStateAdministrator MemberState = "administrator"

	// MemberStateMember is a member with neither privileges nor
	// restrictions.
	MemberStateMember MemberState = "member"

	// MemberStateRestricted is a member under restrictions, which the schema
	// says a basic group and a channel do not have (td_api.tl:2505).
	MemberStateRestricted MemberState = "restricted"

	// MemberStateLeft is a member that is not one: the account is not in
	// the chat.
	MemberStateLeft MemberState = "left"

	// MemberStateBanned is a member that was banned, which keeps the
	// account out of the chat for good until it is unbanned.
	MemberStateBanned MemberState = "banned"
)

// MemberStatus is what this account's own member status says about writing.
//
// It travels beside ChatWrite rather than inside it because it comes from a
// different object: the status is the supergroup's (td_api.tl:2747) and a
// chat member's (td_api.tl:2526), and the permissions are the chat's.
type MemberStatus struct {
	// State is the status itself, and MemberStateUnknown when TDLib has not
	// been asked or has not said.
	State MemberState

	// CanPostMessages is the administrator's right to post, which decides
	// whether a channel can be written in. It is true for the creator, who
	// holds every administrator privilege, and for an administrator whose
	// rights carry it.
	CanPostMessages bool
}

// ChatWrite is what is known about writing in one chat.
//
// Every field is a fact TDLib reported, and the ones that could not be read
// say so rather than holding a zero that would be a claim.
type ChatWrite struct {
	// Kind is the chat's type, and an empty kind is a chat whose type could
	// not be read — which is not a personal chat, and is not any other kind
	// either.
	Kind ChatKind

	// IsChannel is the flag of chatTypeSupergroup that tells a channel from
	// a supergroup (td_api.tl:3446).
	IsChannel bool

	// SupergroupID is the supergroup a channel or a supergroup chat is
	// about, and the identifier getSupergroup (td_api.tl:11511) is asked
	// with. It is also how an update about that supergroup finds the chat
	// it belongs to.
	SupergroupID int64

	// PeerUserID is the person on the other side of a personal chat, and
	// the identifier getUser (td_api.tl:11499) is asked with.
	PeerUserID int64

	// CanSendBasicMessages is chat.permissions.can_send_basic_messages, and
	// PermissionsKnown says the chat carried permissions at all.
	//
	// The two are apart because a chat with no permissions object is a chat
	// nobody has been told anything about, and reading that as "no right to
	// send" would draw a read-only line over a chat this account owns.
	CanSendBasicMessages bool
	PermissionsKnown     bool

	// PeerReachable is user.have_access of the other person of a personal
	// chat, and PeerKnown says it was read.
	PeerReachable bool
	PeerKnown     bool
}

// ChatAccess is the answer: whether this account can write in a chat, and
// what stops it when it cannot.
type ChatAccess struct {
	// CanSend is whether a message typed here would be accepted.
	CanSend bool

	// Reason is why it would not be, and empty when it would.
	Reason ChatAccessReason
}

// ChatAccessReason is what stops a chat from being written in.
//
// The names are this package's own. A value of this type travels to a
// screen, and the words of the screen are in the tui package.
type ChatAccessReason string

const (
	// ChatAccessNone is a chat that can be written in.
	ChatAccessNone ChatAccessReason = ""

	// ChatAccessChannelReadOnly is a channel this account does not post in.
	ChatAccessChannelReadOnly ChatAccessReason = "channelReadOnly"

	// ChatAccessGroupRestricted is a group or a supergroup where this
	// account's right to send has been taken away or was never given.
	ChatAccessGroupRestricted ChatAccessReason = "groupRestricted"

	// ChatAccessPeerUnreachable is a chat with a person whose account is
	// gone, whether they deleted it or this account blocked them.
	ChatAccessPeerUnreachable ChatAccessReason = "peerUnreachable"

	// ChatAccessNotAMember is a chat this account is not in.
	ChatAccessNotAMember ChatAccessReason = "notMember"

	// ChatAccessBanned is a chat this account was banned from.
	ChatAccessBanned ChatAccessReason = "banned"
)

// ChatAccessOf decides whether a chat can be written in, from what is known
// about it.
//
// The second result is false when there is not enough to decide, and a
// caller that is told so keeps whatever it had. A chat whose kind could not
// be read, a group whose permissions were not in the answer and a channel
// whose member status nobody has reported are all "not enough", because
// drawing a read-only line over a chat this account owns is a worse mistake
// than leaving a field that the next read corrects.
func ChatAccessOf(write ChatWrite, member MemberStatus) (ChatAccess, bool) {
	switch {
	case write.Kind == ChatKindPrivate:
		return privateChatAccess(write)
	case write.Kind == ChatKindGroup:
		return groupChatAccess(write, member)
	case write.Kind == ChatKindSupergroup && write.IsChannel:
		return channelChatAccess(member)
	case write.Kind == ChatKindSupergroup:
		return groupChatAccess(write, member)
	}

	return ChatAccess{}, false
}

// privateChatAccess decides a chat with one other person.
//
// Nothing about the rights of a personal chat is asked beyond whether the
// other person is there to be written to: the chat with oneself, a contact
// and a person nobody has ever written to are all the same to Telegram, and
// the one that is not is the one whose account is gone.
func privateChatAccess(write ChatWrite) (ChatAccess, bool) {
	if !write.PeerKnown {
		return ChatAccess{}, false
	}
	if !write.PeerReachable {
		return ChatAccess{Reason: ChatAccessPeerUnreachable}, true
	}

	return ChatAccess{CanSend: true}, true
}

// groupChatAccess decides a basic group or a supergroup, where the right to
// send is in the permissions of the chat.
//
// The member status is read for the reason and not for the decision: a
// group this account is not in cannot be written in whatever its
// permissions say, and the line on the screen is worth more when it says
// which of the two it is.
func groupChatAccess(
	write ChatWrite,
	member MemberStatus,
) (ChatAccess, bool) {
	switch member.State {
	case MemberStateBanned:
		return ChatAccess{Reason: ChatAccessBanned}, true
	case MemberStateLeft:
		return ChatAccess{Reason: ChatAccessNotAMember}, true
	}

	if !write.PermissionsKnown {
		return ChatAccess{}, false
	}
	if write.CanSendBasicMessages {
		return ChatAccess{CanSend: true}, true
	}

	return ChatAccess{Reason: ChatAccessGroupRestricted}, true
}

// channelChatAccess decides a channel.
//
// The member status is the whole of the decision, and it is not a guess
// about what TDLib puts in the permissions of a channel: the schema gives
// the right to post to the administrator rights, and a chat where only
// administrators post is decided by who this account is in it.
func channelChatAccess(member MemberStatus) (ChatAccess, bool) {
	switch member.State {
	case MemberStateUnknown:
		return ChatAccess{}, false

	case MemberStateCreator:
		return ChatAccess{CanSend: true}, true

	case MemberStateAdministrator:
		if member.CanPostMessages {
			return ChatAccess{CanSend: true}, true
		}

		return ChatAccess{Reason: ChatAccessChannelReadOnly}, true

	case MemberStateBanned:
		return ChatAccess{Reason: ChatAccessBanned}, true

	case MemberStateLeft:
		return ChatAccess{Reason: ChatAccessNotAMember}, true

	case MemberStateMember, MemberStateRestricted:
		// A subscriber, and the status the schema says a channel does not
		// have but a supergroup does. Both read as a chat to watch.
		return ChatAccess{Reason: ChatAccessChannelReadOnly}, true
	}

	return ChatAccess{}, false
}

// The wire of the three objects the rights are read from. Each is a
// separate struct rather than one shared `chat` because they are separate
// answers — a chat, a user and a supergroup — and a decoder that served all
// three would be a decoder with a shape none of them has.

type chatAccessChatJSON struct {
	Type        string               `json:"@type"`
	ID          tdInt                `json:"id"`
	Type_       chatAccessTypeJSON   `json:"type"`
	Permissions *chatPermissionsJSON `json:"permissions"`
}

// chatAccessTypeJSON is the `type` of a chat, which is what says whether it
// is a person, a basic group or a supergroup (td_api.tl:3440 onward).
type chatAccessTypeJSON struct {
	Type         string `json:"@type"`
	UserID       tdInt  `json:"user_id"`
	SupergroupID tdInt  `json:"supergroup_id"`
	IsChannel    bool   `json:"is_channel"`
}

// chatPermissionsJSON is the one field of chatPermissions the interface reads
// (td_api.tl:1070). It is a plain object with no `@type` of its own, so a
// pointer to it is how an absent answer is told from an answer of nothing.
type chatPermissionsJSON struct {
	CanSendBasicMessages bool `json:"can_send_basic_messages"`
}

type getUserAccessRequest struct {
	Type   string `json:"@type"`
	UserID int64  `json:"user_id"`
}

// chatAccessUserJSON is the one field of a user the interface reads for
// writing: whether the account is accessible at all (td_api.tl:2403).
type chatAccessUserJSON struct {
	Type       string `json:"@type"`
	ID         tdInt  `json:"id"`
	HaveAccess *bool  `json:"have_access"`
}

type getSupergroupRequest struct {
	Type         string `json:"@type"`
	SupergroupID int64  `json:"supergroup_id"`
}

// supergroupAccessJSON is the one field of a supergroup the interface reads
// for writing: this account's own status in it (td_api.tl:2747).
type supergroupAccessJSON struct {
	ID     tdInt                `json:"id"`
	Status chatMemberStatusJSON `json:"status"`
}

// chatMemberStatusJSON is any constructor of ChatMemberStatus. The name of
// the constructor is the status, and the administrator rights are where the
// right to post is (td_api.tl:2500).
type chatMemberStatusJSON struct {
	Type   string                   `json:"@type"`
	Rights *administratorRightsJSON `json:"rights"`
}

// administratorRightsJSON is the one field of chatAdministratorRights the
// interface reads (td_api.tl:1092).
type administratorRightsJSON struct {
	CanPostMessages bool `json:"can_post_messages"`
}

// GetChatAccess reads whether this account can write in a chat.
//
// It is a read and not a subscription, because the answer is about the chat
// that is open: one getChat, and then one more read for the two things a
// chat object cannot answer on its own — whether the person on the other
// side of a personal chat is reachable, and this account's own status in a
// channel. A group is answered by the chat itself and costs nothing more.
//
// What it read goes into the live store as well, so that an update TDLib
// sends afterwards has a chat to be placed on: updateSupergroup names a
// supergroup and not a chat, and a chat nobody has heard of cannot be found
// from a supergroup identifier.
func (s *AuthorizedSession) GetChatAccess(
	ctx context.Context,
	chatID ChatID,
) (ChatAccess, error) {
	if chatID == 0 {
		return ChatAccess{}, fmt.Errorf("%w: %d", ErrInvalidChatID, chatID)
	}

	write, err := s.chatWrite(ctx, chatID)
	if err != nil {
		return ChatAccess{}, err
	}

	member, err := s.memberStatusOf(ctx, write)
	if err != nil {
		return ChatAccess{}, err
	}

	// The person is recorded before the decision, because the store reads
	// the reachability of a peer from the users it keeps and not from the
	// chat's own record, and an update about that person is what keeps the
	// answer current.
	if write.Kind == ChatKindPrivate {
		reachable, known, err := s.peerReachable(ctx, write.PeerUserID)
		if err != nil {
			return ChatAccess{}, err
		}
		s.live.notePeerAccess(write.PeerUserID, reachable, known)
		write.PeerReachable = reachable
		write.PeerKnown = known
	}

	s.live.noteChatAccess(chatID, write, member)

	access, ok := ChatAccessOf(write, member)
	if !ok {
		return ChatAccess{}, fmt.Errorf(
			"%w: chat %d", ErrChatAccessUnavailable, chatID,
		)
	}

	return access, nil
}

// chatWrite asks the chat itself what kind of chat it is and what this
// account may send in it.
func (s *AuthorizedSession) chatWrite(
	ctx context.Context,
	chatID ChatID,
) (ChatWrite, error) {
	request, err := json.Marshal(getChatRequest{
		Type:   "getChat",
		ChatID: int64(chatID),
	})
	if err != nil {
		return ChatWrite{}, fmt.Errorf("marshal getChat: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return ChatWrite{}, fmt.Errorf("getChat: %w", err)
	}

	return decodeChatWrite(raw, chatID)
}

// decodeChatWrite reads the `chat` answer of getChat into the facts that
// writing in it depends on.
//
// It is a function of its own and not a part of the read above for the
// reason DecodeHistoryPage is: a test that decides from a hand-built value
// proves the decision and not that TDLib's answer is read the way this
// program reads every other answer.
func decodeChatWrite(raw RawMessage, chatID ChatID) (ChatWrite, error) {
	var response chatAccessChatJSON
	if err := json.Unmarshal(raw, &response); err != nil {
		return ChatWrite{}, fmt.Errorf("decode getChat response: %w", err)
	}
	if response.Type != "chat" {
		return ChatWrite{}, fmt.Errorf(
			"%w: %s",
			ErrUnexpectedChatResponse,
			unexpectedResponse("getChat", raw),
		)
	}
	if int64(response.ID) != int64(chatID) {
		return ChatWrite{}, fmt.Errorf(
			"%w: getChat requested id=%d, returned id=%d for @extra=%q",
			ErrUnexpectedChatResponse, chatID, int64(response.ID),
			responseExtra(raw),
		)
	}

	// A kind this build does not know stays empty rather than becoming a
	// personal chat. parseChatType answers an unknown type with a personal
	// one because a row of the list has to be drawn as something; a field
	// has to decide whether it may be typed in, and drawing a read-only
	// line is the mistake this file exists to stop.
	write := ChatWrite{
		Kind:         ChatKind(response.Type_.Type),
		IsChannel:    response.Type_.IsChannel,
		SupergroupID: int64(response.Type_.SupergroupID),
		PeerUserID:   int64(response.Type_.UserID),
	}
	switch write.Kind {
	case ChatKindPrivate, ChatKindGroup, ChatKindSupergroup:
	default:
		return ChatWrite{}, nil
	}

	if response.Permissions != nil {
		write.PermissionsKnown = true
		write.CanSendBasicMessages = response.Permissions.CanSendBasicMessages
	}

	return write, nil
}

// memberStatusOf returns this account's own status in a chat, from the
// store where an update has already put it and from TDLib otherwise.
//
// A chat that is not a supergroup or a channel has no status worth a round
// trip: the permissions of the chat are the whole of its answer.
func (s *AuthorizedSession) memberStatusOf(
	ctx context.Context,
	write ChatWrite,
) (MemberStatus, error) {
	if write.Kind != ChatKindSupergroup {
		return MemberStatus{}, nil
	}
	if status, known := s.live.chatMemberStatus(write.SupergroupID); known {
		return status, nil
	}
	if write.SupergroupID == 0 {
		return MemberStatus{}, nil
	}

	request, err := json.Marshal(getSupergroupRequest{
		Type:         "getSupergroup",
		SupergroupID: write.SupergroupID,
	})
	if err != nil {
		return MemberStatus{}, fmt.Errorf("marshal getSupergroup: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return MemberStatus{}, fmt.Errorf("getSupergroup: %w", err)
	}

	return decodeSupergroupMemberStatus(raw)
}

// decodeSupergroupMemberStatus reads the own member status out of a
// getSupergroup answer.
func decodeSupergroupMemberStatus(raw RawMessage) (MemberStatus, error) {
	var response supergroupAccessJSON
	if err := json.Unmarshal(raw, &response); err != nil {
		return MemberStatus{}, fmt.Errorf("decode getSupergroup response: %w", err)
	}

	return decodeMemberStatusObject(response.Status)
}

// decodeMemberStatusObject reads one constructor of ChatMemberStatus.
func decodeMemberStatusObject(
	status chatMemberStatusJSON,
) (MemberStatus, error) {
	switch status.Type {
	case "chatMemberStatusCreator":
		// The owner of a chat "has all the administrator privileges"
		// (td_api.tl:2486), and posting is one of them.
		return MemberStatus{
			State:           MemberStateCreator,
			CanPostMessages: true,
		}, nil

	case "chatMemberStatusAdministrator":
		out := MemberStatus{State: MemberStateAdministrator}
		if status.Rights != nil {
			out.CanPostMessages = status.Rights.CanPostMessages
		}

		return out, nil

	case "chatMemberStatusMember":
		return MemberStatus{State: MemberStateMember}, nil

	case "chatMemberStatusRestricted":
		return MemberStatus{State: MemberStateRestricted}, nil

	case "chatMemberStatusLeft":
		return MemberStatus{State: MemberStateLeft}, nil

	case "chatMemberStatusBanned":
		return MemberStatus{State: MemberStateBanned}, nil
	}

	// A constructor this build does not know is not a status, and a status
	// that is not one must not be read as a member: the schema grows
	// constructors, and the one it grows next is not a reason to draw a
	// read-only line over a chat this account writes in.
	return MemberStatus{}, fmt.Errorf(
		"%w: member status %q", ErrChatAccessUnavailable, status.Type,
	)
}

// peerReachable asks whether the account on the other side of a personal
// chat is there to be written to.
//
// The answer is `have_access` of the user (td_api.tl:2403), which the
// schema defines as false for an account that is inaccessible — and says
// that an identifier of such an account "can't be passed to any method",
// which is why this is a read of the person and not a read of the chat.
func (s *AuthorizedSession) peerReachable(
	ctx context.Context,
	userID int64,
) (reachable bool, known bool, err error) {
	if userID == 0 {
		return false, false, fmt.Errorf(
			"%w: chat has no peer user id", ErrChatAccessUnavailable,
		)
	}

	request, err := json.Marshal(getUserAccessRequest{
		Type:   "getUser",
		UserID: userID,
	})
	if err != nil {
		return false, false, fmt.Errorf("marshal getUser: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return false, false, fmt.Errorf("getUser: %w", err)
	}

	return decodeUserReachability(raw, userID)
}

// decodeUserReachability reads a `user` answer into whether the account is
// accessible at all.
//
// The identifier is checked because a user of another account is not an
// answer about the person this chat is with, and the reachability of
// somebody else would decide whether this chat can be written in.
func decodeUserReachability(
	raw RawMessage,
	userID int64,
) (reachable bool, known bool, err error) {
	var response chatAccessUserJSON
	if err := json.Unmarshal(raw, &response); err != nil {
		return false, false, fmt.Errorf("decode getUser response: %w", err)
	}
	if response.Type != "user" {
		return false, false, fmt.Errorf(
			"%w: %s",
			ErrUnexpectedChatResponse,
			unexpectedResponse("getUser", raw),
		)
	}
	if int64(response.ID) != userID {
		return false, false, fmt.Errorf(
			"%w: getUser requested id=%d, returned id=%d for @extra=%q",
			ErrUnexpectedChatResponse, userID, int64(response.ID),
			responseExtra(raw),
		)
	}
	if response.HaveAccess == nil {
		// The field is in the schema of this build, so an answer without it
		// is not an answer about a person. Reading it as an inaccessible one
		// would put a read-only line over every personal chat of a TDLib
		// that had stopped writing the field.
		return false, false, nil
	}

	return *response.HaveAccess, true, nil
}
