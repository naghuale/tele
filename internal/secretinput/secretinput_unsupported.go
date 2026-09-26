//go:build !darwin || !cgo

package secretinput

// unsupportedReader refuses to read a secret.
//
// Falling back to an echoing read would put the secret on screen and into
// the scrollback, so an unsupported platform fails instead.
type unsupportedReader struct{}

// NewReader returns the production reader for the platform.
func NewReader() Reader {
	return unsupportedReader{}
}

// ReadSecret always reports that hidden input is unavailable.
func (unsupportedReader) ReadSecret(string) ([]byte, error) {
	return nil, ErrUnsupported
}
