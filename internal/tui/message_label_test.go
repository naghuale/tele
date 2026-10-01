package tui

import (
	"strings"
	"testing"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// What a message carries, in the words the screen draws it in.
//
// The projection names every content type there is — down to the ones this
// build has no words for yet, which are drawn by the @type TDLib gave them
// so that the type can be added (#17). What the screen does with the three
// fields is here: the word in brackets, whatever the payload said about it
// inside them, and the caption after them.

// The labels below are the same strings the adapter sends (internal/telegram,
// message_content.go), written out again here because the TUI may not import
// that package (internal/archdeps). A label that is spelled one way in one of
// the two and another way in the other is a label a user reads in the chat
// list and cannot find in the feed, so the pairs are written side by side.
func TestAMessageSaysWhatItCarries(t *testing.T) {
	for _, label := range []struct {
		name    string
		message Message
		want    string
	}{
		{
			name:    "a sticker with its emoji",
			message: Message{Media: "sticker", MediaDetail: " 😀"},
			want:    "[sticker 😀]",
		},
		{
			name:    "an animation",
			message: Message{Media: "GIF"},
			want:    "[GIF]",
		},
		{
			name:    "a video note",
			message: Message{Media: "video note"},
			want:    "[video note]",
		},
		{
			name:    "a file with its name",
			message: Message{Media: "file", MediaDetail: " счёт.pdf"},
			want:    "[file счёт.pdf]",
		},
		{
			name:    "an audio with its title",
			message: Message{Media: "audio", MediaDetail: " Voice memo"},
			want:    "[audio Voice memo]",
		},
		{
			name:    "a poll with its question",
			message: Message{Media: "poll", MediaDetail: ": Friday at 19:00?"},
			want:    "[poll: Friday at 19:00?]",
		},
		{
			name:    "a location",
			message: Message{Media: "location"},
			want:    "[location]",
		},
		{
			name:    "a venue with its name",
			message: Message{Media: "venue", MediaDetail: " Tennis Club"},
			want:    "[venue Tennis Club]",
		},
		{
			name:    "a contact with their name",
			message: Message{Media: "contact", MediaDetail: " Anna Example"},
			want:    "[contact Anna Example]",
		},
		{
			name:    "a dice with its number",
			message: Message{Media: "dice", MediaDetail: " 🎲 4"},
			want:    "[dice 🎲 4]",
		},
		{
			name:    "a game with its title",
			message: Message{Media: "game", MediaDetail: " Tennis"},
			want:    "[game Tennis]",
		},
		{
			name:    "a story",
			message: Message{Media: "story"},
			want:    "[story]",
		},
		{
			name:    "a photo with its caption",
			message: Message{Media: "photo", Caption: "at the bridge"},
			want:    "[photo] at the bridge",
		},
		{
			name:    "a file with its name and its caption",
			message: Message{Media: "file", MediaDetail: " счёт.pdf", Caption: "February"},
			want:    "[file счёт.pdf] February",
		},
		{
			name:    "words of a text message",
			message: Message{Text: "at the bridge"},
			want:    "at the bridge",
		},
		{
			name:    "an animated emoji is the message",
			message: Message{Text: "🎂"},
			want:    "🎂",
		},
		{
			name:    "a message with a label and words of its own",
			message: Message{Media: "photo", Caption: "the deck", Text: "the final"},
			want:    "[photo] the deck the final",
		},
		{
			name:    "a content this build has no words for",
			message: Message{Media: "messageUnsupported"},
			want:    "[messageUnsupported]",
		},
		{
			name:    "a type that does not exist yet",
			message: Message{Media: "messageSomethingNewerThanThisBuild"},
			want:    "[messageSomethingNewerThanThisBuild]",
		},
	} {
		t.Run(label.name, func(t *testing.T) {
			entry := timelineEntry{message: label.message, first: 0, last: 0}
			if got := entryText(entry); got != label.want {
				t.Errorf("the message reads %q, want %q", got, label.want)
			}
		})
	}
}

// A label of somebody else's making is drawn as it was named and not as a
// file.
//
// The projection names every content type, and a screen that renamed
// "[dice 🎲 4]" to "[file]" because it only knows the six words of an album
// would take away the one thing the label is for.
func TestALabelTheScreenHasNoWordForIsNotRenamed(t *testing.T) {
	for _, media := range []string{
		"GIF", "poll", "dice", "story", "venue", "location", "contact",
		"messageUnsupported", "messageSomethingNewerThanThisBuild",
	} {
		entry := timelineEntry{
			message: Message{Media: media},
			first:   0,
			last:    0,
		}
		if got := entryText(entry); got != "["+media+"]" {
			t.Errorf("the message with the word %q reads %q", media, got)
		}
	}
}

// An album is one entry and one word with a count on it, and the detail of
// its first part is left out.
//
// Three files are one thing somebody did, and the name of one of the three
// is the name of a file the user cannot open: "[3 files счёт.pdf]" is a
// sentence about one of them.
func TestAnAlbumKeepsTheCountAndDropsTheNameOfOnePart(t *testing.T) {
	messages := []Message{
		{
			ID: 1, Media: "file", MediaDetail: " счёт.pdf", AlbumID: 5,
			Time: "10:00", Author: "Anna", AuthorID: 7,
		},
		{
			ID: 2, Media: "file", MediaDetail: " договор.pdf", AlbumID: 5,
			Time: "10:00", Author: "Anna", AuthorID: 7,
		},
		{
			ID: 3, Media: "file", MediaDetail: " акт.pdf", AlbumID: 5,
			Time: "10:00", Author: "Anna", AuthorID: 7,
		},
	}

	entries := timelineEntries(messages)
	if got := entryText(entries[0]); got != "[3 files]" {
		t.Errorf("the album reads %q, want %q", got, "[3 files]")
	}
}

// An album of animations keeps the word the feed uses for one of them, and
// the count with it.
func TestAnAlbumOfAnimationsIsOneGIFEntry(t *testing.T) {
	messages := []Message{
		{ID: 1, Media: "GIF", AlbumID: 5, Time: "10:00", Author: "Anna"},
		{ID: 2, Media: "GIF", AlbumID: 5, Time: "10:00", Author: "Anna"},
	}

	entries := timelineEntries(messages)
	if got := entryText(entries[0]); got != "[2 GIFs]" {
		t.Errorf("the album reads %q, want %q", got, "[2 GIFs]")
	}
}

// What happened in the chat is a phrase and not a label, and there is
// nothing to put around it.
func TestAServiceMessageIsItsPhraseAndNothingElse(t *testing.T) {
	entry := timelineEntry{
		message: Message{Service: "joined the chat by a link", Time: "11:00"},
		first:   0,
		last:    0,
	}
	if got := entryText(entry); got != "joined the chat by a link" {
		t.Errorf("the service message reads %q", got)
	}

	// A service message with a caption and a word of its own is still a
	// service message: the words of the chat about the chat would say
	// nothing the phrase does not.
	withMore := timelineEntry{
		message: Message{
			Service: "pinned a message", Media: "photo",
			MediaDetail: " 😀", Caption: "at the bridge", Text: "the one",
		},
		first: 0,
		last:  0,
	}
	if got := entryText(withMore); got != "pinned a message" {
		t.Errorf("a service message with more in it reads %q", got)
	}
}

// A service message is a row of the feed of its own: no block, no name above
// it, and the words in the middle of the feed.
//
// A block would say that somebody said it, and nobody did — the sender of a
// service message is the chat, and the chat is named at the top of the
// screen. The screen is a narrow one, so that the feed is the whole of it and
// the air around the phrase is the air of the feed.
func TestAServiceMessageIsARowOfItsOwnInTheMiddle(t *testing.T) {
	model := openedProgramModel(t, theme.ProfileNoColor, 60, 20)
	model.focus = FocusHistory
	model.chats[0].Messages = []Message{
		{ID: 1, Author: "Anna Example", Text: "Hello", Time: "10:00"},
		{ID: 2, Author: "Anna Example", Time: "10:01", Service: "joined the chat"},
	}

	row, _, found := lineWith(plain(model.View()), "joined the chat")
	if !found {
		t.Fatalf("the feed does not say what happened in the chat:\n%s",
			plain(model.View()))
	}

	// The phrase stands in the middle of the row: there is air on both
	// sides of it, and it is against neither edge of the feed.
	before, after, _ := strings.Cut(row, "joined the chat")
	if !strings.HasPrefix(before, " ") || !strings.HasSuffix(after, " ") {
		t.Errorf("the phrase is against an edge of the feed: %q", row)
	}
	if strings.Contains(row, "Anna Example") {
		t.Errorf("a service message is signed with a name: %q", row)
	}
	if strings.Contains(row, "10:01") {
		t.Errorf("a service message is dated like a message: %q", row)
	}
}

// The rows of the screen stay a rectangle with a service message in them.
//
// A row one column out is a row the terminal was not told to write over,
// which is what the first defect of the screen was about.
func TestAServiceMessageKeepsTheRowsOfTheScreenARectangle(t *testing.T) {
	model := ownerModel(t, termwidth.ModeCodepoint)
	model.screen = ScreenConversation
	model.focus = FocusComposer
	model.chats[0].Messages = []Message{
		{ID: 1, Author: "Дмитрий С", Text: "Привет", Time: "12:00"},
		{ID: 2, Author: "Дмитрий С", Time: "12:01", Service: "joined the chat"},
		{
			ID: 3, Author: "Дмитрий С", Time: "12:02",
			Service: "renamed the chat to Большой теннис",
		}, {ID: 4, Outgoing: true, Text: "вижу", Time: "12:03"},
	}

	lines := viewLines(model.View())
	if len(lines) != ownerHeight {
		t.Fatalf("the frame is %d rows, want %d", len(lines), ownerHeight)
	}
	for index, line := range lines {
		if got := model.widths.StringWidth(line); got != ownerWidth {
			t.Errorf("row %d is %d columns, want %d", index, got, ownerWidth)
		}
	}
}
