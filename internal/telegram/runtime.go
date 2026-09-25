package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"telecli/internal/telemetry/recorder"
)

type envelope struct {
	ClientID int `json:"@client_id"`
}

type Runtime struct {
	cfg      Config
	native   Native
	recorder recorder.ComponentRecorder
	now      func() time.Time

	mu        sync.RWMutex
	state     LifecycleState
	clients   map[int]*Client
	cancel    context.CancelFunc
	done      chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
	startErr  error
	closeErr  error
}

type Client struct {
	id      int
	updates chan Update
	errors  chan error
}

func NewRuntime(cfg Config, native Native, rec recorder.ComponentRecorder) (*Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if native == nil {
		return nil, ErrNativeUnavailable
	}
	if rec == nil {
		rec = recorder.NewNoop()
	}
	return &Runtime{
		cfg:      cfg,
		native:   native,
		recorder: rec,
		now:      time.Now,
		state:    LifecycleCreated,
		clients:  make(map[int]*Client),
		done:     make(chan struct{}),
	}, nil
}

func (r *Runtime) State() LifecycleState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *Runtime) Start(parent context.Context) error {
	r.mu.RLock()
	state := r.state
	r.mu.RUnlock()
	if state == LifecycleClosed || state == LifecycleClosing {
		return ErrClosed
	}

	called := false
	r.startOnce.Do(func() {
		called = true
		ctx, cancel := context.WithCancel(parent)
		r.mu.Lock()
		r.cancel = cancel
		r.state = LifecycleRunning
		r.mu.Unlock()
		r.recorder.SetState(recorder.StateRunning)
		go r.receiveLoop(ctx)
	})
	if !called {
		return ErrAlreadyStarted
	}
	return r.startErr
}

func (r *Runtime) NewClient() (*Client, error) {
	if r.State() != LifecycleRunning {
		return nil, ErrNotStarted
	}
	id, err := r.native.CreateClientID()
	if err != nil {
		return nil, fmt.Errorf("create TDLib client ID: %w", err)
	}
	client := &Client{
		id:      id,
		updates: make(chan Update, r.cfg.UpdateBuffer),
		errors:  make(chan error, r.cfg.ErrorBuffer),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != LifecycleRunning {
		return nil, ErrClosing
	}
	if _, exists := r.clients[id]; exists {
		return nil, fmt.Errorf("duplicate TDLib client ID %d", id)
	}
	r.clients[id] = client
	return client, nil
}

func (c *Client) ID() int                { return c.id }
func (c *Client) Updates() <-chan Update { return c.updates }
func (c *Client) Errors() <-chan error   { return c.errors }

func (r *Runtime) Send(clientID int, request RawMessage) error {
	if r.State() != LifecycleRunning {
		return ErrClosing
	}
	if err := r.native.Send(clientID, request); err != nil {
		return fmt.Errorf("send TDLib request: %w", err)
	}
	return nil
}

func (r *Runtime) Execute(request RawMessage) (RawMessage, error) {
	raw, err := r.native.Execute(request)
	if err != nil {
		return nil, fmt.Errorf("execute TDLib request: %w", err)
	}
	return append(RawMessage(nil), raw...), nil
}

func (r *Runtime) receiveLoop(ctx context.Context) {
	defer close(r.done)
	defer r.closeClientChannels()
	var sequence uint64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		raw, err := r.native.Receive(r.cfg.ReceiveTimeout)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, ErrClosed) {
				return
			}
			r.recorder.RecordError(recorder.ErrorTDLib, 0)
			r.broadcastError(fmt.Errorf("receive TDLib message: %w", err))
			continue
		}
		if len(raw) == 0 {
			continue
		}
		var msg envelope
		if err := json.Unmarshal(raw, &msg); err != nil {
			r.broadcastError(fmt.Errorf("decode TDLib envelope: %w", err))
			continue
		}
		r.mu.RLock()
		client := r.clients[msg.ClientID]
		r.mu.RUnlock()
		if client == nil {
			r.broadcastError(fmt.Errorf("%w: %d", ErrUnknownClient, msg.ClientID))
			continue
		}
		sequence++
		update := Update{
			ClientID:   msg.ClientID,
			Sequence:   sequence,
			ReceivedAt: r.now(),
			Raw:        append(RawMessage(nil), raw...),
		}
		r.recorder.AddReceived(1, uint64(len(raw)))
		r.recorder.Touch()
		select {
		case client.updates <- update:
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runtime) broadcastError(err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, client := range r.clients {
		select {
		case client.errors <- err:
		default:
		}
	}
}

func (r *Runtime) closeClientChannels() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, client := range r.clients {
		close(client.updates)
		close(client.errors)
	}
}

func (r *Runtime) Close(ctx context.Context) error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		if r.state == LifecycleCreated {
			r.state = LifecycleClosed
			r.mu.Unlock()
			r.closeErr = r.native.Close()
			close(r.done)
			return
		}
		r.state = LifecycleClosing
		cancel := r.cancel
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		select {
		case <-r.done:
		case <-ctx.Done():
			r.closeErr = ctx.Err()
			return
		}
		r.closeErr = r.native.Close()
		r.mu.Lock()
		r.state = LifecycleClosed
		r.mu.Unlock()
		r.recorder.SetState(recorder.StateStopped)
	})
	return r.closeErr
}
