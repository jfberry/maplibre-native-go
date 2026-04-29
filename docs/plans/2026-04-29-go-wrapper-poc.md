# Go wrapper PoC — load-bearing decisions

**Goal:** Prove the maplibre-native-ffi C ABI is consumable from Go via cgo, end-to-end through `STYLE_LOADED`, with a clean threading and error model that survives once the readback ABI lands.

**Out of scope for PoC:** pixel output, log/resource/transform callbacks, Linux/Vulkan, Windows, finalizers, packaging.

## Decision 1 — Build / link

Submodule vs out-of-tree: maplibre-native-ffi lives separately; the Go module does not vendor it. Path is configured via `MLN_FFI_DIR` (default `$(HOME)/dev/maplibre-native-ffi`).

A `Makefile` builds the dylib (delegating to upstream tooling) and runs `go build`. Cgo is driven via env-prepended `CGO_CFLAGS` / `CGO_LDFLAGS` so no path is hard-coded in `.go` files.

```make
MLN_FFI_DIR ?= $(HOME)/dev/maplibre-native-ffi

native:
	cd $(MLN_FFI_DIR) && mise run build

export CGO_CFLAGS  := -I$(MLN_FFI_DIR)/include
export CGO_LDFLAGS := -L$(MLN_FFI_DIR)/build -lmaplibre_native_abi -Wl,-rpath,$(MLN_FFI_DIR)/build

build: native
	go build ./...

test: native
	go test ./...
```

Static linking, install hooks, and Linux Vulkan packaging are deferred. PoC is "build the dylib next door, rpath it."

## Decision 2 — Threading model

Runtime is owner-thread-affine. One dedicated goroutine with `runtime.LockOSThread()` owns the runtime; all ABI calls funnel through it via a command channel; callers block on a per-command done channel.

```go
type dispatcher struct {
    cmds chan func()
    quit chan struct{}
}

func (d *dispatcher) loop() {
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()
    ticker := time.NewTicker(8 * time.Millisecond)
    defer ticker.Stop()
    for {
        select {
        case fn := <-d.cmds:
            fn()
        case <-ticker.C:
            C.mln_runtime_run_once(d.runtime)
        case <-d.quit:
            return
        }
    }
}

func (d *dispatcher) do(fn func()) {
    done := make(chan struct{})
    d.cmds <- func() { fn(); close(done) }
    <-done
}
```

Consequences: every public method is a thin `d.do(...)` wrapper; `mln_thread_last_error_message()` is read inside the same `func()` (same-thread guaranteed); finalizers may *log a leak warning* but must not call into the ABI.

## Decision 3 — Handle wrapping

One Go struct per opaque ABI handle, holding raw pointer + dispatcher back-reference.

```go
type Runtime struct {
    d   *dispatcher
    ptr *C.mln_runtime
}
type Map struct {
    rt  *Runtime
    ptr *C.mln_map
}
type TextureSession struct {
    m   *Map
    ptr *C.mln_texture_session
}
```

Rules: all ABI calls dispatch; `Close()` is idempotent; no Go pointers cross into C land; future callbacks use an int64 cookie keyed into a `sync.Map`.

Package name `maplibre`, types drop the `mln_` prefix.

## Decision 4 — Error & diagnostic mapping

```go
type Status int
const (
    StatusOK              Status = 0
    StatusInvalidArgument Status = -1
    StatusInvalidState    Status = -2
    StatusWrongThread     Status = -3
    StatusUnsupported     Status = -4
    StatusNativeError     Status = -5
)

type Error struct {
    Status  Status
    Op      string
    Message string
}
```

Helper used by every wrapper:

```go
func (d *dispatcher) call(op string, fn func() C.mln_status) error {
    var status C.mln_status
    d.do(func() { status = fn() })
    if status == C.MLN_STATUS_OK {
        return nil
    }
    var msg string
    d.do(func() { msg = C.GoString(C.mln_thread_last_error_message()) })
    return &Error{Status: Status(status), Op: op, Message: msg}
}
```

Diagnostic read happens on the dispatcher thread by construction.

## File layout

```
/
  go.mod
  Makefile
  cgo.go                   // #cgo + #include only
  errors.go
  dispatch.go
  runtime.go
  map.go
  texture_metal_darwin.go
  cmd/poc/main.go
```

## Iteration order

1. `go.mod`, `Makefile`, `cgo.go` calling `mln_abi_version()`. **Acceptance:** `make build && ./poc` prints `ABI v0`.
2. `errors.go` + `dispatch.go` with unit tests.
3. `runtime.go` — `New` / `Close` round-trip.
4. `map.go` — `Map.New` / `Close` round-trip.
5. `map.go` — `SetStyleJSON` with empty style; `PollEvent` until `STYLE_LOADED`.
6. `map.go` — camera ops, `GetCamera` round-trip.
7. `texture_metal_darwin.go` — Metal texture session lifecycle. **Acceptance:** render returns OK, acquired frame texture pointer is non-null. No pixel inspection until readback ABI lands.
8. `cmd/poc/main.go` — full sequence against a real OMT mbtiles + style.
