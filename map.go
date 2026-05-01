package maplibre

/*
#include <stdlib.h>
#include "maplibre_native_c.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"
)

// Map owns a maplibre-native map handle bound to the runtime that created it.
// It is owner-thread affine to the runtime's dispatcher.
type Map struct {
	rt  *Runtime
	ptr *C.mln_map
}

// MapMode picks the rendering protocol the map will use. The zero value
// (MapModeStatic) matches this binding's primary path: RenderStill /
// RenderImage via request_still_image. Set Mode = MapModeContinuous to
// drive rendering yourself via TextureSession.RenderUpdate on
// MAP_RENDER_UPDATE_AVAILABLE events.
//
// MapMode is an opaque Go enum that does NOT mirror the underlying C
// MLN_MAP_MODE_* values directly; the binding translates in toC. This
// preserves Go's "useful zero value" convention — MapOptions{} gives
// you a static-mode map, the most common case — without requiring
// callers to know the C enum encoding.
type MapMode int

const (
	MapModeStatic     MapMode = iota // default zero-value
	MapModeContinuous                //
	MapModeTile                      //
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

// MapOptions configures NewMap.
//
// Width and Height are logical dimensions in pixels; physical render
// size is logical × ScaleFactor.
//
// Zero values:
//   - ScaleFactor: 1.0
//   - Mode: MapModeStatic
//
// Width and Height MUST be non-zero — there is no useful default.
type MapOptions struct {
	Width       uint32
	Height      uint32
	ScaleFactor float64 // zero defaults to 1.0
	Mode        MapMode // zero defaults to MapModeStatic
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
		copts.width = C.uint32_t(opts.Width)
		copts.height = C.uint32_t(opts.Height)
		scale := opts.ScaleFactor
		if scale <= 0 {
			scale = 1
		}
		copts.scale_factor = C.double(scale)
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
	trackForLeak(m, "Map", func() bool { return m.ptr != nil })
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

// LoadStyle dispatches style by inspecting its shape and calling the
// right loader:
//
//   - inline JSON ("{..."): SetStyleJSON
//   - URL with scheme (https://, file://, asset://, mbtiles://, ...): SetStyleURL
//   - filesystem path (otherwise): SetStyleURL("file://" + style)
//
// Does not wait for STYLE_LOADED — pair with WaitForEvent if you
// need that. Session.SetStyle is the blocking equivalent.
func (m *Map) LoadStyle(style string) error {
	switch {
	case strings.HasPrefix(style, "{"):
		return m.SetStyleJSON(style)
	case strings.Contains(style, "://"):
		return m.SetStyleURL(style)
	default:
		return m.SetStyleURL("file://" + style)
	}
}

// WaitForEvent waits for a runtime event whose Source is this Map and
// for which match returns true. Filters out events for other Maps owned
// by the same Runtime; if you need cross-map events use Runtime.WaitForEvent.
//
// Cancellation: returns ctx.Err() wrapped in ErrTimeout when ctx is done.
func (m *Map) WaitForEvent(ctx context.Context, match func(Event) bool) (Event, error) {
	return m.rt.WaitForEvent(ctx, func(e Event) bool {
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
// Cancellation: returns ctx.Err() wrapped in ErrTimeout when ctx is done.
func (m *Map) RenderStill(ctx context.Context, sess *TextureSession) (TextureFrame, error) {
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

	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	for {
		ev, has, err := m.rt.PollEvent()
		if err != nil {
			return TextureFrame{}, err
		}
		if !has {
			select {
			case <-ctx.Done():
				return TextureFrame{}, fmt.Errorf("%w: %w", ErrTimeout, ctx.Err())
			case <-timer.C:
				timer.Reset(pollInterval)
				continue
			}
		}
		if ev.Source != m {
			continue
		}
		switch ev.Type {
		case EventRenderUpdateAvailable:
			if err := sess.RenderUpdate(); err != nil {
				if errors.Is(err, ErrInvalidState) {
					// Renderer caught up between event and call.
					continue
				}
				return TextureFrame{}, err
			}
		case EventStillImageFinished:
			return sess.AcquireFrame()
		case EventStillImageFailed, EventMapLoadingFailed, EventRenderError:
			return TextureFrame{}, eventErr("Map.RenderStill", ev)
		}
	}
}
