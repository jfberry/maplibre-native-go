package maplibre

/*
#include <stdlib.h>
#include "maplibre_native_c.h"
*/
import "C"

import (
	"fmt"
	"time"
	"unsafe"
)

// Map owns a maplibre-native map handle bound to the runtime that created it.
// It is owner-thread affine to the runtime's dispatcher.
type Map struct {
	rt  *Runtime
	ptr *C.mln_map
}

// MapMode mirrors mln_map_mode. The zero-value (MapModeStatic) is the
// natural default for this binding because the public RenderStill /
// RenderImage path uses request_still_image, which requires a static- or
// tile-mode map. Set Mode = MapModeContinuous if you want to drive
// rendering yourself via TextureSession.RenderUpdate on
// MAP_RENDER_UPDATE_AVAILABLE events.
type MapMode int

const (
	MapModeStatic     MapMode = iota // 0 (default; mln_map_mode_static)
	MapModeContinuous                // 1 (mln_map_mode_continuous)
	MapModeTile                      // 2 (mln_map_mode_tile)
)

func (m MapMode) toC() C.uint32_t {
	switch m {
	case MapModeContinuous:
		return C.MLN_MAP_MODE_CONTINUOUS
	case MapModeTile:
		return C.MLN_MAP_MODE_TILE
	default:
		return C.MLN_MAP_MODE_STATIC
	}
}

// MapOptions configures NewMap. Width and Height are logical dimensions;
// physical render size is multiplied by ScaleFactor.
type MapOptions struct {
	Width       uint32
	Height      uint32
	ScaleFactor float64
	Mode        MapMode // defaults to MapModeStatic
}

