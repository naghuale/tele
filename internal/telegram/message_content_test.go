package telegram

import (
	"encoding/json"
	"strings"
	"testing"
)

// The owner read `[unsupported message]` twice in the feed of one chat and
// once in the preview of it, and could not tell what had been sent (01.10,
// #17). Everything below is a TDLib message content of the pinned schema,
// written out the way TDLib writes one, and what the interface has to say
// about it.
//
// The payloads are written here and nowhere else, and none of them is a
// message of a real account: a label is committed to the repository, and a
// committed label outlives the machine it was read on.

// contentLabelCase is one message content and the words for it.
type contentLabelCase struct {
	// name is the content type this case is about, and it is what the
	// completeness test walks the two tables with: a type in either table
	// without a case here is a label nothing proves.
	name    string
	content string
	want    contentLabel
	line    string
}

// mediaLabelCases is every content that carries something, with the detail
// the payload gives it and the caption under it.
var mediaLabelCases = []contentLabelCase{
	{
		name: "messagePhoto",
		content: `{"@type":"messagePhoto","photo":{"@type":"photo","id":"7"},` +
			`"caption":{"@type":"formattedText","text":"at the bridge"}}`,
		want: contentLabel{
			word:    "photo",
			caption: "at the bridge",
		},
		line: "[photo] at the bridge",
	},
	{
		name:    "messageVideo",
		content: `{"@type":"messageVideo","video":{"@type":"video","id":"8"}}`,
		want:    contentLabel{word: "video"},
		line:    "[video]",
	},
	{
		name:    "messageAnimation",
		content: `{"@type":"messageAnimation","animation":{"@type":"animation","id":"9"}}`,
		want:    contentLabel{word: "GIF"},
		line:    "[GIF]",
	},
	{
		name:    "messageAudio",
		content: `{"@type":"messageAudio","audio":{"@type":"audio","id":"10","title":"Voice memo"}}`,
		want:    contentLabel{word: "audio", detail: " Voice memo"},
		line:    "[audio Voice memo]",
	},
	{
		name:    "messageDocument",
		content: `{"@type":"messageDocument","document":{"@type":"document","id":"11","file_name":"счёт.pdf"}}`,
		want:    contentLabel{word: "file", detail: " счёт.pdf"},
		line:    "[file счёт.pdf]",
	},
	{
		name:    "messageSticker",
		content: `{"@type":"messageSticker","sticker":{"@type":"sticker","id":"12","emoji":"😀"}}`,
		want:    contentLabel{word: "sticker", detail: " 😀"},
		line:    "[sticker 😀]",
	},
	{
		name:    "messageVideoNote",
		content: `{"@type":"messageVideoNote","video_note":{"@type":"videoNote","id":"13"}}`,
		want:    contentLabel{word: "video note"},
		line:    "[video note]",
	},
	{
		name:    "messageVoiceNote",
		content: `{"@type":"messageVoiceNote","voice_note":{"@type":"voiceNote","id":"14"}}`,
		want:    contentLabel{word: "voice note"},
		line:    "[voice note]",
	},
	{
		name:    "messageLocation",
		content: `{"@type":"messageLocation","location":{"@type":"location","id":"15"}}`,
		want:    contentLabel{word: "location"},
		line:    "[location]",
	},
	{
		name:    "messageLiveLocation",
		content: `{"@type":"messageLiveLocation","location":{"@type":"liveLocation","id":"16"}}`,
		want:    contentLabel{word: "live location"},
		line:    "[live location]",
	},
	{
		name:    "messageVenue",
		content: `{"@type":"messageVenue","venue":{"@type":"venue","id":"17","title":"Tennis Club"}}`,
		want:    contentLabel{word: "venue", detail: " Tennis Club"},
		line:    "[venue Tennis Club]",
	},
	{
		name: "messageContact",
		content: `{"@type":"messageContact","contact":{"@type":"contact","id":"18",` +
			`"phone_number":"+10000000000","first_name":"Anna","last_name":"Example"}}`,
		want: contentLabel{word: "contact", detail: " Anna Example"},
		line: "[contact Anna Example]",
	},
	{
		name:    "messagePoll",
		content: `{"@type":"messagePoll","poll":{"@type":"poll","id":"19","question":"Friday at 19:00?"}}`,
		want:    contentLabel{word: "poll", detail: ": Friday at 19:00?"},
		line:    "[poll: Friday at 19:00?]",
	},
	{
		name:    "messageDice",
		content: `{"@type":"messageDice","emoji":"🎲","value":4}`,
		want:    contentLabel{word: "dice", detail: " 🎲 4"},
		line:    "[dice 🎲 4]",
	},
	{
		name:    "messageGame",
		content: `{"@type":"messageGame","game":{"@type":"game","id":"20","title":"Tennis"}}`,
		want:    contentLabel{word: "game", detail: " Tennis"},
		line:    "[game Tennis]",
	},
	{
		name:    "messageStory",
		content: `{"@type":"messageStory","story_poster_chat_id":21,"story_id":22}`,
		want:    contentLabel{word: "story"},
		line:    "[story]",
	},
	{
		name:    "messagePaidMedia",
		content: `{"@type":"messagePaidMedia","star_count":5,"media":[]}`,
		want:    contentLabel{word: "paid media"},
		line:    "[paid media]",
	},
	{
		name:    "messageExpiredPhoto",
		content: `{"@type":"messageExpiredPhoto"}`,
		want:    contentLabel{word: "expired photo"},
		line:    "[expired photo]",
	},
	{
		name:    "messageExpiredVideo",
		content: `{"@type":"messageExpiredVideo"}`,
		want:    contentLabel{word: "expired video"},
		line:    "[expired video]",
	},
	{
		name:    "messageExpiredVideoNote",
		content: `{"@type":"messageExpiredVideoNote"}`,
		want:    contentLabel{word: "expired video note"},
		line:    "[expired video note]",
	},
	{
		name:    "messageExpiredVoiceNote",
		content: `{"@type":"messageExpiredVoiceNote"}`,
		want:    contentLabel{word: "expired voice note"},
		line:    "[expired voice note]",
	},
	{
		name:    "messageCall",
		content: `{"@type":"messageCall","unique_id":23,"duration":0}`,
		want:    contentLabel{word: "call"},
		line:    "[call]",
	},
	{
		name:    "messageChecklist",
		content: `{"@type":"messageChecklist","list":{"@type":"checklist","id":24}}`,
		want:    contentLabel{word: "checklist"},
		line:    "[checklist]",
	},
	{
		name:    "messageGiveaway",
		content: `{"@type":"messageGiveaway","winner_count":1}`,
		want:    contentLabel{word: "giveaway"},
		line:    "[giveaway]",
	},
	{
		name:    "messageGift",
		content: `{"@type":"messageGift","gift":{"@type":"gift","id":"25"}}`,
		want:    contentLabel{word: "gift"},
		line:    "[gift]",
	},
	{
		name:    "messageInvoice",
		content: `{"@type":"messageInvoice","currency":"USD","total_amount":100}`,
		want:    contentLabel{word: "invoice"},
		line:    "[invoice]",
	},
}

