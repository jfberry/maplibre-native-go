package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

// validateAttachDims is the shared dimension/scale guard for the
// platform attach paths. Centralised so error messages stay
// identical across backends.
func validateAttachDims(op string, width, height uint32, scaleFactor float64) error {
	if width == 0 || height == 0 || scaleFactor <= 0 {
		return &Error{
			Status:  StatusInvalidArgument,
			Op:      op,
			Message: fmt.Sprintf("invalid dimensions: %dx%d @%v", width, height, scaleFactor),
		}
	}
	return nil
}

// TextureFrame is the platform-neutral shape of a frame acquired from a
// session-owned texture. Backend-specific data lives in the borrowed
// pointers: for Metal, Texture is id<MTLTexture> and Device is
// id<MTLDevice>; for Vulkan, Texture is VkImage and Device is VkDevice
// (with ImageView holding the matching VkImageView and Layout the
// VkImageLayout).
//
// The borrowed pointers remain valid only until ReleaseFrame is called.
// Most callers want RenderImage / RenderImageInto, which use the native
// readback path and never expose this type.
type TextureFrame struct {
	Generation  uint64
	Width       uint32
	Height      uint32
	ScaleFactor float64
	FrameID     uint64
	Texture     unsafe.Pointer
	ImageView   unsafe.Pointer // Vulkan only: VkImageView. Nil on Metal.
	Device      unsafe.Pointer
	PixelFormat uint64 // Metal: MTLPixelFormat. Vulkan: VkFormat (uint32 widened).
	Layout      uint32 // Vulkan only: VkImageLayout. 0 on Metal.
}

// TextureSession wraps an mln_texture_session attached to a Map. It is
// the offscreen-render half of the C ABI's render-target sessions
// (Surface sessions, which present to a UI surface, are a separate
// type — not yet exposed by this binding).
//
// All session methods dispatch through the owning Map's Runtime;
// callers may use TextureSession from any goroutine.
type TextureSession struct {
	m       *Map
	ptr     *C.mln_texture_session
	cleanup func() // called on the dispatcher after destroy succeeds
}

