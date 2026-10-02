package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"telecli/internal/tui/theme"
)

// The presence of the other side of a chat, in the header of the
// conversation (§4.3, §3.1).
//
// Every word here is a fact about somebody who did not ask to be followed,
// and each one of them is on the screen for as long as the user is in the
// chat. A presence that is not known is drawn as nothing: the interface has
// no "presence unknown", and inventing one would be a claim about a person
// that nothing checked.

func TestThePresenceIsDrawnBeforeTheConnection(t *testing.T) {
	model := modelWithPresence(t, theme.ProfileNoColor, 100, 24, Presence{
		Kind:      PresenceUser,
		ExpiresAt: testClock.Add(time.Hour),
	}, StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Queued: 2},
	})

	line := statusLineOf(t, model)
	if line != "online · connected · 2 queued" {
		t.Fatalf("status line = %q", line)
	}
}

func TestEveryPresenceTextIsDrawn(t *testing.T) {
	// The clock is fixed and in a fixed zone: "last seen at 14:05" is a
	// statement about a time zone, and a test that read the machine's zone
	// would pass in one place and fail in another.
	now := testClock
	zone := time.FixedZone("UTC+3", 3*60*60)

	for name, testCase := range map[string]struct {
		presence Presence
		now      time.Time
		want     string
	}{
		"online": {
			presence: Presence{Kind: PresenceUser, ExpiresAt: now.Add(time.Hour)},
			want:     "online",
		},
		"online expires in an hour": {
			presence: Presence{
				Kind:      PresenceUser,
				ExpiresAt: now.Add(time.Hour),
			},
			want: "online",
		},
		// Both of these are 90 and 50 minutes before 15:00 UTC, which is
		// 16:30 and 17:10 in the zone the model reads times in.
		"online that ran out today": {
			presence: Presence{
				Kind:      PresenceUser,
				ExpiresAt: now.Add(-90 * time.Minute),
			},
			want: "last seen at 16:30",
		},
		"offline today": {
			presence: Presence{
				Kind:       PresenceUser,
				LastSeenAt: now.Add(-50 * time.Minute),
			},
			want: "last seen at 17:10",
		},
		"offline yesterday": {
			presence: Presence{
				Kind:       PresenceUser,
				LastSeenAt: now.Add(-20 * time.Hour),
			},
			// 19:00 UTC is 22:00 in the zone the model reads times in, and
			// the zone is the point: a status line that showed UTC to a
			// user three hours east would be wrong about their own day.
			want: "last seen yesterday at 22:00",
		},
		"offline last week": {
			presence: Presence{
				Kind:       PresenceUser,
				LastSeenAt: time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC),
			},
			want: "last seen 21 Sep",
		},
		"recently": {
			presence: Presence{Kind: PresenceUser, Recency: RecencyRecently},
			want:     "last seen recently",
		},
		"within a week": {
			presence: Presence{Kind: PresenceUser, Recency: RecencyLastWeek},
			want:     "last seen within a week",
		},
		"within a month": {
			presence: Presence{Kind: PresenceUser, Recency: RecencyLastMonth},
			want:     "last seen within a month",
		},
		"a bot": {
			presence: Presence{
				Kind:      PresenceUser,
				Bot:       true,
				ExpiresAt: now.Add(time.Hour),
			},
			want: "bot",
		},
		"a group with members online": {
			presence: Presence{Kind: PresenceGroup, OnlineMembers: 3},
			want:     "3 online",
		},
		"a group nobody counted": {
			presence: Presence{Kind: PresenceGroup},
			want:     "",
		},
		"a chat with oneself": {
			presence: Presence{
				Kind:      PresenceUser,
				Self:      true,
				ExpiresAt: now.Add(time.Hour),
			},
			want: "",
		},
		"nothing known": {
			presence: Presence{},
			want:     "",
		},
		"a user with no status": {
			presence: Presence{Kind: PresenceUser},
			want:     "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Nothing but the presence, so that the expectation is about
			// the presence and not about the rest of the line.
			model := modelWithPresence(
				t,
				theme.ProfileNoColor,
				100,
				24,
				testCase.presence,
				StatusSummary{},
			)
			at := testCase.now
			if at.IsZero() {
				at = now
			}
			model = withClock(model, at, zone)

			if got := statusLineOf(t, model); got != testCase.want {
				t.Fatalf("status line = %q, want %q", got, testCase.want)
			}
		})
	}
}

