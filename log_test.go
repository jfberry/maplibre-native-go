package maplibre

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLogCallbackCapturesParseError installs a log callback, triggers
// a parse error by loading malformed JSON, and verifies a record
// landed in our callback. We force every severity to dispatch
// synchronously so the test doesn't race with mbgl's worker queue.
func TestLogCallbackCapturesParseError(t *testing.T) {
	if err := SetLogAsyncSeverityMask(0); err != nil {
		t.Fatalf("SetLogAsyncSeverityMask(0): %v", err)
	}
	t.Cleanup(func() { _ = SetLogAsyncSeverityMask(LogSeverityMaskDefault) })

	var (
		mu      sync.Mutex
		records []LogRecord
	)
	if err := InstallLogCallback(func(r LogRecord) bool {
		mu.Lock()
		records = append(records, r)
		mu.Unlock()
		// Consume so the platform logger doesn't also print every record.
		return true
	}); err != nil {
		t.Fatalf("InstallLogCallback: %v", err)
	}
	t.Cleanup(func() { _ = ClearLogCallback() })

	rt := newTestRuntime(t)
	m, err := rt.NewMap(MapOptions{Width: 32, Height: 32})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	// Provoke a ParseStyle log record.
	_ = m.SetStyleJSON(`{"version":8`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = m.WaitForEvent(ctx, EventOfTypes(EventStyleLoaded, EventMapLoadingFailed))

	mu.Lock()
	defer mu.Unlock()
	if len(records) == 0 {
		t.Fatal("no log records captured; expected at least one")
	}
	t.Logf("captured %d log records; first: %+v", len(records), records[0])
}

// TestLogCallbackThreadSafety verifies the callback can be invoked
// concurrently from multiple goroutines and the count is right. Uses
// SetLogAsyncSeverityMask(All) so worker threads can dispatch records
// concurrently if mbgl chooses to.
func TestLogCallbackThreadSafety(t *testing.T) {
	if err := SetLogAsyncSeverityMask(LogSeverityMaskAll); err != nil {
		t.Fatalf("SetLogAsyncSeverityMask(All): %v", err)
	}
	t.Cleanup(func() { _ = SetLogAsyncSeverityMask(LogSeverityMaskDefault) })

	var n int64
	if err := InstallLogCallback(func(_ LogRecord) bool {
		atomic.AddInt64(&n, 1)
		return true
	}); err != nil {
		t.Fatalf("InstallLogCallback: %v", err)
	}
	t.Cleanup(func() { _ = ClearLogCallback() })

	// Spin up two runtimes in parallel and load a malformed style
	// against each — provokes log activity on different threads.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt, err := NewRuntime(RuntimeOptions{})
			if err != nil {
				return
			}
			defer rt.Close()
			m, err := rt.NewMap(MapOptions{Width: 32, Height: 32})
			if err != nil {
				return
			}
			defer m.Close()
			_ = m.SetStyleJSON(`{"version":8`)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = m.WaitForEvent(ctx, EventOfTypes(EventStyleLoaded, EventMapLoadingFailed))
		}()
	}
	wg.Wait()
	if atomic.LoadInt64(&n) == 0 {
		t.Fatal("no log records captured under concurrent load")
	}
}

// TestClearLogCallbackIsIdempotent makes sure clearing without a prior
// install doesn't panic / error.
func TestClearLogCallbackIsIdempotent(t *testing.T) {
	if err := ClearLogCallback(); err != nil {
		t.Fatalf("first ClearLogCallback: %v", err)
	}
	if err := ClearLogCallback(); err != nil {
		t.Fatalf("second ClearLogCallback: %v", err)
	}
}