// AttachTexture creates a session-owned offscreen texture using the
// platform-default backend (Metal on darwin, Vulkan on linux). mbgl
// allocates the device/queue itself; the caller doesn't need to
// supply or share GPU handles.
//
// Sessions returned by AttachTexture support the readback path
// (RenderImage / RenderImageInto) but not backend-specific
// AcquireFrame access. Use AttachMetalTexture / AttachVulkanTexture
// when you need the rendered MTLTexture or VkImage handle, or when
// you need to share a device with another GPU consumer.
func (m *Map) AttachTexture(width, height uint32, scaleFactor float64) (*TextureSession, error) {
	if m == nil {
		return nil, errClosed("Map.AttachTexture", "map")
	}
	if err := validateAttachDims("Map.AttachTexture", width, height, scaleFactor); err != nil {
		return nil, err
	}
	s := &TextureSession{m: m}
	err := m.rt.runOnOwner("Map.AttachTexture", func() error {
		if m.ptr == nil {
			return errClosed("Map.AttachTexture", "map")
		}
		desc := C.mln_owned_texture_descriptor_default()
		desc.width = C.uint32_t(width)
		desc.height = C.uint32_t(height)
		desc.scale_factor = C.double(scaleFactor)
		var out *C.mln_texture_session
		if status := C.mln_owned_texture_attach(m.ptr, &desc, &out); status != C.MLN_STATUS_OK {
			return statusError("mln_owned_texture_attach", status)
		}
		s.ptr = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	trackForLeak(s, "TextureSession", func() bool { return s.ptr != nil })
	return s, nil
}

// Resize advances the session's generation and reallocates backing storage.
func (s *TextureSession) Resize(width, height uint32, scaleFactor float64) error {
	if s == nil {
		return errClosed("TextureSession.Resize", "session")
	}
	return s.m.rt.runOnOwner("TextureSession.Resize", func() error {
		if s.ptr == nil {
			return errClosed("TextureSession.Resize", "session")
		}
		if status := C.mln_texture_resize(s.ptr, C.uint32_t(width), C.uint32_t(height), C.double(scaleFactor)); status != C.MLN_STATUS_OK {
			return statusError("mln_texture_resize", status)
		}
		return nil
	})
}

// RenderUpdate processes one render-target update for the session.
//
// Continuous-mode only: call after receiving a
// MLN_RUNTIME_EVENT_MAP_RENDER_UPDATE_AVAILABLE event for this session's
// map. Returns StatusInvalidState if no frame is currently produced for
// the update; keep pumping events and try again. Static-mode renders go
// through Map.RenderStill (which uses request_still_image internally
// and never touches this path).
func (s *TextureSession) RenderUpdate() error {
	if s == nil {
		return errClosed("TextureSession.RenderUpdate", "session")
	}
	return s.m.rt.runOnOwner("TextureSession.RenderUpdate", func() error {
		if s.ptr == nil {
			return errClosed("TextureSession.RenderUpdate", "session")
		}
		if status := C.mln_texture_render_update(s.ptr); status != C.MLN_STATUS_OK {
			return statusError("mln_texture_render_update", status)
		}
		return nil
	})
}

// Detach releases backend resources but keeps the session handle live for
// destroy.
func (s *TextureSession) Detach() error {
	if s == nil {
		return nil
	}
	return s.m.rt.runOnOwner("TextureSession.Detach", func() error {
		if s.ptr == nil {
			return nil
		}
		if status := C.mln_texture_detach(s.ptr); status != C.MLN_STATUS_OK {
			return statusError("mln_texture_detach", status)
		}
		return nil
	})
}

// readPremultipliedRGBA8 reads the most recently rendered frame into
// dst, or — when dst is nil — probes for the required buffer size.
// Runs on the dispatcher.
//
// Probe mode (dst == nil): the C ABI returns INVALID_ARGUMENT with
// info filled in. This helper swallows that status and returns the
// dimensions instead. Real-call mode (dst != nil): INVALID_ARGUMENT
// from a too-small dst surfaces to the caller as an *Error.
func (s *TextureSession) readPremultipliedRGBA8(dst []byte) (width, height, byteLength int, err error) {
	probe := len(dst) == 0
	derr := s.m.rt.runOnOwner("TextureSession.readPremultipliedRGBA8", func() error {
		if s.ptr == nil {
			return errClosed("TextureSession.readPremultipliedRGBA8", "session")
		}
		var info C.mln_texture_image_info
		info.size = C.uint32_t(unsafe.Sizeof(info))
		var data *C.uint8_t
		var capacity C.size_t
		if !probe {
			data = (*C.uint8_t)(unsafe.Pointer(&dst[0]))
			capacity = C.size_t(len(dst))
		}
		status := C.mln_texture_read_premultiplied_rgba8(s.ptr, data, capacity, &info)
		// info is filled regardless of status per ABI doc.
		width = int(info.width)
		height = int(info.height)
		byteLength = int(info.byte_length)
		if probe && status == C.MLN_STATUS_INVALID_ARGUMENT {
			return nil
		}
		if status != C.MLN_STATUS_OK {
			return statusError("mln_texture_read_premultiplied_rgba8", status)
		}
		return nil
	})
	return width, height, byteLength, derr
}

// RenderImage drives the static-render protocol and returns the rendered
// map as RGBA bytes.
//
// The returned buffer is **premultiplied** RGBA (matching mbgl's native
// PremultipliedImage), tightly packed (one row = width*4 bytes), with
// width and height in physical pixels (logical * scale_factor). A new
// slice is allocated per call; use RenderImageInto for buffer reuse in
// tight loops.
//
// Cancellation: returns ctx.Err() wrapped in ErrTimeout when ctx is done
// before STILL_IMAGE_FINISHED arrives.
func (m *Map) RenderImage(ctx context.Context, sess *TextureSession) (rgba []byte, width, height int, err error) {
	if err := m.requestStillAndWait(ctx, sess); err != nil {
		return nil, 0, 0, err
	}
	w, h, byteLen, err := sess.readPremultipliedRGBA8(nil)
	if err != nil {
		return nil, 0, 0, err
	}
	rgba = make([]byte, byteLen)
	if _, _, _, err := sess.readPremultipliedRGBA8(rgba); err != nil {
		return nil, 0, 0, err
	}
	return rgba, w, h, nil
}

// RenderImageInto is the buffer-reuse variant of RenderImage.
// dst must have at least width*height*4 bytes; if not, returns an Error
// with StatusInvalidArgument and dst is untouched. Returns the actual
// width and height of the rendered frame; the row stride is always w*4.
//
// width/height aren't known until the frame is rendered, so a typical
// caller pre-allocates a buffer sized to its known viewport (matching
// MapOptions or TextureSession.Resize) and feeds the same slice to every
// render.
func (m *Map) RenderImageInto(ctx context.Context, sess *TextureSession, dst []byte) (width, height int, err error) {
	if err := m.requestStillAndWait(ctx, sess); err != nil {
		return 0, 0, err
	}
	w, h, _, err := sess.readPremultipliedRGBA8(dst)
	return w, h, err
}

// UnpremultiplyRGBA converts premultiplied RGBA bytes (the format
// RenderImage / RenderImageInto returns) to non-premultiplied RGBA in
// place into dst. dst and src must be the same length and a multiple
// of 4. Pixels with alpha 0 or 255 short-circuit since the math is
// the identity. dst may alias src for in-place conversion.
//
// Most PNG/JPEG encoders expect non-premultiplied RGBA; call this
// before handing pixels to image.NewNRGBA + png.Encode and friends.
func UnpremultiplyRGBA(dst, src []byte) {
	for i := 0; i < len(src); i += 4 {
		r, g, b, a := src[i], src[i+1], src[i+2], src[i+3]
		if a == 0 || a == 255 {
			dst[i+0], dst[i+1], dst[i+2], dst[i+3] = r, g, b, a
			continue
		}
		dst[i+0] = byte((uint32(r)*255 + uint32(a)/2) / uint32(a))
		dst[i+1] = byte((uint32(g)*255 + uint32(a)/2) / uint32(a))
		dst[i+2] = byte((uint32(b)*255 + uint32(a)/2) / uint32(a))
		dst[i+3] = a
	}
}

// Close destroys the session handle. If still attached, this detaches first.
// Idempotent.
func (s *TextureSession) Close() error {
	if s == nil {
		return nil
	}
	return s.m.rt.runOnOwner("TextureSession.Close", func() error {
		if s.ptr == nil {
			return nil
		}
		if status := C.mln_texture_destroy(s.ptr); status != C.MLN_STATUS_OK {
			return statusError("mln_texture_destroy", status)
		}
		s.ptr = nil
		if s.cleanup != nil {
			s.cleanup()
			s.cleanup = nil
		}
		return nil
	})
}