// An online status is a promise TDLib makes with a deadline, and it sends
// nothing when the deadline passes. The word has to change anyway, from the
// clock, without a new update: a header that says "online" to somebody who
// is not is the one thing a presence line must never be.
func TestAnOnlinePresenceThatRanOutIsDrawnFromTheClock(t *testing.T) {
	now := testClock
	presence := Presence{Kind: PresenceUser, ExpiresAt: now.Add(30 * time.Minute)}

	model := modelWithPresence(t, theme.ProfileNoColor, 100, 24, presence, StatusSummary{})
	model = withClock(model, now, time.UTC)

	if got := statusLineOf(t, model); got != "online" {
		t.Fatalf("status line = %q, want online", got)
	}

	// Nothing arrives from the store: the same presence, half an hour later.
	later := withClock(model, now.Add(time.Hour), time.UTC)
	if got := statusLineOf(t, later); got != "last seen at 15:30" {
		t.Fatalf("status line = %q, want the expired presence", got)
	}
}

// The screen has to be repainted when the word changes, and there is no
// update to repaint it for.
func TestTheScreenIsRepaintedWhenAnOnlinePresenceRunsOut(t *testing.T) {
	now := testClock
	source := &summarySource{summary: StatusSummary{
		Connection: ConnectionReady,
		Presence:   Presence{Kind: PresenceUser, ExpiresAt: now.Add(time.Hour)},
	}}

	model := modelWithSummarySource(t, theme.ProfileNoColor, 100, 24, source, source.summary)
	model = withClock(model, now, time.UTC)
	model, cmd := updateModel(t, model, model.statusRefresh(t, source))
	if cmd == nil {
		t.Fatal("an online presence with a deadline armed no repaint")
	}

	// The message repaints the screen by being delivered; what it must not
	// do is change the data, because the word is computed from the clock.
	updated, _ := updateModel(t, model, presenceExpiredMsg{
		expires: now.Add(time.Hour),
	})
	if !updated.summary.Presence.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatal("the expiry changed the presence")
	}
}

// §4.3: presence, connection and the queue are independent. A presence the
// store knows nothing about must not take the connection and the counts
// with it.
func TestAMissingPresenceHidesNothingElse(t *testing.T) {
	model := modelWithPresence(t, theme.ProfileNoColor, 100, 24, Presence{}, StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Queued: 4, Retrying: 1},
	})

	if got := statusLineOf(t, model); got != "connected · 4 queued · 1 retrying" {
		t.Fatalf("status line = %q", got)
	}
}

// A narrow screen keeps the presence: it is the only part of the line that
// says something about the person the user is talking to, and the counts
// of a queue are the part that can be read again later.
func TestANarrowScreenKeepsThePresence(t *testing.T) {
	summary := StatusSummary{
		Connection: ConnectionWaitingForNetwork,
		Queue:      QueueSummary{Known: true, Queued: 12, Retrying: 11, Recovering: true},
	}
	presence := Presence{Kind: PresenceUser, ExpiresAt: testClock.Add(time.Hour)}

	for _, width := range []int{100, 80, 60, 40} {
		model := modelWithPresence(t, theme.ProfileNoColor, width, 24, presence, summary)

		lines := statusLinesOf(t, model)
		if len(lines) == 0 {
			t.Fatalf("width %d: no status line", width)
		}
		joined := strings.Join(lines, " ")
		if !strings.Contains(joined, "online") {
			t.Fatalf("width %d: the presence was dropped: %q", width, lines)
		}
		for _, line := range lines {
			if model.widths.StringWidth(line) > width {
				t.Fatalf(
					"width %d: the status line %q is %d columns wide",
					width,
					line,
					model.widths.StringWidth(line),
				)
			}
		}
	}
}

