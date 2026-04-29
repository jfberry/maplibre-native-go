//go:build darwin

package maplibre

import (
	"testing"
)

func TestMetalTextureLifecycle(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	sess, err := m.AttachMetalTexture(256, 256, 1.0)
	if err != nil {
		t.Fatalf("AttachMetalTexture: %v", err)
	}
	if sess == nil {
		t.Fatal("AttachMetalTexture returned nil session")
	}
	t.Cleanup(func() { _ = sess.Close() })

	if err := sess.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}

	frame, err := sess.AcquireFrame()
	if err != nil {
		t.Fatalf("AcquireFrame: %v", err)
	}
	if frame.Texture == nil {
		t.Fatal("acquired frame has nil texture pointer")
	}
	if frame.Device == nil {
		t.Fatal("acquired frame has nil device pointer")
	}
	if frame.Width == 0 || frame.Height == 0 {
		t.Fatalf("acquired frame has zero dimensions: %dx%d", frame.Width, frame.Height)
	}

	if err := sess.ReleaseFrame(frame); err != nil {
		t.Fatalf("ReleaseFrame: %v", err)
	}

	if err := sess.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMetalTextureResizeBetweenRenders(t *testing.T) {
	m := newTestMap(t)
	loadEmptyStyle(t, m)

	sess, err := m.AttachMetalTexture(256, 256, 1.0)
	if err != nil {
		t.Fatalf("AttachMetalTexture: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	if err := sess.Render(); err != nil {
		t.Fatalf("Render 256: %v", err)
	}

	if err := sess.Resize(512, 384, 1.0); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if err := sess.Render(); err != nil {
		t.Fatalf("Render 512x384: %v", err)
	}

	frame, err := sess.AcquireFrame()
	if err != nil {
		t.Fatalf("AcquireFrame: %v", err)
	}
	if frame.Width != 512 || frame.Height != 384 {
		t.Fatalf("frame size after resize = %dx%d, want 512x384", frame.Width, frame.Height)
	}
	if err := sess.ReleaseFrame(frame); err != nil {
		t.Fatalf("ReleaseFrame: %v", err)
	}
}

func TestMetalAttachInvalidDimensions(t *testing.T) {
	m := newTestMap(t)
	if _, err := m.AttachMetalTexture(0, 256, 1); err == nil {
		t.Fatal("expected error for zero width, got nil")
	}
	if _, err := m.AttachMetalTexture(256, 256, 0); err == nil {
		t.Fatal("expected error for zero scaleFactor, got nil")
	}
}
