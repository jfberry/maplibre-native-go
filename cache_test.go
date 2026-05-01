package maplibre

import "testing"

// TestRunAmbientCacheOperationInMemory exercises every ambient cache
// op against a runtime created without a CachePath (mbgl uses an
// in-memory database). We just assert the calls return without error
// — there is no observable side-effect to verify against.
func TestRunAmbientCacheOperationInMemory(t *testing.T) {
	rt := newTestRuntime(t)

	ops := []AmbientCacheOp{
		AmbientCacheClear,
		AmbientCacheInvalidate,
		AmbientCachePack,
		AmbientCacheReset,
	}
	for _, op := range ops {
		if err := rt.RunAmbientCacheOperation(op); err != nil {
			t.Errorf("RunAmbientCacheOperation(%d): %v", op, err)
		}
	}
}

func TestRunAmbientCacheOperationInvalid(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.RunAmbientCacheOperation(AmbientCacheOp(99)); err == nil {
		t.Fatal("expected error for invalid op, got nil")
	}
}
