package telegram

import (
	"encoding/json"
	"fmt"
)

// ConnectionState is what the client knows about its link to Telegram
// servers, as TDLib reports it.
//
// The names and the payload follow the pinned TDLib schema,
// td/generate/scheme/td_api.tl at commit ea97bcdd (TDLib 1.8.67):
//
//	connectionStateWaitingForNetwork = ConnectionState;   // :9927
//	connectionStateConnectingToProxy = ConnectionState;   // :9930
//	connectionStateConnecting = ConnectionState;          // :9933
//	connectionStateUpdating = ConnectionState;            // :9936
//	connectionStateReady = ConnectionState;               // :9939
//	updateConnectionState state:ConnectionState = Update; // :10974
//
// The wire form is an object of the ConnectionState class, so the state
// is a nested object with its own @type and not a string.
type ConnectionState string

const (
	// ConnectionStateUnknown means the store has not been told, or has
	// been told something it does not know. It is not a connection state
	// TDLib has: it is the absence of one, and a consumer must not read
	// anything into it.
	ConnectionStateUnknown ConnectionState = "unknown"

	// ConnectionStateWaitingForNetwork means the network is not available
	// yet.
	ConnectionStateWaitingForNetwork ConnectionState = "waiting-for-network"

	// ConnectionStateConnectingToProxy means a proxy connection is being
	// established.
	ConnectionStateConnectingToProxy ConnectionState = "connecting-to-proxy"

	// ConnectionStateConnecting means the connection to the Telegram
	// servers is being established.
	ConnectionStateConnecting ConnectionState = "connecting"

	// ConnectionStateUpdating means the client is downloading what it
	// missed while it was offline.
	ConnectionStateUpdating ConnectionState = "updating"

	// ConnectionStateReady means there is a working connection.
	ConnectionStateReady ConnectionState = "ready"
)

// IsKnown reports whether s is a state TDLib can report.
func (s ConnectionState) IsKnown() bool {
	switch s {
	case ConnectionStateWaitingForNetwork,
		ConnectionStateConnectingToProxy,
		ConnectionStateConnecting,
		ConnectionStateUpdating,
		ConnectionStateReady:
		return true

	default:
		return false
	}
}

// String returns the wire name, so a value on its way to a log reads the
// way TDLib writes it.
func (s ConnectionState) String() string {
	if s == "" {
		return string(ConnectionStateUnknown)
	}

	return string(s)
}

// connectionStateConstructors maps the TDLib constructors of the pinned
// schema to the states the store keeps.
//
// A constructor that is missing here is one the pinned schema does not
// have. It maps to ConnectionStateUnknown on purpose: the interface is
// then told that the client stopped saying what it was saying, which is
// the truth, instead of keeping the last state and letting a stale "ready"
// be read as the present.
var connectionStateConstructors = map[string]ConnectionState{
	"connectionStateWaitingForNetwork": ConnectionStateWaitingForNetwork,
	"connectionStateConnectingToProxy": ConnectionStateConnectingToProxy,
	"connectionStateConnecting":        ConnectionStateConnecting,
	"connectionStateUpdating":          ConnectionStateUpdating,
	"connectionStateReady":             ConnectionStateReady,
}

// connectionStateUpdateJSON is the shape of updateConnectionState: one
// field, and the state is an object of the ConnectionState class.
type connectionStateUpdateJSON struct {
	State struct {
		Type string `json:"@type"`
	} `json:"state"`
}

// ConnectionState returns what the store last heard about the link to
// Telegram servers.
//
// The value is a copy: a caller cannot change the store through it. The
// zero value is ConnectionStateUnknown, and a nil store answers
// ConnectionStateUnknown rather than panicking, so a caller without a
// session can ask without a check first.
func (l *LiveState) ConnectionState() ConnectionState {
	if l == nil {
		return ConnectionStateUnknown
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.connection == "" {
		return ConnectionStateUnknown
	}

	return l.connection
}

// applyConnectionState applies one updateConnectionState and reports
// whether the state changed.
//
// An update whose constructor the pinned schema does not have is applied
// as ConnectionStateUnknown rather than reported as an error: the
// payload is well formed, and a new constructor in a newer TDLib is a
// protocol growth, not a broken one. The interface loses the connection
// word instead of being told a wrong one.
func (l *LiveState) applyConnectionState(raw RawMessage) (bool, error) {
	var update connectionStateUpdateJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return false, fmt.Errorf("decode connection state update: %w", err)
	}

	state, known := connectionStateConstructors[update.State.Type]
	if !known {
		state = ConnectionStateUnknown
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.connection == state {
		return false, nil
	}
	l.connection = state
	l.signalChanged()

	return true, nil
}
