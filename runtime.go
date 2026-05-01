package maplibre

/*
#include <stdlib.h>
#include "maplibre_native_c.h"
*/
import "C"

import (
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// pollInterval is how long Runtime.WaitForEvent sleeps between
// PollEvent attempts when the queue is empty. Static-mode rendering
// flows through STILL_IMAGE_FINISHED, which mbgl drives in C++; the
// poll loop here is the wait-for-arrival cost only, so the duration
// doesn't materially affect render latency.
const pollInterval = 100 * time.Microsecond

// Runtime owns a maplibre-native runtime handle and the OS thread it lives on.
//
// Every method dispatches through the owner thread; callers may use Runtime
// from any goroutine.
type Runtime struct {
	d   *dispatcher
	ptr *C.mln_runtime
	// maps tracks live Maps under this Runtime, keyed by their C handle, so
	// PollEvent can resolve event.source back to the Go *Map.
	maps sync.Map // map[unsafe.Pointer]*Map
}

// RuntimeOptions configures NewRuntime. Zero-valued fields are passed as
// defaults to the native ABI.
type RuntimeOptions struct {
	// AssetPath is the filesystem root used to resolve asset:// URLs.
	AssetPath string
	// CachePath is forwarded to MapLibre's cache database. When empty, the
	// native ambient cache is in-memory only.
	CachePath string
	// MaximumCacheSize, when non-zero, sets the ambient cache size limit and
	// enables MLN_RUNTIME_OPTION_MAXIMUM_CACHE_SIZE.
	MaximumCacheSize uint64
}

// NewRuntime spins up a dispatcher goroutine, creates an mln_runtime on it,
// and returns a Runtime bound to that thread.
func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	rt := &Runtime{}
	rt.d = newDispatcher(func() {
		if rt.ptr != nil {
			C.mln_runtime_run_once(rt.ptr)
		}
	}, 0)

	var err error
	rt.d.do(func() {
		copts := C.mln_runtime_options_default()

		if opts.AssetPath != "" {
			cs := C.CString(opts.AssetPath)
			defer C.free(unsafe.Pointer(cs))
			copts.asset_path = cs
		}
		if opts.CachePath != "" {
			cs := C.CString(opts.CachePath)
			defer C.free(unsafe.Pointer(cs))
			copts.cache_path = cs
		}
		if opts.MaximumCacheSize > 0 {
			copts.flags |= C.MLN_RUNTIME_OPTION_MAXIMUM_CACHE_SIZE
			copts.maximum_cache_size = C.uint64_t(opts.MaximumCacheSize)
		}

		var out *C.mln_runtime
		status := C.mln_runtime_create(&copts, &out)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_runtime_create", status)
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

// Close destroys the runtime handle and stops the dispatcher.
//
// Idempotent: safe to call on a nil Runtime or one already closed.
func (r *Runtime) Close() error {
	if r == nil || r.ptr == nil {
		return nil
	}
	var err error
	r.d.do(func() {
		status := C.mln_runtime_destroy(r.ptr)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_runtime_destroy", status)
			return
		}
		r.ptr = nil
	})
	if err != nil {
		// Native rejected destroy (e.g. live maps). Keep the dispatcher
		// running so callers can close their maps and retry.
		return err
	}
	r.d.close()
	return nil
}

// PollEvent pops the next queued runtime event, if any. Events are
// shared across all Maps owned by this Runtime; the returned Event's
// Source field identifies the originating Map (or is nil for runtime-
// source events).
//
// The native event message is copied into a Go string before this call
// returns, so it survives subsequent ABI calls.
func (r *Runtime) PollEvent() (Event, bool, error) {
	var out Event
	var has bool
	var err error
	r.d.do(func() {
		var cev C.mln_runtime_event
		cev.size = C.uint32_t(unsafe.Sizeof(cev))
		var hasEvent C.bool
		status := C.mln_runtime_poll_event(r.ptr, &cev, &hasEvent)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_runtime_poll_event", status)
			return
		}
		has = bool(hasEvent)
		if !has {
			return
		}
		out.Type = EventType(cev._type)
		out.Code = int32(cev.code)
		if cev.message != nil && cev.message_size > 0 {
			out.Message = C.GoStringN((*C.char)(unsafe.Pointer(cev.message)), C.int(cev.message_size))
		}
		if cev.source_type == C.MLN_RUNTIME_EVENT_SOURCE_MAP && cev.source != nil {
			if v, ok := r.maps.Load(cev.source); ok {
				out.Source = v.(*Map)
			}
		}
	})
	return out, has, err
}

// WaitForEvent polls runtime events until match returns true, the
// deadline passes, or an error occurs.
func (r *Runtime) WaitForEvent(timeout time.Duration, match func(Event) bool) (Event, error) {
	deadline := time.Now().Add(timeout)
	for {
		ev, has, err := r.PollEvent()
		if err != nil {
			return Event{}, err
		}
		if has && match(ev) {
			return ev, nil
		}
		if time.Now().After(deadline) {
			return Event{}, fmt.Errorf("timeout after %s waiting for runtime event", timeout)
		}
		time.Sleep(pollInterval)
	}
}
