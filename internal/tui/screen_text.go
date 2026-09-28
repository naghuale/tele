package tui

import "strings"

// This file is the boundary between the text of Telegram and the screen.
//
// Everything here is drawn into a terminal, and a terminal does what the
// control characters it is given say: \r puts the cursor back at the start
// of the line, \v and \f move it down without a carriage return, the C1
// controls do the same in a terminal that takes them, and an escape
// sequence can clear the screen, move the cursor to any row of it, retitle
// the window or put a string on the clipboard. The interface never asks
// for any of that, and all of it is a thing anybody in a chat can send: a
// chat name, a preview, a message, a caption and the name above a message
// are all text from Telegram, and a message is the easiest thing in the
// world to send (#53).
//
// So the text is cleaned once, here, on its way into the model, and not in
// each view. A view that cleans what it draws is a view that has to
// remember to, and the field that nobody remembered would be the one that
// moves the cursor. What goes and what stays is the same everywhere:
//
//   - the line breaks of the text stay line breaks: a line feed, the
//     carriage return, the vertical tab and the form feed that older
//     systems ended a line with, NEL, and the Unicode line and paragraph
//     separators. A run of them is one break, so a Windows line ending is
//     one line and not two, and a break is a newline in the feed and a
//     space where there is one row. They are words that a terminal would
//     have taken for spacing, and dropping them would glue two words into
//     one: "Anna" + a carriage return + "Example" is a name with a space
//     in it, not a name with none.
//   - every other C0 control, DEL and every other C1 control go, and they
//     go without a replacement: a backspace erased a character on the
//     terminal, and the character it erased is not the one a reader wants
//     back. A tab becomes a space, because a tab is not a column in a cell
//     of a fixed width, and a cell that let one through would not be the
//     width the program believes it is drawing.
//   - an escape sequence goes whole, with what it carries: CSI, OSC, DCS,
//     APC, PM and SOS, in the seven-bit form that starts with ESC and in
//     the eight-bit form that starts with a C1 control. Dropping the ESC
//     alone would leave "[2J" on the screen as the name of a chat.
//   - the bidi controls go (U+202A–U+202E and U+2066–U+2069), because
//     they order the words around them the other way round, which is a way
//     of putting words on a screen in an order nobody wrote.
//
// What is left is what a person wrote: letters, punctuation, emoji with
// the ZWJ sequences and the variation selectors that make them one
// character, flags, and the combining marks of every script. Nothing that
// makes a character a character is removed, because a conversation in
// emoji is a conversation a user reads.

// screenBody cleans text for a place that has rows, which is the feed: a
// message is as tall as the sender wrote it, and a line break in it is a
// line break on the screen.
func screenBody(text string) string {
	return screenText(text, true)
}

// screenLine cleans text for a place that has one row: the name of a chat,
// its preview, the title of a conversation and the name above a message.
// A line break in any of them is a space here, because a row of the
// interface is a row of the terminal and a word drawn on the next one is a
// word drawn over something else.
func screenLine(text string) string {
	return screenText(text, false)
}

// escapeState is where the cleaner is inside a sequence it is dropping.
type escapeState uint8

const (
	// escapeText is outside any sequence: the ordinary case.
	escapeText escapeState = iota

	// escapeIntroducer is just after an introducer, where the bytes that
	// say what kind of sequence this is have not arrived yet.
	escapeIntroducer

	// escapeCSI is inside a control sequence, between its introducer and
	// the final byte that ends it.
	escapeCSI

	// escapeString is inside a string sequence — OSC, DCS, APC, PM, SOS —
	// which carries text of its own until the string terminator.
	escapeString

	// escapeStringEnd is a single byte after an ESC inside a string
	// sequence, which is the ESC of the ESC \ terminator.
	escapeStringEnd

	// escapeBreak is just after a break of the text, where the rest of the
	// run of them is dropped: a Windows line ending is one break, and so
	// is a line that ends with a form feed and a line feed both.
	escapeBreak
)

