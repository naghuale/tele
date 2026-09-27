package telegram

import (
	"testing"
)

// The fixtures below follow the pinned TDLib schema,
// td/generate/scheme/td_api.tl at commit ea97bcdd (TDLib 1.8.67):
//
//	connectionStateWaitingForNetwork = ConnectionState;            // :9927
//	connectionStateConnectingToProxy = ConnectionState;             // :9930
//	connectionStateConnecting = ConnectionState;                   // :9933
//	connectionStateUpdating = ConnectionState;                     // :9936
//	connectionStateReady = ConnectionState;                        // :9939
//	updateConnectionState state:ConnectionState = Update;          // :10974
//
// The state is an object of the ConnectionState class, so it is a
// `state` field with its own @type, not a string. A fixture written as a
// string would keep both the parser and this file wrong.

func connectionStateUpdate(constructor string) RawMessage {
	return RawMessage(`{"@type":"updateConnectionState","state":{"@type":"` +
		constructor + `"}}`)
}

func TestAConnectionStateIsTheOneTDLibNames(t *testing.T) {
	for constructor, want := range map[string]ConnectionState{
		"connectionStateWaitingForNetwork": ConnectionStateWaitingForNetwork,
		"connectionStateConnectingToProxy": ConnectionStateConnectingToProxy,
		"connectionStateConnecting":        ConnectionStateConnecting,
		"connectionStateUpdating":          ConnectionStateUpdating,
		"connectionStateReady":             ConnectionStateReady,
	} {
		state := NewLiveState()
		changed, err := state.apply(connectionStateUpdate(constructor))
		if err != nil {
			t.Fatalf("apply(%s): %v", constructor, err)
		}
		if !changed {
			t.Fatalf("apply(%s) reported no change", constructor)
		}
		if got := state.ConnectionState(); got != want {
			t.Fatalf("ConnectionState() = %q after %s, want %q", got, constructor, want)
		}
	}
}

// A store that has never seen an update does not know the connection, and
// says so instead of guessing: a status line that claims "Connected"
// before TDLib has said anything is a status line that is wrong.
func TestANewStoreHasNoConnectionState(t *testing.T) {
	if got := NewLiveState().ConnectionState(); got != ConnectionStateUnknown {
		t.Fatalf("ConnectionState() = %q, want unknown", got)
	}
}

// A repeated state is not a change. TDLib sends the state it is already
// in more than once, and a consumer woken for nothing reads and redraws
// for nothing.
func TestTheSameConnectionStateDoesNotSignal(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(connectionStateUpdate("connectionStateConnecting")); err != nil {
		t.Fatal(err)
	}
	drainChanged(state)

	changed, err := state.apply(connectionStateUpdate("connectionStateConnecting"))
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("the same connection state reported a change")
	}
	select {
	case <-state.Changed():
		t.Fatal("an unchanged connection state must not signal")
	default:
	}
	if got := state.ConnectionState(); got != ConnectionStateConnecting {
		t.Fatalf("ConnectionState() = %q, want connecting", got)
	}
}

func TestAChangedConnectionStateSignals(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(connectionStateUpdate("connectionStateConnecting")); err != nil {
		t.Fatal(err)
	}
	drainChanged(state)

	if _, err := state.apply(connectionStateUpdate("connectionStateReady")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-state.Changed():
	default:
		t.Fatal("a changed connection state must signal")
	}
	if got := state.ConnectionState(); got != ConnectionStateReady {
		t.Fatalf("ConnectionState() = %q, want ready", got)
	}
}

// A constructor the pinned schema does not have is not malformed JSON and
// not a chat position, so it is neither an error nor a guess: the store
// reports that it does not know, and the last known state is gone rather
// than left to be read as the truth.
func TestAnUnknownConnectionStateReplacesTheKnownOne(t *testing.T) {
	for name, raw := range map[string]RawMessage{
		"unknown constructor": connectionStateUpdate("connectionStateTeleporting"),
		"no state field":      RawMessage(`{"@type":"updateConnectionState"}`),
	} {
		state := NewLiveState()
		if _, err := state.apply(connectionStateUpdate("connectionStateReady")); err != nil {
			t.Fatal(err)
		}

		changed, err := state.apply(raw)
		if err != nil {
			t.Fatalf("%s: apply: %v", name, err)
		}
		if !changed {
			t.Fatalf("%s: the store must report that it stopped knowing", name)
		}
		if got := state.ConnectionState(); got != ConnectionStateUnknown {
			t.Fatalf("%s: ConnectionState() = %q, want unknown", name, got)
		}
	}
}

// A payload that is not an envelope at all is malformed, and the store
// says so instead of quietly going blank.
func TestAMalformedConnectionUpdateIsAnError(t *testing.T) {
	state := NewLiveState()
	if _, err := state.apply(connectionStateUpdate("connectionStateReady")); err != nil {
		t.Fatal(err)
	}

	if _, err := state.apply(RawMessage(`{"@type":`)); err == nil {
		t.Fatal("a malformed update must be reported as an error")
	}
	if got := state.ConnectionState(); got != ConnectionStateReady {
		t.Fatalf("ConnectionState() = %q, want the state to be kept", got)
	}
}

// The connection state is the one update a user needs before the first
// message, and TDLib sends it during the login wait. Losing it would put
// the interface at "unknown" after a successful login, so the
// authorization phase holds it like the chat list.
func TestTheConnectionStateIsHeldDuringAuthorization(t *testing.T) {
	for constructor := range map[string]struct{}{
		"connectionStateWaitingForNetwork": {},
		"connectionStateConnectingToProxy": {},
		"connectionStateConnecting":        {},
		"connectionStateUpdating":          {},
		"connectionStateReady":             {},
	} {
		if !isLiveStateUpdate(connectionStateUpdate(constructor)) {
			t.Fatalf("%s must be held during authorization", constructor)
		}
	}
}

// The state travels to nothing else: a status line reads the store, and
// the store's own copies are what it hands out.
func TestConnectionStateIsAValue(t *testing.T) {
	if got := ConnectionStateReady.String(); got != "ready" {
		t.Fatalf("String() = %q, want ready", got)
	}
	if ConnectionState("ready").IsKnown() != true {
		t.Fatal("ready must be a known state")
	}
	for _, state := range []ConnectionState{
		ConnectionStateWaitingForNetwork,
		ConnectionStateConnectingToProxy,
		ConnectionStateConnecting,
		ConnectionStateUpdating,
		ConnectionStateReady,
	} {
		if !state.IsKnown() {
			t.Fatalf("%q must be a known state", state)
		}
		if state == ConnectionStateUnknown {
			t.Fatal("a named state must not be the unknown state")
		}
	}
}

// A session that has not built its store yet still has to answer: the
// first poll of a program that is starting asks, and a nil store that
// panicked would take the interface down for a condition that is normal.
func TestANilStoreAnswersTheUnknownState(t *testing.T) {
	var state *LiveState
	if got := state.ConnectionState(); got != ConnectionStateUnknown {
		t.Fatalf("ConnectionState() = %q, want unknown", got)
	}
	if state.Changed() != nil {
		t.Fatal("a nil store must not hand out a change signal")
	}
}
