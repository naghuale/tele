//go:build cgo && (darwin || linux)

package tdjson

/*
#cgo linux LDFLAGS: -ldl
#include <stdlib.h>
#include "loader.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

type library struct {
	stateMu   sync.RWMutex
	receiveMu sync.Mutex
	ptr       *C.telecli_tdjson
	closed    bool
}

func openLibrary(path string) (*library, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	errbuf := make([]byte, 1024)
	ptr := C.telecli_tdjson_open(cpath, (*C.char)(unsafe.Pointer(&errbuf[0])), C.size_t(len(errbuf)))
	if ptr == nil {
		return nil, fmt.Errorf("open TDLib: %s", cString(errbuf))
	}
	return &library{ptr: ptr}, nil
}

func (l *library) createClientID() (int, error) {
	l.stateMu.RLock()
	defer l.stateMu.RUnlock()
	if l.closed {
		return 0, errors.New("TDLib library is closed")
	}
	return int(C.telecli_tdjson_create_client_id(l.ptr)), nil
}
func (l *library) send(clientID int, request []byte) error {
	l.stateMu.RLock()
	defer l.stateMu.RUnlock()
	if l.closed {
		return errors.New("TDLib library is closed")
	}
	creq := C.CString(string(request))
	defer C.free(unsafe.Pointer(creq))
	errbuf := make([]byte, 1024)
	if C.telecli_tdjson_send(l.ptr, C.int(clientID), creq, (*C.char)(unsafe.Pointer(&errbuf[0])), C.size_t(len(errbuf))) != 0 {
		return fmt.Errorf("td_send: %s", cString(errbuf))
	}
	return nil
}
func (l *library) receive(timeout time.Duration) ([]byte, error) {
	l.receiveMu.Lock()
	defer l.receiveMu.Unlock()
	l.stateMu.RLock()
	defer l.stateMu.RUnlock()
	if l.closed {
		return nil, errors.New("TDLib library is closed")
	}
	errbuf := make([]byte, 1024)
	result := C.telecli_tdjson_receive(l.ptr, C.double(timeout.Seconds()), (*C.char)(unsafe.Pointer(&errbuf[0])), C.size_t(len(errbuf)))
	if result == nil {
		if message := cString(errbuf); message != "" {
			return nil, errors.New(message)
		}
		return nil, nil
	}
	return []byte(C.GoString(result)), nil
}
func (l *library) execute(request []byte) ([]byte, error) {
	l.stateMu.RLock()
	defer l.stateMu.RUnlock()
	if l.closed {
		return nil, errors.New("TDLib library is closed")
	}
	creq := C.CString(string(request))
	defer C.free(unsafe.Pointer(creq))
	errbuf := make([]byte, 1024)
	result := C.telecli_tdjson_execute(l.ptr, creq, (*C.char)(unsafe.Pointer(&errbuf[0])), C.size_t(len(errbuf)))
	if result == nil {
		return nil, fmt.Errorf("td_execute: %s", cString(errbuf))
	}
	return []byte(C.GoString(result)), nil
}
func (l *library) close() error {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	C.telecli_tdjson_close(l.ptr)
	l.ptr = nil
	return nil
}
func cString(buf []byte) string {
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}
