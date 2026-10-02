package tui

import (
	"strings"
	"testing"
	"time"
)

// The clock of the interface: what the hour of a moment is written in, and
// where the moment is read.
//
// Every case here states both answers rather than asking for them, and that
// is the whole of the file. A test that read the machine's setting would
// pass on the developer's Mac and fail in CI, and one that read the zone of
// the machine would draw "last seen at 02:00" in one country and "15:00" in
// another — both of which happened before, and both of which are recorded in
// docs/TUI_SPEC.md as the reason `m.now` and `m.location` are fields.

// The formats, as the two spellings of one moment. The moment is the owner's
// report of 29.09.2026: 21:21 UTC, read ten hours east in Vladivostok, where
// Telegram says 07:21 and telecli said 21:21.
var (
	clockMoment = time.Date(2026, 9, 28, 21, 21, 0, 0, time.UTC)
	clockZone   = time.FixedZone("UTC+10", 10*60*60)
)

func TestTheHourIsWrittenInTheFormatOfTheMachine(t *testing.T) {
	for _, testCase := range []struct {
		format ClockFormat
		want   string
	}{
		{format: ClockFormat12h, want: "07:21 AM"},
		{format: ClockFormat24h, want: "07:21"},
	} {
		m := Model{
			hourFormat: testCase.format,
			location:   clockZone,
		}

		if got := m.clockText(clockMoment); got != testCase.want {
			t.Errorf(
				"format %v: clockText = %q, want %q: a reader ten hours east "+
					"of Greenwich reads %q and not %q",
				testCase.format, got, testCase.want, testCase.want, "21:21",
			)
		}
	}
}

// A moment with no moment in it writes nothing. A pending record the queue
// has no time for is a row with no time on it, and "00:00" on that row is a
// claim about midnight that nobody made.
func TestAMomentThatIsNotThereWritesNothing(t *testing.T) {
	m := Model{location: clockZone}

	if got := m.clockText(time.Time{}); got != "" {
		t.Errorf("clockText of no moment = %q, want nothing", got)
	}
	if got := m.chatListTimeText(time.Time{}); got != "" {
		t.Errorf("chatListTimeText of no moment = %q, want nothing", got)
	}
}

// The hour is written in the format the machine says, and the configured
// setting wins over what the machine says — a user who wrote 12h wants twelve
// hours whether or not the system preferences agree.
func TestResolveClock(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		configured ClockMode
		system     SystemSetting
		want       ClockFormat
	}{
		{
			name:       "automatic on a twelve-hour machine",
			configured: ClockAuto,
			system:     System12h,
			want:       ClockFormat12h,
		},
		{
			name:       "automatic on a twenty-four-hour machine",
			configured: ClockAuto,
			system:     System24h,
			want:       ClockFormat24h,
		},
		{
			// A machine nothing could be read from is drawn in twenty-four
			// hours. A chat program that refused to start, or drew nothing,
			// over a preference it could not read would be worse than one
			// that guessed.
			name:       "automatic on a machine that did not answer",
			configured: ClockAuto,
			system:     SystemUnknown,
			want:       ClockFormat24h,
		},
		{
			name:       "configured twelve hours on a twenty-four-hour machine",
			configured: Clock12h,
			system:     System24h,
			want:       ClockFormat12h,
		},
		{
			name:       "configured twenty-four hours on a twelve-hour machine",
			configured: Clock24h,
			system:     System12h,
			want:       ClockFormat24h,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ResolveClock(testCase.configured, testCase.system); got != testCase.want {
				t.Errorf(
					"ResolveClock(%v, %v) = %v, want %v",
					testCase.configured, testCase.system, got, testCase.want,
				)
			}
		})
	}
}

func TestParseClockMode(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  ClockMode
	}{
		{value: "", want: ClockAuto},
		{value: "auto", want: ClockAuto},
		{value: "12h", want: Clock12h},
		{value: "24h", want: Clock24h},
		{value: " 24H ", want: Clock24h},
	} {
		got, err := ParseClockMode(testCase.value)
		if err != nil {
			t.Errorf("ParseClockMode(%q): %v", testCase.value, err)
			continue
		}
		if got != testCase.want {
			t.Errorf("ParseClockMode(%q) = %v, want %v", testCase.value, got, testCase.want)
		}
	}

	// A word that is not one of the three is an error with the three in it:
	// a setting that is quietly ignored is something a user finds out about
	// from a screen drawn wrong.
	_, err := ParseClockMode("half past")
	if err == nil {
		t.Fatal("a clock of \"half past\" was accepted")
	}
	for _, want := range []string{"auto", "12h", "24h", "half past"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %q", err, want)
		}
	}
}

