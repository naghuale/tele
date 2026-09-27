package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
)

// This file is the text of the composer and the cursor in it.
//
// The text is a run of runes and the cursor is an index into that run, so
// a cursor in the middle of a word is a position and not a flag. The keys
// move over grapheme clusters and not over code points: a ZWJ emoji is one
// thing to the person reading it and seven code points to a buffer, and a
// Backspace that leaves half a family behind is a bug everybody sees
// immediately.
//
// The buffer has no opinion about how the text is drawn. A draft is one
// run of runes with newlines in it; the rows it takes on the screen are
// worked out by the view from the width of the pane, and nothing is ever
// inserted into the text to make it fit. What a user types is what goes to
// the queue, byte for byte, and that is the property the whole design is
// here to keep.

// graphemes splits text into the clusters a reader sees.
func graphemes(text []rune) [][]rune {
	if len(text) == 0 {
		return nil
	}

	clusters := uniseg.NewGraphemes(string(text))
	out := make([][]rune, 0, len(text))
	for clusters.Next() {
		out = append(out, []rune(clusters.Str()))
	}

	return out
}

// graphemeStart returns the index of the cluster a cursor belongs to,
// which is the cluster a Backspace removes.
//
// A cursor sits after the cluster it belongs to, so the cluster that
// reaches the cursor is the one before it, and a cursor at the very end
// belongs to the last cluster of the text. A cursor between two clusters
// belongs to the one before: the text after the cursor belongs to the next
// deletion.
func graphemeStart(text []rune, cursor int) int {
	if cursor <= 0 {
		return 0
	}

	cursor = clampIndex(cursor, len(text))
	offset := 0

	for offset < cursor {
		size := firstClusterSize(text[offset:])
		if offset+size >= cursor {
			return offset
		}
		offset += size
	}

	return cursor
}

// firstClusterSize returns how many runes the first grapheme cluster of
// text takes.
//
// The count is in runes and not in bytes. The buffer is a run of runes and
// every index into it is a rune index, so a byte count would put the cursor
// in the middle of a Cyrillic letter — and a letter cut in half is a
// letter the terminal cannot draw.
func firstClusterSize(text []rune) int {
	cluster, _, _, _ := uniseg.FirstGraphemeClusterInString(string(text), -1)

	return len([]rune(cluster))
}

// graphemeEnd returns the index just past the cluster the cursor is at the
// start of, which is the cluster a Delete removes.
func graphemeEnd(text []rune, cursor int) int {
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(text) {
		return len(text)
	}

	for offset := 0; offset < len(text); {
		size := firstClusterSize(text[offset:])

		if offset == cursor {
			return offset + size
		}
		offset += size
	}

	return cursor + 1
}

// insertText puts insert into text at the cursor and returns where the
// cursor ends up.
func insertText(text []rune, cursor int, insert []rune) ([]rune, int) {
	cursor = clampIndex(cursor, len(text))
	if len(insert) == 0 {
		return text, cursor
	}

	result := make([]rune, 0, len(text)+len(insert))
	result = append(result, text[:cursor]...)
	result = append(result, insert...)
	result = append(result, text[cursor:]...)

	return result, cursor + len(insert)
}

// deleteBefore removes the cluster before the cursor.
func deleteBefore(text []rune, cursor int) ([]rune, int) {
	cursor = clampIndex(cursor, len(text))
	if cursor == 0 {
		return text, 0
	}

	start := graphemeStart(text, cursor)

	return append(append([]rune{}, text[:start]...), text[cursor:]...), start
}

// deleteAfter removes the cluster at the cursor.
func deleteAfter(text []rune, cursor int) ([]rune, int) {
	cursor = clampIndex(cursor, len(text))
	if cursor >= len(text) {
		return text, cursor
	}

	end := graphemeEnd(text, cursor)

	return append(append([]rune{}, text[:cursor]...), text[end:]...), cursor
}

