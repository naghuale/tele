package telegram

import (
	"encoding/json"
	"testing"
)

// TDLib's JSON interface writes a 64-bit integer as a JSON string and a
// 53-bit one as a JSON number, and which one a field is depends on the
// field rather than on the release. Every number of TDLib is therefore read
// with one type that takes both shapes, and the table below is that type's
// whole contract.
func TestTDIntReadsBothJSONShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"a number", `42`, 42},
		{"a number with no digits", `0`, 0},
		{"a negative number", `-1001`, -1001},
		{"a number past the 53 bits", `9007199254740993`, 9007199254740993},
		{
			name: "a number at the top of int64",
			raw:  `9223372036854775807`,
			want: 9223372036854775807,
		},
		{"a string", `"42"`, 42},
		{"a string of zeroes", `"0"`, 0},
		{"a negative string", `"-1001"`, -1001},
		{"an album id as a string", `"1234567890123456789"`, 1234567890123456789},
		{"no value at all", `null`, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got tdInt
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.raw, err)
			}
			if int64(got) != tc.want {
				t.Fatalf("unmarshal %s = %d, want %d", tc.raw, int64(got), tc.want)
			}
		})
	}
}

// A value that is neither a number nor a string of digits is a decode
// error, not a zero: a field that has gone wrong is a field nobody can make
// a statement about, and a zero is a statement.
func TestTDIntRejectsWhatIsNotANumber(t *testing.T) {
	for _, raw := range []string{
		`""`,
		`"forty two"`,
		`" 42"`,
		`"42 "`,
		`"1.5"`,
		`true`,
		`{}`,
		`[]`,
		`1.5`,
		`1e3`,
	} {
		t.Run(raw, func(t *testing.T) {
			var got tdInt
			if err := json.Unmarshal([]byte(raw), &got); err == nil {
				t.Fatalf("unmarshal %s = %d, want an error", raw, int64(got))
			}
		})
	}
}

// The same type has to write a number, because telecli sends integers of
// its own in requests and a request that quoted them would be asking TDLib
// for a string where it expects a number.
func TestTDIntWritesANumber(t *testing.T) {
	raw, err := json.Marshal(struct {
		ChatID tdInt `json:"chat_id"`
	}{ChatID: 42})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(raw), `{"chat_id":42}`; got != want {
		t.Fatalf("marshal = %s, want %s", got, want)
	}
}

// A field the object does not carry is zero, and one it carries as null is
// zero: TDLib leaves a number out rather than sending a wrong one.
func TestTDIntIsZeroForAnAbsentField(t *testing.T) {
	var decoded struct {
		Present tdInt `json:"present"`
		Null    tdInt `json:"null_field"`
		Missing tdInt `json:"missing"`
	}
	raw := `{"present":7,"null_field":null}`
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if int64(decoded.Present) != 7 {
		t.Fatalf("present = %d, want 7", int64(decoded.Present))
	}
	if int64(decoded.Null) != 0 || int64(decoded.Missing) != 0 {
		t.Fatalf("null = %d, missing = %d, want zero for both",
			int64(decoded.Null), int64(decoded.Missing))
	}
}