func TestTheWordsOfTheSettingAreItsNames(t *testing.T) {
	for mode, want := range map[ClockMode]string{
		ClockAuto: "auto",
		Clock12h:  "12h",
		Clock24h:  "24h",
	} {
		if got := mode.String(); got != want {
			t.Errorf("ClockMode.String() = %q, want %q", got, want)
		}
	}
}

// The day of a moment is named the way a reader of a conversation needs it
// named: Today, Yesterday, a weekday while the week is still placeable, and a
// date after that.
//
// The cases below are the boundaries, in two zones ten hours apart, and each
// of them is a moment in the reader's day rather than in UTC: a day boundary
// is a local midnight and nothing else. The pair of zones is the point —
// the same instant is one day in Vladivostok and the day before in Adak, and
// a ladder of days that only holds in one zone is a ladder of UTC.
func TestTheDayOfAMomentIsNamedForTheReader(t *testing.T) {
	// The zones are ten hours apart and the cases below are written as
	// moments of a day rather than as hours before now: an hour before the
	// model's clock is Yesterday in one zone and Today in the other for most
	// of the day, and that difference is the reason the ladder of days is
	// asked in the zone of the reader at all. A table of offsets would have
	// to carry the arithmetic of each zone with it, and it would read as a
	// table about hours.
	zones := map[string]*time.Location{
		"UTC+10": clockZone,
		"UTC-9":  time.FixedZone("UTC-9", -9*60*60),
	}

	// The moment the model reads its clock at: ten in the morning of
	// Friday 2 October 2026, in whichever zone the case is drawn in. The
	// dates of the cases below are counted back from it.
	var (
		thisYear = 2026
		thisOct  = time.October
		clock    = 10
	)

	for _, testCase := range []struct {
		name string

		// day is the date of the moment in the zone of the case.
		year  int
		month time.Month
		day   int
		hour  int

		want string
	}{
		{
			name: "this morning",
			year: thisYear, month: thisOct, day: 2, hour: clock,
			want: "Today",
		},
		{
			name: "an hour ago",
			year: thisYear, month: thisOct, day: 2, hour: 9,
			want: "Today",
		},
		{
			name: "last night",
			year: thisYear, month: thisOct, day: 1, hour: 23,
			want: "Yesterday",
		},
		{
			name: "yesterday morning",
			year: thisYear, month: thisOct, day: 1, hour: 7,
			want: "Yesterday",
		},
		{
			// Wednesday, of the week before this one.
			name: "two days ago",
			year: thisYear, month: time.September, day: 30, hour: 12,
			want: "Wednesday",
		},
		{
			// Saturday: six days back, and the last of the weekdays.
			name: "six days ago",
			year: thisYear, month: time.September, day: 26, hour: 12,
			want: "Saturday",
		},
		{
			// A week back is a date. A weekday would name a day of some
			// other week, and a reader looking for Friday would find two.
			name: "seven days ago",
			year: thisYear, month: time.September, day: 25, hour: 12,
			want: "September 25",
		},
		{
			name: "earlier this year",
			year: thisYear, month: time.January, day: 1, hour: 12,
			want: "January 1",
		},
		{
			name: "the turn of the year",
			year: thisYear - 1, month: time.December, day: 31, hour: 12,
			want: "December 31, 2025",
		},
		{
			name: "last year",
			year: thisYear - 1, month: thisOct, day: 2, hour: 12,
			want: "October 2, 2025",
		},
	} {
		for zone, location := range zones {
			t.Run(testCase.name+" in "+zone, func(t *testing.T) {
				now := time.Date(
					thisYear, thisOct, 2, clock, 0, 0, 0, location,
				)
				at := time.Date(
					testCase.year, testCase.month, testCase.day,
					testCase.hour, 30, 0, 0, location,
				)

				m := Model{
					now:        func() time.Time { return now },
					location:   location,
					hourFormat: ClockFormat24h,
				}

				got := m.dayLabel(at)
				if got != testCase.want {
					t.Errorf(
						"dayLabel(%v in %v) = %q, want %q",
						at.Format(time.RFC3339), zone, got, testCase.want,
					)
				}
			})
		}
	}
}

// Yesterday is the day before and not the one that ended twenty-four hours
// ago: a moment at 23:50 yesterday and a moment at 00:10 today are three
// hours apart and on different days, and a divider that counts hours would
// put both of them under one name.
func TestAYesterdayIsTheDayBeforeAndNotTwentyFourHoursAgo(t *testing.T) {
	zone := clockZone
	now := time.Date(2026, 10, 2, 0, 10, 0, 0, zone)
	lateYesterday := time.Date(2026, 10, 1, 23, 50, 0, 0, zone)

	m := Model{
		now:      func() time.Time { return now },
		location: zone,
	}

	if got := m.dayLabel(lateYesterday); got != "Yesterday" {
		t.Errorf(
			"dayLabel(23:50 yesterday, read at 00:10) = %q, want Yesterday",
			got,
		)
	}
}

