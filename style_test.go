package maplibre

import (
	"context"
	"testing"
	"time"
)

func newTestMap(t *testing.T) *Map {
	t.Helper()
	rt := newTestRuntime(t)
	m, err := rt.NewMap(MapOptions{Width: 256, Height: 256, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestSetStyleJSONLoadsAndEmitsStyleLoaded(t *testing.T) {
	m := newTestMap(t)

	const emptyStyle = `{"version":8,"sources":{},"layers":[]}`
	if err := m.SetStyleJSON(emptyStyle); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ev, err := m.WaitForEvent(ctx, func(e Event) bool {
		return e.Type == EventStyleLoaded || e.Type == EventMapLoadingFailed
	})
	if err != nil {
		t.Fatalf("WaitForEvent: %v", err)
	}
	if ev.Type != EventStyleLoaded {
		t.Fatalf("got %s (code=%d msg=%q), want STYLE_LOADED", ev.Type, ev.Code, ev.Message)
	}
}

func TestSetStyleJSONInvalid(t *testing.T) {
	m := newTestMap(t)

	// Malformed JSON: missing the closing brace.
	err := m.SetStyleJSON(`{"version":8`)

	// Either synchronous failure (NATIVE_ERROR + diagnostics) or async
	// MAP_LOADING_FAILED is acceptable per the ABI docs.
	if err != nil {
		// Synchronous path — done.
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ev, err := m.WaitForEvent(ctx, func(e Event) bool {
		return e.Type == EventStyleLoaded || e.Type == EventMapLoadingFailed
	})
	if err != nil {
		t.Fatalf("WaitForEvent: %v", err)
	}
	if ev.Type != EventMapLoadingFailed {
		t.Fatalf("got %s, want MAP_LOADING_FAILED for malformed JSON", ev.Type)
	}
}

func TestRuntimePollEventEmptyQueue(t *testing.T) {
	rt := newTestRuntime(t)
	// On a fresh runtime with no map activity yet, the queue is typically
	// empty; we just assert the call shape is well-behaved.
	ev, has, err := rt.PollEvent()
	if err != nil {
		t.Fatalf("PollEvent: %v", err)
	}
	if !has && (ev.Type != 0 || ev.Source != nil || ev.Code != 0 || ev.Message != "") {
		t.Fatalf("has=false but ev=%+v, expected zero value", ev)
	}
}

func TestEventTypeString(t *testing.T) {
	cases := map[EventType]string{
		EventStyleLoaded:           "STYLE_LOADED",
		EventMapLoadingFailed:      "MAP_LOADING_FAILED",
		EventCameraDidChange:       "CAMERA_DID_CHANGE",
		EventStillImageFinished:    "STILL_IMAGE_FINISHED",
		EventRenderUpdateAvailable: "RENDER_UPDATE_AVAILABLE",
		EventType(99):              "UNKNOWN(99)",
	}
	for ev, want := range cases {
		if got := ev.String(); got != want {
			t.Errorf("EventType(%d).String() = %q, want %q", uint32(ev), got, want)
		}
	}
}
