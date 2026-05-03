package maplibre

/*
#include <stdlib.h>
#include "maplibre_native_c.h"
*/
import "C"

import (
	"context"
	"fmt"
	"runtime/cgo"
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
	// PollEvent can resolve event.source back to the Go *Map. All access
	// (read in PollEvent, mutation in NewMap / Map.Close) happens on the
	// dispatcher thread, so no lock is needed.
	maps map[uintptr]*Map
	// transformHandle holds the cgo.Handle for a registered URL
	// transform callback so the Go closure stays alive across the
	// cgo boundary. Mutated only inside the dispatcher.
	transformHandle cgo.Handle
	// transformUserData is the C-allocated cell holding the handle's
	// uintptr value (used as mbgl's user_data). Mutated only inside
	// the dispatcher; freed in Close.
	transformUserData unsafe.Pointer
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
	trackForLeak(rt, "Runtime", func() bool { return rt.ptr != nil })
	return rt, nil
}

// runOnOwner runs fn on this runtime's owner thread. If the runtime's
// dispatcher has been closed, returns *Error{Status: StatusInvalidState}.
// fn runs serialized with all other ABI calls into this runtime — it is
// the only place where pointer fields (Runtime.ptr, Map.ptr,
// RenderSession.ptr) may be safely read or mutated.
func (r *Runtime) runOnOwner(op string, fn func() error) error {
	var fnErr error
	if dErr := r.d.do(func() {
		fnErr = fn()
	}); dErr != nil {
		return &Error{Status: StatusInvalidState, Op: op, Message: "runtime is closed"}
	}
	return fnErr
}

// errClosed returns the conventional "X is closed" error. Closed
// resources are state errors, not argument errors — match with
// errors.Is(err, ErrInvalidState).
func errClosed(op, what string) error {
	return &Error{Status: StatusInvalidState, Op: op, Message: what + " is closed"}
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
		if r.transformHandle != 0 {
			r.transformHandle.Delete()
			r.transformHandle = 0
		}
		if r.transformUserData != nil {
			C.free(r.transformUserData)
			r.transformUserData = nil
		}
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
		var pErr error
		out, has, pErr = r.pollEventLocked()
		return pErr
	})
	return out, has, err
}

// pollEventLocked runs mln_runtime_poll_event and decodes the result.
// MUST be called from inside the dispatcher (i.e. from a runOnOwner
// closure). Used by the batched render-still / wait-for-event paths
// to avoid one dispatcher hop per drained event.
func (r *Runtime) pollEventLocked() (Event, bool, error) {
	if r.ptr == nil {
		return Event{}, false, errClosed("Runtime.PollEvent", "runtime")
	}
	var cev C.mln_runtime_event
	cev.size = C.uint32_t(unsafe.Sizeof(cev))
	var hasEvent C.bool
	if status := C.mln_runtime_poll_event(r.ptr, &cev, &hasEvent); status != C.MLN_STATUS_OK {
		return Event{}, false, statusError("mln_runtime_poll_event", status)
	}
	if !bool(hasEvent) {
		return Event{}, false, nil
	}
	var out Event
	out.Type = EventType(cev._type)
	out.Code = int32(cev.code)
	if cev.message != nil && cev.message_size > 0 {
		out.Message = C.GoStringN((*C.char)(unsafe.Pointer(cev.message)), C.int(cev.message_size))
	}
	if cev.source_type == C.MLN_RUNTIME_EVENT_SOURCE_MAP && cev.source != nil {
		out.Source = r.maps[uintptr(cev.source)]
	}
	// Decode the borrowed payload before the next poll invalidates it.
	out.Payload = decodePayload(uint32(cev.payload_type), unsafe.Pointer(cev.payload), cev.payload_size)
	return out, true, nil
}

// WaitForEvent polls runtime events until match returns true, ctx is
// cancelled, or an error occurs.
//
// On cancellation returns ctx.Err() (context.Canceled or
// context.DeadlineExceeded) wrapped with ErrTimeout for ergonomic
// matching:
//
//	if errors.Is(err, maplibre.ErrTimeout) { ... }
//	if errors.Is(err, context.DeadlineExceeded) { ... }
//
// Both predicates work.
func (r *Runtime) WaitForEvent(ctx context.Context, match func(Event) bool) (Event, error) {
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	for {
		ev, found, productive, err := r.pumpAndMatch(match)
		if err != nil {
			return Event{}, err
		}
		if found {
			return ev, nil
		}
		if productive {
			// We did real work this iteration but no match yet;
			// pump again immediately rather than wait.
			continue
		}
		select {
		case <-ctx.Done():
			return Event{}, fmt.Errorf("%w: %w", ErrTimeout, ctx.Err())
		case <-timer.C:
			timer.Reset(pollInterval)
		}
	}
}

// pumpAndMatch runs ONE dispatcher op that pumps mbgl via
// mln_runtime_run_once and then drains every queued event, calling
// match on each. Returns the first matching event (found=true) or
// idle (found=false). productive indicates whether the pump or drain
// did real work — when productive is true, callers should re-invoke
// immediately rather than wait, since more progress is likely.
//
// match is invoked on the runtime's owner thread inside the
// dispatcher. It MUST be a pure predicate — calling back into any
// Map / Runtime / Session method from match deadlocks the dispatcher.
func (r *Runtime) pumpAndMatch(match func(Event) bool) (matched Event, found, productive bool, err error) {
	derr := r.runOnOwner("Runtime.pumpAndMatch", func() error {
		if r.ptr == nil {
			return errClosed("Runtime.pumpAndMatch", "runtime")
		}
		if status := C.mln_runtime_run_once(r.ptr); status != C.MLN_STATUS_OK {
			return statusError("mln_runtime_run_once", status)
		}
		for {
			ev, has, perr := r.pollEventLocked()
			if perr != nil {
				return perr
			}
			if !has {
				return nil
			}
			productive = true
			if match(ev) {
				matched = ev
				found = true
				return nil
			}
		}
	})
	return matched, found, productive, derr
}

// registerMap is called by NewMap on the dispatcher thread to record a
// new Map handle for source-resolution in PollEvent.
func (r *Runtime) registerMap(m *Map) {
	r.maps[uintptr(unsafe.Pointer(m.ptr))] = m
}

// unregisterMap is called by Map.Close on the dispatcher thread.
func (r *Runtime) unregisterMap(cptr uintptr) {
	delete(r.maps, cptr)
}