// The control characters, by their bytes and by their code points.
const (
	escapeByte = 0x1B // ESC, the seven-bit introducer
	bellByte   = 0x07 // BEL, the one terminator a string may use on its own
	delByte    = 0x7F // DEL

	c1First = 0x80 // the C1 controls, NEL (U+0085) among them
	c1Last  = 0x9F

	// The C1 controls that are a line break or a sequence end, and the
	// eight-bit introducers. Each of the introducers is a C1 control, and a
	// terminal in eight-bit control mode acts on it exactly as it acts on
	// the seven-bit form, so each one starts a sequence rather than being
	// dropped on its own.
	nelByte       = 0x85 // NEL, the next line, which some terminals draw
	dcs8Bit       = 0x90 // ESC P
	sos8Bit       = 0x98 // ESC X
	csi8Bit       = 0x9B // ESC [
	stringEnd8Bit = 0x9C // ESC \, the terminator in its eight-bit form
	osc8Bit       = 0x9D // ESC ]
	pm8Bit        = 0x9E // ESC ^
	apc8Bit       = 0x9F // ESC _

	// The Unicode separators, which are line breaks written as one
	// character rather than two.
	lineSeparator      = 0x2028
	paragraphSeparator = 0x2029

	// The bytes that end the parts of a sequence, by what they say: an
	// intermediate byte says that more of the sequence is coming, a
	// final byte ends it, and a control sequence is ended by any byte
	// from csiFinalFirst to csiFinalLast.
	intermediateLast = 0x2F
	finalFirst       = 0x30
	csiFinalFirst    = 0x40
	csiFinalLast     = 0x7E
)

// screenText is the one cleaner, and what it does not remove is as much a
// part of it as what it does.
//
// An unfinished sequence eats what is left of the text rather than putting
// half of it on the screen: a truncated sequence has no readable end, and
// the words after it are the ones an author wrote to be read.
func screenText(text string, keepNewlines bool) string {
	if text == "" || !textNeedsCleaning(text) {
		return text
	}

	var out strings.Builder
	out.Grow(len(text))
	state := escapeText

	for _, r := range text {
		// The rest of a run of breaks is dropped wherever it is, and the
		// first break of it has already been written. This is before the
		// switch because a break that ends a run is a normal character
		// again: the state is only about the breaks themselves.
		if state == escapeBreak {
			if isLineBreak(r) {
				continue
			}
			state = escapeText
		}

		switch state {
		case escapeText:
			var keep string
			keep, state = screenRune(r, keepNewlines)
			out.WriteString(keep)

		case escapeIntroducer:
			state = stateAfterIntroducer(r)

		case escapeCSI:
			switch {
			case r == escapeByte:
				// A sequence inside a sequence: the ESC begins a new
				// one, and what the terminal would have done with the
				// half of the old one is nothing anybody can read.
				state = escapeIntroducer
			case r == csi8Bit:
				state = escapeCSI
			case isStringStart(r):
				state = escapeString
			case r >= csiFinalFirst && r <= csiFinalLast:
				// The final byte of a control sequence: 0x40–0x7E ends
				// it, and the parameters and the intermediate bytes
				// before it are dropped with the rest of it.
				state = escapeText
			}

		case escapeString:
			switch r {
			case bellByte, stringEnd8Bit:
				state = escapeText
			case escapeByte:
				state = escapeStringEnd
			}

		case escapeStringEnd:
			// ESC \ ends the string. An ESC that is not followed by a
			// backslash is not the terminator, and the string continues.
			if r == '\\' {
				state = escapeText
			} else {
				state = escapeString
			}
		}
	}

	return out.String()
}

// screenRune is what one rune of ordinary text becomes, and the state the
// cleaner is in after it.
func screenRune(r rune, keepNewlines bool) (string, escapeState) {
	switch {
	case r == escapeByte:
		return "", escapeIntroducer

	case r == csi8Bit:
		return "", escapeCSI

	case isStringStart(r):
		return "", escapeString

	case isLineBreak(r):
		// A line break of the text is a line break of the screen, and the
		// rest of a run of them is dropped: one break stands for all of
		// them, so \r\n is one line and not two.
		if keepNewlines {
			return "\n", escapeBreak
		}
		return " ", escapeBreak

	case r == '\t':
		return " ", escapeText

	case r < 0x20, r == delByte:
		return "", escapeText

	case r >= c1First && r <= c1Last:
		return "", escapeText

	case isBidiControl(r):
		return "", escapeText

	default:
		return string(r), escapeText
	}
}

// isLineBreak reports whether a rune is a line break of the text it comes
// from: a line feed, and the other three controls that systems have ended
// a line with — the carriage return of the old Mac, the vertical tab and
// the form feed of DOS and of Unicode — together with NEL, which a
// terminal may draw as one, and the Unicode line and paragraph
// separators.
//
// They are all spacing: a terminal gives a line to each of them, and a
// reader gave a line to them too. Dropping them without a replacement
// would join the words on either side into one.
func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', nelByte,
		lineSeparator, paragraphSeparator:
		return true
	default:
		return false
	}
}

