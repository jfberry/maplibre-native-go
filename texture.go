package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

import (
	"fmt"
	"time"
	"unsafe"
)

// TextureFrame is the platform-neutral shape of a frame acquired from a
// texture session. Backend-specific data lives in the borrowed pointers:
// for Metal, Texture is id<MTLTexture> and Device is id<MTLDevice>; for
// Vulkan, Texture is VkImage and Device is VkDevice (with ImageView holding
// the matching VkImageView and Layout the VkImageLayout).
//
// The borrowed pointers remain valid only until the frame is released.
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

// TextureSession wraps an mln_texture_session attached via either the Metal
// or Vulkan backend. Backend-neutral lifecycle (resize / render / detach /
// destroy) is shared; backend-specific attach + acquire/release frame live
// in build-tagged files.
type TextureSession struct {
	m       *Map
	ptr     *C.mln_texture_session
	cleanup func()         // called on the dispatcher after destroy succeeds
	backend unsafe.Pointer // backend-private extras (e.g. *vulkanSessionData on linux)
}

// Resize advances the session's generation and reallocates backing storage.
func (s *TextureSession) Resize(width, height uint32, scaleFactor float64) error {
	if s == nil || s.ptr == nil {
		return &Error{Status: StatusInvalidArgument, Op: "TextureSession.Resize", Message: "session is closed"}
	}
	var err error
	s.m.rt.d.do(func() {
		status := C.mln_texture_resize(s.ptr, C.uint32_t(width), C.uint32_t(height), C.double(scaleFactor))
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_texture_resize", status)
		}
	})
	return err
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
	if s == nil || s.ptr == nil {
		return &Error{Status: StatusInvalidArgument, Op: "TextureSession.RenderUpdate", Message: "session is closed"}
	}
	var err error
	s.m.rt.d.do(func() {
		status := C.mln_texture_render_update(s.ptr)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_texture_render_update", status)
		}
	})
	return err
}

// Detach releases backend resources but keeps the session handle live for
// destroy.
func (s *TextureSession) Detach() error {
	if s == nil || s.ptr == nil {
		return nil
	}
	var err error
	s.m.rt.d.do(func() {
		status := C.mln_texture_detach(s.ptr)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_texture_detach", status)
		}
	})
	return err
}

// RenderStillImage drives the static-render protocol and returns the
// rendered map as RGBA bytes.
//
// The returned buffer is **premultiplied** RGBA (matching mbgl's native
// PremultipliedImage), tightly packed (stride == width*4), with width and
// height in physical pixels (logical * scale_factor). A new slice is
// allocated per call; use RenderStillImageInto for buffer reuse in tight
// loops.
//
// Internally: RenderStill -> readback -> ReleaseFrame. The frame's borrowed
// GPU handles never escape this call.
func (m *Map) RenderStillImage(sess *TextureSession, timeout time.Duration) (rgba []byte, width, height, stride int, err error) {
	frame, ferr := m.RenderStill(sess, timeout)
	if ferr != nil {
		return nil, 0, 0, 0, ferr
	}
	defer sess.ReleaseFrame(frame)

	width = int(frame.Width)
	height = int(frame.Height)
	stride = width * 4
	rgba = make([]byte, stride*height)
	if rerr := readbackFrame(sess, frame, rgba); rerr != nil {
		return nil, 0, 0, 0, rerr
	}
	return rgba, width, height, stride, nil
}

// RenderStillImageInto is the buffer-reuse variant of RenderStillImage.
// dst must have at least width*height*4 bytes; if not, returns an Error
// with StatusInvalidArgument and dst is untouched. Returns the actual
// width, height, and stride of the rendered frame.
//
// width/height aren't known until the frame is acquired, so a typical
// caller pre-allocates a buffer sized to its known viewport (matching
// MapOptions or TextureSession.Resize) and feeds the same slice to every
// render.
func (m *Map) RenderStillImageInto(sess *TextureSession, timeout time.Duration, dst []byte) (width, height, stride int, err error) {
	frame, ferr := m.RenderStill(sess, timeout)
	if ferr != nil {
		return 0, 0, 0, ferr
	}
	defer sess.ReleaseFrame(frame)

	width = int(frame.Width)
	height = int(frame.Height)
	stride = width * 4
	needed := stride * height
	if len(dst) < needed {
		return 0, 0, 0, &Error{
			Status:  StatusInvalidArgument,
			Op:      "Map.RenderStillImageInto",
			Message: fmt.Sprintf("dst length %d < needed %d (%dx%d * 4)", len(dst), needed, width, height),
		}
	}
	if rerr := readbackFrame(sess, frame, dst[:needed]); rerr != nil {
		return 0, 0, 0, rerr
	}
	return width, height, stride, nil
}

// Close destroys the session handle. If still attached, this detaches first.
// Idempotent.
func (s *TextureSession) Close() error {
	if s == nil || s.ptr == nil {
		return nil
	}
	var err error
	s.m.rt.d.do(func() {
		status := C.mln_texture_destroy(s.ptr)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_texture_destroy", status)
			return
		}
		s.ptr = nil
		if s.cleanup != nil {
			s.cleanup()
			s.cleanup = nil
		}
	})
	return err
}
