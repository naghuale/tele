package recorder

import "testing"

func TestValidComponentID(t *testing.T) {
	good := []ComponentID{
		"a",
		"event.queue",
		"tdlib.receive",
		"event.dispatcher",
		"foo_bar",
		"foo-bar",
		"a0.b1-c2_d3",
	}
	for _, id := range good {
		if !ValidComponentID(id) {
			t.Fatalf("%q should be valid", id)
		}
	}

	bad := []ComponentID{
		"",
		"Event.Queue",
		"event/queue",
		"event queue",
		"event:queue",
	}
	for _, id := range bad {
		if ValidComponentID(id) {
			t.Fatalf("%q should be invalid", id)
		}
	}
}
