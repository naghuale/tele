package telegram

import (
	"fmt"
	"strconv"
)

// This file is the one way telecli reads a number out of TDLib.
//
// TDLib's JSON interface writes a 64-bit integer as a JSON string, so that
// a client reading the answer in JavaScript cannot lose the low bits of a
// number it cannot hold, and writes a 53-bit one as a plain number. Which
// of the two a field is written as depends on the field and not on the
// release of the library, so the shape cannot be told from the field name:
//
//	message.chat_id        int53   42
//	message.media_album_id int64   "0"
//
// A field declared as a plain int64 therefore failed to read a real answer,
// and the failure was not local to the field: the decode of the whole
// response failed with it, so a page of history that held fifty messages
// and one field written as a string became "Failed to load history" in
// every chat of a real account. The fixtures of this package were all
// written with a number where TDLib writes a string, and no test noticed
// (#53).
//
// chatPosition.order was the one field that already had to be read both
// ways, and it did it in a function of its own. The rule is one type now,
// and parsePositionOrder reads it like every other number.

// tdInt is a TDLib integer of 53 or 64 bits, read from either shape its
// JSON interface writes.
//
// A value that is neither a number nor a string of digits is a decode
// error, because a number this build cannot read is a field nobody can make
// a statement about, and zero is a statement. A value that is null, or a
// field the object does not carry at all, is zero: TDLib leaves a number
// out rather than sending a wrong one, and a message without an album is
// the common case rather than an exception.
type tdInt int64

// UnmarshalJSON reads a TDLib integer from a JSON number or a JSON string.
func (n *tdInt) UnmarshalJSON(data []byte) error {
	text := string(data)

	if text == "null" {
		*n = 0
		return nil
	}

	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		unquoted, err := strconv.Unquote(text)
		if err != nil {
			return fmt.Errorf("read TDLib number %s: %w", text, err)
		}
		text = unquoted
	}

	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("read TDLib number %s: %w", text, err)
	}

	*n = tdInt(value)
	return nil
}

// MarshalJSON writes the number as a JSON number.
//
// telecli sends integers of its own in its requests, and a request that
// quoted them would ask TDLib for a string where the scheme has a number.
// The type is therefore written in both directions, and a field may use it
// without having to think about which side of the conversation it is on.
func (n tdInt) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(int64(n), 10)), nil
}
