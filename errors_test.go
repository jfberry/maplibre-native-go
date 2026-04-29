package maplibre

import "testing"

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusOK:              "OK",
		StatusInvalidArgument: "INVALID_ARGUMENT",
		StatusInvalidState:    "INVALID_STATE",
		StatusWrongThread:     "WRONG_THREAD",
		StatusUnsupported:     "UNSUPPORTED",
		StatusNativeError:     "NATIVE_ERROR",
		Status(-99):           "UNKNOWN(-99)",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", int(status), got, want)
		}
	}
}

func TestErrorWithMessage(t *testing.T) {
	err := &Error{
		Status:  StatusInvalidArgument,
		Op:      "mln_map_create",
		Message: "out_map is null",
	}
	want := "mln_map_create: INVALID_ARGUMENT: out_map is null"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorWithoutMessage(t *testing.T) {
	err := &Error{
		Status: StatusWrongThread,
		Op:     "mln_runtime_destroy",
	}
	want := "mln_runtime_destroy: WRONG_THREAD"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorImplementsErrorInterface(t *testing.T) {
	var _ error = (*Error)(nil)
}
