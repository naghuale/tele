//go:build !cgo || (!darwin && !linux)

package tdjson

import (
	"fmt"
	"time"
)

type library struct{}

func openLibrary(string) (*library, error) {
	return nil, fmt.Errorf("TDLib dynamic loader requires CGO on macOS or Linux")
}
func (*library) createClientID() (int, error)          { return 0, fmt.Errorf("TDLib unavailable") }
func (*library) send(int, []byte) error                { return fmt.Errorf("TDLib unavailable") }
func (*library) receive(time.Duration) ([]byte, error) { return nil, fmt.Errorf("TDLib unavailable") }
func (*library) execute([]byte) ([]byte, error)        { return nil, fmt.Errorf("TDLib unavailable") }
func (*library) close() error                          { return nil }
