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
// RenderStillImage path uses request_still_image, which requires a
// static- or tile-mode map. Set Mode = MapModeContinuous if you want
// to drive rendering yourself via TextureSession.RenderUpdate on
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
	if r == nil || r.ptr == nil {
		return nil, &Error{
			Status:  StatusInvalidArgument,
			Op:      "Runtime.NewMap",
			Message: "runtime is closed",
		}
	}

	m := &Map{rt: r}
	var err error
	r.d.do(func() {
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
		status := C.mln_map_create(r.ptr, &copts, &out)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_create", status)
			return
		}
		m.ptr = out
	})
	if err != nil {
		return nil, err
	}
	// Register so Runtime.PollEvent can resolve event.source -> *Map.
	r.maps.Store(unsafe.Pointer(m.ptr), m)
	return m, nil
}

// Close destroys the map handle.
//
// Idempotent: safe to call on a nil Map or one already closed.
func (m *Map) Close() error {
	if m == nil || m.ptr == nil {
		return nil
	}
	var err error
	cptr := m.ptr
	m.rt.d.do(func() {
		status := C.mln_map_destroy(m.ptr)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_destroy", status)
			return
		}
		m.ptr = nil
	})
	if err == nil {
		m.rt.maps.Delete(unsafe.Pointer(cptr))
	}
	return err
}

// SetStyleURL loads a style by URL through the native style APIs.
// Completion is signalled later via runtime events (StyleLoaded or
// MapLoadingFailed).
func (m *Map) SetStyleURL(url string) error {
	cs := C.CString(url)
	defer C.free(unsafe.Pointer(cs))
	var err error
	m.rt.d.do(func() {
		status := C.mln_map_set_style_url(m.ptr, cs)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_set_style_url", status)
		}
	})
	return err
}

// SetStyleJSON loads inline style JSON through the native style APIs.
// Completion is signalled later via runtime events (StyleLoaded or
// MapLoadingFailed).
func (m *Map) SetStyleJSON(json string) error {
	cs := C.CString(json)
	defer C.free(unsafe.Pointer(cs))
	var err error
	m.rt.d.do(func() {
		status := C.mln_map_set_style_json(m.ptr, cs)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_set_style_json", status)
		}
	})
	return err
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
//
// Replaces the prior continuous-mode polling settle loop. The runtime
// still drives the rendering lifecycle in C++; the binding's job is to
// handle texture-target updates as they're requested.
func (m *Map) RenderStill(sess *TextureSession, timeout time.Duration) (TextureFrame, error) {
	if m == nil || m.ptr == nil {
		return TextureFrame{}, &Error{Status: StatusInvalidArgument, Op: "Map.RenderStill", Message: "map is closed"}
	}
	if sess == nil || sess.ptr == nil {
		return TextureFrame{}, &Error{Status: StatusInvalidArgument, Op: "Map.RenderStill", Message: "session is closed"}
	}

	var reqErr error
	m.rt.d.do(func() {
		if status := C.mln_map_request_still_image(m.ptr); status != C.MLN_STATUS_OK {
			reqErr = statusError("mln_map_request_still_image", status)
		}
	})
	if reqErr != nil {
		return TextureFrame{}, reqErr
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
		// Only events for this map matter; ignore the rest.
		if ev.Source != m {
			continue
		}
		switch ev.Type {
		case EventRenderUpdateAvailable:
			// The runtime is asking us to drive the texture render. In
			// static mode this fires once (or more if the renderer
			// needs multiple passes); each call advances the still
			// toward STILL_IMAGE_FINISHED.
			if err := sess.RenderUpdate(); err != nil {
				var mlnErr *Error
				if asMLN(err, &mlnErr) && mlnErr.Status == StatusInvalidState {
					// "no frame produced for this update" — keep
					// polling, the runtime will fire another update or
					// the still-image completion event.
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
