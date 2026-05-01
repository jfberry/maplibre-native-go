package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

// NetworkStatus mirrors mln_network_status. Process-global; not
// scoped to a Runtime.
type NetworkStatus uint32

const (
	NetworkStatusOnline  NetworkStatus = NetworkStatus(C.MLN_NETWORK_STATUS_ONLINE)
	NetworkStatusOffline NetworkStatus = NetworkStatus(C.MLN_NETWORK_STATUS_OFFLINE)
)

func (s NetworkStatus) String() string {
	switch s {
	case NetworkStatusOnline:
		return "ONLINE"
	case NetworkStatusOffline:
		return "OFFLINE"
	}
	return "UNKNOWN"
}

// GetNetworkStatus reads MapLibre Native's process-global network
// status. Affects every Runtime in the process. Safe from any goroutine.
func GetNetworkStatus() (NetworkStatus, error) {
	var out C.uint32_t
	if status := C.mln_network_status_get(&out); status != C.MLN_STATUS_OK {
		return 0, statusError("mln_network_status_get", status)
	}
	return NetworkStatus(out), nil
}

// SetNetworkStatus changes MapLibre Native's process-global network
// status. NetworkStatusOffline makes mbgl's online file source stop
// starting requests until the next NetworkStatusOnline transition;
// NetworkStatusOnline allows requests and wakes any subscribers that
// went idle while offline. Affects every Runtime in the process.
func SetNetworkStatus(status NetworkStatus) error {
	if s := C.mln_network_status_set(C.uint32_t(status)); s != C.MLN_STATUS_OK {
		return statusError("mln_network_status_set", s)
	}
	return nil
}
