package maplibre

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatcherDoRoundTrip(t *testing.T) {
	d := newDispatcher(nil, 0)
	defer d.close()

	var got int
	d.do(func() { got = 42 })

	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestDispatcherDoSerializes(t *testing.T) {
	d := newDispatcher(nil, 0)
	defer d.close()

	const n = 100
	var counter int
	for i := 0; i < n; i++ {
		d.do(func() { counter++ })
	}

	if counter != n {
		t.Fatalf("counter = %d, want %d", counter, n)
	}
}

func TestDispatcherTickFires(t *testing.T) {
	var ticks int64
	d := newDispatcher(func() {
		atomic.AddInt64(&ticks, 1)
	}, 1*time.Millisecond)
	defer d.close()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&ticks) >= 5 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected at least 5 ticks within 200ms, got %d", atomic.LoadInt64(&ticks))
}

func TestDispatcherTickInterleavesWithCommands(t *testing.T) {
	var ticks int64
	d := newDispatcher(func() {
		atomic.AddInt64(&ticks, 1)
	}, 1*time.Millisecond)
	defer d.close()

	d.do(func() { time.Sleep(5 * time.Millisecond) })
	d.do(func() { time.Sleep(5 * time.Millisecond) })
	d.do(func() { time.Sleep(5 * time.Millisecond) })

	if atomic.LoadInt64(&ticks) == 0 {
		t.Fatal("expected ticks to fire between commands, got 0")
	}
}

func TestDispatcherClose(t *testing.T) {
	d := newDispatcher(nil, 0)

	done := make(chan struct{})
	go func() {
		d.close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("close did not return within 100ms")
	}
}
