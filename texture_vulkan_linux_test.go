//go:build linux

package maplibre

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestVulkanTextureLifecycle(t *testing.T) {
	rt := newTestRuntime(t)
	m, err := rt.NewMap(MapOptions{Width: 256, Height: 256, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if err := m.SetStyleJSON(`{"version":8,"sources":{},"layers":[]}`); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer loadCancel()
	if _, err := m.WaitForEvent(loadCtx, func(e Event) bool {
		return e.Type == EventStyleLoaded || e.Type == EventMapLoadingFailed
	}); err != nil {
		t.Fatalf("waiting for STYLE_LOADED: %v", err)
	}

	sess, err := m.AttachVulkanTexture(256, 256, 1.0)
	if err != nil {
		var mlnErr *Error
		if errors.As(err, &mlnErr) && mlnErr.Status == StatusUnsupported {
			t.Skipf("Vulkan unavailable (no ICD?): %v", err)
		}
		t.Fatalf("AttachVulkanTexture: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	renderCtx, renderCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer renderCancel()
	frame, err := m.RenderStill(renderCtx, sess)
	if err != nil {
		t.Fatalf("RenderStill: %v", err)
	}
	if frame.Texture == nil {
		t.Fatal("acquired frame has nil VkImage")
	}
	if frame.ImageView == nil {
		t.Fatal("acquired frame has nil VkImageView")
	}
	if frame.Device == nil {
		t.Fatal("acquired frame has nil VkDevice")
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

func TestVulkanAttachInvalidArgs(t *testing.T) {
	rt := newTestRuntime(t)
	m, err := rt.NewMap(MapOptions{Width: 256, Height: 256, ScaleFactor: 1})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	// Empty context — should reject before touching the ABI.
	if _, err := m.AttachVulkanTextureWithContext(VulkanContext{}, 256, 256, 1); err == nil {
		t.Fatal("expected error for empty VulkanContext, got nil")
	}
}
