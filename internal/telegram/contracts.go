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

	// LogFilePath is where TDLib's own journal is written. An empty value
	// means there is no file to write it to, and the library is told to
	// write its journal nowhere — which is the one answer that keeps a
	// terminal clean for a program with no data folder.
	LogFilePath string

	// LogVerbosity is how much of TDLib's journal is written, from
	// DefaultLogVerbosity (errors) down to 0. Levels above
	// MaxLogVerbosity are refused: what they add is a dump of the
	// requests, and a request carries credentials and message text.
	LogVerbosity int
}

func DefaultConfig() Config {
	return Config{
		ReceiveTimeout:  250 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
		UpdateBuffer:    64,
		ErrorBuffer:     8,
		LogVerbosity:    DefaultLogVerbosity,
	}
}

func (c Config) Validate() error {
	if c.ReceiveTimeout <= 0 || c.ShutdownTimeout <= 0 || c.UpdateBuffer <= 0 || c.ErrorBuffer <= 0 {
		return ErrInvalidConfig
	}
	if c.LogVerbosity < 0 || c.LogVerbosity > MaxLogVerbosity {
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
