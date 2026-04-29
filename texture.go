package maplibre

/*
#include "maplibre_native_abi.h"
*/
import "C"

import "unsafe"

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
	cleanup func() // called on the dispatcher after destroy succeeds
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

// Render draws the latest map state into the offscreen texture.
func (s *TextureSession) Render() error {
	if s == nil || s.ptr == nil {
		return &Error{Status: StatusInvalidArgument, Op: "TextureSession.Render", Message: "session is closed"}
	}
	var err error
	s.m.rt.d.do(func() {
		status := C.mln_texture_render(s.ptr)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_texture_render", status)
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
