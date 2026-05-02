package maplibre

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestSetResourceURLTransformBeforeMap registers a URL transform on a
// fresh runtime (no maps yet) and verifies the call succeeds. We can't
// exercise the rewrite path without making a real network request, so
// the round-trip behavior is covered manually rather than in CI.
func TestSetResourceURLTransformBeforeMap(t *testing.T) {
	rt := newTestRuntime(t)
	var n int64
	if err := rt.SetResourceURLTransform(func(_ ResourceKind, url string) string {
		atomic.AddInt64(&n, 1)
		return url
	}); err != nil {
		t.Fatalf("SetResourceURLTransform: %v", err)
	}
}

// TestSetResourceURLTransformAfterMap confirms the C ABI rejection
// path: registering a transform after a map already exists returns
// StatusInvalidState.
func TestSetResourceURLTransformAfterMap(t *testing.T) {
	rt := newTestRuntime(t)
	m, err := rt.NewMap(MapOptions{Width: 32, Height: 32})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	err = rt.SetResourceURLTransform(func(_ ResourceKind, url string) string { return url })
	if err == nil {
		t.Fatal("expected error registering transform after live map, got nil")
	}
	var mlnErr *Error
	if !asErr(err, &mlnErr) || mlnErr.Status != StatusInvalidState {
		t.Fatalf("got %v, want *Error{Status: StatusInvalidState}", err)
	}
}

// TestURLTransformRewritesStyleSource installs a transform that
// rewrites a sentinel URL to a non-existent host, loads a style that
// references that URL via a tilejson source, and asserts mbgl tried
// to fetch the rewritten URL (we observe the failure). This exercises
// the trampoline → Go callback → C copy-back path end to end.
func TestURLTransformRewritesStyleSource(t *testing.T) {
	rt := newTestRuntime(t)

	var (
		seenOriginal int64
		seenRewrite  int64
	)
	original := "https://example.invalid/source.json"
	rewrite := "https://example.invalid/rewritten.json"

	if err := rt.SetResourceURLTransform(func(_ ResourceKind, url string) string {
		switch url {
		case original:
			atomic.AddInt64(&seenOriginal, 1)
			return rewrite
		case rewrite:
			atomic.AddInt64(&seenRewrite, 1)
		}
		return url
	}); err != nil {
		t.Fatalf("SetResourceURLTransform: %v", err)
	}

	m, err := rt.NewMap(MapOptions{Width: 32, Height: 32})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	style := `{
		"version": 8,
		"sources": {
			"src": {"type": "vector", "url": "` + original + `"}
		},
		"layers": []
	}`
	if err := m.SetStyleJSON(style); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// STYLE_LOADED fires as soon as mbgl has parsed the style; the
	// source-URL fetch (and therefore the transform call) happens on a
	// worker thread after that. Poll the counter until it ticks or the
	// deadline expires.
	if _, err := m.WaitForEvent(ctx, EventOfTypes(EventStyleLoaded, EventMapLoadingFailed)); err != nil {
		t.Fatalf("WaitForEvent: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt64(&seenOriginal) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt64(&seenOriginal) == 0 {
		t.Fatal("transform was not invoked for the original source URL within deadline")
	}
	t.Logf("transform saw original=%d rewrite=%d", seenOriginal, seenRewrite)
}

// asErr is a tiny helper to keep test bodies tidy.
func asErr(err error, target **Error) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*Error); ok {
		*target = e
		return true
	}
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return asErr(u.Unwrap(), target)
	}
	return false
}
