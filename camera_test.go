package maplibre

import (
	"context"
	"math"
	"testing"
	"time"
)

func loadEmptyStyle(t *testing.T, m *Map) {
	t.Helper()
	if err := m.SetStyleJSON(`{"version":8,"sources":{},"layers":[]}`); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := m.WaitForEvent(ctx, func(e Event) bool {
		return e.Type == EventStyleLoaded || e.Type == EventMapLoadingFailed
	}); err != nil {
		t.Fatalf("waiting for STYLE_LOADED: %v", err)
	}
}

func TestGetCameraReturnsAllFields(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	cam, err := m.GetCamera()
	if err != nil {
		t.Fatalf("GetCamera: %v", err)
	}
	want := CameraFieldCenter | CameraFieldZoom | CameraFieldBearing | CameraFieldPitch
	if cam.Fields&want != want {
		t.Fatalf("GetCamera Fields = %#x, want all of %#x set", cam.Fields, want)
	}
}

func TestJumpToRoundTripsCamera(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	target := Camera{
		Fields:    CameraFieldCenter | CameraFieldZoom | CameraFieldBearing | CameraFieldPitch,
		Latitude:  37.7749,
		Longitude: -122.4194,
		Zoom:      10,
		Bearing:   45,
		Pitch:     30,
	}
	if err := m.JumpTo(target); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}

	cam, err := m.GetCamera()
	if err != nil {
		t.Fatalf("GetCamera: %v", err)
	}
	if math.Abs(cam.Latitude-target.Latitude) > 1e-6 {
		t.Errorf("Latitude = %v, want %v", cam.Latitude, target.Latitude)
	}
	if math.Abs(cam.Longitude-target.Longitude) > 1e-6 {
		t.Errorf("Longitude = %v, want %v", cam.Longitude, target.Longitude)
	}
	if math.Abs(cam.Zoom-target.Zoom) > 1e-6 {
		t.Errorf("Zoom = %v, want %v", cam.Zoom, target.Zoom)
	}
	if math.Abs(cam.Bearing-target.Bearing) > 1e-6 {
		t.Errorf("Bearing = %v, want %v", cam.Bearing, target.Bearing)
	}
	if math.Abs(cam.Pitch-target.Pitch) > 1e-6 {
		t.Errorf("Pitch = %v, want %v", cam.Pitch, target.Pitch)
	}
}

func TestMoveByChangesCenter(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	if err := m.JumpTo(Camera{
		Fields:    CameraFieldCenter | CameraFieldZoom,
		Latitude:  0,
		Longitude: 0,
		Zoom:      4,
	}); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}

	before, _ := m.GetCamera()
	if err := m.MoveBy(100, 50); err != nil {
		t.Fatalf("MoveBy: %v", err)
	}
	after, _ := m.GetCamera()

	if before.Latitude == after.Latitude && before.Longitude == after.Longitude {
		t.Fatalf("MoveBy did not change center: before=%+v after=%+v", before, after)
	}
}

func TestScaleByChangesZoom(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	if err := m.JumpTo(Camera{Fields: CameraFieldZoom, Zoom: 4}); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}
	before, _ := m.GetCamera()
	if err := m.ScaleBy(2.0, nil); err != nil {
		t.Fatalf("ScaleBy: %v", err)
	}
	after, _ := m.GetCamera()
	if after.Zoom <= before.Zoom {
		t.Fatalf("ScaleBy(2.0) did not increase zoom: before=%v after=%v", before.Zoom, after.Zoom)
	}
}

func TestRotatePitchAndCancelTransitions(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	if err := m.RotateBy(ScreenPoint{0, 0}, ScreenPoint{50, 50}); err != nil {
		t.Fatalf("RotateBy: %v", err)
	}
	if err := m.PitchBy(10); err != nil {
		t.Fatalf("PitchBy: %v", err)
	}
	if err := m.CancelTransitions(); err != nil {
		t.Fatalf("CancelTransitions: %v", err)
	}
}
