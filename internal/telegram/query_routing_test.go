package telegram

// The tests in this file are about one guarantee: an answer reaches the
// request that asked for it and no other request.
//
// The guarantee was broken at startup on a real account, where the program
// printed
//
//	telegram own user unavailable: telegram chat lifecycle: unexpected
//	response: getMe returned @type="ok"
//
// An `ok` is what the authorization phase receives for the requests it
// sends, so getMe was answered by a request the authorization phase had
// made. Nothing about the wire was wrong: two components minted @extra
// values from two counters, and the strings came out equal.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// authStateUpdate renders the pushed form of an authorization state, the
// way TDLib delivers one.
func authStateUpdate(state string) RawMessage {
	return RawMessage(
		`{"@type":"updateAuthorizationState",` +
			`"authorization_state":{"@type":"` + state + `"}}`,
	)
}

// newSessionOnClient starts a session over a client the test owns.
//
// newSessionWithFakes builds its own client, and the tests here need one
// that already carries the traffic of an earlier phase.
func newSessionOnClient(
	t *testing.T,
	sender AuthSender,
	client *Client,
) *AuthorizedSession {
	t.Helper()

	session := newAuthorizedSession(
		sender,
		&fakeCloser{},
		client,
		100*time.Millisecond,
		nil,
	)

	t.Cleanup(func() {
		session.cancel()

		select {
		case <-session.pumpDone:
		case <-time.After(time.Second):
			t.Error("session pump did not exit during cleanup")
		}
	})

	return session
}

// waitForRequestOfType waits until a request of the given TDLib type has
// been sent and returns it.
//
// It looks for the type rather than for "the newest request", so it stays
// correct on a sender that is also carrying the traffic of another phase.
func waitForRequestOfType(
	t *testing.T,
	sender *fakeSender,
	tdlibType string,
) RawMessage {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sender.mu.Lock()
		for _, request := range sender.requests {
			var envelope struct {
				Type string `json:"@type"`
			}
			if err := json.Unmarshal(request, &envelope); err != nil {
				sender.mu.Unlock()
				t.Fatalf("decode sent request: %v", err)
			}
			if envelope.Type == tdlibType {
				sender.mu.Unlock()
				return request
			}
		}
		sender.mu.Unlock()
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("no %s request was sent", tdlibType)
	return nil
}

