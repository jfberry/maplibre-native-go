//go:build darwin

package maplibre

/*
#cgo darwin LDFLAGS: -framework Metal -framework Foundation

#include <stdint.h>
#include "maplibre_native_abi.h"

// MTLCreateSystemDefaultDevice is declared in <Metal/MTLDevice.h> but we keep
// the prototype here to avoid pulling Objective-C headers into cgo. The
// function returns an id<MTLDevice> with +1 retain (NS_RETURNS_RETAINED). For
// this PoC we hold the reference for the lifetime of the process; an
// explicit release path can be added when texture sessions are reattached.
extern void* MTLCreateSystemDefaultDevice(void);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// AttachMetalTexture creates a Metal texture session bound to the map.
// Allocates a default Metal device internally. Use AttachMetalTextureWithDevice
// if you need to share a device with another Metal consumer (e.g. a host
// CAMetalLayer compositor).
func (m *Map) AttachMetalTexture(width, height uint32, scaleFactor float64) (*TextureSession, error) {
	device := C.MTLCreateSystemDefaultDevice()
	if device == nil {
		return nil, &Error{
			Status:  StatusUnsupported,
			Op:      "Map.AttachMetalTexture",
			Message: "MTLCreateSystemDefaultDevice returned nil",
		}
	}
	return m.AttachMetalTextureWithDevice(unsafe.Pointer(device), width, height, scaleFactor)
}

// AttachMetalTextureWithDevice creates a Metal texture session bound to the
// map using a caller-provided id<MTLDevice>. The device must remain valid for
// the lifetime of the texture session.
//
// Use this variant when the host renderer (e.g. a CAMetalLayer compositor)
// already owns a device and you want maplibre's offscreen texture to live on
// the same device so it can be sampled directly without cross-device copies.
func (m *Map) AttachMetalTextureWithDevice(device unsafe.Pointer, width, height uint32, scaleFactor float64) (*TextureSession, error) {
	if m == nil || m.ptr == nil {
		return nil, &Error{
			Status:  StatusInvalidArgument,
			Op:      "Map.AttachMetalTextureWithDevice",
			Message: "map is closed",
		}
	}
	if device == nil {
		return nil, &Error{
			Status:  StatusInvalidArgument,
			Op:      "Map.AttachMetalTextureWithDevice",
			Message: "device must not be nil",
		}
	}
	if width == 0 || height == 0 || scaleFactor <= 0 {
		return nil, &Error{
			Status:  StatusInvalidArgument,
			Op:      "Map.AttachMetalTextureWithDevice",
			Message: fmt.Sprintf("invalid dimensions: %dx%d @%v", width, height, scaleFactor),
		}
	}

	s := &TextureSession{m: m}
	var err error
	m.rt.d.do(func() {
		desc := C.mln_metal_texture_descriptor_default()
		desc.width = C.uint32_t(width)
		desc.height = C.uint32_t(height)
		desc.scale_factor = C.double(scaleFactor)
		desc.device = device

		var out *C.mln_texture_session
		status := C.mln_metal_texture_attach(m.ptr, &desc, &out)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_metal_texture_attach", status)
			return
		}
		s.ptr = out
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// AcquireFrame borrows the most recently rendered Metal texture. Each acquire
// must be balanced by ReleaseFrame before the next render or destroy.
func (s *TextureSession) AcquireFrame() (TextureFrame, error) {
	var out TextureFrame
	if s == nil || s.ptr == nil {
		return out, &Error{Status: StatusInvalidArgument, Op: "TextureSession.AcquireFrame", Message: "session is closed"}
	}
	var err error
	s.m.rt.d.do(func() {
		var frame C.mln_metal_texture_frame
		frame.size = C.uint32_t(unsafe.Sizeof(frame))
		status := C.mln_metal_texture_acquire_frame(s.ptr, &frame)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_metal_texture_acquire_frame", status)
			return
		}
		out = TextureFrame{
			Generation:  uint64(frame.generation),
			Width:       uint32(frame.width),
			Height:      uint32(frame.height),
			ScaleFactor: float64(frame.scale_factor),
			FrameID:     uint64(frame.frame_id),
			Texture:     frame.texture,
			Device:      frame.device,
			PixelFormat: uint64(frame.pixel_format),
		}
	})
	return out, err
}

// ReleaseFrame returns ownership of a previously acquired frame.
func (s *TextureSession) ReleaseFrame(f TextureFrame) error {
	if s == nil || s.ptr == nil {
		return &Error{Status: StatusInvalidArgument, Op: "TextureSession.ReleaseFrame", Message: "session is closed"}
	}
	var err error
	s.m.rt.d.do(func() {
		var frame C.mln_metal_texture_frame
		frame.size = C.uint32_t(unsafe.Sizeof(frame))
		frame.generation = C.uint64_t(f.Generation)
		frame.width = C.uint32_t(f.Width)
		frame.height = C.uint32_t(f.Height)
		frame.scale_factor = C.double(f.ScaleFactor)
		frame.frame_id = C.uint64_t(f.FrameID)
		frame.texture = f.Texture
		frame.device = f.Device
		frame.pixel_format = C.uint64_t(f.PixelFormat)
		status := C.mln_metal_texture_release_frame(s.ptr, &frame)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_metal_texture_release_frame", status)
		}
	})
	return err
}
