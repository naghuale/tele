package telegram

import (
	"encoding/json"
	"fmt"
	"time"
)

// This file is the presence half of the store: what the interface says
// about the other side of a chat, and nothing else about it.
//
// Two facts are enough for a status line, and both are small. Whether the
// other person is online, and how many members of a group are. Everything
// else TDLib sends about a user - a name, a phone number, a username, a
// profile photo - is personal data about somebody who did not ask to be
// followed, and it is dropped where it arrives. A store that kept it would
// be a store a diagnostic dump could print, and the reader of this store is
// a header line.
//
// The chat type decides what presence means for a chat, so the type is
// remembered when the chat arrives: one other person, or a group.

// chatKind is what kind of other side a chat has.
type chatKind uint8

const (
	// chatKindUnknown means the type of the chat has not arrived. A chat
	// with no type has no presence, and the interface says nothing rather
	// than guessing from the title.
	chatKindUnknown chatKind = iota

	// chatKindOneUser is a private or a secret chat: the presence is the
	// status of the one user on the other side.
	chatKindOneUser

	// chatKindGroup is a basic group, a supergroup or a channel: the
	// presence is how many members are online.
	chatKindGroup
)

// chatTypeKinds maps the chatType constructors of the pinned schema to the
// kind of presence a chat can have.
//
// A constructor the schema does not have is a kind this store does not
// know, and it maps to chatKindUnknown for the same reason an unknown
// connection state does: the interface is told that nothing is known,
// instead of being given a neighbour's meaning.
var chatTypeKinds = map[string]chatKind{
	"chatTypePrivate":    chatKindOneUser,
	"chatTypeSecret":     chatKindOneUser,
	"chatTypeBasicGroup": chatKindGroup,
	"chatTypeSupergroup": chatKindGroup,
}

// UserStatusKind is the state TDLib reports for a user.
type UserStatusKind uint8

const (
	// UserStatusUnknown means no status has arrived for the user.
	UserStatusUnknown UserStatusKind = iota

	// UserStatusEmpty is what TDLib reports for a user it has no status
	// for. It is not "offline": it says nothing, and the interface shows
	// nothing.
	UserStatusEmpty

	UserStatusOnline
	UserStatusOffline
	UserStatusRecently
	UserStatusLastWeek
	UserStatusLastMonth
)

// userStatusConstructors maps the UserStatus constructors of the pinned
// schema to the kinds the store keeps.
var userStatusConstructors = map[string]UserStatusKind{
	"UserStatusEmpty":     UserStatusEmpty,
	"UserStatusOnline":    UserStatusOnline,
	"UserStatusOffline":   UserStatusOffline,
	"UserStatusRecently":  UserStatusRecently,
	"UserStatusLastWeek":  UserStatusLastWeek,
	"UserStatusLastMonth": UserStatusLastMonth,
}

// UserStatus is the presence of one user, as TDLib reports it.
//
// The two times are Unix seconds from TDLib, kept as time.Time in UTC. The
// zone a user reads a time in is the view's business, not the store's.
type UserStatus struct {
	Kind UserStatusKind

	// Expires is when an online status runs out. TDLib sends no update at
	// that moment, so the interface has to read a time that has passed as
	// offline with Expires as the last-seen time.
	Expires time.Time

	// WasOnline is when the user was last online, for an offline status.
	WasOnline time.Time

	// ByPrivacySettings says that a coarse status was shown because of the
	// reader's privacy settings rather than because of the account. The
	// interface draws the same words either way, and the store keeps the
	// flag so that a future word for the difference has a field to read.
	ByPrivacySettings bool
}

// PresenceKind is what the presence of a chat is about.
type PresenceKind uint8

const (
	// PresenceUnknown means the store has nothing to say about the other
	// side: an unknown chat, or a chat whose user has no status yet.
	PresenceUnknown PresenceKind = iota

	// PresenceOneUser is a private or a secret chat.
	PresenceOneUser

	// PresenceGroup is a group, a supergroup or a channel.
	PresenceGroup
)

// Presence is what the store knows about the other side of a chat.
//
// It is a value and not a pointer, and it carries no names, phones or
// usernames: those are dropped where they arrive and never leave the
// package.
type Presence struct {
	Kind PresenceKind

	// UserID is the other person, for a chat that has one.
	UserID int64

	// Bot says that the other side is a bot. A bot's online status is
	// not what a reader is looking for, so the interface draws "bot" and
	// stops.
	Bot bool

	// Status is the presence of the other person.
	Status UserStatus

	// OnlineMemberCount is how many members of a group are online. Zero
	// means TDLib has not counted: it is not a claim that nobody is there.
	OnlineMemberCount int64
}

