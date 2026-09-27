package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The presence of the other side of a chat: whether the person is online,
// and how many members of a group are.
//
// Only the two facts are kept. A user update carries a name, a phone
// number, a username and a profile photo, and none of that is presence:
// keeping it would put somebody's personal data in a store whose only
// reader is a status line, so the store keeps the identifier and the
// status and drops the rest of the object on the floor.
//
// The fixtures below follow the pinned TDLib schema,
// td/generate/scheme/td_api.tl at commit ea97bcdd (TDLib 1.8.67):
//
//	user id:int53 first_name:string last_name:string usernames:usernames
//	    phone_number:string status:UserStatus ... type:UserType ...;    // :2403
//	userTypeRegular = UserType;                                       // :795
//	userTypeBot can_be_edited:Bool ... = UserType;                    // :816
//	chatTypePrivate user_id:int53 = ChatType;                         // :3440
//	chatTypeBasicGroup basic_group_id:int53 = ChatType;               // :3443
//	chatTypeSupergroup supergroup_id:int53 is_channel:Bool = ChatType; // :3446
//	chatTypeSecret secret_chat_id:int32 user_id:int53 = ChatType;     // :3449
//	UserStatusEmpty = UserStatus;                                     // :6408
//	UserStatusOnline expires:int32 = UserStatus;                      // :6411
//	UserStatusOffline was_online:int32 = UserStatus;                  // :6414
//	UserStatusRecently by_my_privacy_settings:Bool = UserStatus;      // :6417
//	UserStatusLastWeek by_my_privacy_settings:Bool = UserStatus;      // :6420
//	UserStatusLastMonth by_my_privacy_settings:Bool = UserStatus;     // :6423
//	updateChatOnlineMemberCount chat_id:int53 online_member_count:int32
//	    = Update;                                                    // :10613
//	updateUserStatus user_id:int53 status:UserStatus = Update;       // :10730
//	updateUser user:user = Update;                                   // :10733

// presenceUserUpdate is a real-shaped updateUser.
//
// The first name, the last name and the phone number are values a test can
// look for, because the whole point of the fixture is that they are read
// by the parser and then dropped.
var presenceUserUpdate = RawMessage(`{"@type":"updateUser","user":{"@type":"user","id":700,` +
	`"first_name":"Alex","last_name":"Morgan","phone_number":"+15550100",` +
	`"status":{"@type":"UserStatusOnline","expires":1800000000},"type":{"@type":"userTypeRegular"}}}`)

func presenceUserStatusUpdate(userID int64, status string) RawMessage {
	return RawMessage(`{"@type":"updateUserStatus","user_id":` +
		presenceID(userID) + `,"status":` + status + `}`)
}

func presenceChatUpdate(chatID int64, chatType string) RawMessage {
	return RawMessage(`{"@type":"updateNewChat","chat":{"@type":"chat","id":` + presenceID(chatID) +
		`,"title":"A","unread_count":0,"type":{"@type":"` + chatType + `"` +
		presenceUserIDField(chatType) + `},"positions":[]}}`)
}

func presenceUserIDField(chatType string) string {
	switch chatType {
	case "chatTypePrivate", "chatTypeSecret":
		return `,"user_id":700`
	default:
		return ""
	}
}

// presenceID renders an identifier for a fixture.
func presenceID(value int64) string {
	return strconv.FormatInt(value, 10)
}

// Every chat type of the schema is remembered, because the presence of a
// chat is read from the type: a private chat is about one person, a group
// is about a count, and getting it wrong would put a person's status on a
// channel.
func TestTheChatTypeIsRemembered(t *testing.T) {
	for chatType, want := range map[string]chatKind{
		"chatTypePrivate":    chatKindOneUser,
		"chatTypeSecret":     chatKindOneUser,
		"chatTypeBasicGroup": chatKindGroup,
		"chatTypeSupergroup": chatKindGroup,
	} {
		t.Run(chatType, func(t *testing.T) {
			state := NewLiveState()
			if _, err := state.apply(presenceChatUpdate(10, chatType)); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if got := state.chatKind(10); got != want {
				t.Fatalf("chatKind = %v, want %v", got, want)
			}
		})
	}
}