// deleteWordBefore removes the word before the cursor, which is what
// Ctrl+W does in readline.
//
// A word is a run of non-space runes, and the spaces in front of it go with
// it: readline deletes the word and the gap before it, so that deleting
// twice leaves "one two" from "one two three" rather than "one twothree".
func deleteWordBefore(text []rune, cursor int) ([]rune, int) {
	cursor = clampIndex(cursor, len(text))

	start := cursor
	if cursor > 0 && !isSpaceRune(text[cursor-1]) {
		// A word sits right in front of the cursor, so the word goes and
		// the gap in front of it goes with it: deleting twice leaves
		// "one two" out of "one two three" and not "one twothree".
		for start > 0 && !isSpaceRune(text[start-1]) {
			start--
		}
		for start > 0 && isSpaceRune(text[start-1]) {
			start--
		}
	} else {
		// The cursor is after a gap, and the gap is what goes. A trailing
		// space in a draft is a slip, not a word.
		for start > 0 && isSpaceRune(text[start-1]) {
			start--
		}
	}

	return append(append([]rune{}, text[:start]...), text[cursor:]...), start
}

// clearToCursor removes everything from the start of the line to the cursor,
// which is what Ctrl+U does in readline.
func clearToCursor(text []rune, cursor int) ([]rune, int) {
	cursor = clampIndex(cursor, len(text))

	start := lineStart(text, cursor)
	if start == cursor {
		return text, cursor
	}

	return append(append([]rune{}, text[:start]...), text[cursor:]...), start
}

// clearLine removes the line the cursor is on and the newline that ended
// it, which is what Ctrl+K does in readline.
func clearLine(text []rune, cursor int) ([]rune, int) {
	cursor = clampIndex(cursor, len(text))

	end := lineEnd(text, cursor)
	if end < len(text) {
		end++
	}

	return append(append([]rune{}, text[:cursor]...), text[end:]...), cursor
}

// lineStart returns the index of the first rune of the line the cursor is
// on.
func lineStart(text []rune, cursor int) int {
	cursor = clampIndex(cursor, len(text))

	for cursor > 0 && text[cursor-1] != '\n' {
		cursor--
	}

	return cursor
}

// lineEnd returns the index just past the last rune of the line the cursor
// is on, not counting the newline that ends it.
func lineEnd(text []rune, cursor int) int {
	cursor = clampIndex(cursor, len(text))

	for cursor < len(text) && text[cursor] != '\n' {
		cursor++
	}

	return cursor
}

// moveLeft moves the cursor one cluster towards the start of the text.
func moveLeft(text []rune, cursor int) int {
	cursor = clampIndex(cursor, len(text))
	if cursor == 0 {
		return 0
	}

	return graphemeStart(text, cursor)
}

// moveRight moves the cursor one cluster towards the end of the text.
func moveRight(text []rune, cursor int) int {
	cursor = clampIndex(cursor, len(text))
	if cursor >= len(text) {
		return len(text)
	}

	return graphemeEnd(text, cursor)
}

// clampIndex puts a cursor inside the text.
func clampIndex(cursor, length int) int {
	if cursor < 0 {
		return 0
	}
	if cursor > length {
		return length
	}

	return cursor
}

// isSpaceRune reports whether a rune separates words.
//
// A newline is a space for the purpose of Ctrl+W: it is a gap, and a word
// before it is still a word.
func isSpaceRune(value rune) bool {
	return value == ' ' || value == '\n' || value == '\t'
}

// blankDraft reports whether a draft has nothing to send.
//
// The test is on the trimmed text and the text that is sent is not trimmed:
// spaces and newlines inside a message are content, and only a draft that
// is nothing but gaps is an empty one (§7.3).
func blankDraft(text []rune) bool {
	return strings.TrimSpace(string(text)) == ""
}

// composerPlaceholderDelay is how long the placeholder stays lit after a
// blank Enter.
//
// It is long enough to be seen and short enough not to be a thing a user
// waits out. One message puts it out; nothing repaints the screen in the
// meantime.
const composerPlaceholderDelay = 700 * time.Millisecond

// composerPlaceholderExpiredMsg puts the lit placeholder out again.
type composerPlaceholderExpiredMsg struct{}

// composerPlaceholderCmd returns the command that unlights the placeholder.
func composerPlaceholderCmd() tea.Cmd {
	return tea.Tick(
		composerPlaceholderDelay,
		func(time.Time) tea.Msg {
			return composerPlaceholderExpiredMsg{}
		},
	)
}
