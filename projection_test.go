package maplibre

import (
	"math"
	"testing"
)

// TestProjectedMetersRoundTrip verifies the standalone Mercator
// helpers. These are pure functions — no Map/Runtime needed.
func TestProjectedMetersRoundTrip(t *testing.T) {
	cases := []LatLng{
		{0, 0},
		{55.07, -3.58},
		{-33.8688, 151.2093},
		{37.7749, -122.4194},
	}
	for _, ll := range cases {
		m, err := ProjectedMetersForLatLng(ll)
		if err != nil {
			t.Fatalf("ProjectedMetersForLatLng(%v): %v", ll, err)
		}
		got, err := LatLngForProjectedMeters(m)
		if err != nil {
			t.Fatalf("LatLngForProjectedMeters(%v): %v", m, err)
		}
		if math.Abs(got.Latitude-ll.Latitude) > 1e-9 || math.Abs(got.Longitude-ll.Longitude) > 1e-9 {
			t.Errorf("round-trip drift: in=%v meters=%v out=%v", ll, m, got)
		}
	}
}

// TestProjectedMetersAtEquator pins down the well-known anchor point:
// (0,0) <-> (0,0) meters.
func TestProjectedMetersAtEquator(t *testing.T) {
	m, err := ProjectedMetersForLatLng(LatLng{0, 0})
	if err != nil {
		t.Fatalf("ProjectedMetersForLatLng: %v", err)
	}
	if math.Abs(m.Northing) > 1e-6 || math.Abs(m.Easting) > 1e-6 {
		t.Errorf("origin = %v, want ~(0, 0)", m)
	}
}

// TestPixelLatLngRoundTrip exercises the live-map conversion path.
// Loads an empty style, jumps the camera, then asserts that converting
// a known LatLng to pixels and back recovers the same LatLng.
func TestPixelLatLngRoundTrip(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	if err := m.JumpTo(Camera{
		Fields:    CameraFieldCenter | CameraFieldZoom,
		Latitude:  55.07,
		Longitude: -3.58,
		Zoom:      8,
	}); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}

	in := LatLng{55.0750, -3.5825}
	pt, err := m.PixelForLatLng(in)
	if err != nil {
		t.Fatalf("PixelForLatLng: %v", err)
	}
	out, err := m.LatLngForPixel(pt)
	if err != nil {
		t.Fatalf("LatLngForPixel: %v", err)
	}
	if math.Abs(out.Latitude-in.Latitude) > 1e-6 || math.Abs(out.Longitude-in.Longitude) > 1e-6 {
		t.Errorf("round-trip: in=%v pixel=%v out=%v", in, pt, out)
	}
}

// TestPixelsForLatLngsBatch exercises the array-form conversion. We
// don't require equality with single-point conversion (mbgl is allowed
// to take faster paths) — we only require round-trip stability.
func TestPixelsForLatLngsBatch(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)
	if err := m.JumpTo(Camera{Fields: CameraFieldCenter | CameraFieldZoom, Zoom: 4}); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}

	in := []LatLng{
		{0, 0},
		{55.07, -3.58},
		{-33.8688, 151.2093},
	}
	pts, err := m.PixelsForLatLngs(in)
	if err != nil {
		t.Fatalf("PixelsForLatLngs: %v", err)
	}
	if len(pts) != len(in) {
		t.Fatalf("len(pts) = %d, want %d", len(pts), len(in))
	}
	out, err := m.LatLngsForPixels(pts)
	if err != nil {
		t.Fatalf("LatLngsForPixels: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if math.Abs(out[i].Latitude-in[i].Latitude) > 1e-6 || math.Abs(out[i].Longitude-in[i].Longitude) > 1e-6 {
			t.Errorf("idx %d: in=%v out=%v", i, in[i], out[i])
		}
	}
}

// TestPixelsForLatLngsEmpty verifies the empty-input fast path doesn't
// cross the ABI.
func TestPixelsForLatLngsEmpty(t *testing.T) {
	m := newTestMap(t)
	pts, err := m.PixelsForLatLngs(nil)
	if err != nil {
		t.Fatalf("PixelsForLatLngs(nil): %v", err)
	}
	if len(pts) != 0 {
		t.Fatalf("len(pts) = %d, want 0", len(pts))
	}
	lls, err := m.LatLngsForPixels([]ScreenPoint{})
	if err != nil {
		t.Fatalf("LatLngsForPixels([]): %v", err)
	}
	if len(lls) != 0 {
		t.Fatalf("len(lls) = %d, want 0", len(lls))
	}
}

