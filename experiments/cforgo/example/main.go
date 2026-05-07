// Demonstrates calling the auto-generated maplibre bindings — and
// what falls out of the cracks.
//
// This is what consumers of a c-for-go-generated binding actually have
// to write. The generated package gets you ~80% of the way to a Go
// callable surface; the remaining ~20% is hand-written wrappers
// because c-for-go's pointer/lifetime conventions don't compose with
// the C ABI's out-parameter idiom or modern-Go strict typing.
//
// Compare against ../../idiomatic/example/main.go for the same
// program written against a hand-written idiomatic Go API.
package main

/*
// We end up dropping into raw cgo for any C function with a `**T`
// out-parameter. c-for-go renders these as `[][]T`, which can't carry
// a NULL — but the C ABI rejects with INVALID_ARGUMENT when
// `*out != NULL`. The "auto-gen" output is therefore unusable for
// every constructor in maplibre-native-c without hand-written shims.

#cgo CFLAGS:  -I/Users/james/dev/maplibre-native-ffi/include
#cgo LDFLAGS: -L/Users/james/dev/maplibre-native-ffi/build -Wl,-rpath,/Users/james/dev/maplibre-native-ffi/build -lmaplibre-native-c

#include "maplibre_native_c.h"
*/
import "C"

import (
	"fmt"
	"log"
	"runtime"
	"unsafe"

	cforgo "github.com/jfberry/maplibre-native-go/experiments/cforgo/cforgo"
)

// runtimeCreate is the hand-written shim for mln_runtime_create. The
// auto-gen `cforgo.RuntimeCreate` exists but its `[][]Runtime` out
// parameter can't pass NULL, so the C ABI always rejects it.
func runtimeCreate(opts *cforgo.RuntimeOptions) (*cforgo.Runtime, error) {
	cOpts := C.mln_runtime_options_default()
	var out *C.mln_runtime
	if status := C.mln_runtime_create(&cOpts, &out); status != C.MLN_STATUS_OK {
		return nil, fmt.Errorf("mln_runtime_create: status=%d", status)
	}
	return (*cforgo.Runtime)(unsafe.Pointer(out)), nil
}

func mapCreate(rt *cforgo.Runtime, w, h uint32, scale float64) (*cforgo.Map, error) {
	cOpts := C.mln_map_options_default()
	cOpts.width = C.uint32_t(w)
	cOpts.height = C.uint32_t(h)
	cOpts.scale_factor = C.double(scale)
	var out *C.mln_map
	if status := C.mln_map_create((*C.mln_runtime)(unsafe.Pointer(rt)), &cOpts, &out); status != C.MLN_STATUS_OK {
		return nil, fmt.Errorf("mln_map_create: status=%d", status)
	}
	return (*cforgo.Map)(unsafe.Pointer(out)), nil
}

func main() {
	// The C ABI requires every runtime/map/session call to happen on
	// the runtime's owner thread. With the auto-gen there's no
	// dispatcher — we LockOSThread ourselves and the binding gives
	// no guidance about thread affinity. Consumers who don't read
	// the C ABI docs will segfault.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	rt, err := runtimeCreate(nil)
	if err != nil {
		log.Fatal(err)
	}
	defer cforgo.RuntimeDestroy(rt) // this one works — single * not **

	m, err := mapCreate(rt, 256, 256, 1)
	if err != nil {
		log.Fatal(err)
	}
	defer cforgo.MapDestroy(m)

	// SetStyleJSON works through the auto-gen because the C function
	// is `mln_map_set_style_json(map*, const char*)` — single * out,
	// no struct round-trip. Strings get marshalled OK.
	if status := cforgo.MapSetStyleJson(m, `{"version":8,"sources":{},"layers":[]}`); status != cforgo.StatusOk {
		log.Fatalf("MapSetStyleJson: status=%d", status)
	}

	// Pump events until STYLE_LOADED. No batching, no Context, no
	// dispatcher — caller writes the loop manually and remembers to
	// call run_once to drive mbgl forward.
	deadline := 200
	for i := 0; i < deadline; i++ {
		if status := cforgo.RuntimeRunOnce(rt); status != cforgo.StatusOk {
			log.Fatalf("RuntimeRunOnce: status=%d", status)
		}
		// Drain. The auto-gen wraps PollEvent's two out-params
		// (event struct + has_event bool) as []T parameters. We
		// pre-allocate single-element slices, set Size on the event
		// struct (mandatory C ABI requirement, easy to forget),
		// then read back from index 0.
		for {
			ev := cforgo.RuntimeEvent{Size: uint32(C.sizeof_mln_runtime_event)}
			evSlot := []cforgo.RuntimeEvent{ev}
			hasSlot := []bool{false}
			if status := cforgo.RuntimePollEvent(rt, evSlot, hasSlot); status != cforgo.StatusOk {
				log.Fatalf("RuntimePollEvent: status=%d", status)
			}
			if !hasSlot[0] {
				break
			}
			ev = evSlot[0]
			ev.Deref() // re-hydrate Go-side fields from the C cache
			switch ev.Type {
			case uint32(cforgo.RuntimeEventMapStyleLoaded):
				fmt.Println("style loaded")
				return
			case uint32(cforgo.RuntimeEventMapLoadingFailed):
				log.Fatalf("MAP_LOADING_FAILED")
			}
		}
	}
	log.Fatalf("timed out waiting for STYLE_LOADED")
}
