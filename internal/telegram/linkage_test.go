package telegram

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestLinkageRequestIsCredentialFreeAndExact(t *testing.T) {
	request, err := buildLinkageVersionRequest()
	if err != nil {
		t.Fatalf("buildLinkageVersionRequest: %v", err)
	}

	var document map[string]any
	if err := json.Unmarshal(request, &document); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	if got := document["@type"]; got != "getOption" {
		t.Fatalf("@type = %#v, want getOption", got)
	}
	if got := document["name"]; got != "version" {
		t.Fatalf("name = %#v, want version", got)
	}
	if len(document) != 2 {
		t.Fatalf("field count = %d, want 2", len(document))
	}
	for _, forbidden := range []string{
		"api_hash", "phone_number", "api_id", "password",
		"@extra", "@client_id", "settings",
	} {
		if _, exists := document[forbidden]; exists {
			t.Fatalf("forbidden field present: %s", forbidden)
		}
	}
}

func TestLinkageResultReportsFailure(t *testing.T) {
	passing := LinkageResult{
		ClientCreated:   LinkageStatusPass,
		SendReceive:     LinkageStatusPass,
		ExtraRoundTrip:  LinkageStatusPass,
		ClientIDMatched: LinkageStatusPass,
	}
	if passing.Failed() {
		t.Fatal("a fully passing result must not report failure")
	}

	for name, mutate := range map[string]func(*LinkageResult){
		"client":    func(r *LinkageResult) { r.ClientCreated = LinkageStatusFail },
		"send":      func(r *LinkageResult) { r.SendReceive = LinkageStatusFail },
		"extra":     func(r *LinkageResult) { r.ExtraRoundTrip = LinkageStatusFail },
		"clientID":  func(r *LinkageResult) { r.ClientIDMatched = LinkageStatusFail },
		"unknown":   func(r *LinkageResult) { r.SendReceive = LinkageStatusUnknown },
		"zeroState": func(r *LinkageResult) { r.ClientIDMatched = LinkageStatusUnknown },
	} {
		result := passing
		mutate(&result)
		if !result.Failed() {
			t.Fatalf("case %s: a non-passing result must report failure", name)
		}
	}
}

func TestLinkageStatusStringIsClosed(t *testing.T) {
	cases := map[LinkageStatus]string{
		LinkageStatusUnknown: "UNKNOWN",
		LinkageStatusPass:    "PASS",
		LinkageStatusFail:    "FAIL",
		LinkageStatus(99):    "UNKNOWN",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Fatalf("LinkageStatus(%d).String() = %q, want %q", status, got, want)
		}
	}
}

// newLinkageProbeClient builds a client whose update channel is fed by the test
// so correlation can be exercised without a native library.
func newLinkageProbeClient(t *testing.T, buffered int) *Client {
	t.Helper()
	return &Client{
		id:      4242,
		updates: make(chan Update, buffered),
		errors:  make(chan error, buffered),
	}
}

