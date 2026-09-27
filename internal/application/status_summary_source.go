package application

import (
	"context"
	"errors"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// LiveStatusSummarySource reads what the status line shows: the connection
// TDLib reports and the counters of the durable queue.
//
// It exists as one type because the two halves are read on one poll, and
// because they are independent: a queue that cannot be read leaves the
// connection on the screen, and a client that is reconnecting leaves the
// counts there. Reading them apart and joining them in the view would
// spread that rule over two files and let one of them forget it.
//
// The interface takes no payload of any kind. It is read for a screen and
// for a log line, and both of those must stay free of message text
// (§11.3, §19).
type LiveStatusSummarySource struct {
	live   LiveConnectionState
	health MessageDeliveryHealthSource

	// ownUserID is the user this client is authorized as, or zero when it
	// was not resolved.
	//
	// It is what tells a chat with oneself from a chat with a contact:
	// TDLib sends the current user as an ordinary user, so the presence in
	// Saved Messages is the user's own status and would be drawn as
	// "Online" over a conversation with nobody.
	ownUserID int64
}

// LiveConnectionState is the part of the Telegram live state the status
// line needs.
//
// It is a narrow interface rather than *telegram.LiveState so that the
// adapter can be tested without a session, and so that adding a field to
// the store cannot silently change what the interface is told.
type LiveConnectionState interface {
	ConnectionState() telegram.ConnectionState
	ChatPresence(chatID telegram.ChatID) telegram.Presence
}

// NewLiveStatusSummarySource builds the source.
//
// The live state may be nil: a program with no session has no connection
// to report, and the queue counters are still worth showing. The health
// source may not: without it there is nothing to count, and a source that
// reported an empty queue for a store it never read would be a lie.
func NewLiveStatusSummarySource(
	live LiveConnectionState,
	health MessageDeliveryHealthSource,
	ownUserID int64,
) (*LiveStatusSummarySource, error) {
	if health == nil {
		return nil, errors.New("status summary source: health source is required")
	}

	return &LiveStatusSummarySource{
		live:      live,
		health:    health,
		ownUserID: ownUserID,
	}, nil
}

// ReadStatusSummary returns the status line's data.
//
// A failed queue read is returned as an error together with everything
// that was read, so the interface can show the connection and leave the
// counts out rather than show counts from a moment ago. The error carries
// a cause that can name a path, and it goes to the diagnostic stream; the
// summary never carries it.
func (s *LiveStatusSummarySource) ReadStatusSummary(
	ctx context.Context,
	chatID int64,
) (tui.StatusSummary, error) {
	if s == nil || s.health == nil {
		return tui.StatusSummary{}, ErrMessageDeliveryHealthUnavailable
	}
	if err := ctx.Err(); err != nil {
		return tui.StatusSummary{}, err
	}

	summary := tui.StatusSummary{
		Connection: tuiConnectionState(s.liveState()),
		Presence:   s.presence(chatID),
	}

	health, err := s.health.ReadMessageDeliveryHealth(ctx)
	if err != nil {
		return summary, err
	}

	summary.Queue = tui.QueueSummary{
		Known: true,
		// A failed entry that will be tried again is what "retrying" means
		// to a user. A dispatching entry is neither: it is on its way out,
		// and the message under the cursor says so.
		Queued:     int(health.Queued),
		Retrying:   int(health.FailedRetryable),
		Recovering: health.State == MessageDeliveryHealthStarting,
	}

	return summary, nil
}

// presence returns what the store knows about the other side of a chat.
//
// A zero chatID is a program with no conversation open, and the presence of
// no chat is nothing. A presence the store does not have is nothing too, and
// the interface has no word for "I do not know" about a person.
func (s *LiveStatusSummarySource) presence(chatID int64) tui.Presence {
	if s == nil || s.live == nil || chatID == 0 {
		return tui.Presence{}
	}

	return tuiPresence(
		s.live.ChatPresence(telegram.ChatID(chatID)),
		s.ownUserID,
	)
}

// tuiPresence maps what the store keeps onto what the header draws.
//
// The times come through as they are: whether an online status has run out
// is a question about the clock, and the clock is the view's.
func tuiPresence(presence telegram.Presence, ownUserID int64) tui.Presence {
	switch presence.Kind {
	case telegram.PresenceGroup:
		return tui.Presence{
			Kind:          tui.PresenceGroup,
			OnlineMembers: int(presence.OnlineMemberCount),
		}

	case telegram.PresenceOneUser:
		out := tui.Presence{
			Kind: tui.PresenceUser,
			Bot:  presence.Bot,
			// A zero own id means the question was never answered, and a
			// zero id belongs to no user, so nothing is a chat with
			// oneself by accident.
			Self: ownUserID != 0 && presence.UserID == ownUserID,
		}

		switch presence.Status.Kind {
		case telegram.UserStatusOnline:
			out.ExpiresAt = presence.Status.Expires
		case telegram.UserStatusOffline:
			out.LastSeenAt = presence.Status.WasOnline
		case telegram.UserStatusRecently:
			out.Recency = tui.RecencyRecently
		case telegram.UserStatusLastWeek:
			out.Recency = tui.RecencyLastWeek
		case telegram.UserStatusLastMonth:
			out.Recency = tui.RecencyLastMonth
		}

		return out

	default:
		return tui.Presence{}
	}
}

// liveState returns the connection the Telegram store last heard about.
//
// A nil store answers ConnectionStateUnknown from its own method, so a
// session that has none is a session whose connection is unknown rather
// than a panic on the first poll.
func (s *LiveStatusSummarySource) liveState() telegram.ConnectionState {
	if s.live == nil {
		return telegram.ConnectionStateUnknown
	}

	return s.live.ConnectionState()
}

// tuiConnectionState maps what TDLib reports onto the states the interface
// names.
//
// Every constructor of the pinned schema is listed. A value that is not one
// of them is a state this interface does not know, and it is mapped to the
// unknown state rather than to a neighbour: guessing would put a word on
// the screen that nothing checked.
func tuiConnectionState(state telegram.ConnectionState) tui.ConnectionState {
	switch state {
	case telegram.ConnectionStateReady:
		return tui.ConnectionReady
	case telegram.ConnectionStateConnecting:
		return tui.ConnectionConnecting
	case telegram.ConnectionStateUpdating:
		return tui.ConnectionUpdating
	case telegram.ConnectionStateWaitingForNetwork:
		return tui.ConnectionWaitingForNetwork
	case telegram.ConnectionStateConnectingToProxy:
		return tui.ConnectionConnectingToProxy
	default:
		return tui.ConnectionUnknown
	}
}
