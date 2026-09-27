package telegram

import (
	"errors"
	"time"
)

type RawMessage []byte

type Native interface {
	CreateClientID() (int, error)
	Send(clientID int, request []byte) error
	Receive(timeout time.Duration) ([]byte, error)
	Execute(request []byte) ([]byte, error)
	Close() error
}

var (
	ErrAlreadyStarted    = errors.New("telegram runtime already started")
	ErrNotStarted        = errors.New("telegram runtime not started")
	ErrClosing           = errors.New("telegram runtime is closing")
	ErrClosed            = errors.New("telegram runtime is closed")
	ErrInvalidConfig     = errors.New("invalid telegram runtime configuration")
	ErrUnknownClient     = errors.New("telegram message references an unknown client")
	ErrNativeUnavailable = errors.New("TDLib native runtime unavailable")
	ErrRuntimeFailed     = errors.New("telegram runtime failed")
)

type LifecycleState uint8

const (
	LifecycleCreated LifecycleState = iota
	LifecycleRunning
	LifecycleClosing
	LifecycleClosed
	LifecycleFailed
)

func (s LifecycleState) String() string {
	switch s {
	case LifecycleCreated:
		return "created"
	case LifecycleRunning:
		return "running"
	case LifecycleClosing:
		return "closing"
	case LifecycleClosed:
		return "closed"
	case LifecycleFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type Config struct {
	ReceiveTimeout  time.Duration
	ShutdownTimeout time.Duration
	UpdateBuffer    int
	ErrorBuffer     int
}

func DefaultConfig() Config {
	return Config{
		ReceiveTimeout:  250 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
		UpdateBuffer:    64,
		ErrorBuffer:     8,
	}
}

func (c Config) Validate() error {
	if c.ReceiveTimeout <= 0 || c.ShutdownTimeout <= 0 || c.UpdateBuffer <= 0 || c.ErrorBuffer <= 0 {
		return ErrInvalidConfig
	}
	return nil
}

type Update struct {
	ClientID   int
	Sequence   uint64
	ReceivedAt time.Time
	Raw        RawMessage
}
