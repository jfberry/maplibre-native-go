//go:build mln_debug

package maplibre

import (
	"fmt"
	"os"
	"runtime"
)

// trackForLeak attaches a finalizer that warns on stderr if obj was
// garbage-collected with one of its native handles still live. liveFn
// returns true when the resource still holds a native handle (i.e.
// Close hasn't run). label identifies the resource in the warning.
//
// Only compiled into the binding when the mln_debug build tag is set:
//
//	go build -tags mln_debug ./...
//	go test  -tags mln_debug ./...
//
// Off by default — finalizers add per-allocation overhead and we
// don't want to pay for it in release builds.
func trackForLeak(obj any, label string, liveFn func() bool) {
	runtime.SetFinalizer(obj, func(_ any) {
		if liveFn() {
			fmt.Fprintf(os.Stderr,
				"maplibre: leak: %s garbage-collected without Close\n", label)
		}
	})
}
