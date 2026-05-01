package maplibre

import (
	"errors"
	"runtime"
	"sync"
	"time"
)

// errDispatcherClosed is returned by dispatcher.do when the runtime owning
// the dispatcher has already been closed (or its goroutine has exited).
// Wrapped into a Status-bearing *Error at call sites so consumers see a
// consistent error type from the public API.
var errDispatcherClosed = errors.New("maplibre: runtime closed")

// dispatcher owns a single OS thread (via runtime.LockOSThread) and serializes
// every call into the maplibre-native-ffi ABI through a command channel.
//
// The model is required because the ABI's runtime and map handles are
// owner-thread affine: any call from a different OS thread returns
// MLN_STATUS_WRONG_THREAD. Funneling all calls through one locked goroutine
// keeps the contract trivially satisfied without per-call thread checks.
//
// onTick (optional) is invoked between commands at tick interval and is the
// hook that drives mln_runtime_run_once once a runtime is created.
type dispatcher struct {
	cmds      chan func()
	quit      chan struct{}
	onTick    func()
	tick      time.Duration
	closeOnce sync.Once
}

func newDispatcher(onTick func(), tick time.Duration) *dispatcher {
	if tick <= 0 {
		tick = 8 * time.Millisecond
	}
	d := &dispatcher{
		cmds:   make(chan func()),
		quit:   make(chan struct{}),
		onTick: onTick,
		tick:   tick,
	}
	started := make(chan struct{})
	go d.loop(started)
	<-started
	return d
}

func (d *dispatcher) loop(started chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	close(started)

	var tickC <-chan time.Time
	if d.onTick != nil {
		ticker := time.NewTicker(d.tick)
		defer ticker.Stop()
		tickC = ticker.C
	}

	for {
		select {
		case fn, ok := <-d.cmds:
			if !ok {
				return
			}
			d.runOne(fn)
		case <-tickC:
			d.runOne(d.onTick)
		case <-d.quit:
			return
		}
	}
}

// runOne executes a single dispatched closure inside the autorelease pool
// and recovers from panics so a single bad command doesn't kill the
// dispatcher goroutine. Panics are logged to stderr; the closure's `done`
// channel is still closed by the caller's `defer close(done)` so callers
// don't deadlock waiting on a panicked command.
func (d *dispatcher) runOne(fn func()) {
	if fn == nil {
		return
	}
	withAutoreleasePool(func() {
		defer func() {
			if r := recover(); r != nil {
				// Don't crash the dispatcher; surface to operator via stderr.
				// The caller still sees `done` close (deferred in do()),
				// so they unblock — but the operation's effect is undefined.
				logDispatcherPanic(r)
			}
		}()
		fn()
	})
}

// do enqueues fn on the dispatcher thread and blocks until it returns.
//
// Returns errDispatcherClosed if the dispatcher has been closed (either
// before do is called or while it's waiting for completion). Callers
// should wrap this into a Status-bearing *Error.
func (d *dispatcher) do(fn func()) error {
	done := make(chan struct{})
	wrapped := func() {
		defer close(done) // run even if fn panics
		fn()
	}
	select {
	case d.cmds <- wrapped:
	case <-d.quit:
		return errDispatcherClosed
	}
	select {
	case <-done:
		return nil
	case <-d.quit:
		return errDispatcherClosed
	}
}

// close stops the dispatcher loop. Idempotent; safe to call concurrently.
// After close, do() returns errDispatcherClosed and never blocks.
func (d *dispatcher) close() {
	d.closeOnce.Do(func() {
		close(d.quit)
	})
}
