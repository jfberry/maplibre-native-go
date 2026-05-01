package maplibre

import (
	"errors"
	"testing"
)

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt, err := NewRuntime(RuntimeOptions{})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func TestMapNewClose(t *testing.T) {
	rt := newTestRuntime(t)

	m, err := rt.NewMap(MapOptions{Width: 256, Height: 256, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	if m == nil {
		t.Fatal("NewMap returned nil map")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMapCloseIdempotent(t *testing.T) {
	rt := newTestRuntime(t)
	m, err := rt.NewMap(MapOptions{Width: 256, Height: 256, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestMapNilCloseSafe(t *testing.T) {
	var m *Map
	if err := m.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}

func TestNewMapOnClosedRuntime(t *testing.T) {
	rt, err := NewRuntime(RuntimeOptions{})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = rt.NewMap(MapOptions{Width: 256, Height: 256, ScaleFactor: 1})
	if err == nil {
		t.Fatal("expected error from NewMap on closed runtime, got nil")
	}
	var mlnErr *Error
	if !errors.As(err, &mlnErr) || mlnErr.Status != StatusInvalidState {
		t.Fatalf("got %v, want *Error{Status: StatusInvalidState}", err)
	}
}

func TestRuntimeCloseRefusesWhileMapLive(t *testing.T) {
	rt, err := NewRuntime(RuntimeOptions{})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	m, err := rt.NewMap(MapOptions{Width: 256, Height: 256, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}

	// Runtime destroy should refuse with INVALID_STATE while a map is live.
	closeErr := rt.Close()
	if closeErr == nil {
		t.Fatal("expected runtime Close to fail while map is live, got nil")
	}
	var mlnErr *Error
	if !errors.As(closeErr, &mlnErr) || mlnErr.Status != StatusInvalidState {
		t.Fatalf("got %v, want *Error{Status: StatusInvalidState}", closeErr)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close map: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close runtime after map closed: %v", err)
	}
}
