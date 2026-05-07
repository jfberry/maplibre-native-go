// Package idiomatic is a hand-written baseline showing the shape of
// a Go binding for maplibre-native-c that encodes the C ABI's
// invariants instead of leaving them to the caller.
//
// Scope is intentionally narrow: just enough to render a still image
// from a style. Demonstrates:
//
//   - Owner-thread affinity via a per-Runtime dispatcher goroutine
//   - Resource lifecycle (Close methods, defer-friendly)
//   - Go-typed errors with a Status sentinel
//   - context.Context for cancellation
//   - The render-still + native-readback flow as one call
//
// What's deliberately omitted vs the full maplibre-native-go binding:
// surface sessions, projection helpers, log callbacks, URL transform,
// resource provider, payload decoding, GeoJSON, style queries,
// camera animation, offline regions, feature state, style values.
// All of those live on top of the same primitives shown here.
package idiomatic

/*
#cgo CFLAGS: -I/Users/james/dev/maplibre-native-ffi/include
#cgo LDFLAGS: -L/Users/james/dev/maplibre-native-ffi/build -Wl,-rpath,/Users/james/dev/maplibre-native-ffi/build -lmaplibre-native-c

#include <stdlib.h>
#include "maplibre_native_c.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

// ============================================================
// Errors
// ============================================================

type Status int32

const (
	StatusOK              Status = Status(C.MLN_STATUS_OK)
	StatusInvalidArgument Status = Status(C.MLN_STATUS_INVALID_ARGUMENT)
	StatusInvalidState    Status = Status(C.MLN_STATUS_INVALID_STATE)
	StatusWrongThread     Status = Status(C.MLN_STATUS_WRONG_THREAD)
	StatusUnsupported     Status = Status(C.MLN_STATUS_UNSUPPORTED)
	StatusNativeError     Status = Status(C.MLN_STATUS_NATIVE_ERROR)
)

// Error is the typed error returned by every binding call that maps
// to a non-OK Status. Op carries a short tag of the failing function
// and Message captures mbgl's thread-local diagnostic string.
type Error struct {
	Status  Status
	Op      string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: status=%d %s", e.Op, e.Status, e.Message)
	}
	return fmt.Sprintf("%s: status=%d", e.Op, e.Status)
}

// Is matches *Error values by Status. errors.Is(err, ErrInvalidState)
// returns true for any Error carrying StatusInvalidState regardless
// of Op or Message — the typical idiom for sentinel matching.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && e.Status == t.Status
}

var (
	ErrInvalidArgument = &Error{Status: StatusInvalidArgument}
	ErrInvalidState    = &Error{Status: StatusInvalidState}
	ErrTimeout         = errors.New("maplibre: timeout waiting for runtime event")
)

func statusError(op string, s C.mln_status) error {
	if s == C.MLN_STATUS_OK {
		return nil
	}
	return &Error{
		Status:  Status(s),
		Op:      op,
		Message: C.GoString(C.mln_thread_last_error_message()),
	}
}

// ============================================================
// Dispatcher — encodes owner-thread affinity
// ============================================================

// dispatcher owns one OS thread. Every C call into a Runtime/Map/
// RenderSession goes through it via dispatcher.do, so the C ABI's
// thread-affinity contract holds without callers having to know
// anything about it.
type dispatcher struct {
	cmds      chan func()
	quit      chan struct{}
	closeOnce sync.Once
}

func newDispatcher() *dispatcher {
	d := &dispatcher{
		cmds: make(chan func()),
		quit: make(chan struct{}),
	}
	started := make(chan struct{})
	go d.loop(started)
	<-started
	return d
}

func (d *dispatcher) loop(started chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	close(started)
	for {
		select {
		case fn := <-d.cmds:
			fn()
		case <-d.quit:
			return
		}
	}
}

// do runs fn on the owner thread and waits for it to return.
func (d *dispatcher) do(fn func()) error {
	done := make(chan struct{})
	wrapped := func() { defer close(done); fn() }
	select {
	case d.cmds <- wrapped:
	case <-d.quit:
		return errors.New("dispatcher closed")
	}
	<-done
	return nil
}

func (d *dispatcher) close() {
	d.closeOnce.Do(func() { close(d.quit) })
}

// ============================================================
// Runtime
// ============================================================

// Runtime owns one mbgl runtime and the OS thread it runs on. Every
// method dispatches through that thread; callers may use Runtime
// from any goroutine.
type Runtime struct {
	d   *dispatcher
	ptr *C.mln_runtime
}

func NewRuntime() (*Runtime, error) {
	rt := &Runtime{d: newDispatcher()}
	var err error
	rt.d.do(func() {
		opts := C.mln_runtime_options_default()
		var out *C.mln_runtime
		if s := C.mln_runtime_create(&opts, &out); s != C.MLN_STATUS_OK {
			err = statusError("mln_runtime_create", s)
			return
		}
		rt.ptr = out
	})
	if err != nil {
		rt.d.close()
		return nil, err
	}
	return rt, nil
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	var err error
	r.d.do(func() {
		if r.ptr == nil {
			return
		}
		if s := C.mln_runtime_destroy(r.ptr); s != C.MLN_STATUS_OK {
			err = statusError("mln_runtime_destroy", s)
			return
		}
		r.ptr = nil
	})
	if err == nil {
		r.d.close()
	}
	return err
}

// ============================================================
// Map
// ============================================================

type Map struct {
	rt  *Runtime
	ptr *C.mln_map
}

// NewMap creates a static-mode map sized at width × height physical
// pixels (scale factor 1).
func (r *Runtime) NewMap(width, height uint32) (*Map, error) {
	m := &Map{rt: r}
	var err error
	r.d.do(func() {
		if r.ptr == nil {
			err = &Error{Status: StatusInvalidState, Op: "NewMap", Message: "runtime closed"}
			return
		}
		opts := C.mln_map_options_default()
		opts.width = C.uint32_t(width)
		opts.height = C.uint32_t(height)
		opts.scale_factor = 1.0
		opts.map_mode = C.MLN_MAP_MODE_STATIC
		var out *C.mln_map
		if s := C.mln_map_create(r.ptr, &opts, &out); s != C.MLN_STATUS_OK {
			err = statusError("mln_map_create", s)
			return
		}
		m.ptr = out
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Map) Close() error {
	if m == nil {
		return nil
	}
	var err error
	m.rt.d.do(func() {
		if m.ptr == nil {
			return
		}
		if s := C.mln_map_destroy(m.ptr); s != C.MLN_STATUS_OK {
			err = statusError("mln_map_destroy", s)
			return
		}
		m.ptr = nil
	})
	return err
}

// SetStyleJSON loads inline style JSON. Completion is signalled by a
// later STYLE_LOADED or MAP_LOADING_FAILED event; use WaitForStyle
// for the blocking equivalent.
func (m *Map) SetStyleJSON(json string) error {
	cs := C.CString(json)
	defer C.free(unsafe.Pointer(cs))
	var err error
	m.rt.d.do(func() {
		if m.ptr == nil {
			err = &Error{Status: StatusInvalidState, Op: "SetStyleJSON", Message: "map closed"}
			return
		}
		if s := C.mln_map_set_style_json(m.ptr, cs); s != C.MLN_STATUS_OK {
			err = statusError("mln_map_set_style_json", s)
		}
	})
	return err
}

// WaitForStyle blocks until STYLE_LOADED arrives for this map, or
// returns an error on MAP_LOADING_FAILED / ctx cancellation.
func (m *Map) WaitForStyle(ctx context.Context) error {
	for {
		ev, found, productive, err := m.pumpAndDrain(0)
		if err != nil {
			return err
		}
		if found {
			switch ev._type {
			case C.MLN_RUNTIME_EVENT_MAP_STYLE_LOADED:
				return nil
			case C.MLN_RUNTIME_EVENT_MAP_LOADING_FAILED:
				return &Error{
					Status:  StatusNativeError,
					Op:      "WaitForStyle",
					Message: "MAP_LOADING_FAILED",
				}
			}
		}
		if productive {
			continue
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrTimeout, ctx.Err())
		case <-time.After(100 * time.Microsecond):
		}
	}
}

// ============================================================
// RenderSession
// ============================================================

type RenderSession struct {
	m   *Map
	ptr *C.mln_render_session
}

// AttachTexture creates an offscreen render target using the
// platform-default backend. The session is read-only via the native
// readback path — for backend-specific GPU handles, use the explicit
// AttachMetalTexture / AttachVulkanTexture forms (not implemented in
// this PoC).
func (m *Map) AttachTexture(width, height uint32) (*RenderSession, error) {
	s := &RenderSession{m: m}
	var err error
	m.rt.d.do(func() {
		if m.ptr == nil {
			err = &Error{Status: StatusInvalidState, Op: "AttachTexture", Message: "map closed"}
			return
		}
		desc := C.mln_owned_texture_descriptor_default()
		desc.width = C.uint32_t(width)
		desc.height = C.uint32_t(height)
		desc.scale_factor = 1.0
		var out *C.mln_render_session
		if st := C.mln_owned_texture_attach(m.ptr, &desc, &out); st != C.MLN_STATUS_OK {
			err = statusError("mln_owned_texture_attach", st)
			return
		}
		s.ptr = out
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *RenderSession) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.m.rt.d.do(func() {
		if s.ptr == nil {
			return
		}
		if st := C.mln_render_session_destroy(s.ptr); st != C.MLN_STATUS_OK {
			err = statusError("mln_render_session_destroy", st)
			return
		}
		s.ptr = nil
	})
	return err
}

// ============================================================
// RenderImage — the static-render hot path
// ============================================================

// RenderImage drives one still render and returns the image as RGBA
// bytes (premultiplied, tightly packed). Width and height are in
// physical pixels.
//
// One call hides the C-ABI choreography: request still image, pump
// run_once until STILL_IMAGE_FINISHED, native readback into a
// caller-managed buffer.
func (m *Map) RenderImage(ctx context.Context, sess *RenderSession) ([]byte, int, int, error) {
	if err := m.requestStillAndWait(ctx, sess); err != nil {
		return nil, 0, 0, err
	}
	w, h, byteLen, err := sess.readPremultipliedRGBA8(nil) // probe
	if err != nil {
		return nil, 0, 0, err
	}
	rgba := make([]byte, byteLen)
	if _, _, _, err := sess.readPremultipliedRGBA8(rgba); err != nil {
		return nil, 0, 0, err
	}
	return rgba, w, h, nil
}

func (m *Map) requestStillAndWait(ctx context.Context, sess *RenderSession) error {
	var err error
	m.rt.d.do(func() {
		if s := C.mln_map_request_still_image(m.ptr); s != C.MLN_STATUS_OK {
			err = statusError("mln_map_request_still_image", s)
		}
	})
	if err != nil {
		return err
	}
	for {
		ev, found, productive, err := m.pumpAndDrain(sess)
		if err != nil {
			return err
		}
		if found {
			switch ev._type {
			case C.MLN_RUNTIME_EVENT_MAP_STILL_IMAGE_FINISHED:
				return nil
			case C.MLN_RUNTIME_EVENT_MAP_STILL_IMAGE_FAILED,
				C.MLN_RUNTIME_EVENT_MAP_RENDER_ERROR,
				C.MLN_RUNTIME_EVENT_MAP_LOADING_FAILED:
				return &Error{Status: StatusNativeError, Op: "RenderImage", Message: "render failed"}
			}
		}
		if productive {
			continue
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrTimeout, ctx.Err())
		case <-time.After(100 * time.Microsecond):
		}
	}
}

// pumpAndDrain runs ONE dispatcher op that:
//   - calls mln_runtime_run_once to drive mbgl forward
//   - drains queued events, servicing RUA inline if sess != nil
//   - returns the first terminal event for this map, or idle
func (m *Map) pumpAndDrain(sess interface{}) (C.mln_runtime_event, bool, bool, error) {
	var ev C.mln_runtime_event
	ev.size = C.uint32_t(unsafe.Sizeof(ev))
	var found, productive bool
	var rerr error

	rs, _ := sess.(*RenderSession)

	m.rt.d.do(func() {
		if m.rt.ptr == nil {
			rerr = &Error{Status: StatusInvalidState, Op: "pumpAndDrain"}
			return
		}
		if s := C.mln_runtime_run_once(m.rt.ptr); s != C.MLN_STATUS_OK {
			rerr = statusError("mln_runtime_run_once", s)
			return
		}
		for {
			var has C.bool
			var pollEv C.mln_runtime_event
			pollEv.size = C.uint32_t(unsafe.Sizeof(pollEv))
			if s := C.mln_runtime_poll_event(m.rt.ptr, &pollEv, &has); s != C.MLN_STATUS_OK {
				rerr = statusError("mln_runtime_poll_event", s)
				return
			}
			if !bool(has) {
				return
			}
			productive = true
			// Filter to events sourced at this map.
			if pollEv.source_type != C.MLN_RUNTIME_EVENT_SOURCE_MAP || pollEv.source != unsafe.Pointer(m.ptr) {
				continue
			}
			switch pollEv._type {
			case C.MLN_RUNTIME_EVENT_MAP_RENDER_UPDATE_AVAILABLE:
				if rs != nil && rs.ptr != nil {
					st := C.mln_render_session_render_update(rs.ptr)
					if st != C.MLN_STATUS_OK && st != C.MLN_STATUS_INVALID_STATE {
						rerr = statusError("mln_render_session_render_update", st)
						return
					}
				}
			case C.MLN_RUNTIME_EVENT_MAP_STYLE_LOADED,
				C.MLN_RUNTIME_EVENT_MAP_LOADING_FAILED,
				C.MLN_RUNTIME_EVENT_MAP_STILL_IMAGE_FINISHED,
				C.MLN_RUNTIME_EVENT_MAP_STILL_IMAGE_FAILED,
				C.MLN_RUNTIME_EVENT_MAP_RENDER_ERROR:
				ev = pollEv
				found = true
				return
			}
		}
	})
	return ev, found, productive, rerr
}

func (s *RenderSession) readPremultipliedRGBA8(dst []byte) (w, h, byteLen int, err error) {
	probe := len(dst) == 0
	s.m.rt.d.do(func() {
		var info C.mln_texture_image_info
		info.size = C.uint32_t(unsafe.Sizeof(info))
		var data *C.uint8_t
		var capacity C.size_t
		if !probe {
			data = (*C.uint8_t)(unsafe.Pointer(&dst[0]))
			capacity = C.size_t(len(dst))
		}
		st := C.mln_texture_read_premultiplied_rgba8(s.ptr, data, capacity, &info)
		w = int(info.width)
		h = int(info.height)
		byteLen = int(info.byte_length)
		if probe && st == C.MLN_STATUS_INVALID_ARGUMENT {
			return // probe path: capacity-too-small with info filled is the contract
		}
		if st != C.MLN_STATUS_OK {
			err = statusError("mln_texture_read_premultiplied_rgba8", st)
		}
	})
	return
}
