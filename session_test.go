package maplibre

import (
	"context"
	"errors"
	"testing"
	"time"
)

const sessionTestStyle = `{"version":8,"sources":{},"layers":[{"id":"bg","type":"background","paint":{"background-color":"#0033CC"}}]}`

// TestSessionLifecycle exercises NewSession -> Render -> SetStyleJSON
// (in-place swap) -> Render -> Close. Verifies the bundle's invariant
// that Render and SetStyle work without the caller managing event
// pumping.
func TestSessionLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := NewSession(ctx, SessionOptions{
		Map:   MapOptions{Width: 64, Height: 64},
		Style: sessionTestStyle,
	})
	if err != nil {
		var mlnErr *Error
		if errors.As(err, &mlnErr) && mlnErr.Status == StatusUnsupported {
			t.Skipf("backend unavailable: %v", err)
		}
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	rgba, w, h, err := sess.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if w != 64 || h != 64 {
		t.Fatalf("dimensions = %dx%d, want 64x64", w, h)
	}
	if len(rgba) != 64*64*4 {
		t.Fatalf("rgba len = %d, want %d", len(rgba), 64*64*4)
	}
	// Center pixel ≈ #0033CC premultiplied. Tolerance for color-space wiggle.
	off := (32*64 + 32) * 4
	r, g, b, a := rgba[off], rgba[off+1], rgba[off+2], rgba[off+3]
	if abs(int(r)-0x00) > 4 || abs(int(g)-0x33) > 4 || abs(int(b)-0xCC) > 4 || a != 255 {
		t.Errorf("center pixel rgba(%d,%d,%d,%d), want ~rgba(0,51,204,255)", r, g, b, a)
	}

	// In-place style swap.
	const yellowStyle = `{"version":8,"sources":{},"layers":[{"id":"bg","type":"background","paint":{"background-color":"#FFAA00"}}]}`
	if err := sess.SetStyleJSON(ctx, yellowStyle); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}

	rgba2, _, _, err := sess.Render(ctx)
	if err != nil {
		t.Fatalf("Render after swap: %v", err)
	}
	r2, g2, b2, _ := rgba2[off], rgba2[off+1], rgba2[off+2], rgba2[off+3]
	if abs(int(r2)-0xFF) > 4 || abs(int(g2)-0xAA) > 4 || abs(int(b2)-0x00) > 4 {
		t.Errorf("after swap, center pixel rgba(%d,%d,%d), want ~rgba(255,170,0)", r2, g2, b2)
	}
}

// TestSessionStyleLoadFailure verifies that NewSession reports an error
// when the style cannot be loaded.
func TestSessionStyleLoadFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := NewSession(ctx, SessionOptions{
		Map:   MapOptions{Width: 32, Height: 32},
		Style: `{"version":99999,"this is invalid"`,
	})
	if err == nil {
		t.Fatal("expected error for malformed style, got nil")
	}
}

// TestSessionRequiresStyle verifies the input validation.
func TestSessionRequiresStyle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := NewSession(ctx, SessionOptions{
		Map: MapOptions{Width: 32, Height: 32},
	})
	if err == nil {
		t.Fatal("expected error when Style is empty, got nil")
	}
	var mlnErr *Error
	if !errors.As(err, &mlnErr) || mlnErr.Status != StatusInvalidArgument {
		t.Fatalf("got %v, want *Error{Status: StatusInvalidArgument}", err)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
