package maplibre

/*
#cgo CFLAGS: -I/Users/james/dev/maplibre-native-ffi/include
#cgo LDFLAGS: -L/Users/james/dev/maplibre-native-ffi/build -lmaplibre_native_abi -Wl,-rpath,/Users/james/dev/maplibre-native-ffi/build

#include "maplibre_native_abi.h"
*/
import "C"

// ABIVersion returns the maplibre-native-ffi C ABI contract version.
//
// Returns 0 while the ABI is unstable. Stable ABI editions use YYYYMM.
func ABIVersion() uint32 {
	return uint32(C.mln_abi_version())
}
