//go:build darwin

package maplibre

/*
#include <objc/objc.h>

extern void* objc_autoreleasePoolPush(void);
extern void objc_autoreleasePoolPop(void* pool);
*/
import "C"

// withAutoreleasePool runs fn between objc_autoreleasePoolPush and Pop.
//
// Required on goroutine OS threads that call into MapLibre's Metal backend.
// The dispatcher goroutine is not the macOS main thread, so it has no
// implicit pool; without this wrapper, Metal command buffers stall on
// internal semaphore waits as autoreleased objects accumulate.
func withAutoreleasePool(fn func()) {
	pool := C.objc_autoreleasePoolPush()
	defer C.objc_autoreleasePoolPop(pool)
	fn()
}
