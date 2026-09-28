package tui

import (
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
	if line != "Online · Connected · 2 queued" {
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
			want:     "Online",
		},
		"online expires in an hour": {
			presence: Presence{
				Kind:      PresenceUser,
				ExpiresAt: now.Add(time.Hour),
			},
			want: "Online",
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
// clock, without a new update: a header that says "Online" to somebody who
// is not is the one thing a presence line must never be.
func TestAnOnlinePresenceThatRanOutIsDrawnFromTheClock(t *testing.T) {
	now := testClock
	presence := Presence{Kind: PresenceUser, ExpiresAt: now.Add(30 * time.Minute)}

	model := modelWithPresence(t, theme.ProfileNoColor, 100, 24, presence, StatusSummary{})
	model = withClock(model, now, time.UTC)

	if got := statusLineOf(t, model); got != "Online" {
		t.Fatalf("status line = %q, want Online", got)
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

	if got := statusLineOf(t, model); got != "Connected · 4 queued · 1 retrying" {
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
		if !strings.Contains(joined, "Online") {
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
// presence it is and nothing about the person.
func TestPresencePrintsWithoutTheStatus(t *testing.T) {
	presence := Presence{
		Kind:       PresenceUser,
		ExpiresAt:  time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC),
		LastSeenAt: time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC),
	}

	for name, printed := range map[string]string{
		"v":    presence.String(),
		"s":    presenceText(presence, time.Now(), time.UTC),
		"plus": presenceText(presence, time.Now(), time.UTC),
	} {
		if strings.Contains(printed, "2026") || strings.Contains(printed, "15:00") {
			t.Fatalf("%s printed the times: %q", name, printed)
		}
	}
}

// testClock is the moment every presence test draws at. A fixed moment and
// a fixed zone are what make "last seen at 14:05" a fact about the code
// rather than about the machine the test runs on.
var testClock = time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)

// ---- helpers ----

// modelWithPresence is a conversation with a presence and a summary read.
func modelWithPresence(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
	presence Presence,
	summary StatusSummary,
) Model {
	t.Helper()

	source := &summarySource{summary: summary}
	source.summary.Presence = presence
	model := modelWithSummarySource(t, profile, width, height, source, summary)
	model, _ = updateModel(t, model, model.statusRefresh(t, source))

	return model
}

// withClock pins the clock and the zone a model reads times from.
func withClock(model Model, now time.Time, location *time.Location) Model {
	model.now = func() time.Time { return now }
	model.location = location

	return model
}