// TestProjectionModeRoundTrip writes and reads back the axonometric
// render-transform fields.
func TestProjectionModeRoundTrip(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	want := ProjectionMode{
		Fields:      ProjectionFieldAxonometric | ProjectionFieldXSkew | ProjectionFieldYSkew,
		Axonometric: true,
		XSkew:       0.5,
		YSkew:       0.25,
	}
	if err := m.SetProjectionMode(want); err != nil {
		t.Fatalf("SetProjectionMode: %v", err)
	}
	got, err := m.GetProjectionMode()
	if err != nil {
		t.Fatalf("GetProjectionMode: %v", err)
	}
	if got.Axonometric != want.Axonometric ||
		math.Abs(got.XSkew-want.XSkew) > 1e-9 ||
		math.Abs(got.YSkew-want.YSkew) > 1e-9 {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestStandaloneProjection exercises the snapshot helper end to end.
func TestStandaloneProjection(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	if err := m.JumpTo(Camera{
		Fields:    CameraFieldCenter | CameraFieldZoom,
		Latitude:  55.07,
		Longitude: -3.58,
		Zoom:      8,
	}); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}

	proj, err := m.NewProjection()
	if err != nil {
		t.Fatalf("NewProjection: %v", err)
	}
	defer proj.Close()

	// The helper snapshots the camera, so its GetCamera should return
	// the same fields the map has right now.
	cam, err := proj.GetCamera()
	if err != nil {
		t.Fatalf("Projection.GetCamera: %v", err)
	}
	if math.Abs(cam.Latitude-55.07) > 1e-6 || math.Abs(cam.Longitude-(-3.58)) > 1e-6 || math.Abs(cam.Zoom-8) > 1e-6 {
		t.Errorf("snapshot camera = %+v, want ~(55.07,-3.58,zoom=8)", cam)
	}

	// Round-trip via the helper.
	in := LatLng{55.07, -3.58}
	pt, err := proj.PixelForLatLng(in)
	if err != nil {
		t.Fatalf("Projection.PixelForLatLng: %v", err)
	}
	out, err := proj.LatLngForPixel(pt)
	if err != nil {
		t.Fatalf("Projection.LatLngForPixel: %v", err)
	}
	if math.Abs(out.Latitude-in.Latitude) > 1e-6 || math.Abs(out.Longitude-in.Longitude) > 1e-6 {
		t.Errorf("helper round-trip: in=%v out=%v", in, out)
	}

	// Helper isolation: change the source map's camera; the helper
	// must not move.
	if err := m.JumpTo(Camera{Fields: CameraFieldCenter, Latitude: 0, Longitude: 0}); err != nil {
		t.Fatalf("JumpTo(0,0): %v", err)
	}
	cam2, err := proj.GetCamera()
	if err != nil {
		t.Fatalf("Projection.GetCamera (post-jump): %v", err)
	}
	if math.Abs(cam2.Latitude-cam.Latitude) > 1e-6 || math.Abs(cam2.Longitude-cam.Longitude) > 1e-6 {
		t.Errorf("helper drifted with map: before=%+v after=%+v", cam, cam2)
	}
}

// TestProjectionSetVisibleCoordinates fits a bounding box around two
// points and verifies the camera centred between them.
func TestProjectionSetVisibleCoordinates(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	proj, err := m.NewProjection()
	if err != nil {
		t.Fatalf("NewProjection: %v", err)
	}
	defer proj.Close()

	in := []LatLng{
		{50, -10},
		{60, 0},
	}
	if err := proj.SetVisibleCoordinates(in, EdgeInsets{Top: 16, Left: 16, Bottom: 16, Right: 16}); err != nil {
		t.Fatalf("SetVisibleCoordinates: %v", err)
	}
	cam, err := proj.GetCamera()
	if err != nil {
		t.Fatalf("Projection.GetCamera: %v", err)
	}
	// Centre should be roughly the bbox midpoint. mbgl picks the centre
	// to exactly fit the box, so allow a degree of slop.
	if cam.Latitude < 53 || cam.Latitude > 57 {
		t.Errorf("fit centre lat = %v, expected ~55", cam.Latitude)
	}
	if cam.Longitude < -7 || cam.Longitude > -3 {
		t.Errorf("fit centre lon = %v, expected ~-5", cam.Longitude)
	}
}

// TestProjectionSetVisibleCoordinatesEmpty verifies input validation.
func TestProjectionSetVisibleCoordinatesEmpty(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)
	proj, err := m.NewProjection()
	if err != nil {
		t.Fatalf("NewProjection: %v", err)
	}
	defer proj.Close()

	if err := proj.SetVisibleCoordinates(nil, EdgeInsets{}); err == nil {
		t.Fatal("expected error for empty coords, got nil")
	}
}

// TestRequestRepaintRejectsStaticMode verifies that RequestRepaint on
// the default static-mode map returns an error (per the C ABI docs:
// MLN_STATUS_INVALID_STATE for non-continuous maps).
func TestRequestRepaintRejectsStaticMode(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)
	if err := m.RequestRepaint(); err == nil {
		t.Fatal("expected error from RequestRepaint on static map, got nil")
	}
}