func TestTheUserIDOfAPrivateChatIsRemembered(t *testing.T) {
	for _, chatType := range []string{"chatTypePrivate", "chatTypeSecret"} {
		state := NewLiveState()
		if _, err := state.apply(presenceChatUpdate(10, chatType)); err != nil {
			t.Fatalf("%s: apply: %v", chatType, err)
		}
		if got := state.peerUserID(10); got != 700 {
			t.Fatalf("%s: peerUserID = %d, want 700", chatType, got)
		}
	}
}

// A group has no single other side, so there is no user to look up: a
// group that somehow had one would be drawn with a person's status.
func TestAGroupHasNoPeerUser(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceChatUpdate(10, "chatTypeSupergroup")); err != nil {
		t.Fatal(err)
	}
	if got := state.peerUserID(10); got != 0 {
		t.Fatalf("peerUserID = %d, want 0", got)
	}
}

func TestAUserUpdateSetsTheStatus(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceUserUpdate); err != nil {
		t.Fatalf("apply: %v", err)
	}

	status, known := state.userStatus(700)
	if !known {
		t.Fatal("the status of the user is unknown after updateUser")
	}
	if status.Kind != UserStatusOnline {
		t.Fatalf("status kind = %v, want online", status.Kind)
	}
	if want := time.Unix(1800000000, 0).UTC(); !status.Expires.Equal(want) {
		t.Fatalf("expires = %s, want %s", status.Expires, want)
	}
}

// The update is a whole user object, and the store keeps two fields of it.
// The name and the phone number must not survive: they are personal data
// that a status line does not need and that a diagnostic dump must not
// print.
func TestAUserUpdateKeepsNoNameAndNoPhone(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceUserUpdate); err != nil {
		t.Fatal(err)
	}

	if state.mu.RLock(); len(state.users) != 1 {
		state.mu.RUnlock()
		t.Fatalf("the store keeps %d user records, want 1", len(state.users))
	}
	for id, record := range state.users {
		if record.name != "" || record.phone != "" {
			t.Fatalf("user %d keeps the name %q and the phone %q", id, record.name, record.phone)
		}
	}
	state.mu.RUnlock()
}

func TestTheBotFlagIsKeptAndTheNameIsNot(t *testing.T) {
	state := NewLiveState()
	raw := RawMessage(`{"@type":"updateUser","user":{"@type":"user","id":700,` +
		`"first_name":"Helper Bot","phone_number":"",` +
		`"status":{"@type":"UserStatusOnline","expires":1800000000},` +
		`"type":{"@type":"userTypeBot","is_inline":false,"can_join_groups":true,` +
		`"can_read_all_group_messages":false,"is_support":false}}}`)

	if _, err := state.apply(raw); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !state.userIsBot(700) {
		t.Fatal("a bot was not recognized")
	}
	state.mu.RLock()
	bot := state.users[700]
	state.mu.RUnlock()
	if bot.name != "" {
		t.Fatalf("the store keeps the bot name %q", bot.name)
	}
}

func TestAUserStatusReplacesTheOldOne(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceUserUpdate); err != nil {
		t.Fatal(err)
	}

	offline := time.Unix(1799000000, 0).UTC()
	raw := presenceUserStatusUpdate(700, `{"@type":"UserStatusOffline","was_online":1799000000}`)
	if _, err := state.apply(raw); err != nil {
		t.Fatal(err)
	}

	status, known := state.userStatus(700)
	if !known {
		t.Fatal("the status of the user is unknown")
	}
	if status.Kind != UserStatusOffline {
		t.Fatalf("status kind = %v, want offline", status.Kind)
	}
	if !status.WasOnline.Equal(offline) {
		t.Fatalf("was online = %s, want %s", status.WasOnline, offline)
	}
}

