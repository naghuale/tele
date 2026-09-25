package tdjson

import (
	"time"
)

type Native struct{ handle *library }

func Open(path string) (*Native, error) {
	handle, err := openLibrary(path)
	if err != nil {
		return nil, err
	}
	return &Native{handle: handle}, nil
}

func (n *Native) CreateClientID() (int, error)                  { return n.handle.createClientID() }
func (n *Native) Send(clientID int, request []byte) error       { return n.handle.send(clientID, request) }
func (n *Native) Receive(timeout time.Duration) ([]byte, error) { return n.handle.receive(timeout) }
func (n *Native) Execute(request []byte) ([]byte, error)        { return n.handle.execute(request) }
func (n *Native) Close() error                                  { return n.handle.close() }
