package maplibre

// Cgo flags are supplied externally so the binding can be built against any
// local maplibre-native-ffi checkout without patching source. The Makefile
// exports CGO_CFLAGS and CGO_LDFLAGS based on MLN_FFI_DIR; downstream
// projects can do the same. When upstream ships a pkg-config .pc file the
// directive below can be replaced with:
//
//	// #cgo pkg-config: maplibre_native_abi
//
// See README.md for the supported build flows.

/*
#include "maplibre_native_abi.h"
*/
import "C"

// ABIVersion returns the maplibre-native-ffi C ABI contract version.
//
// Returns 0 while the ABI is unstable. Stable ABI editions use YYYYMM.
func ABIVersion() uint32 {
	return uint32(C.mln_abi_version())
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
