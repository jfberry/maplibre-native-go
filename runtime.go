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
	// PollEvent can resolve event.source back to the Go *Map. Mutation only
	// happens inside the dispatcher (NewMap, Map.Close), so plain map +
	// dispatcher serialization is safe; sync.Map would also work but is
	// overkill for owner-thread-mutated state.
	mapsMu sync.RWMutex
	maps   map[uintptr]*Map
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
	rt := &Runtime{maps: make(map[uintptr]*Map)}
	rt.d = newDispatcher(func() {
		if rt.ptr != nil {
			C.mln_runtime_run_once(rt.ptr)
		}
	}, 0)

	createErr := rt.runOnOwner("NewRuntime", func() error {
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
		if status := C.mln_runtime_create(&copts, &out); status != C.MLN_STATUS_OK {
			return statusError("mln_runtime_create", status)
		}
		rt.ptr = out
		return nil
	})
	if createErr != nil {
		rt.d.close()
		return nil, createErr
	}
	return rt, nil
}

// runOnOwner runs fn on this runtime's owner thread. If the runtime's
// dispatcher has been closed, returns an *Error tagged with op (Status
// matches the convention used by errClosed elsewhere in the binding).
// Otherwise returns whatever fn returns. fn runs serialized with all
// other ABI calls into this runtime — it is the only place where
// pointer fields (Runtime.ptr, Map.ptr, TextureSession.ptr) may be
// safely read or mutated, since no other goroutine touches them.
func (r *Runtime) runOnOwner(op string, fn func() error) error {
	var fnErr error
	if dErr := r.d.do(func() {
		fnErr = fn()
	}); dErr != nil {
		return &Error{Status: StatusInvalidArgument, Op: op, Message: "runtime is closed"}
	}
	return fnErr
}

// errClosed returns the conventional "X is closed" error.
func errClosed(op, what string) error {
	return &Error{Status: StatusInvalidArgument, Op: op, Message: what + " is closed"}
}

// Close destroys the runtime handle and stops the dispatcher.
//
// All Maps created from this Runtime must be closed first; otherwise
// Close returns an error and leaves the runtime running so the caller
// can close its maps and retry.
//
// Idempotent: safe to call on a nil Runtime or one already closed.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	var destroyErr error
	dErr := r.d.do(func() {
		if r.ptr == nil {
			return
		}
		if status := C.mln_runtime_destroy(r.ptr); status != C.MLN_STATUS_OK {
			destroyErr = statusError("mln_runtime_destroy", status)
			return
		}
		r.ptr = nil
	})
	if dErr != nil {
		// Already closed; no-op.
		return nil
	}
	if destroyErr != nil {
		// Native rejected destroy (live maps). Keep dispatcher running so
		// the caller can close its maps and retry.
		return destroyErr
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
	err := r.runOnOwner("Runtime.PollEvent", func() error {
		if r.ptr == nil {
			return errClosed("Runtime.PollEvent", "runtime")
		}
		var cev C.mln_runtime_event
		cev.size = C.uint32_t(unsafe.Sizeof(cev))
		var hasEvent C.bool
		if status := C.mln_runtime_poll_event(r.ptr, &cev, &hasEvent); status != C.MLN_STATUS_OK {
			return statusError("mln_runtime_poll_event", status)
		}
		has = bool(hasEvent)
		if !has {
			return nil
		}
		out.Type = EventType(cev._type)
		out.Code = int32(cev.code)
		if cev.message != nil && cev.message_size > 0 {
			out.Message = C.GoStringN((*C.char)(unsafe.Pointer(cev.message)), C.int(cev.message_size))
		}
		if cev.source_type == C.MLN_RUNTIME_EVENT_SOURCE_MAP && cev.source != nil {
			r.mapsMu.RLock()
			out.Source = r.maps[uintptr(cev.source)]
			r.mapsMu.RUnlock()
		}
		return nil
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

// registerMap is called by NewMap on the dispatcher thread to record a
// new Map handle for source-resolution in PollEvent.
func (r *Runtime) registerMap(m *Map) {
	r.mapsMu.Lock()
	r.maps[uintptr(unsafe.Pointer(m.ptr))] = m
	r.mapsMu.Unlock()
}

// unregisterMap is called by Map.Close on the dispatcher thread.
func (r *Runtime) unregisterMap(cptr uintptr) {
	r.mapsMu.Lock()
	delete(r.maps, cptr)
	r.mapsMu.Unlock()
}
