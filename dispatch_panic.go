package maplibre

import (
	"fmt"
	"os"
	"runtime/debug"
)

// logDispatcherPanic surfaces a panicked dispatched closure to stderr.
// Lives in its own file so tests can swap it via a build tag if they want
// silent panics.
func logDispatcherPanic(r any) {
	fmt.Fprintf(os.Stderr, "maplibre: dispatcher recovered from panic: %v\n%s\n", r, debug.Stack())
}