// serviceLabelCases is every service message the table names, one case for
// each @type, with the phrase written out here rather than read out of the
// table: a test that compared the table with itself would prove nothing.
//
// A service message is something that happened in the chat rather than
// something somebody wrote, and the interface draws the phrase as a row of
// the feed of its own.
var serviceLabelCases = []contentLabelCase{
	{
		name:    "messageBasicGroupChatCreate",
		content: `{"@type":"messageBasicGroupChatCreate","title":"Tennis"}`,
		want:    contentLabel{service: "created the group"},
		line:    "created the group",
	},
	{
		name:    "messageSupergroupChatCreate",
		content: `{"@type":"messageSupergroupChatCreate","title":"Tennis"}`,
		want:    contentLabel{service: "created the group"},
		line:    "created the group",
	},
	{
		name:    "messageChatUpgradeTo",
		content: `{"@type":"messageChatUpgradeTo","supergroup_id":1}`,
		want:    contentLabel{service: "upgraded the group"},
		line:    "upgraded the group",
	},
	{
		name:    "messageChatUpgradeFrom",
		content: `{"@type":"messageChatUpgradeFrom","title":"Tennis","basic_group_id":2}`,
		want:    contentLabel{service: "upgraded the group"},
		line:    "upgraded the group",
	},
	{
		name:    "messageChatChangeTitle",
		content: `{"@type":"messageChatChangeTitle","title":"Большой теннис"}`,
		want:    contentLabel{service: "renamed the chat to Большой теннис"},
		line:    "renamed the chat to Большой теннис",
	},
	{
		name:    "messageChatChangePhoto",
		content: `{"@type":"messageChatChangePhoto","photo":{"@type":"chatPhoto"}}`,
		want:    contentLabel{service: "changed the chat photo"},
		line:    "changed the chat photo",
	},
	{
		name:    "messageChatDeletePhoto",
		content: `{"@type":"messageChatDeletePhoto"}`,
		want:    contentLabel{service: "deleted the chat photo"},
		line:    "deleted the chat photo",
	},
	{
		name:    "messageChatOwnerLeft",
		content: `{"@type":"messageChatOwnerLeft","new_owner_user_id":0}`,
		want:    contentLabel{service: "left the chat"},
		line:    "left the chat",
	},
	{
		name:    "messageChatOwnerChanged",
		content: `{"@type":"messageChatOwnerChanged","new_owner_user_id":3}`,
		want:    contentLabel{service: "is now the owner of the chat"},
		line:    "is now the owner of the chat",
	},
	{
		name: "messageChatHasProtectedContentToggled",
		content: `{"@type":"messageChatHasProtectedContentToggled","old_has_protected_content":false,` +
			`"new_has_protected_content":true}`,
		want: contentLabel{service: "changed the protection of the chat"},
		line: "changed the protection of the chat",
	},
	{
		name:    "messageChatJoinByLink",
		content: `{"@type":"messageChatJoinByLink"}`,
		want:    contentLabel{service: "joined the chat by a link"},
		line:    "joined the chat by a link",
	},
	{
		name:    "messageChatJoinByRequest",
		content: `{"@type":"messageChatJoinByRequest"}`,
		want:    contentLabel{service: "joined the chat"},
		line:    "joined the chat",
	},
	{
		name:    "messageChatJoinFromCommunity",
		content: `{"@type":"messageChatJoinFromCommunity","community_id":4}`,
		want:    contentLabel{service: "joined the chat from a community"},
		line:    "joined the chat from a community",
	},
	{
		name:    "messageChatDeleteMember",
		content: `{"@type":"messageChatDeleteMember","user_id":5}`,
		want:    contentLabel{service: "removed a member"},
		line:    "removed a member",
	},
	{
		name:    "messageChatAddedToCommunity",
		content: `{"@type":"messageChatAddedToCommunity","community_id":6}`,
		want:    contentLabel{service: "added the chat to a community"},
		line:    "added the chat to a community",
	},
	{
		name:    "messageChatRemovedFromCommunity",
		content: `{"@type":"messageChatRemovedFromCommunity"}`,
		want:    contentLabel{service: "removed the chat from a community"},
		line:    "removed the chat from a community",
	},
	{
		name:    "messagePinMessage",
		content: `{"@type":"messagePinMessage","message_id":26}`,
		want:    contentLabel{service: "pinned a message"},
		line:    "pinned a message",
	},
	{
		name:    "messageScreenshotTaken",
		content: `{"@type":"messageScreenshotTaken"}`,
		want:    contentLabel{service: "took a screenshot"},
		line:    "took a screenshot",
	},
	{
		name:    "messageChatSetBackground",
		content: `{"@type":"messageChatSetBackground","only_for_self":true}`,
		want:    contentLabel{service: "changed the background"},
		line:    "changed the background",
	},
	{
		name:    "messageChatSetTheme",
		content: `{"@type":"messageChatSetTheme","theme":null}`,
		want:    contentLabel{service: "changed the theme of the chat"},
		line:    "changed the theme of the chat",
	},
	{
		name:    "messageChatSetMessageAutoDeleteTime",
		content: `{"@type":"messageChatSetMessageAutoDeleteTime","message_auto_delete_time":86400}`,
		want:    contentLabel{service: "changed the timer of the messages"},
		line:    "changed the timer of the messages",
	},
	{
		name:    "messageChatBoost",
		content: `{"@type":"messageChatBoost","boost_count":1}`,
		want:    contentLabel{service: "boosted the chat"},
		line:    "boosted the chat",
	},
	{
		name:    "messageForumTopicCreated",
		content: `{"@type":"messageForumTopicCreated","name":"Court 1"}`,
		want:    contentLabel{service: "created the topic Court 1"},
		line:    "created the topic Court 1",
	},
	{
		name:    "messageForumTopicEdited",
		content: `{"@type":"messageForumTopicEdited","name":"Court 2"}`,
		want:    contentLabel{service: "edited the topic"},
		line:    "edited the topic",
	},
	{
		name:    "messageForumTopicIsClosedToggled",
		content: `{"@type":"messageForumTopicIsClosedToggled","is_closed":true}`,
		want:    contentLabel{service: "reopened the topic"},
		line:    "reopened the topic",
	},
	{
		name:    "messageForumTopicIsHiddenToggled",
		content: `{"@type":"messageForumTopicIsHiddenToggled","is_hidden":false}`,
		want:    contentLabel{service: "hidden the topic"},
		line:    "hidden the topic",
	},
	{
		name:    "messageVideoChatScheduled",
		content: `{"@type":"messageVideoChatScheduled","group_call_id":7,"start_date":8}`,
		want:    contentLabel{service: "scheduled a video chat"},
		line:    "scheduled a video chat",
	},
	{
		name:    "messageVideoChatStarted",
		content: `{"@type":"messageVideoChatStarted","group_call_id":9}`,
		want:    contentLabel{service: "started a video chat"},
		line:    "started a video chat",
	},
	{
		name:    "messageVideoChatEnded",
		content: `{"@type":"messageVideoChatEnded","duration":600}`,
		want:    contentLabel{service: "ended the video chat"},
		line:    "ended the video chat",
	},
	{
		name:    "messagePollOptionAdded",
		content: `{"@type":"messagePollOptionAdded","poll_message_id":10,"option_id":"a"}`,
		want:    contentLabel{service: "added an option to the poll"},
		line:    "added an option to the poll",
	},
	{
		name:    "messagePollOptionDeleted",
		content: `{"@type":"messagePollOptionDeleted","poll_message_id":11,"option_id":"a"}`,
		want:    contentLabel{service: "deleted an option of the poll"},
		line:    "deleted an option of the poll",
	},
	{
		name:    "messageGameScore",
		content: `{"@type":"messageGameScore","game_message_id":12,"game_id":13,"score":7}`,
		want:    contentLabel{service: "played a game"},
		line:    "played a game",
	},
	{
		name:    "messageGiveawayCreated",
		content: `{"@type":"messageGiveawayCreated","star_count":14}`,
		want:    contentLabel{service: "created a giveaway"},
		line:    "created a giveaway",
	},
	{
		name:    "messageGiveawayCompleted",
		content: `{"@type":"messageGiveawayCompleted","winner_count":1}`,
		want:    contentLabel{service: "ended the giveaway"},
		line:    "ended the giveaway",
	},
	{
		name:    "messageGiveawayWinners",
		content: `{"@type":"messageGiveawayWinners","winner_count":2}`,
		want:    contentLabel{service: "chose the winners of the giveaway"},
		line:    "chose the winners of the giveaway",
	},
	{
		name:    "messageContactRegistered",
		content: `{"@type":"messageContactRegistered"}`,
		want:    contentLabel{service: "added a contact"},
		line:    "added a contact",
	},
	{
		name:    "messageChatAddMembers",
		content: `{"@type":"messageChatAddMembers","member_user_ids":[28,29,30]}`,
		want:    contentLabel{service: "added 3 members"},
		line:    "added 3 members",
	},
	{
		name:    "messageCustomServiceAction",
		content: `{"@type":"messageCustomServiceAction","text":"The owner turned the timer on"}`,
		want:    contentLabel{service: "The owner turned the timer on"},
		line:    "The owner turned the timer on",
	},
}

