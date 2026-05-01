package maplibre

import (
	"context"
	"errors"
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
//
// Backend selection is automatic: AttachMetalTexture on darwin,
// AttachVulkanTexture on linux. If the platform's GPU stack isn't
// available (e.g. no Vulkan ICD installed) the test SKIPs rather than
// fails.
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

	if err := m.LoadStyle(style); err != nil {
		t.Fatalf("load style: %v", err)
	}
	loadCtx, loadCancel := context.WithTimeout(context.Background(), timeout)
	defer loadCancel()
	if _, err := m.WaitForEvent(loadCtx, func(e Event) bool {
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

	sess, err := attachSmokeSession(t, m, 256, 256, 1)
	if err != nil {
		var mlnErr *Error
		if errors.As(err, &mlnErr) && mlnErr.Status == StatusUnsupported {
			t.Skipf("backend unavailable: %v", err)
		}
		t.Fatalf("attachSmokeSession: %v", err)
	}
	defer sess.Close()

	renderCtx, renderCancel := context.WithTimeout(context.Background(), timeout)
	defer renderCancel()
	frame, err := m.RenderStill(renderCtx, sess)
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

	t.Logf("smoke OK: frame %dx%d gen=%d", frame.Width, frame.Height, frame.Generation)
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