// String returns the chat presence without the status.
//
// §19 and the privacy of a status line: whether somebody is online and
// when they were last there is theirs to share or not, and a value on its
// way to a log is not the place to share it. What is left is enough to
// tell two presences apart in a test failure.
func (p Presence) String() string {
	switch p.Kind {
	case PresenceOneUser:
		return fmt.Sprintf("presence: user %d", p.UserID)

	case PresenceGroup:
		return fmt.Sprintf(
			"presence: group with %d online",
			p.OnlineMemberCount,
		)

	default:
		return "presence: unknown"
	}
}

// GoString returns the same as String, so %#v of a presence cannot print
// the status either.
func (p Presence) GoString() string { return p.String() }

// chatTypePatch returns the presence fields of a chat type.
//
// An absent type leaves the patch alone: a chat whose type TDLib did not
// send has no presence, and inventing one would give a group the status of
// a person.
func chatTypePatch(chatType chatTypeJSON) (kind chatKind, userID int64) {
	return chatTypeKinds[chatType.Type], chatType.UserID
}

// userRecord is what the store keeps about a user.
//
// There is no field here for a name or a phone number, and that is the
// whole design: the struct is the smallest thing that can hold a presence,
// so a field that nobody thought of cannot be added later without someone
// noticing that this is where a user becomes a person. The privacy test
// reads the whole store and checks that nothing personal is in it.
//
// reachable is the one field that is not a presence, and it is a fact
// about the account rather than about the person: `have_access` is false
// for an account that deleted itself or that was blocked, and it is what
// says whether a chat with that person can be written in at all.
type userRecord struct {
	status UserStatus
	bot    bool

	reachable      bool
	reachableKnown bool
}

// chatTypeJSON is the chatType of a chat, with the one field that says who
// the other side is.
type chatTypeJSON struct {
	Type   string `json:"@type"`
	UserID int64  `json:"user_id"`
}

// updateChatOnlineMemberCountJSON is updateChatOnlineMemberCount.
type updateChatOnlineMemberCountJSON struct {
	ChatID            int64 `json:"chat_id"`
	OnlineMemberCount int64 `json:"online_member_count"`
}

// updateUserStatusJSON is updateUserStatus.
type updateUserStatusJSON struct {
	UserID int64          `json:"user_id"`
	Status UserStatusJSON `json:"status"`
}

// updateUserJSON is the user field of updateUser.
type updateUserJSON struct {
	User struct {
		ID     int64          `json:"id"`
		Status UserStatusJSON `json:"status"`
		Type   struct {
			Type string `json:"@type"`
		} `json:"type"`
		HaveAccess *bool `json:"have_access"`
	} `json:"user"`
}

// UserStatusJSON is the UserStatus class: a constructor and at most one
// time.
type UserStatusJSON struct {
	Type              string `json:"@type"`
	Expires           int64  `json:"expires"`
	WasOnline         int64  `json:"was_online"`
	ByMyPrivacySettin bool   `json:"by_my_privacy_settings"`
}

// decodeUserStatus reads a UserStatus object.
func decodeUserStatus(raw UserStatusJSON) (UserStatus, bool) {
	kind, known := userStatusConstructors[raw.Type]
	if !known {
		return UserStatus{}, false
	}

	status := UserStatus{
		Kind:              kind,
		ByPrivacySettings: raw.ByMyPrivacySettin,
	}
	if kind == UserStatusOnline {
		status.Expires = instantOf(raw.Expires)
	}
	if kind == UserStatusOffline {
		status.WasOnline = instantOf(raw.WasOnline)
	}

	return status, true
}

