package maplibre

/*
#include <stdlib.h>
#include "maplibre_native_abi.h"
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

// MapOptions configures NewMap. Width and Height are logical dimensions;
// physical render size is multiplied by ScaleFactor.
type MapOptions struct {
	Width       uint32
	Height      uint32
	ScaleFactor float64
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
	m.rt.d.do(func() {
		status := C.mln_map_destroy(m.ptr)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_destroy", status)
			return
		}
		m.ptr = nil
	})
	return err
}

// SetStyleURL loads a style by URL through the native style APIs.
// Completion is signalled later via PollEvent (StyleLoaded or MapLoadingFailed).
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
// Completion is signalled later via PollEvent (StyleLoaded or MapLoadingFailed).
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

// EventType mirrors mln_map_event_type.
type EventType uint32

const (
	EventNone               EventType = 0
	EventCameraWillChange   EventType = 1
	EventCameraIsChanging   EventType = 2
	EventCameraDidChange    EventType = 3
	EventStyleLoaded        EventType = 4
	EventMapLoadingStarted  EventType = 5
	EventMapLoadingFinished EventType = 6
	EventMapLoadingFailed   EventType = 7
	EventMapIdle            EventType = 8
	EventRenderInvalidated  EventType = 9
	EventRenderError        EventType = 10
)

func (e EventType) String() string {
	switch e {
	case EventNone:
		return "NONE"
	case EventCameraWillChange:
		return "CAMERA_WILL_CHANGE"
	case EventCameraIsChanging:
		return "CAMERA_IS_CHANGING"
	case EventCameraDidChange:
		return "CAMERA_DID_CHANGE"
	case EventStyleLoaded:
		return "STYLE_LOADED"
	case EventMapLoadingStarted:
		return "MAP_LOADING_STARTED"
	case EventMapLoadingFinished:
		return "MAP_LOADING_FINISHED"
	case EventMapLoadingFailed:
		return "MAP_LOADING_FAILED"
	case EventMapIdle:
		return "MAP_IDLE"
	case EventRenderInvalidated:
		return "RENDER_INVALIDATED"
	case EventRenderError:
		return "RENDER_ERROR"
	}
	return fmt.Sprintf("UNKNOWN(%d)", uint32(e))
}

// Event mirrors mln_map_event with the message field copied to a Go string.
type Event struct {
	Type    EventType
	Code    int32
	Message string
}

// PollEvent pops the next queued map event, if any.
func (m *Map) PollEvent() (Event, bool, error) {
	var out Event
	var has bool
	var err error
	m.rt.d.do(func() {
		var cev C.mln_map_event
		cev.size = C.uint32_t(unsafe.Sizeof(cev))
		var hasEvent C.bool
		status := C.mln_map_poll_event(m.ptr, &cev, &hasEvent)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_poll_event", status)
			return
		}
		has = bool(hasEvent)
		if has {
			out = Event{
				Type:    EventType(cev._type),
				Code:    int32(cev.code),
				Message: C.GoString(&cev.message[0]),
			}
		}
	})
	return out, has, err
}

// WaitForEvent polls until match returns true, the deadline passes, or an
// error occurs. Returns the matched event on success.
func (m *Map) WaitForEvent(timeout time.Duration, match func(Event) bool) (Event, error) {
	deadline := time.Now().Add(timeout)
	for {
		ev, has, err := m.PollEvent()
		if err != nil {
			return Event{}, err
		}
		if has && match(ev) {
			return ev, nil
		}
		if time.Now().After(deadline) {
			return Event{}, fmt.Errorf("timeout after %s waiting for map event", timeout)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