// Every type of both tables is read here, and every case is a payload this
// build can be handed by Telegram.
//
// The completeness test below is what makes the table and these cases one
// thing: a type that is added to the table without a case here would be a
// label no test proves, and a label nothing proves is a label nobody reads.
func TestAContentLabelSaysWhatTheMessageCarries(t *testing.T) {
	for _, testCase := range allContentLabelCases() {
		t.Run(testCase.name, func(t *testing.T) {
			got := readContentLabel(json.RawMessage(testCase.content))
			if got != testCase.want {
				t.Errorf(
					"readContentLabel(%s) = %+v, want %+v",
					testCase.name, got, testCase.want,
				)
			}
			if line := got.line(); line != testCase.line {
				t.Errorf(
					"the line of %s is %q, want %q",
					testCase.name, line, testCase.line,
				)
			}
		})
	}
}

func allContentLabelCases() []contentLabelCase {
	cases := make([]contentLabelCase, 0, len(mediaLabelCases)+len(serviceLabelCases))
	cases = append(cases, mediaLabelCases...)
	cases = append(cases, serviceLabelCases...)

	return cases
}

// Every name in either table is proven here, in both directions: a name the
// tables hold with no case is a label nothing proves, and a case about a
// name neither table holds is a case of a type that has been renamed or
// dropped, which is a silent change of what the screen says.
func TestEveryTypeOfTheLabelTableIsProvenByACase(t *testing.T) {
	proven := make(map[string]bool, len(mediaLabelCases)+len(serviceLabelCases))
	for _, testCase := range allContentLabelCases() {
		if proven[testCase.name] {
			t.Errorf("%s has two cases: one of them is not read", testCase.name)
		}
		proven[testCase.name] = true
	}

	for contentType := range contentWords {
		if !proven[contentType] {
			t.Errorf("contentWords has %q and no case proves it", contentType)
		}
	}
	for contentType := range servicePhrases {
		if !proven[contentType] {
			t.Errorf("servicePhrases has %q and no case proves it", contentType)
		}
	}
	for _, testCase := range allContentLabelCases() {
		if contentWords[testCase.name] == "" &&
			servicePhrase(testCase.name, json.RawMessage(testCase.content)) == "" {
			t.Errorf(
				"%q has a case and no label to be drawn by: the case is "+
					"about a type this build does not name",
				testCase.name,
			)
		}
	}
}