// NewMap creates a map on the runtime owner thread.
//
// The runtime must be live; if it has been closed, the call returns an Error
// with StatusInvalidArgument.
func (r *Runtime) NewMap(opts MapOptions) (*Map, error) {
	if r == nil {
		return nil, errClosed("Runtime.NewMap", "runtime")
	}
	m := &Map{rt: r}
	err := r.runOnOwner("Runtime.NewMap", func() error {
		if r.ptr == nil {
			return errClosed("Runtime.NewMap", "runtime")
		}
		copts := C.mln_map_options_default()
		if opts.Width > 0 {
			copts.width = C.uint32_t(opts.Width)
		}
		if opts.Height > 0 {
			copts.height = C.uint32_t(opts.Height)
		}
		if opts.ScaleFactor > 0 {
			copts.scale_factor = C.double(opts.ScaleFactor)
		}
		copts.map_mode = opts.Mode.toC()

		var out *C.mln_map
		if status := C.mln_map_create(r.ptr, &copts, &out); status != C.MLN_STATUS_OK {
			return statusError("mln_map_create", status)
		}
		m.ptr = out
		// Register inside the dispatcher so a concurrent PollEvent can never
		// see a Map registered for an address that was about to be reused.
		r.registerMap(m)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

// Close destroys the map handle.
//
// Idempotent: safe to call on a nil Map or one already closed.
func (m *Map) Close() error {
	if m == nil {
		return nil
	}
	return m.rt.runOnOwner("Map.Close", func() error {
		if m.ptr == nil {
			return nil
		}
		cptr := uintptr(unsafe.Pointer(m.ptr))
		if status := C.mln_map_destroy(m.ptr); status != C.MLN_STATUS_OK {
			return statusError("mln_map_destroy", status)
		}
		m.ptr = nil
		m.rt.unregisterMap(cptr)
		return nil
	})
}

// SetStyleURL loads a style by URL through the native style APIs.
// Completion is signalled later via runtime events (StyleLoaded or
// MapLoadingFailed).
func (m *Map) SetStyleURL(url string) error {
	if m == nil {
		return errClosed("Map.SetStyleURL", "map")
	}
	cs := C.CString(url)
	defer C.free(unsafe.Pointer(cs))
	return m.rt.runOnOwner("Map.SetStyleURL", func() error {
		if m.ptr == nil {
			return errClosed("Map.SetStyleURL", "map")
		}
		if status := C.mln_map_set_style_url(m.ptr, cs); status != C.MLN_STATUS_OK {
			return statusError("mln_map_set_style_url", status)
		}
		return nil
	})
}

// SetStyleJSON loads inline style JSON through the native style APIs.
// Completion is signalled later via runtime events (StyleLoaded or
// MapLoadingFailed).
func (m *Map) SetStyleJSON(json string) error {
	if m == nil {
		return errClosed("Map.SetStyleJSON", "map")
	}
	cs := C.CString(json)
	defer C.free(unsafe.Pointer(cs))
	return m.rt.runOnOwner("Map.SetStyleJSON", func() error {
		if m.ptr == nil {
			return errClosed("Map.SetStyleJSON", "map")
		}
		if status := C.mln_map_set_style_json(m.ptr, cs); status != C.MLN_STATUS_OK {
			return statusError("mln_map_set_style_json", status)
		}
		return nil
	})
}

// WaitForEvent waits for a runtime event whose Source is this Map and
// for which match returns true. Filters out events for other Maps owned
// by the same Runtime; if you need cross-map events use Runtime.WaitForEvent.
func (m *Map) WaitForEvent(timeout time.Duration, match func(Event) bool) (Event, error) {
	return m.rt.WaitForEvent(timeout, func(e Event) bool {
		return e.Source == m && match(e)
	})
}

// RenderStill drives a single static-mode render against sess and
// returns the acquired frame. Internally: requests a still image via
// mln_map_request_still_image, then pumps RenderUpdate on every
// MAP_RENDER_UPDATE_AVAILABLE event for this map until the runtime
// fires STILL_IMAGE_FINISHED (or STILL_IMAGE_FAILED).
//
// The caller owns the returned frame and must call sess.ReleaseFrame on
// it before the next render or destroy.
func (m *Map) RenderStill(sess *TextureSession, timeout time.Duration) (TextureFrame, error) {
	if m == nil {
		return TextureFrame{}, errClosed("Map.RenderStill", "map")
	}
	if sess == nil {
		return TextureFrame{}, errClosed("Map.RenderStill", "session")
	}

	if err := m.rt.runOnOwner("Map.RenderStill", func() error {
		if m.ptr == nil {
			return errClosed("Map.RenderStill", "map")
		}
		if sess.ptr == nil {
			return errClosed("Map.RenderStill", "session")
		}
		if status := C.mln_map_request_still_image(m.ptr); status != C.MLN_STATUS_OK {
			return statusError("mln_map_request_still_image", status)
		}
		return nil
	}); err != nil {
		return TextureFrame{}, err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ev, has, err := m.rt.PollEvent()
		if err != nil {
			return TextureFrame{}, err
		}
		if !has {
			time.Sleep(pollInterval)
			continue
		}
		if ev.Source != m {
			continue
		}
		switch ev.Type {
		case EventRenderUpdateAvailable:
			if err := sess.RenderUpdate(); err != nil {
				var mlnErr *Error
				if asMLN(err, &mlnErr) && mlnErr.Status == StatusInvalidState {
					continue
				}
				return TextureFrame{}, err
			}
		case EventStillImageFinished:
			return sess.AcquireFrame()
		case EventStillImageFailed:
			return TextureFrame{}, fmt.Errorf("STILL_IMAGE_FAILED: code=%d msg=%q", ev.Code, ev.Message)
		case EventMapLoadingFailed, EventRenderError:
			return TextureFrame{}, fmt.Errorf("%s: code=%d msg=%q", ev.Type, ev.Code, ev.Message)
		}
	}
	return TextureFrame{}, fmt.Errorf("RenderStill: timeout after %s without STILL_IMAGE_FINISHED", timeout)
}

// asMLN unwraps to *Error without importing errors here.
func asMLN(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
