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

	return copySecret(cValue)
}

// copySecret copies a C secret into a caller-owned slice.
//
// The copy is returned intact; wiping it is the caller's job once the
// secret has been used, as the Reader contract says. Only the C buffer is
// wiped here, by the caller's deferred telecli_zero. Zeroing the copy
// before returning handed every caller NUL bytes instead of the secret.
func copySecret(cValue *C.char) ([]byte, error) {
	length := C.strlen(cValue)
	if length == 0 {
		return nil, ErrEmpty
	}
	return C.GoBytes(unsafe.Pointer(cValue), C.int(length)), nil
}

// copySecretFromString runs copySecret on a C copy of value and wipes
// that copy afterwards, as ReadSecret does. Test files cannot use cgo,
// so the tests reach copySecret through it.
func copySecretFromString(value string) ([]byte, error) {
	cValue := C.CString(value)
	defer C.free(unsafe.Pointer(cValue))
	defer C.telecli_zero(cValue)
	return copySecret(cValue)
}