// A message about added members says how many joined, and one that added
// nobody says the smallest sentence that is still one.
//
// The count is a vector of identifiers in the schema and not a number
// (td_api.tl:5346), so it has to be counted here. "added a member" in two
// rows of a feed is a sentence a reader cannot tell one row from the other.
func TestAMessageAboutAddedMembersSaysHowManyJoined(t *testing.T) {
	for _, count := range []struct {
		ids  string
		want string
	}{
		{ids: `[9]`, want: "added a member"},
		{ids: `[9,10]`, want: "added 2 members"},
		{ids: `[9,10,11]`, want: "added 3 members"},
	} {
		content := `{"@type":"messageChatAddMembers","member_user_ids":` + count.ids + `}`

		got := readContentLabel(json.RawMessage(content))
		if got.service != count.want {
			t.Errorf("readContentLabel(%s) says %q, want %q",
				count.ids, got.service, count.want)
		}
	}
}

// A content this build has no words for is drawn by the name TDLib gave it.
//
// That is the whole of what can be said about a message the program knows
// nothing else of, and it is the row that has to be addable: the name on the
// screen is the name to look for in the schema. "[unsupported message]" said
// nothing about the message at all, and the owner could not tell what a
// chat had sent them (01.10, #17).
func TestAContentWithNoWordsIsDrawnByItsOwnType(t *testing.T) {
	for _, contentType := range []string{
		"messageUnsupported",
		"messageSomethingNewerThanThisBuild",
		"messageRichMessage",
	} {
		content := `{"@type":"` + contentType + `"}`

		got := readContentLabel(json.RawMessage(content))
		if got.word != contentType {
			t.Errorf(
				"readContentLabel(%s) has the word %q, want the type itself",
				contentType, got.word,
			)
		}
		if line := got.line(); line != "["+contentType+"]" {
			t.Errorf("the line of %s is %q", contentType, line)
		}
	}
}