// chatKind returns what kind of presence the chat of chatID can have.
func (l *LiveState) chatKind(chatID ChatID) chatKind {
	if l == nil {
		return chatKindUnknown
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	entry, known := l.chats[chatID]
	if !known {
		return chatKindUnknown
	}

	return entry.chatKind
}

// peerUserID returns the user on the other side of a chat, or zero for a
// group and for a chat with no type yet.
func (l *LiveState) peerUserID(chatID ChatID) int64 {
	if l == nil {
		return 0
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	entry, known := l.chats[chatID]
	if !known || entry.chatKind != chatKindOneUser {
		return 0
	}

	return entry.peerUserID
}

// onlineMemberCount returns how many members of a chat are online, and
// zero when the chat has not been counted.
func (l *LiveState) onlineMemberCount(chatID ChatID) int64 {
	if l == nil {
		return 0
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	entry, known := l.chats[chatID]
	if !known {
		return 0
	}

	return entry.onlineMemberCount
}

// UserStatus returns the status of a user and whether the store has one.
func (l *LiveState) userStatus(userID int64) (UserStatus, bool) {
	if l == nil {
		return UserStatus{}, false
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	record, known := l.users[userID]
	if !known {
		return UserStatus{}, false
	}

	return record.status, true
}

// userIsBot reports whether the user is a bot.
//
// An unknown user is not a bot: a chat with a person whose user object has
// not arrived would otherwise say "bot", and a bot header on a person's
// chat is a lie a user cannot check.
func (l *LiveState) userIsBot(userID int64) bool {
	if l == nil {
		return false
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	record, known := l.users[userID]
	if !known {
		return false
	}

	return record.bot
}

// ChatPresence returns the presence of the other side of a chat.
//
// A private or a secret chat answers with the status of its user, a group
// with the number of members that are online, and a chat the store knows
// nothing about with PresenceUnknown. The value is a copy: a caller cannot
// change the store through it.
func (l *LiveState) ChatPresence(chatID ChatID) Presence {
	if l == nil {
		return Presence{}
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	entry, known := l.chats[chatID]
	if !known {
		return Presence{}
	}

	switch entry.chatKind {
	case chatKindOneUser:
		if entry.peerUserID == 0 {
			return Presence{}
		}
		record, hasUser := l.users[entry.peerUserID]
		if !hasUser {
			return Presence{}
		}

		return Presence{
			Kind:   PresenceOneUser,
			UserID: entry.peerUserID,
			Bot:    record.bot,
			Status: record.status,
		}

	case chatKindGroup:
		return Presence{
			Kind:              PresenceGroup,
			OnlineMemberCount: entry.onlineMemberCount,
		}

	default:
		return Presence{}
	}
}

// applyUserStatus applies one updateUserStatus and reports whether the
// store changed.
func (l *LiveState) applyUserStatus(raw RawMessage) (bool, error) {
	var update updateUserStatusJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return false, fmt.Errorf("decode updateUserStatus: %w", err)
	}
	if update.UserID == 0 {
		return false, nil
	}

	status, known := decodeUserStatus(update.Status)
	if !known {
		// A constructor the pinned schema does not have. The status of a
		// user that changed shape is not something to guess at, and the
		// last known one stays: it is what Telegram said last.
		return false, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	record, exists := l.users[update.UserID]
	if !exists {
		record = userRecord{status: status}
		l.users[update.UserID] = record
		l.signalChanged()

		return true, nil
	}
	if record.status == status {
		return false, nil
	}

	record.status = status
	l.users[update.UserID] = record
	l.signalChanged()

	return true, nil
}

// applyUser applies the presence fields of one updateUser.
//
// The name, the last name, the phone number and the usernames of the user
// are read by the parser and then dropped. Only the identifier, the status
// and the bot flag are kept, and a user that is already known keeps the bot
// flag it had: updateUser carries the whole object every time, and a
// partial one that omits the type must not turn a bot into a person.
func (l *LiveState) applyUser(raw RawMessage) (bool, error) {
	var update updateUserJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return false, fmt.Errorf("decode updateUser: %w", err)
	}
	if update.User.ID == 0 {
		return false, nil
	}

	changed := false

	l.mu.Lock()
	defer l.mu.Unlock()

	record, exists := l.users[update.User.ID]
	if !exists {
		record = userRecord{}
	}

	if isBot := update.User.Type.Type == "userTypeBot"; isBot != record.bot {
		record.bot = isBot
		changed = true
	}

	if status, known := decodeUserStatus(update.User.Status); known &&
		status != record.status {
		record.status = status
		changed = true
	}

	// A pointer and not a bool, because an update without the field is not
	// an update that says the account is gone: it says nothing about it,
	// and reading it as false would put a read-only line over the personal
	// chat of every user a TDLib had stopped writing the field for.
	if reachable := update.User.HaveAccess; reachable != nil &&
		(*reachable != record.reachable || !record.reachableKnown) {
		record.reachable = *reachable
		record.reachableKnown = true
		changed = true
	}

	if changed {
		// The record is written even when it already existed, because a
		// new user has to be stored with an empty status and no signal
		// would be a change nobody was told about.
		l.users[update.User.ID] = record
		l.signalChanged()
		return true, nil
	}

	if !exists {
		l.users[update.User.ID] = record
	}

	return false, nil
}

// applyOnlineMemberCount applies one updateChatOnlineMemberCount.
//
// The count is only sent for a chat that has been opened, and it is
// replaced rather than added: TDLib sends the count of the chat, not a
// delta.
func (l *LiveState) applyOnlineMemberCount(raw RawMessage) (bool, error) {
	var update updateChatOnlineMemberCountJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return false, fmt.Errorf("decode updateChatOnlineMemberCount: %w", err)
	}

	entry, known := l.chats[ChatID(update.ChatID)]
	if !known {
		// The count of a chat the store does not have cannot be drawn, and
		// a record created here would be a partial one with no type and no
		// title.
		return false, nil
	}
	if entry.onlineMemberCount == update.OnlineMemberCount {
		return false, nil
	}

	after := *entry
	after.onlineMemberCount = update.OnlineMemberCount
	l.chats[ChatID(update.ChatID)] = &after
	l.signalChanged()

	return true, nil
}
