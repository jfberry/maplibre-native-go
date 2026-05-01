//go:build darwin

package maplibre

import (
	"testing"
	"time"
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

	frame, err := m.RenderStill(sess, 5*time.Second)
	if err != nil {
		t.Fatalf("RenderStill: %v", err)
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

	frame1, err := m.RenderStill(sess, 5*time.Second)
	if err != nil {
		t.Fatalf("RenderStill 256: %v", err)
	}
	if err := sess.ReleaseFrame(frame1); err != nil {
		t.Fatalf("ReleaseFrame 256: %v", err)
	}

	if err := sess.Resize(512, 384, 1.0); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	frame2, err := m.RenderStill(sess, 5*time.Second)
	if err != nil {
		t.Fatalf("RenderStill 512x384: %v", err)
	}
	if frame2.Width != 512 || frame2.Height != 384 {
		t.Fatalf("frame size after resize = %dx%d, want 512x384", frame2.Width, frame2.Height)
	}
	if err := sess.ReleaseFrame(frame2); err != nil {
		t.Fatalf("ReleaseFrame 512x384: %v", err)
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