// A text message is its own words and has no label, and an animated emoji
// is a message of one emoji rather than a sticker with an emoji in it.
//
// A block that said "[messageText]" above the sentence somebody wrote would
// say it twice, and "[sticker 🎂]" for an animated emoji would be a sticker
// it is not: the emoji is the whole of that message and nothing is attached
// to it.
func TestAMessageOfWordsHasNoLabel(t *testing.T) {
	text := readContentLabel(json.RawMessage(
		`{"@type":"messageText","text":{"@type":"formattedText","text":"at the bridge"}}`,
	))
	if (text != contentLabel{}) {
		t.Errorf("a text message reads as %+v, want nothing", text)
	}
	if line := text.line(); line != "" {
		t.Errorf("the line of a text message is %q, want no label", line)
	}
	if got := extractMessageText(json.RawMessage(
		`{"@type":"messageText","text":{"@type":"formattedText","text":"at the bridge"}}`,
	)); got != "at the bridge" {
		t.Errorf("the text of a text message is %q", got)
	}

	emoji := readContentLabel(json.RawMessage(
		`{"@type":"messageAnimatedEmoji","emoji":"🎂"}`,
	))
	if (emoji != contentLabel{}) {
		t.Errorf("an animated emoji reads as %+v, want nothing", emoji)
	}
	if got := extractMessageText(json.RawMessage(
		`{"@type":"messageAnimatedEmoji","emoji":"🎂"}`,
	)); got != "🎂" {
		t.Errorf("an animated emoji reads as %q, want the emoji", got)
	}
}

