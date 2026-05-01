//go:build darwin

package maplibre

/*
#cgo darwin LDFLAGS: -framework Metal -framework Foundation

#include <stdint.h>
#include <stddef.h>
#include "maplibre_native_c.h"

// MTLCreateSystemDefaultDevice is declared in <Metal/MTLDevice.h> but we keep
// the prototype here to avoid pulling Objective-C headers into cgo. The
// function returns an id<MTLDevice> with +1 retain (NS_RETURNS_RETAINED). For
// this PoC we hold the reference for the lifetime of the process; an
// explicit release path can be added when texture sessions are reattached.
extern void* MTLCreateSystemDefaultDevice(void);

// Implemented in metal_readback_darwin.m. Blits a borrowed id<MTLTexture>
// into a transient host-visible MTLBuffer and memcpys to out_rgba. Returns
// 0 on success; non-zero with err_out populated on failure.
extern int mln_go_metal_readback(
    void *device, void *texture,
    uint8_t *out_rgba, size_t out_capacity,
    uint32_t width, uint32_t height,
    char *err_out, size_t err_len);
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
	if m == nil {
		return nil, errClosed("Map.AttachMetalTextureWithDevice", "map")
	}
	if device == nil {
		return nil, &Error{
			Status:  StatusInvalidArgument,
			Op:      "Map.AttachMetalTextureWithDevice",
			Message: "device must not be nil",
		}
	}
	if err := validateAttachDims("Map.AttachMetalTextureWithDevice", width, height, scaleFactor); err != nil {
		return nil, err
	}

	s := &TextureSession{m: m}
	err := m.rt.runOnOwner("Map.AttachMetalTextureWithDevice", func() error {
		if m.ptr == nil {
			return errClosed("Map.AttachMetalTextureWithDevice", "map")
		}
		desc := C.mln_metal_texture_descriptor_default()
		desc.width = C.uint32_t(width)
		desc.height = C.uint32_t(height)
		desc.scale_factor = C.double(scaleFactor)
		desc.device = device

		var out *C.mln_texture_session
		if status := C.mln_metal_texture_attach(m.ptr, &desc, &out); status != C.MLN_STATUS_OK {
			return statusError("mln_metal_texture_attach", status)
		}
		s.ptr = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	trackForLeak(s, "TextureSession (Metal)", func() bool { return s.ptr != nil })
	return s, nil
}

// AcquireFrame borrows the most recently rendered Metal texture. Each acquire
// must be balanced by ReleaseFrame before the next render or destroy.
func (s *TextureSession) AcquireFrame() (TextureFrame, error) {
	if s == nil {
		return TextureFrame{}, errClosed("TextureSession.AcquireFrame", "session")
	}
	var out TextureFrame
	err := s.m.rt.runOnOwner("TextureSession.AcquireFrame", func() error {
		if s.ptr == nil {
			return errClosed("TextureSession.AcquireFrame", "session")
		}
		var frame C.mln_metal_texture_frame
		frame.size = C.uint32_t(unsafe.Sizeof(frame))
		if status := C.mln_metal_texture_acquire_frame(s.ptr, &frame); status != C.MLN_STATUS_OK {
			return statusError("mln_metal_texture_acquire_frame", status)
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
		return nil
	})
	return out, err
}

// readbackFrame copies the GPU contents of a freshly acquired Metal frame
// into dst as tightly-packed premultiplied RGBA. Caller is responsible for
// dst having at least width*height*4 capacity.
func readbackFrame(s *TextureSession, f TextureFrame, dst []byte) error {
	if f.Texture == nil || f.Device == nil {
		return &Error{
			Status:  StatusInvalidArgument,
			Op:      "readbackFrame",
			Message: "frame texture or device is nil",
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
	var errBuf [256]C.char
	var rc C.int
	if dErr := s.m.rt.runOnOwner("readbackFrame", func() error {
		rc = C.mln_go_metal_readback(
			f.Device,
			f.Texture,
			(*C.uint8_t)(unsafe.Pointer(&dst[0])),
			C.size_t(len(dst)),
			C.uint32_t(f.Width),
			C.uint32_t(f.Height),
			&errBuf[0], C.size_t(len(errBuf)),
		)
		return nil
	}); dErr != nil {
		return dErr
	}
	if rc != 0 {
		return &Error{
			Status:  StatusNativeError,
			Op:      "mln_go_metal_readback",
			Message: C.GoString(&errBuf[0]),
		}
	}
	return nil
}

// ReleaseFrame returns ownership of a previously acquired frame.
func (s *TextureSession) ReleaseFrame(f TextureFrame) error {
	if s == nil {
		return errClosed("TextureSession.ReleaseFrame", "session")
	}
	return s.m.rt.runOnOwner("TextureSession.ReleaseFrame", func() error {
		if s.ptr == nil {
			return errClosed("TextureSession.ReleaseFrame", "session")
		}
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
		if status := C.mln_metal_texture_release_frame(s.ptr, &frame); status != C.MLN_STATUS_OK {
			return statusError("mln_metal_texture_release_frame", status)
		}
		return nil
	})
}
