package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
)

// Status mirrors the mln_status enum in maplibre_native_c.h.
//
// Values are typed against the C constants — drift in upstream values
// fails to compile rather than silently misroutes.
type Status int32

const (
	StatusOK              Status = Status(C.MLN_STATUS_OK)
	StatusInvalidArgument Status = Status(C.MLN_STATUS_INVALID_ARGUMENT)
	StatusInvalidState    Status = Status(C.MLN_STATUS_INVALID_STATE)
	StatusWrongThread     Status = Status(C.MLN_STATUS_WRONG_THREAD)
	StatusUnsupported     Status = Status(C.MLN_STATUS_UNSUPPORTED)
	StatusNativeError     Status = Status(C.MLN_STATUS_NATIVE_ERROR)
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
	return fmt.Sprintf("UNKNOWN(%d)", int32(s))
}

// Error wraps a non-OK status returned by the native ABI together with the
// thread-local diagnostic message captured immediately after the call.
//
// Use errors.Is to match by Status:
//
//	if errors.Is(err, maplibre.ErrInvalidState) { ... }
//
// or errors.As for richer inspection:
//
//	var mlnErr *maplibre.Error
//	if errors.As(err, &mlnErr) {
//	    log.Printf("native op=%s status=%s msg=%s", mlnErr.Op, mlnErr.Status, mlnErr.Message)
//	}
type Error struct {
	Status  Status
	Op      string
	Message string
}

// Error formats the wrapped status into a compact human-readable message.
// Empty Op/Message components are omitted so sentinel errors print cleanly.
func (e *Error) Error() string {
	parts := make([]string, 0, 3)
	if e.Op != "" {
		parts = append(parts, e.Op)
	}
	parts = append(parts, e.Status.String())
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, ": ")
}

// Is matches *Error values by Status, so errors.Is(err, ErrInvalidState)
// returns true whenever the underlying error carries StatusInvalidState
// regardless of Op or Message.
func (e *Error) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Status == t.Status
}

// Sentinel errors for status codes. Match with errors.Is. Op and Message
// are empty on the sentinels themselves; they only carry Status, which is
// what (*Error).Is uses to compare.
var (
	ErrInvalidArgument = &Error{Status: StatusInvalidArgument}
	ErrInvalidState    = &Error{Status: StatusInvalidState}
	ErrWrongThread     = &Error{Status: StatusWrongThread}
	ErrUnsupported     = &Error{Status: StatusUnsupported}
	ErrNativeError     = &Error{Status: StatusNativeError}

	// ErrTimeout is returned by RenderStill / RenderImage / WaitForEvent
	// when the supplied context's deadline expires before the operation
	// completes. errors.Is(err, context.DeadlineExceeded) also matches when
	// the timeout came from a context.WithTimeout / WithDeadline.
	ErrTimeout = errors.New("maplibre: timeout waiting for runtime event")
)