// A text message with nothing in it is named by its type, like any other.
//
// The one case of a message with no words and nothing to label is a text
// message with no text in it — a link preview and an empty string — and a
// block with nothing in it is a block a user cannot tell from a message that
// failed to load.
func TestATextMessageWithNoWordsIsNamedByItsType(t *testing.T) {
	got := readContentLabel(json.RawMessage(
		`{"@type":"messageText","text":{"@type":"formattedText","text":""}}`,
	))
	if got.word != "messageText" {
		t.Errorf("an empty text message reads as %+v, want the type", got)
	}
}

// A payload with nothing in the field the label reads is a label without its
// detail and not a label with a hole in it.
//
// An audio with no title is "[audio]" and a sticker out of a set with no
// emoji is "[sticker]": a name and an empty space where the rest of the
// label should be is a sentence about a thing that is not there.
func TestAMediaWithNothingToSaySaysOnlyItsWord(t *testing.T) {
	for _, testCase := range []contentLabelCase{
		{
			name:    "audio without a title",
			content: `{"@type":"messageAudio","audio":{"@type":"audio","id":"9"}}`,
			want:    contentLabel{word: "audio"},
			line:    "[audio]",
		},
		{
			name:    "sticker without an emoji",
			content: `{"@type":"messageSticker","sticker":{"@type":"sticker","id":"9"}}`,
			want:    contentLabel{word: "sticker"},
			line:    "[sticker]",
		},
		{
			name:    "poll without a question",
			content: `{"@type":"messagePoll","poll":{"@type":"poll","id":"9"}}`,
			want:    contentLabel{word: "poll"},
			line:    "[poll]",
		},
		{
			name:    "contact with a number and no name",
			content: `{"@type":"messageContact","contact":{"@type":"contact","phone_number":"+10000000000"}}`,
			want:    contentLabel{word: "contact", detail: " +10000000000"},
			line:    "[contact +10000000000]",
		},
		{
			name:    "dice that has not settled yet",
			content: `{"@type":"messageDice","emoji":"🎲","value":0}`,
			want:    contentLabel{word: "dice", detail: " 🎲"},
			line:    "[dice 🎲]",
		},
		{
			name:    "one member added",
			content: `{"@type":"messageChatAddMembers","member_user_ids":[9]}`,
			want:    contentLabel{service: "added a member"},
			line:    "added a member",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := readContentLabel(json.RawMessage(testCase.content))
			if got != testCase.want {
				t.Errorf("readContentLabel = %+v, want %+v", got, testCase.want)
			}
			if line := got.line(); line != testCase.line {
				t.Errorf("the line is %q, want %q", line, testCase.line)
			}
		})
	}
}