// The row of a chat list says the time today and the day before it, which is
// what a list of chats is for: whether a message is from this morning or from
// last night. Everything else is the ladder of the feed with Today standing
// in for the time of day.
func TestTheRowOfAChatListNamesTheDayToo(t *testing.T) {
	zone := clockZone
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, zone)

	m := Model{
		now:        func() time.Time { return now },
		location:   zone,
		hourFormat: ClockFormat24h,
	}

	for _, testCase := range []struct {
		name string
		at   time.Time
		want string
	}{
		{
			name: "an hour and a half ago, as the time of day",
			at:   time.Date(2026, 10, 2, 8, 30, 0, 0, zone),
			want: "08:30",
		},
		{
			name: "last night, as Yesterday",
			at:   time.Date(2026, 10, 1, 23, 50, 0, 0, zone),
			want: "Yesterday",
		},
		{
			name: "earlier in the week, as a weekday",
			at:   time.Date(2026, 9, 30, 12, 0, 0, 0, zone),
			want: "Wednesday",
		},
		{
			name: "a week ago, as a date",
			at:   time.Date(2026, 9, 24, 12, 0, 0, 0, zone),
			want: "September 24",
		},
		{
			name: "a year ago, as a date with the year in it",
			at:   time.Date(2025, 10, 1, 12, 0, 0, 0, zone),
			want: "October 1, 2025",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := m.chatListTimeText(testCase.at)
			if got != testCase.want {
				t.Errorf("chatListTimeText = %q, want %q", got, testCase.want)
			}
		})
	}
}

// The row of the list is written in the clock of the machine as well: half
// past eight in the evening is "08:30 PM" on a machine set that way, and a
// list whose times are in one format and whose feed in another is a screen
// with two clocks in it.
func TestTheRowOfAChatListUsesTheFormatOfTheMachine(t *testing.T) {
	zone := clockZone
	now := time.Date(2026, 10, 2, 22, 0, 0, 0, zone)
	earlier := time.Date(2026, 10, 2, 20, 30, 0, 0, zone)

	for _, testCase := range []struct {
		format ClockFormat
		want   string
	}{
		{format: ClockFormat12h, want: "08:30 PM"},
		{format: ClockFormat24h, want: "20:30"},
	} {
		m := Model{
			now:        func() time.Time { return now },
			location:   zone,
			hourFormat: testCase.format,
		}

		if got := m.chatListTimeText(earlier); got != testCase.want {
			t.Errorf("chatListTimeText in %v = %q, want %q", testCase.format, got, testCase.want)
		}
	}
}

// A model built as a value rather than by a constructor draws twenty-four
// hours: a struct field of a clock that started at twelve would draw a
// screen in the format of an American keyboard for a model nobody
// configured.
func TestAModelWithNoClockDrawsTwentyFourHours(t *testing.T) {
	m := Model{location: clockZone}

	if got := m.clockFormat(); got != ClockFormat24h {
		t.Errorf("clockFormat() = %v, want 24h", got)
	}
	if got := m.clockText(clockMoment); got != "07:21" {
		t.Errorf("clockText = %q, want the twenty-four-hour spelling", got)
	}
}

// The reading of a machine is a function of its preferences and not a call
// into them, and this is the table that decides what macOS answers.
//
// The keys are asked first: AppleICUForce24HourTime and AppleICUForce12HourTime
// are what a user sets in "System Settings → General → Language & Region",
// and an answer there outranks everything else. When neither is set the
// locale decides, and the locale decides through a table — there is no way
// round one — which is why the table is here and why its cases are named.
func TestTheMacLocaleSaysWhichHourItWrites(t *testing.T) {
	for _, testCase := range []struct {
		locale string
		want   SystemSetting
	}{
		{locale: "en_US", want: System12h},
		{locale: "en_GB", want: System24h},
		{locale: "ru_RU", want: System24h},
		{locale: "ja_JP", want: System12h},
		{locale: "de_DE", want: System24h},
		{locale: "fr", want: System24h},
		{locale: "en", want: System12h},
		// A locale copied from somewhere else carries its codeset and its
		// modifier, and a table that stopped at the underscore would have
		// no opinion about either of these.
		{locale: "de_DE.UTF-8", want: System24h},
		{locale: "de_DE@euro", want: System24h},
		{locale: "de-DE", want: System24h},
		// A locale nobody has heard of is read as twelve: that is what a
		// reader whose name means nothing to this table is most likely to
		// have set themselves.
		{locale: "xx_YY", want: System12h},
	} {
		t.Run(testCase.locale, func(t *testing.T) {
			got := map[bool]SystemSetting{
				true:  System24h,
				false: System12h,
			}[writesTwentyFourHours(testCase.locale)]

			if got != testCase.want {
				t.Errorf(
					"the locale %q resolves to %v, want %v",
					testCase.locale, got, testCase.want,
				)
			}
		})
	}
}
