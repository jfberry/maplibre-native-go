package maplibre

// Cgo flags resolve via pkg-config. The Makefile prepends
// $MLN_FFI_DIR/build/pkgconfig to PKG_CONFIG_PATH so that the binding tracks
// any local maplibre-native-ffi checkout without patching source.

/*
#cgo pkg-config: maplibre-native-c

#include "maplibre_native_c.h"
*/
import "C"

// ABIVersion returns the maplibre-native C API contract version.
//
// Returns 0 while the API is unstable. Stable editions use YYYYMM.
func ABIVersion() uint32 {
	return uint32(C.mln_c_version())
}

// statusError reads the current thread's diagnostic message and builds a Go
// Error. Must be called on the dispatcher thread that just produced status,
// which is satisfied when invoked inside dispatcher.do.
func statusError(op string, status C.mln_status) error {
	if status == C.MLN_STATUS_OK {
		return nil
	}
	return &Error{
		Status:  Status(status),
		Op:      op,
		Message: C.GoString(C.mln_thread_last_error_message()),
	}
}
