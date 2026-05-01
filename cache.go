package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

// AmbientCacheOp picks an ambient cache maintenance operation. Mirrors
// mln_ambient_cache_operation.
type AmbientCacheOp uint32

const (
	// AmbientCacheReset drops every row from the ambient cache, including
	// tile data, schema metadata, and offline-region records. Use after a
	// schema migration or a corrupted database.
	AmbientCacheReset AmbientCacheOp = AmbientCacheOp(C.MLN_AMBIENT_CACHE_OPERATION_RESET_DATABASE)

	// AmbientCachePack runs SQLite VACUUM-equivalent maintenance to
	// reclaim freed pages.
	AmbientCachePack AmbientCacheOp = AmbientCacheOp(C.MLN_AMBIENT_CACHE_OPERATION_PACK_DATABASE)

	// AmbientCacheInvalidate marks every cached resource as stale so the
	// next access revalidates against origin.
	AmbientCacheInvalidate AmbientCacheOp = AmbientCacheOp(C.MLN_AMBIENT_CACHE_OPERATION_INVALIDATE)

	// AmbientCacheClear removes every ambient cache entry while keeping
	// schema and offline-region rows intact.
	AmbientCacheClear AmbientCacheOp = AmbientCacheOp(C.MLN_AMBIENT_CACHE_OPERATION_CLEAR)
)

// RunAmbientCacheOperation invokes an ambient-cache maintenance
// operation on this runtime's cache database and blocks until mbgl's
// internal database callback reports completion.
//
// When the runtime was created without a CachePath, the operation
// applies to mbgl's default in-memory database — useful for tests but
// the effects do not persist beyond runtime teardown.
func (r *Runtime) RunAmbientCacheOperation(op AmbientCacheOp) error {
	if r == nil {
		return errClosed("Runtime.RunAmbientCacheOperation", "runtime")
	}
	return r.runOnOwner("Runtime.RunAmbientCacheOperation", func() error {
		if r.ptr == nil {
			return errClosed("Runtime.RunAmbientCacheOperation", "runtime")
		}
		if status := C.mln_runtime_run_ambient_cache_operation(r.ptr, C.uint32_t(op)); status != C.MLN_STATUS_OK {
			return statusError("mln_runtime_run_ambient_cache_operation", status)
		}
		return nil
	})
}