func TestEveryUserStatusOfTheSchemaIsKept(t *testing.T) {
	for name, testCase := range map[string]struct {
		raw  string
		kind UserStatusKind
	}{
		"empty": {
			raw:  `{"@type":"UserStatusEmpty"}`,
			kind: UserStatusEmpty,
		},
		"online": {
			raw:  `{"@type":"UserStatusOnline","expires":1800000000}`,
			kind: UserStatusOnline,
		},
		"offline": {
			raw:  `{"@type":"UserStatusOffline","was_online":1799000000}`,
			kind: UserStatusOffline,
		},
		"recently": {
			raw:  `{"@type":"UserStatusRecently","by_my_privacy_settings":true}`,
			kind: UserStatusRecently,
		},
		"last week": {
			raw:  `{"@type":"UserStatusLastWeek","by_my_privacy_settings":true}`,
			kind: UserStatusLastWeek,
		},
		"last month": {
			raw:  `{"@type":"UserStatusLastMonth","by_my_privacy_settings":false}`,
			kind: UserStatusLastMonth,
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := NewLiveState()
			raw := presenceUserStatusUpdate(700, testCase.raw)
			if _, err := state.apply(raw); err != nil {
				t.Fatalf("apply: %v", err)
			}
			status, known := state.userStatus(700)
			if !known {
				t.Fatal("the status is unknown")
			}
			if status.Kind != testCase.kind {
				t.Fatalf("status kind = %v, want %v", status.Kind, testCase.kind)
			}
		})
	}
}

func TestAnUnknownUserStatusConstructorIsIgnored(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceUserUpdate); err != nil {
		t.Fatal(err)
	}
	drainChanged(state)

	raw := presenceUserStatusUpdate(700, `{"@type":"userStatusLevitating"}`)
	changed, err := state.apply(raw)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if changed {
		t.Fatal("a status the schema does not have changed the store")
	}
	status, _ := state.userStatus(700)
	if status.Kind != UserStatusOnline {
		t.Fatalf("status kind = %v, want the one that was known", status.Kind)
	}
}

func TestTheOnlineMemberCountIsKept(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceChatUpdate(10, "chatTypeSupergroup")); err != nil {
		t.Fatal(err)
	}

	raw := RawMessage(`{"@type":"updateChatOnlineMemberCount","chat_id":10,` +
		`"online_member_count":42}`)
	if _, err := state.apply(raw); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := state.onlineMemberCount(10); got != 42 {
		t.Fatalf("onlineMemberCount = %d, want 42", got)
	}
}

// A member count of zero is not "nobody is online": TDLib sends zero when
// it has not counted, and a status line that says "0 online" for a group it
// never counted is a claim it did not check.
func TestAChangedPresenceSignalsAndAnUnchangedOneDoesNot(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceUserUpdate); err != nil {
		t.Fatal(err)
	}
	drainChanged(state)

	raw := presenceUserStatusUpdate(700, `{"@type":"UserStatusOnline","expires":1800000000}`)
	changed, err := state.apply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("the same status reported a change")
	}
	select {
	case <-state.Changed():
		t.Fatal("an unchanged presence must not signal")
	default:
	}

	other := presenceUserStatusUpdate(700, `{"@type":"UserStatusOffline","was_online":1799000000}`)
	if _, err := state.apply(other); err != nil {
		t.Fatal(err)
	}
	select {
	case <-state.Changed():
	default:
		t.Fatal("a changed presence must signal")
	}
}

// ---- ChatPresence ----

func TestThePresenceOfAPrivateChatIsTheStatusOfItsUser(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceChatUpdate(10, "chatTypePrivate")); err != nil {
		t.Fatal(err)
	}
	if _, err := state.apply(presenceUserUpdate); err != nil {
		t.Fatal(err)
	}

	presence := state.ChatPresence(10)
	if presence.Kind != PresenceOneUser {
		t.Fatalf("presence kind = %v, want one user", presence.Kind)
	}
	if presence.UserID != 700 {
		t.Fatalf("user id = %d, want 700", presence.UserID)
	}
	if presence.Status.Kind != UserStatusOnline {
		t.Fatalf("status = %v, want online", presence.Status.Kind)
	}
	if presence.Bot {
		t.Fatal("a regular user was reported as a bot")
	}
}

