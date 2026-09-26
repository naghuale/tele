//go:build darwin && cgo

package secretinput

/*
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// getpass reads one line from /dev/tty with echo disabled. It is the
// simplest no-echo reader available on macOS and avoids putting the
// terminal into raw mode from Go.
static const char *telecli_read_secret(const char *prompt) {
    return getpass(prompt);
}

static void telecli_zero(const char *value) {
    if (value == NULL) {
        return;
    }
    memset((void *)value, 0, strlen(value));
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// ttyReader reads secrets through getpass(3).
type ttyReader struct{}

// NewReader returns the production no-echo reader for the platform.
func NewReader() Reader {
	return ttyReader{}
}

// ReadSecret prompts on the controlling terminal and returns the value.
//
// The static buffer getpass uses is zeroed before returning, so the
// secret does not linger in the process image.
func (ttyReader) ReadSecret(prompt string) ([]byte, error) {
	if prompt == "" {
		prompt = "Password: "
	}

	cPrompt := C.CString(prompt)
	defer C.free(unsafe.Pointer(cPrompt))

	cValue := C.telecli_read_secret(cPrompt)
	if cValue == nil {
		return nil, errors.New("secretinput: terminal read failed")
	}
	defer C.telecli_zero(cValue)

	length := C.strlen(cValue)
	if length == 0 {
		return nil, ErrEmpty
	}

	value := C.GoBytes(unsafe.Pointer(cValue), C.int(length))

	// A Go copy of a secret is unavoidable at the call boundary, so it
	// is zeroed as soon as the caller is done with it.
	zeroBytes(value)

	return value, nil
}

// zeroBytes overwrites a secret slice in place.
func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
