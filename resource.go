package maplibre

/*
#include <stdint.h>
#include <stdlib.h>
#include "maplibre_native_c.h"

mln_resource_transform_callback mlnGoGetTransformTrampoline(void);
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// ResourceKind mirrors mln_resource_kind — what mbgl thinks a
// network request is for. URL transforms typically branch on this.
type ResourceKind uint32

const (
	ResourceKindUnknown     ResourceKind = ResourceKind(C.MLN_RESOURCE_KIND_UNKNOWN)
	ResourceKindStyle       ResourceKind = ResourceKind(C.MLN_RESOURCE_KIND_STYLE)
	ResourceKindSource      ResourceKind = ResourceKind(C.MLN_RESOURCE_KIND_SOURCE)
	ResourceKindTile        ResourceKind = ResourceKind(C.MLN_RESOURCE_KIND_TILE)
	ResourceKindGlyphs      ResourceKind = ResourceKind(C.MLN_RESOURCE_KIND_GLYPHS)
	ResourceKindSpriteImage ResourceKind = ResourceKind(C.MLN_RESOURCE_KIND_SPRITE_IMAGE)
	ResourceKindSpriteJSON  ResourceKind = ResourceKind(C.MLN_RESOURCE_KIND_SPRITE_JSON)
	ResourceKindImage       ResourceKind = ResourceKind(C.MLN_RESOURCE_KIND_IMAGE)
)

// URLTransform receives a request kind and original URL and returns
// the URL mbgl should actually fetch. Returning the same URL (or "")
// leaves the request unchanged. The callback may run on any worker
// thread; implementations MUST be thread-safe, return quickly, and
// MUST NOT call back into the maplibre package.
type URLTransform func(kind ResourceKind, url string) string

//export mlnGoResourceTransformTrampoline
func mlnGoResourceTransformTrampoline(userData unsafe.Pointer, kind C.uint32_t, url *C.char, out *C.mln_resource_transform_response) C.mln_status {
	if userData == nil {
		return C.MLN_STATUS_OK
	}
	h := cgo.Handle(*(*uintptr)(userData))
	cb, ok := h.Value().(URLTransform)
	if !ok || cb == nil {
		return C.MLN_STATUS_OK
	}
	var goURL string
	if url != nil {
		goURL = C.GoString(url)
	}
	rewrite := cb(ResourceKind(kind), goURL)
	if rewrite == "" || rewrite == goURL {
		return C.MLN_STATUS_OK
	}
	// The C ABI copies out->url before this callback returns, so the
	// CString allocation can be freed on the way out.
	cstr := C.CString(rewrite)
	defer C.free(unsafe.Pointer(cstr))
	out.url = cstr
	return C.MLN_STATUS_OK
}

// SetResourceURLTransform registers a runtime-scoped URL rewrite hook
// for network resources. Must be called BEFORE any Map is created on
// this runtime — mbgl rejects the registration once the runtime owns
// live maps (returns *Error with StatusInvalidState).
//
// The transform applies to mbgl's OnlineFileSource only. Built-in
// schemes (file://, asset://, mbtiles://, pmtiles://) skip the
// transform; nested PMTiles network range requests do go through it.
//
// Pass nil to clear a previously-registered transform.
//
// The callback may be invoked from worker threads. Lifetime: the
// Runtime keeps a reference to the callback until Close, so it is
// safe to discard your variable after this call returns.
func (r *Runtime) SetResourceURLTransform(fn URLTransform) error {
	if r == nil {
		return errClosed("Runtime.SetResourceURLTransform", "runtime")
	}
	return r.runOnOwner("Runtime.SetResourceURLTransform", func() error {
		if r.ptr == nil {
			return errClosed("Runtime.SetResourceURLTransform", "runtime")
		}
		// Drop any previous transform's handle and its C cell.
		if r.transformHandle != 0 {
			r.transformHandle.Delete()
			r.transformHandle = 0
		}
		if r.transformUserData != nil {
			C.free(r.transformUserData)
			r.transformUserData = nil
		}
		actual := fn
		if actual == nil {
			// mbgl has no "clear" entrypoint; register a no-op
			// transform that returns the input URL unchanged.
			actual = func(_ ResourceKind, url string) string { return url }
		}
		h := cgo.NewHandle(actual)
		// Store the handle's uintptr value in a C-allocated cell so
		// we can hand mbgl a real pointer for user_data without
		// tripping checkptr's uintptr→unsafe.Pointer guard. The
		// trampoline reads *(uintptr_t*)user_data to recover the
		// handle.
		cell := C.malloc(C.size_t(unsafe.Sizeof(uintptr(0))))
		*(*uintptr)(cell) = uintptr(h)
		var t C.mln_resource_transform
		t.size = C.uint32_t(unsafe.Sizeof(t))
		t.callback = C.mlnGoGetTransformTrampoline()
		t.user_data = cell
		if status := C.mln_runtime_set_resource_transform(r.ptr, &t); status != C.MLN_STATUS_OK {
			h.Delete()
			C.free(cell)
			return statusError("mln_runtime_set_resource_transform", status)
		}
		r.transformHandle = h
		r.transformUserData = cell
		return nil
	})
}
