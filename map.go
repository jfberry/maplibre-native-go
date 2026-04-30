package maplibre

/*
#include <stdlib.h>
#include "maplibre_native_c.h"
*/
import "C"

import (
	"fmt"
	"runtime"
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
//
// Drives the native runloop (mln_runtime_run_once) once before draining
// the queue so any pending observer callbacks have already fired. Without
// this, hot poll loops were rate-limited by the dispatcher's idle tick
// interval rather than by the poll cadence — tile-arrival events would
// queue up on C++ worker threads and only get pushed onto the ABI event
// queue when the dispatcher tick happened to fire between commands.
func (m *Map) PollEvent() (Event, bool, error) {
	var out Event
	var has bool
	var err error
	m.rt.d.do(func() {
		C.mln_runtime_run_once(m.rt.ptr)
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

// pollInterval is how long WaitForEvent sleeps between PollEvent attempts
// when the queue is empty. RenderStill itself does not sleep — see the
// runtime.Gosched call inside its inner loop.
//
// The default 100 µs is a no-op on most Linux kernels (Sleep rounds up to
// the timer-tick floor, ~1 ms with HZ=250), but matters on macOS / busy
// containers where finer-grained sleeps are honoured. On stock Linux the
// effective floor is timer-quantized regardless of this constant. Any
// further tightening of WaitForEvent's latency floor requires moving off
// time.Sleep onto the same Gosched path RenderStill uses — left as a
// follow-up if a real consumer hits a quantization issue with style
// loading.
const pollInterval = 100 * time.Microsecond

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
		time.Sleep(pollInterval)
	}
}

// RenderStill drives the static-render protocol against sess: render once,
// then re-render on every RENDER_INVALIDATED event until MAP_IDLE arrives.
// At that point the renderer reports it is fully loaded after the most
// recent render and the acquired frame is the canonical settled view.
//
// Returns the acquired frame (caller must release), the number of render
// calls issued, or an error.
//
// The function does not own the frame lifetime; callers must call
// sess.ReleaseFrame on the returned frame before the next render or
// destroy.
//
// Implementation: the inner settle loop (run_once, poll, render) executes
// inside a single dispatcher.do invocation so each iteration is plain
// cgo on the runtime owner thread without a Go-side channel round-trip.
// That reduces total round-trips per render from ~2*event_count to two
// (the loop, then a final AcquireFrame), which is the dominant latency
// improvement for tile-stitched static renders. The trick requires a
// concrete *TextureSession because backend-specific acquire/release
// can't share an interface across the locked-already dispatcher thread
// without cyclic dispatch.
func (m *Map) RenderStill(sess *TextureSession, timeout time.Duration) (TextureFrame, int, error) {
	if m == nil || m.ptr == nil {
		return TextureFrame{}, 0, &Error{Status: StatusInvalidArgument, Op: "Map.RenderStill", Message: "map is closed"}
	}
	if sess == nil || sess.ptr == nil {
		return TextureFrame{}, 0, &Error{Status: StatusInvalidArgument, Op: "Map.RenderStill", Message: "session is closed"}
	}

	deadline := time.Now().Add(timeout)
	var (
		renders int
		settled bool
		loopErr error
	)

	m.rt.d.do(func() {
		// Initial render seeds the renderer with what tiles it needs. Run
		// it in its own nested pool so Metal autoreleased objects (command
		// buffers, etc) drain before the loop starts.
		var startupErr error
		withAutoreleasePool(func() {
			if status := C.mln_texture_render(sess.ptr); status != C.MLN_STATUS_OK {
				startupErr = statusError("mln_texture_render", status)
			}
		})
		if startupErr != nil {
			loopErr = startupErr
			return
		}
		renders = 1

		// Each iteration must drain its own autorelease pool. Without a
		// nested pool the loop accumulates Metal command-buffer references
		// across all ~16-20 renders that a tile-stitched still typically
		// needs to settle, and the Metal command-buffer pool deadlocks
		// inside _MTLCommandBuffer init waiting for slots — exactly the
		// failure mode the dispatcher's outer pool fix addressed for
		// single-shot renders.
		//
		// Each iteration drains ALL pending events from the C++ queue
		// before deciding what to do, then issues at most one render per
		// iteration regardless of how many RENDER_INVALIDATED events were
		// queued. mbgl's renderer responds to "any new state since last
		// render" so coalescing N invalidations into one render is correct
		// and saves redundant GPU work when tile decoders complete in
		// quick succession.
		for time.Now().Before(deadline) {
			var done bool
			withAutoreleasePool(func() {
				C.mln_runtime_run_once(m.rt.ptr)

				// Drain pending events. Track the last meaningful event
				// (RENDER_INVALIDATED or MAP_IDLE) — order matters: a
				// later IDLE supersedes earlier invalidations (the render
				// that produced IDLE already addressed them); a later
				// invalidation supersedes IDLE (post-idle async resource
				// arrival).
				var lastSignificant EventType
				var failureMsg string
				var failureType EventType
				var failureCode int32

				const maxDrain = 64
				for i := 0; i < maxDrain; i++ {
					var cev C.mln_map_event
					cev.size = C.uint32_t(unsafe.Sizeof(cev))
					var hasEvent C.bool
					if s := C.mln_map_poll_event(m.ptr, &cev, &hasEvent); s != C.MLN_STATUS_OK {
						loopErr = statusError("mln_map_poll_event", s)
						done = true
						return
					}
					if !bool(hasEvent) {
						break
					}
					t := EventType(cev._type)
					switch t {
					case EventRenderInvalidated, EventMapIdle:
						lastSignificant = t
					case EventMapLoadingFailed, EventRenderError:
						failureType = t
						failureCode = int32(cev.code)
						failureMsg = C.GoString(&cev.message[0])
					}
				}

				if failureMsg != "" {
					loopErr = fmt.Errorf("%s: code=%d msg=%q", failureType, failureCode, failureMsg)
					done = true
					return
				}

				switch lastSignificant {
				case EventMapIdle:
					settled = true
					done = true
				case EventRenderInvalidated:
					rs := C.mln_texture_render(sess.ptr)
					switch rs {
					case C.MLN_STATUS_OK:
						renders++
					case C.MLN_STATUS_INVALID_STATE:
						// renderer caught up between invalidation and
						// this call; keep polling.
					default:
						loopErr = statusError("mln_texture_render", rs)
						done = true
					}
				default:
					// No actionable events this iteration. Yield
					// cooperatively rather than time.Sleep, which rounds
					// up to the OS timer-tick floor (~1 ms on stock
					// Linux HZ=250). Gosched is a near-noop on a
					// LockOSThread'd dispatcher goroutine — other Go
					// goroutines run on other OS threads, so there's
					// nothing to yield to — but it keeps the loop
					// cooperative without timer involvement. The result
					// is a hot busy-wait between events bounded only by
					// the cgo cost of the next run_once (~10-50 µs),
					// trading per-render CPU during settle for ~10x
					// lower polling latency.
					runtime.Gosched()
				}
			})
			if done {
				return
			}
		}
	})

	if loopErr != nil {
		return TextureFrame{}, renders, loopErr
	}
	if !settled {
		return TextureFrame{}, renders, fmt.Errorf("RenderStill: timeout after %s without MAP_IDLE (renders=%d)", timeout, renders)
	}

	// Single dispatcher round-trip for the backend-specific acquire.
	frame, err := sess.AcquireFrame()
	if err != nil {
		return TextureFrame{}, renders, err
	}
	return frame, renders, nil
}
