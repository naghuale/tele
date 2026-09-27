package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- withQueryID ----

func TestWithQueryIDCopiesRequest(t *testing.T) {
	original := RawMessage(`{"@type":"getChats","limit":50}`)
	originalCopy := append(RawMessage(nil), original...)

	got, err := withQueryID(original, "telecli:1:1")
	if err != nil {
		t.Fatalf("withQueryID: %v", err)
	}
	if string(original) != string(originalCopy) {
		t.Fatalf("withQueryID mutated the caller's slice")
	}

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["@extra"] != "telecli:1:1" {
		t.Fatalf("@extra = %v", decoded["@extra"])
	}
	if decoded["@type"] != "getChats" {
		t.Fatalf("@type = %v", decoded["@type"])
	}
}

func TestWithQueryIDRejectsEmptyRequest(t *testing.T) {
	if _, err := withQueryID(nil, "telecli:1:1"); !errors.Is(err, ErrQueryInvalidRequest) {
		t.Fatalf("err = %v, want ErrQueryInvalidRequest", err)
	}
}

func TestWithQueryIDRejectsArray(t *testing.T) {
	_, err := withQueryID(RawMessage(`[1,2,3]`), "telecli:1:1")
	if !errors.Is(err, ErrQueryInvalidRequest) {
		t.Fatalf("err = %v, want ErrQueryInvalidRequest", err)
	}
}

func TestWithQueryIDRejectsEmptyQueryID(t *testing.T) {
	_, err := withQueryID(RawMessage(`{"@type":"x"}`), "")
	if !errors.Is(err, ErrQueryInvalidRequest) {
		t.Fatalf("err = %v, want ErrQueryInvalidRequest", err)
	}
}

func TestWithQueryIDRejectsExistingExtra(t *testing.T) {
	_, err := withQueryID(
		RawMessage(`{"@type":"getChats","@extra":"caller"}`),
		"telecli:1:1",
	)
	if !errors.Is(err, ErrQueryInvalidRequest) {
		t.Fatalf("error = %v, want ErrQueryInvalidRequest", err)
	}
}

// ---- responseQueryID ----