// TestTheAuthorizationAnswerNeverReachesASessionQuery is the regression
// test for the startup warning.
//
// On an account TDLib already knows, setTdlibParameters is accepted and
// the state becomes ready inside the handling of that one request. The
// ready state is therefore pushed before the `ok` the request produces,
// and the coordinator returns on the ready state with that `ok` still on
// the wire.
//
// The leftover `ok` carries a telecli @extra, so the session pump
// consumes it. It is harmless as long as no session query is waiting
// under the same identifier, and the two identifiers came from two
// counters, so they were equal.
func TestTheAuthorizationAnswerNeverReachesASessionQuery(t *testing.T) {
	// The first request of the process is the one that collided, so the
	// counter is put back to its start. The test is sequential, and Go
	// runs sequential tests one at a time, so no other test can be
	// holding an identifier minted before the reset.
	querySequence.Store(0)

	client := newTestClient(t)
	sender := &fakeSender{}

	// An account the client is already authorized as: the parameters
	// request is the only request, and the state is ready before its
	// answer has been read.
	client.updates <- Update{
		ClientID: client.id,
		Raw:      authStateUpdate("authorizationStateWaitTdlibParameters"),
	}
	client.updates <- Update{
		ClientID: client.id,
		Raw:      authStateUpdate("authorizationStateReady"),
	}

	result, err := RunAuthWithClient(
		context.Background(),
		sender,
		client,
		validAuthParams(),
		&fakeProvider{},
	)
	if err != nil {
		t.Fatalf("RunAuthWithClient: %v", err)
	}
	if result.State != AuthStateReady {
		t.Fatalf("state = %q, want ready", result.State)
	}

	authExtra := extractExtra(t, waitForRequestOfType(
		t,
		sender,
		"setTdlibParameters",
	))

	session := newSessionOnClient(t, sender, client)

	type ownUser struct {
		id  int64
		err error
	}
	ownCh := make(chan ownUser, 1)
	go func() {
		id, err := session.GetMeUserID(context.Background())
		ownCh <- ownUser{id: id, err: err}
	}()

	// getMe is registered by the time its request is on the wire, so the
	// answer the authorization phase left behind arrives while it is
	// waiting, which is exactly the window the bug lived in.
	meExtra := extractExtra(t, waitForRequestOfType(t, sender, "getMe"))

	client.updates <- Update{
		ClientID: client.id,
		Raw:      RawMessage(`{"@type":"ok","@extra":"` + authExtra + `"}`),
	}
	client.updates <- Update{
		ClientID: client.id,
		Raw: RawMessage(
			`{"@type":"user","id":77,"@extra":"` + meExtra + `"}`,
		),
	}

	select {
	case got := <-ownCh:
		if got.err != nil {
			t.Fatalf(
				"GetMeUserID: %v (the answer the authorization phase "+
					"left behind was delivered to it)",
				got.err,
			)
		}
		if got.id != 77 {
			t.Fatalf("GetMeUserID id = %d, want 77", got.id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetMeUserID did not return")
	}
}

// TestNoTwoRequestsShareAnIdentifier is the invariant the warning came
// from, stated directly: within one process no two requests carry the
// same @extra, whoever sent them.
func TestNoTwoRequestsShareAnIdentifier(t *testing.T) {
	seen := make(map[QueryID]string)
	record := func(owner string, request RawMessage) QueryID {
		extra := QueryID(extractExtra(t, request))
		if extra == "" {
			t.Fatalf("%s sent a request with no @extra", owner)
		}
		if previous, taken := seen[extra]; taken {
			t.Fatalf(
				"%s and %s both used @extra %q",
				previous,
				owner,
				extra,
			)
		}
		seen[extra] = owner
		return extra
	}

	// Two authorization phases over two clients of one runtime, which is
	// what a repeated login does.
	for run := range 2 {
		client := newTestClient(t)
		sender := &fakeSender{}

		client.updates <- Update{
			ClientID: client.id,
			Raw:      authStateUpdate("authorizationStateWaitPhoneNumber"),
		}
		client.updates <- Update{
			ClientID: client.id,
			Raw:      authStateUpdate("authorizationStateReady"),
		}

		if _, err := RunAuthWithClient(
			context.Background(),
			sender,
			client,
			validAuthParams(),
			&fakeProvider{phone: "+15550001111"},
		); err != nil {
			t.Fatalf("run %d: RunAuthWithClient: %v", run, err)
		}
		record(
			fmt.Sprintf("authorization run %d", run),
			waitForRequestOfType(t, sender, "setAuthenticationPhoneNumber"),
		)

		session := newSessionOnClient(t, sender, client)

		// Nobody answers the query, so it is abandoned once its
		// request is on the wire. The identifier is what this test is
		// about.
		abandoned := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(
				context.Background(),
				100*time.Millisecond,
			)
			defer cancel()
			_, err := session.GetMeUserID(ctx)
			abandoned <- err
		}()

		record(
			fmt.Sprintf("session query of run %d", run),
			waitForRequestOfType(t, sender, "getMe"),
		)
		<-abandoned
	}
}

// TestTheUnexpectedAnswerNamesTheRequest proves the message a caller sees
// is enough to find the request that got the wrong answer: it names the
// request, the type that answered it, and the identifier the answer
// carried. Nothing else of the answer is read, because an answer can hold
// a name, a phone number or a message body.
//
// The shape is the one the real account produced: an `ok`, which is what
// every request that returns nothing answers with.
func TestTheUnexpectedAnswerNamesTheRequest(t *testing.T) {
	const secret = "+15550002222"

	session, sender, _, client := newSessionWithFakes(t)

	type ownUser struct {
		id  int64
		err error
	}
	ownCh := make(chan ownUser, 1)
	go func() {
		id, err := session.GetMeUserID(context.Background())
		ownCh <- ownUser{id: id, err: err}
	}()

	meExtra := extractExtra(t, waitForRequestOfType(t, sender, "getMe"))
	client.updates <- Update{
		ClientID: client.id,
		Raw: RawMessage(
			`{"@type":"ok","phone_number":"` + secret + `",` +
				`"@extra":"` + meExtra + `"}`,
		),
	}

	select {
	case got := <-ownCh:
		if got.err == nil {
			t.Fatal("GetMeUserID accepted an answer of the wrong type")
		}
		message := got.err.Error()

		for _, want := range []string{"getMe", `@type="ok"`, meExtra} {
			if !strings.Contains(message, want) {
				t.Fatalf("error = %q, want it to name %q", message, want)
			}
		}
		if strings.Contains(message, secret) {
			t.Fatalf("the error carried part of the answer: %q", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetMeUserID did not return")
	}
}

// ---- stub TDLib ----

// stubMessage is one object a stub TDLib hands to the receive loop.
type stubMessage struct {
	raw RawMessage
}

// stubRequest is one request a stub TDLib was asked to answer.
type stubRequest struct {
	clientID int
	request  RawMessage
	extra    QueryID
}

// stubTDLib is a TDLib that answers what it is asked with whatever the
// test chooses, and it answers in whatever order it chooses.
//
// A real TDLib answers a request when the request is done, not when it
// was sent, so several requests are in flight at once and the answers come
// back in an order the caller never chose. The stub can do the same, and
// can also add a delay per answer, so a test can prove that an answer
// reaches the request that asked for it rather than the one that was sent
// first.
type stubTDLib struct {
	queue  chan stubMessage
	closed chan struct{}

	// answer renders the reply to one request. A nil answer means the
	// stub stays silent about that request.
	answer func(stubRequest) RawMessage

	// delay is the time between accepting a request and answering it.
	delay time.Duration

	nextClientID atomic.Int64
	sent         atomic.Int64
	wg           sync.WaitGroup
}

func newStubTDLib(
	answer func(stubRequest) RawMessage,
	delay time.Duration,
) *stubTDLib {
	return &stubTDLib{
		queue:  make(chan stubMessage, 256),
		closed: make(chan struct{}),
		answer: answer,
		delay:  delay,
	}
}

func (s *stubTDLib) CreateClientID() (int, error) {
	return int(s.nextClientID.Add(1)), nil
}

func (s *stubTDLib) Send(clientID int, request []byte) error {
	var envelope struct {
		Type  string `json:"@type"`
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &envelope); err != nil {
		return err
	}
	s.sent.Add(1)

	pending := stubRequest{
		clientID: clientID,
		request:  append(RawMessage(nil), request...),
		extra:    QueryID(envelope.Extra),
	}

	if envelope.Type == "close" {
		s.push(stubMessage{raw: RawMessage(fmt.Sprintf(
			`{"@client_id":%d,"@type":`+
				`"updateAuthorizationState",`+
				`"authorization_state":{"@type":`+
				`"authorizationStateClosed"}}`,
			clientID,
		))})
		return nil
	}

	if s.answer == nil || envelope.Extra == "" {
		return nil
	}

	raw := s.answer(pending)
	if raw == nil {
		return nil
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if s.delay > 0 {
			time.Sleep(s.delay)
		}
		s.push(stubMessage{raw: withClientID(clientID, raw)})
	}()

	return nil
}

// withClientID adds the routing field the receive loop needs to an object
// an answer function produced.
//
// The answer function writes the object the caller would recognise, and
// the routing field belongs to the transport rather than to the answer, so
// it is spliced in here.
func withClientID(clientID int, object RawMessage) RawMessage {
	if len(object) == 0 || object[0] != '{' {
		return object
	}
	return RawMessage(
		fmt.Sprintf(`{"@client_id":%d,`, clientID) + string(object[1:]),
	)
}

// push hands one object to the receive loop, or gives up when the stub is
// closed and nobody is left to read it.
func (s *stubTDLib) push(message stubMessage) {
	select {
	case s.queue <- message:
	case <-s.closed:
	}
}

// waitForSentRequests waits until the stub has been asked to send at
// least count requests.
func (s *stubTDLib) waitForSentRequests(t *testing.T, count int64) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.sent.Load() >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf(
		"the stub was asked for %d requests, it has %d",
		count,
		s.sent.Load(),
	)
}

func (s *stubTDLib) Receive(timeout time.Duration) ([]byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case message := <-s.queue:
		return message.raw, nil
	case <-timer.C:
		return nil, nil
	}
}

func (s *stubTDLib) Execute([]byte) ([]byte, error) {
	return []byte(`{"@type":"ok"}`), nil
}

// Close stops the stub. The answers still on their way are released
// first, so nothing is left blocked on a queue with no reader.
func (s *stubTDLib) Close() error {
	close(s.closed)
	s.wg.Wait()
	return nil
}

// stubUserAnswer answers every request with the user object of the marker
// the request carries, so a test can tell the answers apart.
func stubUserAnswer(request stubRequest) RawMessage {
	var envelope struct {
		Marker string `json:"test_marker"`
	}
	_ = json.Unmarshal(request.request, &envelope)

	return RawMessage(
		`{"@type":"user","id":4242,"test_marker":"` + envelope.Marker +
			`","@extra":"` + string(request.extra) + `"}`,
	)
}

// startStubRuntime starts a Runtime over the stub and registers the
// cleanup that stops it.
func startStubRuntime(t *testing.T, native Native) *Runtime {
	t.Helper()

	rt, err := NewRuntime(DefaultConfig(), native, nil)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			2*time.Second,
		)
		defer cancel()
		_ = rt.Close(ctx)
	})

	return rt
}

