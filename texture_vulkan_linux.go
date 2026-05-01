//go:build linux

package maplibre

/*
#cgo linux pkg-config: vulkan

#include <stdint.h>
#include <stdlib.h>
#include "maplibre_native_c.h"

// Mirrors the struct in vulkan_helper_linux.c. Field types are void* so the
// Go side does not need <vulkan/vulkan.h>.
typedef struct mln_go_vulkan_context {
  void *instance;
  void *physical_device;
  void *device;
  void *queue;
  uint32_t queue_family_index;
} mln_go_vulkan_context;

extern int  mln_go_vulkan_context_create(mln_go_vulkan_context *out,
                                         char *err_out, size_t err_len);
extern void mln_go_vulkan_context_destroy(mln_go_vulkan_context *ctx);

// Implemented in vulkan_readback_linux.c.
extern int mln_go_vulkan_readback(
    void *instance, void *physical_device, void *device, void *queue,
    uint32_t queue_family_index,
    void *image, uint32_t image_layout,
    uint32_t width, uint32_t height,
    uint8_t *out_rgba, size_t out_capacity,
    char *err_out, size_t err_len);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// VulkanContext bundles the Vulkan handles maplibre-native needs to render
// into a host-managed device. All four pointer fields must be valid Vulkan
// handles for the lifetime of the texture session.
type VulkanContext struct {
	Instance            unsafe.Pointer // VkInstance
	PhysicalDevice      unsafe.Pointer // VkPhysicalDevice
	Device              unsafe.Pointer // VkDevice
	GraphicsQueue       unsafe.Pointer // VkQueue
	GraphicsQueueFamily uint32
}

// AttachVulkanTexture creates a Vulkan texture session bound to the map.
// Spins up a default Vulkan context internally — the first physical device
// with a graphics queue family, no extensions, no surfaces. On a host with
// only Mesa lavapipe installed this picks lavapipe; on a host with a real
// GPU it picks that.
//
// The returned session owns the internally-created context and tears it down
// on Close. Use AttachVulkanTextureWithContext to share with an existing
// Vulkan stack.
func (m *Map) AttachVulkanTexture(width, height uint32, scaleFactor float64) (*TextureSession, error) {
	if m == nil {
		return nil, errClosed("Map.AttachVulkanTexture", "map")
	}
	var raw C.mln_go_vulkan_context
	var errBuf [256]C.char
	rc := C.mln_go_vulkan_context_create(&raw, &errBuf[0], C.size_t(len(errBuf)))
	if rc != 0 {
		return nil, &Error{
			Status:  StatusUnsupported,
			Op:      "Map.AttachVulkanTexture",
			Message: C.GoString(&errBuf[0]),
		}
	}

	ctx := VulkanContext{
		Instance:            raw.instance,
		PhysicalDevice:      raw.physical_device,
		Device:              raw.device,
		GraphicsQueue:       raw.queue,
		GraphicsQueueFamily: uint32(raw.queue_family_index),
	}
	s, err := m.AttachVulkanTextureWithContext(ctx, width, height, scaleFactor)
	if err != nil {
		C.mln_go_vulkan_context_destroy(&raw)
		return nil, err
	}
	rawCopy := raw
	s.cleanup = func() { C.mln_go_vulkan_context_destroy(&rawCopy) }
	return s, nil
}

// AttachVulkanTextureWithContext creates a Vulkan texture session using a
// caller-provided Vulkan context. The handles must remain valid for the
// session lifetime; teardown of the context is the caller's responsibility.
func (m *Map) AttachVulkanTextureWithContext(ctx VulkanContext, width, height uint32, scaleFactor float64) (*TextureSession, error) {
	if m == nil {
		return nil, errClosed("Map.AttachVulkanTextureWithContext", "map")
	}
	if ctx.Instance == nil || ctx.PhysicalDevice == nil ||
		ctx.Device == nil || ctx.GraphicsQueue == nil {
		return nil, &Error{
			Status:  StatusInvalidArgument,
			Op:      "Map.AttachVulkanTextureWithContext",
			Message: "all four Vulkan handles must be non-nil",
		}
	}
	if err := validateAttachDims("Map.AttachVulkanTextureWithContext", width, height, scaleFactor); err != nil {
		return nil, err
	}

	s := &TextureSession{m: m}
	err := m.rt.runOnOwner("Map.AttachVulkanTextureWithContext", func() error {
		if m.ptr == nil {
			return errClosed("Map.AttachVulkanTextureWithContext", "map")
		}
		desc := C.mln_vulkan_texture_descriptor_default()
		desc.width = C.uint32_t(width)
		desc.height = C.uint32_t(height)
		desc.scale_factor = C.double(scaleFactor)
		desc.instance = ctx.Instance
		desc.physical_device = ctx.PhysicalDevice
		desc.device = ctx.Device
		desc.graphics_queue = ctx.GraphicsQueue
		desc.graphics_queue_family_index = C.uint32_t(ctx.GraphicsQueueFamily)

		var out *C.mln_texture_session
		if status := C.mln_vulkan_texture_attach(m.ptr, &desc, &out); status != C.MLN_STATUS_OK {
			return statusError("mln_vulkan_texture_attach", status)
		}
		s.ptr = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Stash the context so readbackFrame can find the queue/queue-family/
	// physical device that aren't carried in TextureFrame.
	data := &vulkanSessionData{ctx: ctx}
	s.backend = unsafe.Pointer(data)
	trackForLeak(s, "TextureSession (Vulkan)", func() bool { return s.ptr != nil })
	return s, nil
}

// AcquireFrame borrows the most recently rendered Vulkan frame. The returned
// VkImage / VkImageView are valid only until ReleaseFrame is called.
func (s *TextureSession) AcquireFrame() (TextureFrame, error) {
	if s == nil {
		return TextureFrame{}, errClosed("TextureSession.AcquireFrame", "session")
	}
	var out TextureFrame
	err := s.m.rt.runOnOwner("TextureSession.AcquireFrame", func() error {
		if s.ptr == nil {
			return errClosed("TextureSession.AcquireFrame", "session")
		}
		var frame C.mln_vulkan_texture_frame
		frame.size = C.uint32_t(unsafe.Sizeof(frame))
		if status := C.mln_vulkan_texture_acquire_frame(s.ptr, &frame); status != C.MLN_STATUS_OK {
			return statusError("mln_vulkan_texture_acquire_frame", status)
		}
		out = TextureFrame{
			Generation:  uint64(frame.generation),
			Width:       uint32(frame.width),
			Height:      uint32(frame.height),
			ScaleFactor: float64(frame.scale_factor),
			FrameID:     uint64(frame.frame_id),
			Texture:     frame.image,
			ImageView:   frame.image_view,
			Device:      frame.device,
			PixelFormat: uint64(frame.format),
			Layout:      uint32(frame.layout),
		}
		return nil
	})
	return out, err
}

// vulkanSessionData is held off-band via TextureSession.backend so
// readbackFrame can find the Vulkan handles it needs (the frame only
// carries VkImage and VkDevice; readback also needs the VkQueue + queue
// family + VkPhysicalDevice).
type vulkanSessionData struct {
	ctx VulkanContext
}

func sessionVulkanContext(s *TextureSession) (VulkanContext, bool) {
	if s == nil || s.backend == nil {
		return VulkanContext{}, false
	}
	return (*vulkanSessionData)(s.backend).ctx, true
}

// readbackFrame copies the GPU contents of a Vulkan frame into dst as
// tightly-packed premultiplied RGBA. Adapts the
// vkCmdCopyImageToBuffer pattern from mbgl::vulkan::Texture2D::readImage.
func readbackFrame(s *TextureSession, f TextureFrame, dst []byte) error {
	if f.Texture == nil {
		return &Error{
			Status:  StatusInvalidArgument,
			Op:      "readbackFrame",
			Message: "frame VkImage is nil",
		}
	}
	needed := int(f.Width) * int(f.Height) * 4
	if len(dst) < needed {
		return &Error{
			Status:  StatusInvalidArgument,
			Op:      "readbackFrame",
			Message: fmt.Sprintf("dst length %d < needed %d", len(dst), needed),
		}
	}
	ctx, ok := sessionVulkanContext(s)
	if !ok {
		return &Error{
			Status:  StatusInvalidState,
			Op:      "readbackFrame",
			Message: "session has no Vulkan context",
		}
	}

	var errBuf [256]C.char
	var rc C.int
	if dErr := s.m.rt.runOnOwner("readbackFrame", func() error {
		rc = C.mln_go_vulkan_readback(
			ctx.Instance,
			ctx.PhysicalDevice,
			ctx.Device,
			ctx.GraphicsQueue,
			C.uint32_t(ctx.GraphicsQueueFamily),
			f.Texture,
			C.uint32_t(f.Layout),
			C.uint32_t(f.Width),
			C.uint32_t(f.Height),
			(*C.uint8_t)(unsafe.Pointer(&dst[0])),
			C.size_t(len(dst)),
			&errBuf[0], C.size_t(len(errBuf)),
		)
		return nil
	}); dErr != nil {
		return dErr
	}
	if rc != 0 {
		return &Error{
			Status:  StatusNativeError,
			Op:      "mln_go_vulkan_readback",
			Message: C.GoString(&errBuf[0]),
		}
	}
	return nil
}

// ReleaseFrame returns ownership of a previously acquired Vulkan frame.
func (s *TextureSession) ReleaseFrame(f TextureFrame) error {
	if s == nil {
		return errClosed("TextureSession.ReleaseFrame", "session")
	}
	return s.m.rt.runOnOwner("TextureSession.ReleaseFrame", func() error {
		if s.ptr == nil {
			return errClosed("TextureSession.ReleaseFrame", "session")
		}
		var frame C.mln_vulkan_texture_frame
		frame.size = C.uint32_t(unsafe.Sizeof(frame))
		frame.generation = C.uint64_t(f.Generation)
		frame.width = C.uint32_t(f.Width)
		frame.height = C.uint32_t(f.Height)
		frame.scale_factor = C.double(f.ScaleFactor)
		frame.frame_id = C.uint64_t(f.FrameID)
		frame.image = f.Texture
		frame.image_view = f.ImageView
		frame.device = f.Device
		frame.format = C.uint32_t(f.PixelFormat)
		frame.layout = C.uint32_t(f.Layout)
		if status := C.mln_vulkan_texture_release_frame(s.ptr, &frame); status != C.MLN_STATUS_OK {
			return statusError("mln_vulkan_texture_release_frame", status)
		}
		return nil
	})
}
