package maplibre

import "fmt"

// Status mirrors the mln_status enum in maplibre_native_c.h.
//
// Values are stable: zero is success, negative codes are failures.
type Status int

const (
	StatusOK              Status = 0
	StatusInvalidArgument Status = -1
	StatusInvalidState    Status = -2
	StatusWrongThread     Status = -3
	StatusUnsupported     Status = -4
	StatusNativeError     Status = -5
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusInvalidArgument:
		return "INVALID_ARGUMENT"
	case StatusInvalidState:
		return "INVALID_STATE"
	case StatusWrongThread:
		return "WRONG_THREAD"
	case StatusUnsupported:
		return "UNSUPPORTED"
	case StatusNativeError:
		return "NATIVE_ERROR"
	}
	return fmt.Sprintf("UNKNOWN(%d)", int(s))
}

// Error wraps a non-OK status returned by the native ABI together with the
// thread-local diagnostic message captured immediately after the call.
type Error struct {
	Status  Status
	Op      string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s: %s", e.Op, e.Status)
	}
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Status, e.Message)
}