func TestThePresenceOfAGroupIsItsOnlineCount(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceChatUpdate(10, "chatTypeBasicGroup")); err != nil {
		t.Fatal(err)
	}
	if _, err := state.apply(RawMessage(
		`{"@type":"updateChatOnlineMemberCount","chat_id":10,"online_member_count":3}`,
	)); err != nil {
		t.Fatal(err)
	}

	presence := state.ChatPresence(10)
	if presence.Kind != PresenceGroup {
		t.Fatalf("presence kind = %v, want a group", presence.Kind)
	}
	if presence.OnlineMemberCount != 3 {
		t.Fatalf("online members = %d, want 3", presence.OnlineMemberCount)
	}
	if presence.UserID != 0 {
		t.Fatalf("a group has the user %d", presence.UserID)
	}
}

func TestThePresenceOfAGroupThatWasNotCountedIsUnknown(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(presenceChatUpdate(10, "chatTypeSupergroup")); err != nil {
		t.Fatal(err)
	}

	presence := state.ChatPresence(10)
	if presence.Kind != PresenceGroup {
		t.Fatalf("presence kind = %v, want a group", presence.Kind)
	}
	if presence.OnlineMemberCount != 0 {
		t.Fatalf("online members = %d, want 0", presence.OnlineMemberCount)
	}
}

// A chat the store has never heard of, and a private chat whose user has no
// status yet, both say nothing. There is no word for "we do not know" and
// the interface must not make one up.
func TestAnUnknownPresenceIsNothing(t *testing.T) {
	state := NewLiveState()
	if got := state.ChatPresence(404); got.Kind != PresenceUnknown {
		t.Fatalf("presence kind = %v, want unknown", got.Kind)
	}

	if _, err := state.apply(presenceChatUpdate(10, "chatTypePrivate")); err != nil {
		t.Fatal(err)
	}
	if got := state.ChatPresence(10); got.Kind != PresenceUnknown {
		t.Fatalf("presence kind = %v, want unknown without a status", got.Kind)
	}
}

func TestANilStoreHasNoPresence(t *testing.T) {
	var state *LiveState
	if got := state.ChatPresence(10); got.Kind != PresenceUnknown {
		t.Fatalf("presence kind = %v, want unknown", got.Kind)
	}
	if state.userIsBot(700) {
		t.Fatal("a nil store reported a bot")
	}
}

// The presence of a person is for the screen and for nothing else. A value
// on its way to a log prints the chat and the kind, never the status: the
// status says where somebody is and when they were last there, and that is
// nobody's business but the two of them.
func TestPresencePrintsWithoutTheStatus(t *testing.T) {
	presence := Presence{
		Kind:   PresenceOneUser,
		UserID: 700,
		Status: UserStatus{Kind: UserStatusOnline, Expires: time.Unix(1800000000, 0)},
	}

	// %s and %v of a value with a String method both go through it, so
	// the three verbs below are the three forms a value takes on its way
	// to a log.
	for name, printed := range map[string]string{
		"%v":       presence.String(),
		"%+v":      fmt.Sprintf("%+v", presence),
		"%#v":      fmt.Sprintf("%#v", presence),
		"GoString": presence.GoString(),
	} {
		if strings.Contains(printed, "1800000000") ||
			strings.Contains(printed, "online") {
			t.Fatalf("%s printed the status: %q", name, printed)
		}
	}
}

// TDLib has no user data before the client is authorized, so an updateUser
// cannot arrive while a code is being typed: there is nothing cached to
// report. The first one arrives right after authorization, and the
// interface reads it on its next poll.
//
// Holding these updates would be the other answer, and it would be the
// wrong one twice over: an updateUser is a whole user object with a name
// and a phone number, and the held slice would grow with the number of
// users an account has.
func TestUserUpdatesAreNotHeldDuringAuthorization(t *testing.T) {
	for _, raw := range []RawMessage{
		presenceUserUpdate,
		presenceUserStatusUpdate(700, `{"@type":"UserStatusOnline","expires":1800000000}`),
	} {
		if isLiveStateUpdate(raw) {
			t.Fatalf("%s must not be held during authorization", raw)
		}
	}

	// They are still applied once the session is running.
	state := NewLiveState()
	if _, err := state.apply(presenceUserUpdate); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, known := state.userStatus(700); !known {
		t.Fatal("the status was dropped instead of applied")
	}
}
