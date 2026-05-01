package maplibre

import "testing"

// TestNetworkStatusGetSet flips the global network status and asserts
// reads echo writes. We reset to ONLINE on cleanup so other tests are
// not affected.
func TestNetworkStatusGetSet(t *testing.T) {
	got, err := GetNetworkStatus()
	if err != nil {
		t.Fatalf("GetNetworkStatus: %v", err)
	}
	t.Cleanup(func() { _ = SetNetworkStatus(got) })

	if err := SetNetworkStatus(NetworkStatusOffline); err != nil {
		t.Fatalf("SetNetworkStatus(Offline): %v", err)
	}
	if s, _ := GetNetworkStatus(); s != NetworkStatusOffline {
		t.Errorf("after Offline, GetNetworkStatus = %v, want Offline", s)
	}

	if err := SetNetworkStatus(NetworkStatusOnline); err != nil {
		t.Fatalf("SetNetworkStatus(Online): %v", err)
	}
	if s, _ := GetNetworkStatus(); s != NetworkStatusOnline {
		t.Errorf("after Online, GetNetworkStatus = %v, want Online", s)
	}
}

func TestNetworkStatusInvalid(t *testing.T) {
	if err := SetNetworkStatus(NetworkStatus(99)); err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}
}
