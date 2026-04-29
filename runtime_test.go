package maplibre

import (
	"testing"
)

func TestRuntimeNewClose(t *testing.T) {
	rt, err := NewRuntime(RuntimeOptions{})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("NewRuntime returned nil runtime")
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRuntimeCloseIdempotent(t *testing.T) {
	rt, err := NewRuntime(RuntimeOptions{})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestRuntimeNilCloseSafe(t *testing.T) {
	var rt *Runtime
	if err := rt.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}

func TestRuntimeWithCachePath(t *testing.T) {
	dir := t.TempDir()
	rt, err := NewRuntime(RuntimeOptions{
		CachePath:        dir + "/cache.db",
		MaximumCacheSize: 50 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