// A presence is a fact about a person, and it is the kind of fact §19 keeps
// out of logs and reports. A value on its way to a log prints what kind of
// presence it is and nothing else: no moment anybody was there, no day of
// it, no hour.
//
// The screen is a different matter and the difference is the point. It says
// when somebody was last there — that is what somebody came to find out —
// and what it must not do is print the store's own words for it: a presence
// with an hour of online left says Online, and one that ran out says the
// time it ran out at and no year and no day of it.
//
// Every moment here is testClock, which is a fixed moment for the same
// reason every other presence test fixes it: this one used to read the
// machine's clock, and on 28 September 2026 at 15:00 UTC the presence it
// had built ran out, the screen said "last seen at 15:00", and the test
// read its own fixture as a leak. It was correct about the text and wrong
// about the moment, and only the moment was the fault.
func TestPresencePrintsWithoutTheStatus(t *testing.T) {
	online := Presence{
		Kind:       PresenceUser,
		ExpiresAt:  testClock.Add(time.Hour),
		LastSeenAt: testClock.Add(-24 * time.Hour),
	}
	ranOut := Presence{
		Kind:       PresenceUser,
		ExpiresAt:  testClock,
		LastSeenAt: testClock.Add(-24 * time.Hour),
	}

	// The year is the store's and nobody reads it; the hour of it is on the
	// screen on purpose, and a time of day off a *log* is not.
	noStoreYear := []string{"2026", "28 Sep", "27 Sep"}

	for name, testCase := range map[string]struct {
		printed string
		want    string
		leaks   []string
	}{
		"a user on its way to a log": {
			printed: online.String(),
			want:    "presence: user",
			leaks:   append(noStoreYear, "15:00", "09:00", "16:00"),
		},
		"a group on its way to a log": {
			printed: Presence{Kind: PresenceGroup, OnlineMembers: 3}.String(),
			want:    "presence: group",
			leaks:   noStoreYear,
		},
		"a presence nobody knows about on its way to a log": {
			printed: Presence{}.String(),
			want:    "presence: none",
			leaks:   noStoreYear,
		},
		"an online presence on the screen": {
			printed: presenceText(online, testClock, time.UTC, ClockFormat24h),
			want:    "online",
			leaks:   noStoreYear,
		},
		"a presence that ran out an hour ago on the screen": {
			printed: presenceText(ranOut, testClock, time.UTC, ClockFormat24h),
			want:    "last seen at 15:00",
			leaks:   noStoreYear,
		},
		"a presence that ran out last week on the screen": {
			printed: presenceText(
				Presence{
					Kind:       PresenceUser,
					LastSeenAt: testClock.Add(-8 * 24 * time.Hour),
				},
				testClock, time.UTC, ClockFormat24h,
			),
			want:  "last seen 20 Sep",
			leaks: noStoreYear,
		},
	} {
		if testCase.printed != testCase.want {
			t.Errorf(
				"%s printed %q, want %q",
				name, testCase.printed, testCase.want,
			)
		}

		for _, leak := range testCase.leaks {
			if strings.Contains(testCase.printed, leak) {
				t.Errorf("%s printed %q out of the store: %q", name, leak, testCase.printed)
			}
		}
	}
}

// A test that reads the machine's clock to build what it asserts on is a
// test that passes today and fails on the day the data it built runs out.
// The one above was one, and it went off at a moment printed in a date
// literal somebody had written months earlier.
//
// So the wall clock is read in one place — wallClock below — and it is read
// there for the two things that are about time passing rather than about
// what a test asserts: a deadline to give up waiting by, and a length of
// time to have spent. Everything else draws at testClock.
func TestNoTestOfThisPackageBuildsItsDataFromTheWallClock(t *testing.T) {
	// The needle is put together rather than written out, so that the file
	// this check lives in does not hold the one thing it is looking for.
	needle := "time." + "Now("

	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("the tests of this package: %v", err)
	}

	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}

		reads := strings.Count(string(source), needle)

		want := 0
		if file == "presence_test.go" {
			want = 1 // wallClock, and nothing else.
		}
		if reads != want {
			t.Errorf(
				"%s reads the wall clock %d times, want %d: a test that "+
					"builds what it asserts on out of the machine's clock "+
					"is a test that fails on the day the data runs out",
				file, reads, want,
			)
		}
	}
}

// wallClock is the machine's clock, and it is the only place in a test of
// this package that reads it.
//
// It is for time passing and not for what a test is about: a deadline to
// stop waiting at, and a length of time to have spent. A test that needs
// the moment of something builds it out of testClock instead, which is a
// moment that does not run out while the test is looking at it.
func wallClock() time.Time {
	return time.Now()
}

