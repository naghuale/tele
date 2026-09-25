package recorder

// ErrorKind classifies recorded errors.
type ErrorKind string

const (
	ErrorGeneric ErrorKind = "generic"
	ErrorIO      ErrorKind = "io"
	ErrorNetwork ErrorKind = "network"
	ErrorTDLib   ErrorKind = "tdlib"
)

// Valid reports whether k is a canonical error kind. Empty is invalid.
func (k ErrorKind) Valid() bool {
	switch k {
	case ErrorGeneric, ErrorIO, ErrorNetwork, ErrorTDLib:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (k ErrorKind) String() string { return string(k) }

// ErrorRecorder records error counters.
type ErrorRecorder interface {
	RecordError(kind ErrorKind, code int)
}