// A content that is not an object at all has no label and no line, and it
// takes the rest of the page with it in neither: a message whose content
// this build cannot read is still a message with a place in the order of the
// conversation (history.go).
func TestAReadThatFailsHasNoLabel(t *testing.T) {
	for _, content := range []string{`null`, `[]`, `{"@type":42}`, `"messagePhoto"`} {
		got := readContentLabel(json.RawMessage(content))
		if (got != contentLabel{}) {
			t.Errorf("readContentLabel(%s) = %+v, want nothing", content, got)
		}
	}
}

// The two tables name constructors of the pinned schema and not names of
// this build's own making.
//
// A name TDLib does not have is a label no message can ever carry, and a
// dead row of a table is a row nobody finds until a user reports the empty
// block it was supposed to fill. The file is the schema of the version this
// build loads, so the question is asked of the file and not remembered.
func TestEveryTypeTheTableNamesIsAMessageContentOfThePinnedSchema(t *testing.T) {
	constructors := messageContentConstructors(t)

	named := map[string]bool{}
	for contentType := range contentWords {
		named[contentType] = true
	}
	for contentType := range servicePhrases {
		named[contentType] = true
	}
	for contentType := range messageTexts {
		named[contentType] = true
	}

	for contentType := range named {
		if !constructors[contentType] {
			t.Errorf(
				"%q is not a messageContent of %s",
				contentType, pinnedSchema,
			)
		}
	}
}

// messageContentConstructors returns every constructor of the pinned schema
// that is a messageContent.
func messageContentConstructors(t *testing.T) map[string]bool {
	t.Helper()

	constructors := map[string]bool{}
	for _, line := range readSchemaLines(t) {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, "= MessageContent;") {
			continue
		}
		name, _, _ := strings.Cut(line, " ")
		constructors[strings.TrimSpace(name)] = true
	}

	if len(constructors) < 100 {
		t.Fatalf(
			"%s has %d messageContent constructors, which is far fewer "+
				"than a TL scheme of TDLib 1.8.67 has: is it the whole file?",
			pinnedSchema, len(constructors),
		)
	}

	return constructors
}
