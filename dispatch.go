package maplibre

import (
	"runtime"
	"time"
)

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
	cmds   chan func()
	quit   chan struct{}
	onTick func()
	tick   time.Duration
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
		case fn := <-d.cmds:
			fn()
		case <-tickC:
			d.onTick()
		case <-d.quit:
			return
		}
	}
}

// do enqueues fn on the dispatcher thread and blocks until it returns.
func (d *dispatcher) do(fn func()) {
	done := make(chan struct{})
	d.cmds <- func() {
		fn()
		close(done)
	}
	<-done
}

// close stops the dispatcher loop. After close, no further calls to do may be
// made.
func (d *dispatcher) close() {
	close(d.quit)
}
