package maplibre

import (
	"context"
	"errors"
	"testing"
	"time"
)

const yellowBackgroundStyle = `{
  "version": 8,
  "sources": {},
  "layers": [{"id":"bg","type":"background","paint":{"background-color":"#FFAA00"}}]
}`

// TestRenderImageBackgroundColor renders a 64x64 viewport with a
// solid #FFAA00 background and asserts the readback bytes match. This is
// a real correctness test for the GPU->CPU pipeline: it catches byte
// order (RGBA vs BGRA), incorrect stride, alpha premultiplication
// regressions, and dimension mismatches in one shot.
//
// Skipped if the platform's GPU backend isn't available (no Metal device,
// no Vulkan ICD).
func TestRenderImageBackgroundColor(t *testing.T) {
	rt := newTestRuntime(t)
	const w, h = 64, 64
	m, err := rt.NewMap(MapOptions{Width: w, Height: h, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if err := m.SetStyleJSON(yellowBackgroundStyle); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer loadCancel()
	if _, err := m.WaitForEvent(loadCtx, func(e Event) bool {
		return e.Type == EventStyleLoaded || e.Type == EventMapLoadingFailed
	}); err != nil {
		t.Fatalf("waiting for STYLE_LOADED: %v", err)
	}

	sess, err := attachSmokeSession(t, m, w, h, 1)
	if err != nil {
		var mlnErr *Error
		if errors.As(err, &mlnErr) && mlnErr.Status == StatusUnsupported {
			t.Skipf("backend unavailable: %v", err)
		}
		t.Fatalf("attachSmokeSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	renderCtx, renderCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer renderCancel()
	rgba, gw, gh, err := m.RenderImage(renderCtx, sess)
	if err != nil {
		t.Fatalf("RenderImage: %v", err)
	}
	if gw != w || gh != h {
		t.Fatalf("dimensions = %dx%d, want %dx%d", gw, gh, w, h)
	}
	if len(rgba) != w*h*4 {
		t.Fatalf("rgba len = %d, want %d", len(rgba), w*h*4)
	}

	// Center pixel.
	cx, cy := w/2, h/2
	off := (cy*gw + cx) * 4
	r, g, b, a := rgba[off], rgba[off+1], rgba[off+2], rgba[off+3]

	// Premultiplied #FFAA00 with alpha=255 is unchanged: rgba(255, 170, 0, 255).
	// Allow ±3 per channel for any color-space wiggle.
	const tol = 3
	exp := [4]byte{255, 170, 0, 255}
	got := [4]byte{r, g, b, a}
	for i := range exp {
		d := int(got[i]) - int(exp[i])
		if d < -tol || d > tol {
			t.Errorf("center pixel rgba(%d,%d,%d,%d), want ~rgba(%d,%d,%d,%d) (component %d off by %d)",
				r, g, b, a, exp[0], exp[1], exp[2], exp[3], i, d)
			return
		}
	}
}

// TestRenderImageInto exercises the buffer-reuse path: pre-allocate
// once, render twice, assert both renders fill the same buffer with
// matching bytes (since the style and camera are unchanged).
func TestRenderImageInto(t *testing.T) {
	rt := newTestRuntime(t)
	const w, h = 32, 32
	m, err := rt.NewMap(MapOptions{Width: w, Height: h, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if err := m.SetStyleJSON(yellowBackgroundStyle); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer loadCancel()
	if _, err := m.WaitForEvent(loadCtx, func(e Event) bool {
		return e.Type == EventStyleLoaded || e.Type == EventMapLoadingFailed
	}); err != nil {
		t.Fatalf("waiting for STYLE_LOADED: %v", err)
	}

	sess, err := attachSmokeSession(t, m, w, h, 1)
	if err != nil {
		var mlnErr *Error
		if errors.As(err, &mlnErr) && mlnErr.Status == StatusUnsupported {
			t.Skipf("backend unavailable: %v", err)
		}
		t.Fatalf("attachSmokeSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	buf := make([]byte, w*h*4)
	render1Ctx, render1Cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer render1Cancel()
	gw, gh, err := m.RenderImageInto(render1Ctx, sess, buf)
	if err != nil {
		t.Fatalf("first RenderImageInto: %v", err)
	}
	if gw != w || gh != h {
		t.Fatalf("got %dx%d", gw, gh)
	}
	first := append([]byte(nil), buf...)

	render2Ctx, render2Cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer render2Cancel()
	gw2, gh2, err := m.RenderImageInto(render2Ctx, sess, buf)
	if err != nil {
		t.Fatalf("second RenderImageInto: %v", err)
	}
	if gw2 != gw || gh2 != gh {
		t.Fatalf("dimensions changed between renders: %dx%d -> %dx%d", gw, gh, gw2, gh2)
	}

	// Same style, same camera, same buffer -> bytes must match exactly.
	for i := range first {
		if first[i] != buf[i] {
			t.Fatalf("byte %d: first=%d second=%d (renders should be deterministic)", i, first[i], buf[i])
		}
	}
}

// TestRenderImageIntoTooSmall verifies the size check.
func TestRenderImageIntoTooSmall(t *testing.T) {
	rt := newTestRuntime(t)
	m, err := rt.NewMap(MapOptions{Width: 32, Height: 32, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if err := m.SetStyleJSON(yellowBackgroundStyle); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer loadCancel()
	if _, err := m.WaitForEvent(loadCtx, func(e Event) bool {
		return e.Type == EventStyleLoaded || e.Type == EventMapLoadingFailed
	}); err != nil {
		t.Fatalf("waiting for STYLE_LOADED: %v", err)
	}

	sess, err := attachSmokeSession(t, m, 32, 32, 1)
	if err != nil {
		var mlnErr *Error
		if errors.As(err, &mlnErr) && mlnErr.Status == StatusUnsupported {
			t.Skipf("backend unavailable: %v", err)
		}
		t.Fatalf("attachSmokeSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	renderCtx, renderCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer renderCancel()
	tooSmall := make([]byte, 100)
	_, _, err = m.RenderImageInto(renderCtx, sess, tooSmall)
	if err == nil {
		t.Fatal("expected error for undersized dst, got nil")
	}
	var mlnErr *Error
	if !errors.As(err, &mlnErr) || mlnErr.Status != StatusInvalidArgument {
		t.Fatalf("got %v, want *Error{Status: StatusInvalidArgument}", err)
	}
}
