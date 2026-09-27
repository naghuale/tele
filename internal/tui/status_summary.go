package tui

import (
	"context"
)

// ConnectionState is the link between telecli and Telegram, as the client
// reports it.
//
// The names are the ones TDLib uses, with the separator of this
// interface: a value that travels to a log reads the way TDLib wrote it,
// and a value that travels to a screen is drawn with the interface's own
// words (see view_status.go).
type ConnectionState string

const (
	// ConnectionUnknown means nobody has said yet, or said something this
	// interface does not know. It is not a connection TDLib has, and a
	// status line must not turn it into one: telling a user they are
	// connected to a client that has not answered is a claim nobody
	// checked.
	ConnectionUnknown ConnectionState = "unknown"

	// ConnectionWaitingForNetwork means there is no network yet.
	ConnectionWaitingForNetwork ConnectionState = "waiting-for-network"

	// ConnectionConnectingToProxy means a proxy is being reached.
	ConnectionConnectingToProxy ConnectionState = "connecting-to-proxy"

	// ConnectionConnecting means the Telegram servers are being reached.
	ConnectionConnecting ConnectionState = "connecting"

	// ConnectionUpdating means the client is downloading what it missed
	// while it was offline.
	ConnectionUpdating ConnectionState = "updating"

	// ConnectionReady means there is a working connection.
	ConnectionReady ConnectionState = "ready"
)

// QueueSummary is what the durable message queue holds.
//
// Known is not decoration. A queue that could not be read is not a queue
// with nothing in it, and a status line that says "0 queued" for a queue
// it failed to open tells a user their messages are gone.
type QueueSummary struct {
	// Known reports whether the queue could be read at all.
	Known bool

	// Queued counts the entries waiting to be sent.
	Queued int

	// Retrying counts the entries that failed and will be tried again.
	Retrying int

	// Recovering reports that the delivery runtime is still starting, so
	// the entries a previous run left behind are being picked up.
	Recovering bool
}

// StatusSummary is what the status line says that is not about one chat.
//
// The two parts are independent by design (§4.3): the connection is TDLib
// telling us about the network, the queue is the durable store telling us
// about messages, and one of them failing must not take the other off the
// screen.
type StatusSummary struct {
	Connection ConnectionState
	Queue      QueueSummary
}

// StatusSummarySource reads the data of the status line.
//
// It is separate from MessageStatusSource on purpose. That one is
// payload-free because its values travel to logs and reports; this one
// carries counters and a connection state, which is all the status line may
// show, and it is read for the whole program rather than for one chat.
//
// A nil source means the interface has nothing to say: a program built
// without Telegram draws no status line rather than an empty one.
type StatusSummarySource interface {
	ReadStatusSummary(ctx context.Context) (StatusSummary, error)
}