func TestResponseQueryIDMissing(t *testing.T) {
	_, owned, err := responseQueryID(RawMessage(`{"@type":"ok"}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if owned {
		t.Fatal("owned = true, want false")
	}
}

func TestResponseQueryIDString(t *testing.T) {
	id, owned, err := responseQueryID(
		RawMessage(`{"@type":"ok","@extra":"telecli:1:5"}`),
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !owned {
		t.Fatal("owned = false, want true")
	}
	if id != "telecli:1:5" {
		t.Fatalf("id = %q, want telecli:1:5", id)
	}
}

func TestResponseQueryIDIgnoresObjectExtra(t *testing.T) {
	queryID, owned, err := responseQueryID(
		RawMessage(`{"@type":"updateSome","@extra":{"source":"other"}}`),
	)
	if err != nil {
		t.Fatalf("responseQueryID() error = %v", err)
	}
	if owned {
		t.Fatal("object @extra must not be owned by query router")
	}
	if queryID != "" {
		t.Fatalf("query ID = %q, want empty", queryID)
	}
}

func TestResponseQueryIDIgnoresForeignStringExtra(t *testing.T) {
	queryID, owned, err := responseQueryID(
		RawMessage(`{"@type":"updateSome","@extra":"other:9:9"}`),
	)
	if err != nil {
		t.Fatalf("responseQueryID() error = %v", err)
	}
	if owned {
		t.Fatal("foreign string @extra must not be owned")
	}
	if queryID != "" {
		t.Fatalf("query ID = %q, want empty", queryID)
	}
}

func TestIsTelecliQueryID(t *testing.T) {
	if !isTelecliQueryID("telecli:1:1") {
		t.Fatal("telecli:1:1 should be in namespace")
	}
	if isTelecliQueryID("other:1:1") {
		t.Fatal("other:1:1 must not be in namespace")
	}
	if isTelecliQueryID("") {
		t.Fatal("empty ID must not be in namespace")
	}
}

// ---- decodeQueryResult ----

func TestDecodeQueryResultSuccess(t *testing.T) {
	raw := RawMessage(`{"@type":"chats","chat_ids":[1,2]}`)
	got, err := decodeQueryResult(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("got %s, want %s", got, raw)
	}
}

func TestDecodeQueryResultTDLibError(t *testing.T) {
	raw := RawMessage(`{"@type":"error","code":400,"message":"bad","@extra":"telecli:1:9"}`)
	_, err := decodeQueryResult(raw)
	if !errors.Is(err, ErrTDLibResponse) {
		t.Fatalf("err = %v, want ErrTDLibResponse", err)
	}
	var tdErr *TDLibError
	if !errors.As(err, &tdErr) {
		t.Fatalf("err = %v, want *TDLibError", err)
	}
	if tdErr.Code != 400 {
		t.Fatalf("Code = %d, want 400", tdErr.Code)
	}
	if tdErr.Message != "bad" {
		t.Fatalf("Message = %q, want bad", tdErr.Message)
	}
	if tdErr.Extra != "telecli:1:9" {
		t.Fatalf("Extra = %q", tdErr.Extra)
	}
}

// ---- helpers ----

// waitForSentRequest returns the most recently sent request.
func waitForSentRequest(t *testing.T, sender *fakeSender) RawMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sender.mu.Lock()
		if len(sender.requests) > 0 {
			req := append(RawMessage(nil), sender.requests[len(sender.requests)-1]...)
			sender.mu.Unlock()
			return req
		}
		sender.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no request sent")
	return nil
}

// extractExtra reads @extra from a serialized request.
func extractExtra(t *testing.T, request RawMessage) string {
	t.Helper()
	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return decoded.Extra
}

// waitForNewExtra waits until a request carrying an @extra other than
// previous has been sent, and returns that identifier.
//
// waitForSentRequest returns the most recent request without removing it,
// so a test that sent an earlier request must wait for a distinguishable
// one rather than read whatever happens to be newest.
func waitForNewExtra(
	t *testing.T,
	sender *fakeSender,
	previous string,
) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if extra := extractExtra(t, waitForSentRequest(t, sender)); extra != previous {
			return extra
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no request with a new @extra (previous %q)", previous)
	return ""
}

// ---- Query happy paths ----

func TestQueryRoutesMatchingResponse(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		raw RawMessage
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		raw, err := session.Query(
			context.Background(),
			RawMessage(`{"@type":"getChats","limit":1}`),
		)
		resultCh <- result{raw: raw, err: err}
	}()

	request := waitForSentRequest(t, sender)
	extra := extractExtra(t, request)

	resp := RawMessage(`{"@type":"chats","chat_ids":[],"@extra":"` + extra + `"}`)
	client.updates <- Update{ClientID: client.id, Raw: resp}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("Query err = %v", r.err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(r.raw, &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if decoded["@type"] != "chats" {
			t.Fatalf("@type = %v", decoded["@type"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Query did not return")
	}
}

func TestQueryReturnsTDLibError(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		raw RawMessage
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		raw, err := session.Query(
			context.Background(),
			RawMessage(`{"@type":"getChats","limit":1}`),
		)
		resultCh <- result{raw: raw, err: err}
	}()

	request := waitForSentRequest(t, sender)
	extra := extractExtra(t, request)

	resp := RawMessage(`{"@type":"error","code":400,"message":"bad","@extra":"` + extra + `"}`)
	client.updates <- Update{ClientID: client.id, Raw: resp}

	select {
	case r := <-resultCh:
		if !errors.Is(r.err, ErrTDLibResponse) {
			t.Fatalf("err = %v, want ErrTDLibResponse", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Query did not return")
	}
}

func TestQueryIgnoresUnrelatedUpdate(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		raw RawMessage
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		raw, err := session.Query(
			context.Background(),
			RawMessage(`{"@type":"getChats","limit":1}`),
		)
		resultCh <- result{raw: raw, err: err}
	}()

	request := waitForSentRequest(t, sender)
	extra := extractExtra(t, request)

	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"updateNewChat","chat":{"id":1}}`),
	}
	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"ok","@extra":"` + extra + `"}`),
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("Query err = %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Query did not return")
	}
}

// ---- Cancellation ----

func TestQueryContextCancellationRemovesPending(t *testing.T) {
	session, sender, _, _ := newSessionWithFakes(t)

	ctx, cancel := context.WithCancel(context.Background())

	type result struct {
		raw RawMessage
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		raw, err := session.Query(ctx, RawMessage(`{"@type":"getChats","limit":1}`))
		resultCh <- result{raw: raw, err: err}
	}()

	waitForSentRequest(t, sender)
	cancel()

	select {
	case r := <-resultCh:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Query did not return after cancel")
	}

	session.queryMu.Lock()
	n := len(session.pendingQueries)
	session.queryMu.Unlock()
	if n != 0 {
		t.Fatalf("pending queries = %d, want 0", n)
	}
}

func TestLateResponseAfterCancellationIsConsumed(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	ctx, cancel := context.WithCancel(context.Background())

	type result struct {
		raw RawMessage
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		raw, err := session.Query(ctx, RawMessage(`{"@type":"getChats","limit":1}`))
		resultCh <- result{raw: raw, err: err}
	}()

	request := waitForSentRequest(t, sender)
	extra := extractExtra(t, request)
	cancel()

	select {
	case <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Query did not return")
	}

	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"ok","@extra":"` + extra + `"}`),
	}

	// A late reply must be consumed by the pump: it is neither handed to
	// a later query nor applied to the store.
	type second struct {
		raw RawMessage
		err error
	}
	secondCh := make(chan second, 1)
	go func() {
		raw, err := session.Query(
			context.Background(),
			RawMessage(`{"@type":"getMe"}`),
		)
		secondCh <- second{raw: raw, err: err}
	}()

	// waitForSentRequest returns the most recent request and does not pop,
	// so the first query's request is still there. Wait for one carrying a
	// different @extra instead of taking whatever is newest.
	secondExtra := waitForNewExtra(t, sender, extra)
	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"user","id":42,"@extra":"` + secondExtra + `"}`),
	}

	select {
	case r := <-secondCh:
		if r.err != nil {
			t.Fatalf("second Query: %v", r.err)
		}
		if !strings.Contains(string(r.raw), `"id":42`) {
			t.Fatalf("a later query was answered with a stale reply: %s", r.raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the second Query did not return")
	}

	if chats := session.LiveState().ChatList(); len(chats) != 0 {
		t.Fatalf("a query reply was applied to the store: %v", chats)
	}
}

// ---- Pump failure ----

func TestPumpFailureFailsPendingQuery(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		raw RawMessage
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		raw, err := session.Query(
			context.Background(),
			RawMessage(`{"@type":"getChats","limit":1}`),
		)
		resultCh <- result{raw: raw, err: err}
	}()

	waitForSentRequest(t, sender)
	close(client.updates)

	select {
	case r := <-resultCh:
		if r.err == nil {
			t.Fatal("expected error after pump exit")
		}
		if !errors.Is(r.err, ErrQuerySessionClosed) {
			t.Fatalf("err = %v, want ErrQuerySessionClosed", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Query did not return after pump exit")
	}
}

// ---- Send error ----

func TestQuerySendErrorRemovesPending(t *testing.T) {
	session, sender, _, _ := newSessionWithFakes(t)

	sendErr := errors.New("send failed")
	sender.err = sendErr

	_, err := session.Query(
		context.Background(),
		RawMessage(`{"@type":"getChats","limit":1}`),
	)
	if !errors.Is(err, sendErr) {
		t.Fatalf("err = %v, want send error", err)
	}

	session.queryMu.Lock()
	n := len(session.pendingQueries)
	session.queryMu.Unlock()
	if n != 0 {
		t.Fatalf("pending queries = %d, want 0", n)
	}
}

// ---- Concurrent ----

// TestConcurrentQueriesReceiveOwnResponses sends two queries with
// distinct test_marker fields so the test can bind each @extra to its
// originating goroutine without relying on the order in which the
// goroutines are scheduled.
func TestConcurrentQueriesReceiveOwnResponses(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		tag string
		raw RawMessage
		err error
	}
	results := make(chan result, 2)
	var waitGroup sync.WaitGroup

	mkQuery := func(tag string) {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()

			request, err := json.Marshal(map[string]any{
				"@type":       "getChats",
				"limit":       1,
				"test_marker": tag,
			})
			if err != nil {
				results <- result{tag: tag, err: err}
				return
			}

			raw, err := session.Query(
				context.Background(),
				RawMessage(request),
			)
			results <- result{tag: tag, raw: raw, err: err}
		}()
	}

	mkQuery("A")
	mkQuery("B")

	extrasByTag := make(map[string]string)
	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		sender.mu.Lock()
		for _, request := range sender.requests {
			var envelope struct {
				Extra      string `json:"@extra"`
				TestMarker string `json:"test_marker"`
			}
			if err := json.Unmarshal(request, &envelope); err != nil {
				sender.mu.Unlock()
				t.Fatalf("decode request: %v", err)
			}
			extrasByTag[envelope.TestMarker] = envelope.Extra
		}
		sender.mu.Unlock()

		if len(extrasByTag) == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	if extrasByTag["A"] == "" || extrasByTag["B"] == "" {
		t.Fatalf("query extras = %#v, want A and B", extrasByTag)
	}

	// Deliver responses in reverse order to prove per-query routing.
	client.updates <- Update{
		ClientID: client.id,
		Raw: RawMessage(
			`{"@type":"ok","tag":"B","@extra":"` + extrasByTag["B"] + `"}`,
		),
	}
	client.updates <- Update{
		ClientID: client.id,
		Raw: RawMessage(
			`{"@type":"ok","tag":"A","@extra":"` + extrasByTag["A"] + `"}`,
		),
	}

	seen := map[string]string{}
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatalf("Query %s err = %v", r.tag, r.err)
			}
			var decoded struct {
				Tag string `json:"tag"`
			}
			if err := json.Unmarshal(r.raw, &decoded); err != nil {
				t.Fatalf("decode: %v", err)
			}
			seen[r.tag] = decoded.Tag
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for query results")
		}
	}

	if seen["A"] != "A" || seen["B"] != "B" {
		t.Fatalf("mismatched routing: %+v", seen)
	}
	waitGroup.Wait()
}

// ---- Query on closed session ----

func TestQueryOnClosedSession(t *testing.T) {
	session, _, _, client := newSessionWithFakes(t)

	feedAuth(t, client, "authorizationStateClosed")

	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := session.Query(
		context.Background(),
		RawMessage(`{"@type":"getChats","limit":1}`),
	)
	if err == nil {
		t.Fatal("expected error on closed session")
	}
}

// ---- Namespace check via routeQueryResponse ----

func TestRouteQueryResponseIgnoresForeignExtra(t *testing.T) {
	session, _, _, _ := newSessionWithFakes(t)

	raw := RawMessage(`{"@type":"updateSome","@extra":"other:9:9"}`)
	routed, err := session.routeQueryResponse(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if routed {
		t.Fatal("foreign @extra was routed")
	}
}

func TestRouteQueryResponseIgnoresForeignObjectExtra(t *testing.T) {
	session, _, _, _ := newSessionWithFakes(t)

	raw := RawMessage(`{"@type":"updateSome","@extra":{"source":"other"}}`)
	routed, err := session.routeQueryResponse(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if routed {
		t.Fatal("foreign object @extra was routed")
	}
}

// ---- Pump routes a foreign @extra update to the store ----

func TestPumpAppliesUpdateWithForeignObjectExtra(t *testing.T) {
	session, _, _, client := newSessionWithFakes(t)

	// A foreign @extra must not make the pump treat the object as a query
	// reply. The update belongs to the store, so it lands there.
	raw := RawMessage(`{
		"@type": "updateNewChat",
		"@extra": {"source": "other"},
		"chat": {
			"id": 7,
			"title": "Alice",
			"positions": {
				"@type": "chatPositions",
				"positions": [
					{"position": {"@type": "chatPosition", "source": {"@type": "chatListMain"}, "order": "100"}, "chat_id": 7}
				]
			}
		}
	}`)

	client.updates <- Update{ClientID: client.id, Raw: raw}

	waitForLiveChat(t, session.LiveState(), 7)
}

func TestPumpDoesNotApplyQueryReplyToTheStore(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		raw RawMessage
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		raw, err := session.Query(
			context.Background(),
			RawMessage(`{"@type":"getMe"}`),
		)
		resultCh <- result{raw: raw, err: err}
	}()

	request := waitForSentRequest(t, sender)
	extra := extractExtra(t, request)
	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"user","id":42,"@extra":"` + extra + `"}`),
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("Query: %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Query did not return")
	}

	if chats := session.LiveState().ChatList(); len(chats) != 0 {
		t.Fatalf("a query reply reached the store: %v", chats)
	}
}
