package application

import (
	"context"
	"time"
)

// ownUserIDTimeout bounds the getMe call.
//
// The own identifier is one round trip to a local TDLib, and the presence
// of a chat with oneself is the only thing it is for. A slow answer must
// not hold the interface behind it, so the wait is short and the interface
// starts without the answer.
const ownUserIDTimeout = 5 * time.Second

// ownUserResolver is the part of a session the own identifier needs.
type ownUserResolver interface {
	GetMeUserID(ctx context.Context) (int64, error)
}

// resolveOwnUserID returns the identifier of the user this client is
// authorized as, or zero when the question could not be answered.
//
// Zero means "not known", and a store cannot tell a chat with oneself from
// a chat with a contact without it: Telegram sends the current user as an
// ordinary user. The interface then shows no presence in Saved Messages,
// which is the smaller mistake next to showing the user's own Online there.
func resolveOwnUserID(
	parent context.Context,
	session ownUserResolver,
) (int64, error) {
	if parent == nil {
		parent = context.Background()
	}
	if session == nil {
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(parent, ownUserIDTimeout)
	defer cancel()

	id, err := session.GetMeUserID(ctx)
	if err != nil {
		return 0, err
	}

	return id, nil
}