// TestEveryQueryGetsItsOwnAnswerFromADelayedTDLib drives many concurrent
// queries against a stub that answers after a delay, so the answers come
// back in an order nobody chose.
func TestEveryQueryGetsItsOwnAnswerFromADelayedTDLib(t *testing.T) {
	rt := startStubRuntime(t, newStubTDLib(stubUserAnswer, 5*time.Millisecond))

	client, err := rt.NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	session := newSessionOnClient(t, rt, client)

	const queries = 24

	type result struct {
		marker string
		raw    RawMessage
		err    error
	}
	results := make(chan result, queries)

	var waitGroup sync.WaitGroup
	for i := range queries {
		marker := fmt.Sprintf("q%02d", i)
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()

			request, err := json.Marshal(map[string]any{
				"@type":       "getMe",
				"test_marker": marker,
			})
			if err != nil {
				results <- result{marker: marker, err: err}
				return
			}

			raw, err := session.Query(
				context.Background(),
				RawMessage(request),
			)
			results <- result{marker: marker, raw: raw, err: err}
		}()
	}
	waitGroup.Wait()
	close(results)

	for got := range results {
		if got.err != nil {
			t.Fatalf("Query %s: %v", got.marker, got.err)
		}

		var answer struct {
			Marker string `json:"test_marker"`
		}
		if err := json.Unmarshal(got.raw, &answer); err != nil {
			t.Fatalf("Query %s: decode: %v", got.marker, err)
		}
		if answer.Marker != got.marker {
			t.Fatalf(
				"Query %s was answered by %q",
				got.marker,
				answer.Marker,
			)
		}
	}
}