// The presence is a statement about two things the model has to read: the
// moment it is drawing at, and the zone it draws times in. A test that
// pinned neither was a test about the machine it ran on, and the machine
// changes its answer at 16:00 UTC on a day written into a fixture somebody
// read as "recently" when they wrote it.
//
// The cases below are moments and zones no machine is on. Each one fails if
// anything on the path from the fixture to the header reads the real clock
// or the real zone: the first fails on the real clock (a presence an hour
// of online left is long expired by now), the second on a clock left
// behind (a presence of 2026 read at 2031 is six years gone, and a model
// still reading testClock would call it online), the third on the zone (the
// same two moments are the same day in Vladivostok and the day before in
// Adak, so the zone is the only thing that can tell the two words apart).
func TestThePresenceIsDrawnFromTheClockOfTheCaseAndNotTheMachine(t *testing.T) {
	// A moment and a zone no machine is on. Adak is nine hours behind and
	// Vladivostok ten hours ahead, so a time in one of them is a different
	// day from the same time in the other for most of the day.
	far := time.Date(2031, 7, 1, 0, 0, 0, 0, time.UTC)
	adak, err := time.LoadLocation("America/Adak")
	if err != nil {
		t.Skipf("the zone database is not here: %v", err)
	}
	vladivostok, err := time.LoadLocation("Asia/Vladivostok")
	if err != nil {
		t.Skipf("the zone database is not here: %v", err)
	}

	for name, testCase := range map[string]struct {
		presence Presence
		now      time.Time
		zone     *time.Location
		want     string
	}{
		// The fixture's own moment, drawn an hour before it runs out. A
		// model that read the real clock says "last seen" from 2026-09-28
		// 16:00 UTC on, in whatever zone the runner is in.
		"the moment of the fixture": {
			presence: Presence{
				Kind:      PresenceUser,
				ExpiresAt: testClock.Add(time.Hour),
			},
			now:  testClock,
			zone: time.UTC,
			want: "online",
		},
		// Six years on, the same presence is long gone, and the header says
		// the day rather than the hour: the days are years apart, so
		// neither "today" nor "yesterday" is the answer.
		"a moment no machine is on": {
			presence: Presence{
				Kind:      PresenceUser,
				ExpiresAt: testClock.Add(time.Hour),
			},
			now:  far,
			zone: adak,
			want: "last seen 28 Sep",
		},
		// The zone is the point: 2031-06-02 00:00 UTC is 10:00 in
		// Vladivostok and 15:00 on the day before in Adak, and a model
		// reading the runner's zone says one or the other.
		"a zone no machine is on": {
			presence: Presence{
				Kind:       PresenceUser,
				LastSeenAt: time.Date(2031, 6, 2, 0, 0, 0, 0, time.UTC),
			},
			now:  far.Add(-29 * 24 * time.Hour),
			zone: adak,
			want: "last seen at 15:00",
		},
		// The word of an online presence, drawn years on. A model with a
		// clock left behind at testClock would still say it here — the
		// case above is the one that catches that — and this one says that
		// a presence an hour of online left is Online at whatever hour it
		// is looked at, which is the half of §4.3 the other cases do not
		// reach.
		"online at a moment no machine is on": {
			presence: Presence{
				Kind:      PresenceUser,
				ExpiresAt: time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			now:  far,
			zone: vladivostok,
			want: "online",
		},
	} {
		t.Run(name, func(t *testing.T) {
			model := modelWithPresenceAt(
				t, theme.ProfileNoColor, 100, 24,
				testCase.presence, StatusSummary{},
				testCase.now, testCase.zone,
			)

			if got := statusLineOf(t, model); got != testCase.want {
				t.Fatalf(
					"status line = %q, want %q: the header is drawn from "+
						"the moment and the zone of the case",
					got, testCase.want,
				)
			}
		})
	}
}

// testClock is the moment every presence test draws at. A fixed moment and
// a fixed zone are what make "last seen at 14:05" a fact about the code
// rather than about the machine the test runs on.
var testClock = time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)

// ---- helpers ----

// modelWithPresence is a conversation with a presence and a summary read,
// reading the clock and the zone of testClock.
//
// The clock and the zone are pinned here rather than in each test, and that
// is the whole of the fix for a test that used to answer for the machine it
// ran on. A model reads the clock through two doors: `m.now`, which is
// `time.Now` until something pins it, and `m.location`, which is
// `time.Local` until something pins that. A presence test that pinned
// neither judged a presence of 2026-09-28 against a clock of whenever the
// test happened to run, and on 28 September 2026 at 16:00 UTC the answer
// turned from "online" to "last seen at 02:00" — in the zone of the machine,
// which is a second thing nobody pinned.
//
// Every presence test builds its model here, so every presence test now has
// both. A test that wants another moment or another zone says so after.
func modelWithPresence(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
	presence Presence,
	summary StatusSummary,
) Model {
	t.Helper()

	return modelWithPresenceAt(
		t, profile, width, height, presence, summary, testClock, time.UTC,
	)
}

// modelWithPresenceAt is the same conversation, reading the clock and the
// zone the case names.
func modelWithPresenceAt(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
	presence Presence,
	summary StatusSummary,
	now time.Time,
	location *time.Location,
) Model {
	t.Helper()

	source := &summarySource{summary: summary}
	source.summary.Presence = presence
	model := modelWithSummarySource(t, profile, width, height, source, summary)
	model, _ = updateModel(t, model, model.statusRefresh(t, source))

	return withClock(model, now, location)
}

// withClock pins the clock and the zone a model reads times from.
func withClock(model Model, now time.Time, location *time.Location) Model {
	model.now = func() time.Time { return now }
	model.location = location

	return model
}
