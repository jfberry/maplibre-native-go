package maplibre

/*
#include <stdlib.h>
#include "maplibre_native_c.h"
*/
import "C"

import "unsafe"

// Runtime owns a maplibre-native runtime handle and the OS thread it lives on.
//
// Every method dispatches through the owner thread; callers may use Runtime
// from any goroutine.
type Runtime struct {
	d   *dispatcher
	ptr *C.mln_runtime
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