// TestTwoSessionsOverOneClientEachGetTheirOwnAnswer covers the shape that
// production never builds: two pumps reading the same client. Whichever
// pump reads an answer must hand it to the session that is waiting for
// it, not keep it.
func TestTwoSessionsOverOneClientEachGetTheirOwnAnswer(t *testing.T) {
	rt := startStubRuntime(t, newStubTDLib(stubUserAnswer, time.Millisecond))

	client, err := rt.NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	first := newSessionOnClient(t, rt, client)
	second := newSessionOnClient(t, rt, client)

	type result struct {
		owner  string
		marker string
		raw    RawMessage
		err    error
	}
	results := make(chan result, 16)

	ask := func(session *AuthorizedSession, owner string, count int) {
		var waitGroup sync.WaitGroup
		for i := range count {
			marker := fmt.Sprintf("%s-%02d", owner, i)
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()

				request, err := json.Marshal(map[string]any{
					"@type":       "getMe",
					"test_marker": marker,
				})
				if err != nil {
					results <- result{
						owner:  owner,
						marker: marker,
						err:    err,
					}
					return
				}

				ctx, cancel := context.WithTimeout(
					context.Background(),
					2*time.Second,
				)
				defer cancel()

				raw, err := session.Query(ctx, RawMessage(request))
				results <- result{
					owner:  owner,
					marker: marker,
					raw:    raw,
					err:    err,
				}
			}()
		}
		waitGroup.Wait()
	}

	var all sync.WaitGroup
	all.Add(2)
	go func() {
		defer all.Done()
		ask(first, "first", 8)
	}()
	go func() {
		defer all.Done()
		ask(second, "second", 8)
	}()
	all.Wait()
	close(results)

	for got := range results {
		if got.err != nil {
			t.Fatalf("Query %s: %v", got.marker, got.err)
		}

		var answer struct {
			Marker string `json:"test_marker"`
		}
		if err := json.Unmarshal(got.raw, &answer); err != nil {
			t.Fatalf("Query %s: decode: %v", got.marker, err)
		}
		if answer.Marker != got.marker {
			t.Fatalf(
				"Query %s was answered by %q",
				got.marker,
				answer.Marker,
			)
		}
	}
}

// TestAnAnswerThatArrivesAfterTheTimeoutIsNotHandedOn proves a late
// answer is consumed and never given to whatever request is waiting next.
func TestAnAnswerThatArrivesAfterTheTimeoutIsNotHandedOn(t *testing.T) {
	stub := newStubTDLib(stubUserAnswer, 60*time.Millisecond)
	rt := startStubRuntime(t, stub)

	client, err := rt.NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	session := newSessionOnClient(t, rt, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	abandoned := make(chan error, 1)
	go func() {
		_, err := session.Query(
			ctx,
			RawMessage(`{"@type":"getMe","test_marker":"late"}`),
		)
		abandoned <- err
	}()

	// The request is abandoned only once it is on the wire, so the
	// answer to it is certain to be on its way afterwards.
	stub.waitForSentRequests(t, 1)
	cancel()

	if err := <-abandoned; !errors.Is(err, context.Canceled) {
		t.Fatalf("Query err = %v, want context.Canceled", err)
	}

	// The next query must not be given the abandoned one's answer.
	raw, err := session.Query(
		context.Background(),
		RawMessage(`{"@type":"getMe","test_marker":"next"}`),
	)
	if err != nil {
		t.Fatalf("the second Query: %v", err)
	}

	var answer struct {
		Marker string `json:"test_marker"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Marker != "next" {
		t.Fatalf(
			"the second query was answered by %q, which belonged to the "+
				"abandoned one",
			answer.Marker,
		)
	}
}
