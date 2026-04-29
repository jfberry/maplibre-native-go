//go:build darwin

package maplibre

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// TestSmokeRealAssets exercises the full lifecycle against a real style+
// mbtiles when MLN_TEST_STYLE is set. Skipped otherwise so it never blocks
// a stock developer environment.
//
// Required env:
//
//	MLN_TEST_STYLE - URL or absolute file path to a style JSON (e.g. an
//	                 OMT-vector style.prepared.json that references local
//	                 mbtiles + sprite + glyph file:// URLs).
//
// Optional env (defaults in parens):
//
//	MLN_TEST_LAT     (55.07)
//	MLN_TEST_LON     (-3.58)
//	MLN_TEST_ZOOM    (8)
//	MLN_TEST_TIMEOUT (30s)
func TestSmokeRealAssets(t *testing.T) {
	style := os.Getenv("MLN_TEST_STYLE")
	if style == "" {
		t.Skip("set MLN_TEST_STYLE to a style URL/path to enable real-asset smoke")
	}
	lat := envFloat(t, "MLN_TEST_LAT", 55.07)
	lon := envFloat(t, "MLN_TEST_LON", -3.58)
	zoom := envFloat(t, "MLN_TEST_ZOOM", 8)
	timeout := envDuration(t, "MLN_TEST_TIMEOUT", 30*time.Second)

	rt, err := NewRuntime(RuntimeOptions{})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	m, err := rt.NewMap(MapOptions{Width: 256, Height: 256, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	defer m.Close()

	if err := loadStyle(m, style); err != nil {
		t.Fatalf("load style: %v", err)
	}
	if _, err := m.WaitForEvent(timeout, func(e Event) bool {
		return e.Type == EventStyleLoaded || e.Type == EventMapLoadingFailed
	}); err != nil {
		t.Fatalf("waiting for STYLE_LOADED: %v", err)
	}

	if err := m.JumpTo(Camera{
		Fields:    CameraFieldCenter | CameraFieldZoom,
		Latitude:  lat,
		Longitude: lon,
		Zoom:      zoom,
	}); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}

	sess, err := m.AttachMetalTexture(256, 256, 1)
	if err != nil {
		t.Fatalf("AttachMetalTexture: %v", err)
	}
	defer sess.Close()

	frame, renders, err := m.RenderStill(sess, timeout)
	if err != nil {
		t.Fatalf("RenderStill: %v", err)
	}
	defer sess.ReleaseFrame(frame)

	if frame.Texture == nil {
		t.Fatal("acquired frame has nil texture pointer")
	}
	if frame.Width == 0 || frame.Height == 0 {
		t.Fatalf("acquired frame has zero dimensions: %dx%d", frame.Width, frame.Height)
	}

	t.Logf("smoke OK: %d renders to settle, frame %dx%d gen=%d",
		renders, frame.Width, frame.Height, frame.Generation)
}

func loadStyle(m *Map, style string) error {
	switch {
	case len(style) > 0 && style[0] == '{':
		return m.SetStyleJSON(style)
	case containsScheme(style):
		return m.SetStyleURL(style)
	default:
		return m.SetStyleURL("file://" + style)
	}
}

func containsScheme(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == ':' && s[i+1] == '/' && s[i+2] == '/' {
			return true
		}
	}
	return false
}

func envFloat(t *testing.T, key string, def float64) float64 {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		t.Fatalf("%s=%q: %v", key, v, err)
	}
	return f
}

func envDuration(t *testing.T, key string, def time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		t.Fatalf("%s=%q: %v", key, v, err)
	}
	return d
}