// stateAfterIntroducer is the state after the byte that follows ESC.
func stateAfterIntroducer(r rune) escapeState {
	switch {
	case r == '[' || r == csi8Bit:
		return escapeCSI

	// The same five sequences in their seven-bit form. The letter means
	// that only after an ESC: a square bracket, a caret or an underscore
	// in a chat name is the character it looks like.
	case isStringIntroducer(r):
		return escapeString

	// An intermediate byte says that what follows is still a sequence.
	case r >= 0x20 && r <= intermediateLast:
		return escapeIntroducer

	// A final byte, 0x30–0x7E, ends a sequence of its own: ESC c resets
	// the terminal, ESC 7 saves the cursor, ESC 8 restores it. Both bytes
	// of it are dropped together. A byte that is not one of those ends
	// the sequence as well, and the ESC with it.
	case r >= finalFirst && r <= csiFinalLast:
		return escapeText

	default:
		return escapeText
	}
}

// isStringStart reports whether a byte is one of the eight-bit introducers
// of a sequence that carries text: OSC, DCS, SOS, PM or APC. Each of them
// is a C1 control and opens the sequence on its own.
func isStringStart(r rune) bool {
	switch r {
	case dcs8Bit, sos8Bit, osc8Bit, pm8Bit, apc8Bit:
		return true
	default:
		return false
	}
}

// isStringIntroducer reports whether a byte after an ESC opens a sequence
// that carries text.
func isStringIntroducer(r rune) bool {
	switch r {
	case ']', 'P', '_', '^', 'X':
		return true
	default:
		return false
	}
}

// isBidiControl reports whether a rune is one of the controls that order
// the words around them the other way round.
func isBidiControl(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	default:
		return false
	}
}

// textNeedsCleaning reports whether there is anything in the text to
// clean, which is the whole cost of a chat list of five hundred names that
// hold nothing a terminal would act on: the text goes through unchanged and
// nothing is allocated.
func textNeedsCleaning(text string) bool {
	for _, r := range text {
		if r == '\n' || r == '\t' || !plainRune(r) {
			return true
		}
	}

	return false
}

// plainRune reports whether a rune is a character a terminal draws where
// it stands: not a control, not a separator and not a bidi control. Every
// line break of the text is a control or a separator, so a text with a
// break in it is always cleaned and a text without one is not.
func plainRune(r rune) bool {
	switch {
	case r < 0x20, r == delByte:
		return false
	case r >= c1First && r <= c1Last:
		return false
	case r == lineSeparator, r == paragraphSeparator:
		return false
	case isBidiControl(r):
		return false
	default:
		return true
	}
}

// ---- the two projections, cleaned once ----

// safeChats returns the chat list with every string of it cleaned.
//
// The list is copied rather than cleaned in place: it belongs to whoever
// produced it, and a source that kept a reference to it would then see the
// interface reach into its own data.
func safeChats(chats []Chat) []Chat {
	if chats == nil {
		return nil
	}

	safe := make([]Chat, 0, len(chats))
	for _, chat := range chats {
		safe = append(safe, safeChat(chat))
	}

	return safe
}

// safeChat cleans one chat: its name, its preview, its time and the other
// names it is found by in the search, plus the messages it arrived with.
//
// Every string of a chat is a single row of something, so every one of them
// is cleaned as a line. The aliases never reach the screen — they are a way
// of finding a row — and they are cleaned anyway, because a search that
// matches a name the terminal would act on is a search over a name that is
// not the one on the screen.
func safeChat(chat Chat) Chat {
	chat.Title = screenLine(chat.Title)
	chat.Preview = screenLine(chat.Preview)
	chat.Time = screenLine(chat.Time)
	chat.Aliases = safeAliases(chat.Aliases)
	chat.Messages = safeMessages(chat.Messages)

	return chat
}

func safeAliases(aliases []string) []string {
	if aliases == nil {
		return nil
	}

	safe := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		safe = append(safe, screenLine(alias))
	}

	return safe
}

// safeMessages returns the messages of a page with every string of them
// cleaned. The page is copied for the reason safeChats copies the list.
func safeMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}

	safe := make([]Message, 0, len(messages))
	for _, message := range messages {
		safe = append(safe, safeMessage(message))
	}

	return safe
}

// safeMessage cleans one message.
//
// Its text is the body of the feed and keeps its line breaks; everything
// else about it — the time, the name above it, the word for what it
// carries and the caption under it — is a row of something, and a row
// cannot be two rows whatever the sender typed.
func safeMessage(message Message) Message {
	message.Text = screenBody(message.Text)
	message.Time = screenLine(message.Time)
	message.Author = screenLine(message.Author)
	message.Media = screenLine(message.Media)
	message.Caption = screenBody(message.Caption)

	return message
}