func linkageResponse(t *testing.T, clientID int, extra string, body string) RawMessage {
	t.Helper()
	document := map[string]any{
		"@type":      "optionValueString",
		"@client_id": clientID,
		"value":      body,
	}
	if extra != "" {
		document["@extra"] = extra
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	return RawMessage(encoded)
}

func TestLinkageAcceptsOnlyExactExtraOnExactClient(t *testing.T) {
	queryID := newQueryID(4242)

	client := newLinkageProbeClient(t, 4)
	client.updates <- Update{
		ClientID: 4242,
		Raw:      linkageResponse(t, 4242, string(queryID), "1.8.67"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	matched, raw, err := awaitLinkageResponse(ctx, client, queryID)
	if err != nil {
		t.Fatalf("awaitLinkageResponse: %v", err)
	}
	if !matched {
		t.Fatal("the exact @extra on the exact client must be accepted")
	}

	var value optionValueString
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode accepted response: %v", err)
	}
	if value.Value != "1.8.67" {
		t.Fatalf("value = %q, want 1.8.67", value.Value)
	}
}

func TestLinkageRejectsForeignAndMalformedResponses(t *testing.T) {
	queryID := newQueryID(4242)

	cases := map[string]Update{
		"foreign extra": {
			ClientID: 4242,
			Raw:      linkageResponse(t, 4242, "telecli:9999:1", "1.8.67"),
		},
		"missing extra": {
			ClientID: 4242,
			Raw:      linkageResponse(t, 4242, "", "1.8.67"),
		},
		"malformed extra": {
			ClientID: 4242,
			Raw:      RawMessage(`{"@type":"optionValueString","@client_id":4242,"@extra":{"nested":true}}`),
		},
		"foreign client": {
			ClientID: 7777,
			Raw:      linkageResponse(t, 7777, string(queryID), "1.8.67"),
		},
	}

	for name, update := range cases {
		update := update
		t.Run(name, func(t *testing.T) {
			client := newLinkageProbeClient(t, len(cases)+2)
			for _, other := range cases {
				other := other
				if other.ClientID == update.ClientID && string(other.Raw) == string(update.Raw) {
					continue
				}
				client.updates <- other
			}
			client.updates <- update

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			matched, _, err := awaitLinkageResponse(ctx, client, queryID)
			if err == nil && matched {
				t.Fatalf("case %s must not be accepted as the response", name)
			}
		})
	}
}

func TestLinkageNeverAcceptsAnErrorObject(t *testing.T) {
	queryID := newQueryID(4242)

	client := newLinkageProbeClient(t, 1)
	encoded, err := json.Marshal(map[string]any{
		"@type":      "error",
		"@client_id": 4242,
		"@extra":     string(queryID),
		"code":       400,
		"message":    "an identifier that must never be printed",
	})
	if err != nil {
		t.Fatalf("encode error response: %v", err)
	}
	client.updates <- Update{ClientID: 4242, Raw: RawMessage(encoded)}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	matched, raw, err := awaitLinkageResponse(ctx, client, queryID)
	if err == nil && matched {
		t.Fatal("an error object must never count as a successful round trip")
	}
	if len(raw) != 0 {
		t.Fatal("a rejected response must not be handed back to the caller")
	}
	// The probe must not surface the backend message.
	if err != nil && containsSubstring(err.Error(), "identifier that must never be printed") {
		t.Fatal("the backend message leaked into the probe error")
	}
}

func TestLinkageStopsAtTheIgnoredUpdateLimit(t *testing.T) {
	queryID := newQueryID(4242)

	client := newLinkageProbeClient(t, linkageMaxIgnoredUpdates+4)
	for i := 0; i < linkageMaxIgnoredUpdates+3; i++ {
		client.updates <- Update{
			ClientID: 4242,
			Raw:      linkageResponse(t, 4242, "telecli:1:1", "1.8.67"),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if _, _, err := awaitLinkageResponse(ctx, client, queryID); err == nil {
		t.Fatal("the probe must give up once the ignored-update bound is exceeded")
	}
}

func TestLinkageTreatsClosedUpdateChannelAsFailure(t *testing.T) {
	queryID := newQueryID(4242)

	client := newLinkageProbeClient(t, 1)
	close(client.updates)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if _, _, err := awaitLinkageResponse(ctx, client, queryID); err == nil {
		t.Fatal("a closed update channel must not be reported as a round trip")
	}
}

func TestLinkageNeverPrintsForbiddenFieldNames(t *testing.T) {
	result := LinkageResult{
		Source:          NativeLibrarySourcePackaged,
		Version:         "1.8.67",
		Commit:          "ea97bcdd3a15523c58ddfe772b4547187cf5bbeb",
		Compatibility:   CompatibilityVerified,
		ClientCreated:   LinkageStatusPass,
		SendReceive:     LinkageStatusPass,
		ExtraRoundTrip:  LinkageStatusPass,
		ClientIDMatched: LinkageStatusPass,
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("encode result: %v", err)
	}
	for _, forbidden := range []string{
		"api_hash", "phone_number", "authentication_code", "password", "library_path",
	} {
		if containsSubstring(string(encoded), forbidden) {
			t.Fatalf("result encoding leaked %s", forbidden)
		}
	}
}

func containsSubstring(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}
