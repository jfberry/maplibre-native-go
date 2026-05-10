# Comments On The Revised Go Binding Conventions Doc

Follow-up to earlier feedback on
[`development/bindings-go/`](https://maplibre.org/maplibre-native-ffi/development/bindings-go/).
Upstream has revised both the cross-language
[`development/bindings/`](https://maplibre.org/maplibre-native-ffi/development/bindings/)
page and the Go-specific page in response to the previous round.
This document summarises what was addressed, lists what is still
outstanding for an implementer working from the docs alone, and
closes with an updated candidate replacement page that incorporates
upstream's new framing.

## What The Revision Addresses

Five of the original gaps are now answered by the revised pages:

- **Diagnostic timing.** "Use a short `runtime.LockOSThread` window
  to keep the C call and diagnostic read together." Tight and
  correct.
- **Public-surface shape.** The Go page now explicitly identifies
  two layers: a direct handle API as the base layer, plus a small
  opt-in owner goroutine helper that locks an OS thread, runs
  owner-thread calls, and pumps runtime events. The cross-language
  Owner Threads section gives this same pattern its general
  blessing for languages whose schedulers separate logical
  execution from native thread identity.
- **`runtime.Pinner` usage.** "Use `runtime.Pinner` for retained
  Go memory. Let cgo's per-call pinning cover ordinary buffers."
- **`cgo.Handle` plumbing through `void* user_data`.** "Store
  callback closures through `runtime/cgo.Handle` ... Use C-owned
  storage for retained strings, buffers, and callback `user_data`
  cells." The exact mechanism (a C-allocated cell containing the
  handle's uintptr value, dereferenced from the trampoline) is now
  implied by combining those two sentences.
- **Adapter-layer relationship.** "Application scheduling and
  framework integration stay above that helper." Direct framing.

## Outstanding Items

The following gaps would still leave a contextless implementer
without enough material to produce a buildable, idiomatic Go
binding from the docs alone.

### 1. Build setup is undocumented

The Go page does not show how a buildable cgo package wires up
`pkg-config: maplibre-native-c`, `CFLAGS` / `LDFLAGS`,
`PKG_CONFIG_PATH` for consumers, or a build tag for development-time
leak reporting.

### 2. Platform-tagged files are not described

Metal-only (darwin) and Vulkan-only (linux) attach paths require Go
build tags and platform-specific `#cgo` directives. Without explicit
guidance, an implementer wires both into one file and breaks both.

### 3. The cgo `//export` cast-helper file pattern is not addressed

cgo's auto-generated `_cgo_export.h` declares `//export`'d Go
functions with `char*` parameters. mbgl's typed callback function
pointers expect `const char*`. Casting in the same Go-file
`import "C"` preamble produces `conflicting types` errors. The
conventional resolution is a small companion `.c` translation unit
that includes both `_cgo_export.h` and `maplibre_native_c.h` and
exposes typed-cast accessors. This is Go-specific, non-obvious, and
the implementer hits it hard.

### 4. The `*Error` shape is not pinned

The page says "Expose stable Go error categories that work with
`errors.Is` and include the copied diagnostic message." This is the
right idiom but does not pin the field set, the sentinel values, or
the `Is` method. Two implementers will produce two different error
types.

### 5. Owner goroutine helper API surface is not pinned

The page introduces the helper but does not pin the methods. Without
that, two implementers will pick different method shapes
(`Do(fn) error` vs `Run(fn) error` vs a more-typed shape). The
helper is small but having a single canonical API makes adapters
above it portable across consumers.

### 6. Handle struct layout is not pinned

What's inside `RuntimeHandle`? Native pointer + parent + closed flag
(atomic? mutex?) + finalizer-context? An implementer produces
something plausible but two implementers will diverge on which Go
synchronisation primitive guards the closed state, which then
affects whether `Close()` is safe to call from any goroutine.

### 7. Idempotent + nil-safe `Close()` is not pinned

Standard Go convention is that `Close()` on a nil receiver returns
nil and that calling on an already-closed handle is a no-op. The
page mentions release-once idempotency but does not extend to
nil-receiver safety.

### 8. Constants generation is not addressed

The C API exposes ~200 enum values. The page does not say whether
to handwrite these, generate them, or where the generator's output
lives. Both choices are defensible; one needs to be pinned.

### 9. Strings handling needs Go-specific guidance

The cross-language page covers UTF-8 / NUL rejection. Go-specific
choices — `C.GoStringN` over `C.GoString` when length is known,
`(*C.char)(unsafe.Pointer(&b[0]))` for `mln_string_view` byte-slice
inputs, `C.CString` + `defer C.free` for borrowed inputs — would
prevent inconsistent string handling across calls.

### 10. There is no end-to-end example

The Java conventions page opens with a five-line example showing
runtime → map → render-session lifecycle. The Go page has none.
With the new direct-API + owner-helper split, an example that
shows the helper-driven shape would anchor the helper's API and
demonstrate how the layers fit.

## Proposed Replacement Page

The text below is a direct candidate for replacing
`docs/src/content/docs/development/bindings-go.md`. It keeps every
upstream design decision from the revised page (direct handle API
as base layer, opt-in owner goroutine helper, no application-level
scheduling, `Handle` suffix, `NativePointer` value type,
`cgo.Handle` for callback state) and fills the outstanding gaps.

The page deliberately does not introduce `context.Context`,
internal cancellation channels, parallel-renderer pools, or
`Session`-style ergonomic bundles. Those concerns sit in adapter
packages above the binding, written by downstream consumers.

````markdown
---
title: Go Bindings
description: Language-specific implementation conventions for Go bindings.
sidebar:
  order: 4
---

## Resources

- Tracking issue: [#43](https://github.com/maplibre/maplibre-native-ffi/issues/43)
- Go [`cgo` documentation](https://pkg.go.dev/cmd/cgo)
- [`runtime.Pinner`](https://pkg.go.dev/runtime#Pinner)
- [`runtime/cgo.Handle`](https://pkg.go.dev/runtime/cgo#Handle)

## Scope

The Go binding is a thin, low-level layer over the public C API.
It exposes the C API's runtime, map, render session, event,
callback, and render target model with Go ownership, error,
memory, and threading rules.

The binding ships in two layers:

- **Direct handle API.** Calls run on the calling goroutine and
  return the C `WrongThread` error when they reach the wrong
  owner thread. The binding does not silently marshal calls.
- **Opt-in owner goroutine helper.** A small helper that locks an
  OS thread, runs owner-thread calls on it, and pumps runtime
  events. Limited to generic owner-thread execution and event
  draining; does not own or manage handle lifecycles.

Application scheduling, `context.Context` integration,
parallel-renderer pools, and framework integration belong in
adapter packages above the binding, written by downstream
consumers.

The binding uses `cgo` over the public C headers and keeps raw C
declarations private. It targets Go 1.21 or newer for
`runtime.Pinner` and the modern `runtime/cgo.Handle` API.

## Package And Build

The binding ships as one Go module:

```text
github.com/maplibre/maplibre-native-go
```

Public types live in package `maplibre`. The internal cgo wiring
stays in the same package; downstream adapters consume only the
public surface.

The binding links against `libmaplibre-native-c` via pkg-config:

```go
// cgo.go
package maplibre

/*
#cgo pkg-config: maplibre-native-c
#include "maplibre_native_c.h"
*/
import "C"
```

Consumers set `PKG_CONFIG_PATH` to point at a built
`maplibre-native-c.pc`.

Backend-specific attach paths live in build-tagged files:

```go
//go:build darwin

/*
#cgo darwin LDFLAGS: -framework Metal -framework Foundation
*/
import "C"
```

```go
//go:build linux

/*
#cgo linux pkg-config: vulkan
*/
import "C"
```

A `mln_debug` build tag installs `runtime.SetFinalizer`-based leak
reporting on every owned handle. Off by default; release builds pay
zero cost.

The binding compiles cleanly with `-race`.

## API Shape

Owned long-lived native objects use the cross-language `Handle`
suffix:

```text
maplibre.RuntimeHandle
maplibre.MapHandle
maplibre.MapProjectionHandle
maplibre.RenderSessionHandle
```

Go-owned values, descriptors, events, copied data, and one-shot
snapshots are plain structs without the suffix:

```text
maplibre.RuntimeOptions
maplibre.MapOptions
maplibre.CameraOptions
maplibre.LatLng
maplibre.Event
maplibre.RenderingStats
maplibre.TextureImageInfo
```

Drop the `mln_` prefix and strip `_options` / `_t` where Go
readability benefits.

## Status And Errors

Status-returning C calls return `error`. A non-OK status produces an
`*Error`:

```go
type Status int32

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
    Op      string  // tag of failing operation, e.g. "MapHandle.SetStyleJSON"
    Message string  // mbgl thread-local diagnostic captured at failure site
}

func (e *Error) Error() string  { /* "Op: STATUS_NAME: message" */ }
func (e *Error) Is(target error) bool  { /* matches by Status */ }
```

Sentinel `*Error` values for `errors.Is` matching:

```go
var (
    ErrInvalidArgument = &Error{Status: StatusInvalidArgument}
    ErrInvalidState    = &Error{Status: StatusInvalidState}
    ErrWrongThread     = &Error{Status: StatusWrongThread}
    ErrUnsupported     = &Error{Status: StatusUnsupported}
    ErrNativeError     = &Error{Status: StatusNativeError}
)

if errors.Is(err, maplibre.ErrInvalidState) { /* ... */ }
```

## Diagnostics

`mln_thread_last_error_message` is thread-local. Go goroutines
migrate across OS threads. Status-returning calls capture the
diagnostic on the same OS thread that returned the status. Use a
short `runtime.LockOSThread` window to keep the C call and the
diagnostic read together:

```go
func statusError(op string, status C.mln_status) error {
    if status == C.MLN_STATUS_OK { return nil }
    return &Error{
        Status:  Status(status),
        Op:      op,
        Message: C.GoString(C.mln_thread_last_error_message()),
    }
}
```

`statusError` is called inline at every status-checking site,
inside the same `LockOSThread` window as the failing C call. The
binding does not defer diagnostic capture across function returns.
The owner goroutine helper performs all calls on its locked OS
thread, so callers using the helper inherit this behaviour
automatically.

## Owned Handles

Every long-lived C-owned opaque handle maps to a Go struct with an
explicit `Close() error` method:

```go
type RuntimeHandle struct {
    ptr    *C.mln_runtime  // private; nil after Close
    closed atomic.Bool     // observable from any goroutine
    // optional debug leak context
}

func NewRuntime(opts RuntimeOptions) (*RuntimeHandle, error)
func (r *RuntimeHandle) Close() error
```

A handle stores the native pointer, parent handles required for
native validity, an open-or-closed flag, and optional debug leak
context. `Close()` is callable on a nil receiver and on an
already-closed handle, returning nil in both cases.

Parents stay reachable while children are live. `MapHandle` keeps
its `*RuntimeHandle` reachable; `RenderSessionHandle` keeps its
`*MapHandle` reachable. Garbage-collecting a parent before its
children is impossible by reachability.

`MapProjectionHandle` is created from a `MapHandle`, owns a
standalone transform snapshot, and releases via
`mln_map_projection_destroy`. After creation it does not depend
on the source `MapHandle` for native validity; map camera or
projection changes after the snapshot do not update it.

Closing a parent before its children returns `ErrInvalidState`.
The binding does not cascade-close.

Under the `mln_debug` build tag, `runtime.SetFinalizer` reports
leaked handles to stderr. The finalizer reports only; it does not
destroy thread-affine native handles, which would race with the
GC thread.

## Owner Threads

Goroutines do not preserve OS-thread identity. Callers using the
direct handle API call `runtime.LockOSThread` when they need
deterministic owner-thread affinity. Ordinary calls run on the
calling goroutine and return `StatusWrongThread` when they reach
the wrong owner thread. The binding does not silently marshal
ordinary calls to a different goroutine or OS thread.

Public type boundaries align with C owner concepts:

```text
RuntimeHandle         runtime owner thread in C
MapHandle             map owner thread in C
MapProjectionHandle   projection owner thread in C
RenderSessionHandle   session owner thread in C
```

The binding ships an opt-in owner goroutine helper that locks an
OS thread, runs owner-thread calls on it, and pumps runtime
events:

```go
type Owner struct { /* private */ }

// StartOwner spawns a goroutine that calls runtime.LockOSThread
// for the lifetime of the Owner.
func StartOwner() *Owner

// Run executes fn on the Owner's locked OS thread, blocking until
// fn returns. Calls inside fn run on the Owner's thread.
func (o *Owner) Run(fn func() error) error

// PumpEvents drains rt's event queue, calling consume for each
// event. It calls mln_runtime_run_once between drains. Returns
// when consume returns true, the deadline passes, or a fatal C
// error occurs. PumpEvents must be called via Run.
func (o *Owner) PumpEvents(rt *RuntimeHandle, deadline time.Time,
    consume func(Event) bool) error

// Stop unlocks the OS thread. Caller must Close all handles
// before Stop. Idempotent.
func (o *Owner) Stop() error
```

The Owner does not own `RuntimeHandle`, `MapHandle`, or
`RenderSessionHandle`. Callers create handles inside `Run` and
close them inside `Run` before `Stop`. The Owner is limited to
generic owner-thread execution and event draining; application
scheduling and framework integration stay above it.

## Strings

Encode Go strings as UTF-8 at the C boundary.

For null-terminated `const char*` inputs, allocate with
`C.CString` and free immediately:

```go
cs := C.CString(s)
defer C.free(unsafe.Pointer(cs))
// ... pass cs to C ...
```

The binding does not retain `*C.char` pointers past the C call
that consumes them. The binding rejects strings containing
embedded NUL for null-terminated inputs.

For explicit-length `mln_string_view` inputs, pass the byte slice
directly:

```go
data := (*C.char)(unsafe.Pointer(&b[0]))
size := C.size_t(len(b))
```

For `const char*` outputs whose length is known, copy with
`C.GoStringN(ptr, length)` rather than `C.GoString(ptr)` to avoid
an unnecessary `strlen` scan.

## Native Pointers

Backend-native opaque addresses use a small uintptr-based value
type:

```go
type NativePointer uintptr
```

It converts to `unsafe.Pointer` only at the cgo boundary. Public
APIs accept or return `NativePointer` only where the C API accepts
or returns an opaque backend-native handle (Metal devices and
textures, Vulkan instances / devices / queues / images, native
surfaces).

## Callback-Scoped Borrows

Native data exposed only during a callback's lifetime uses a
callback-scoped accessor pattern. The frame type is not freely
returned and not publicly closeable; the binding acquires before
the callback runs and releases when it returns or panics.

```go
func (s *RenderSessionHandle) WithMetalOwnedTextureFrame(
    fn func(*MetalOwnedTextureFrame) error,
) error
```

The frame type stores private state and exposes its native handles
through accessor methods that fail when the frame is no longer
active:

```go
type MetalOwnedTextureFrame struct {
    // private fields
}

func (f *MetalOwnedTextureFrame) TextureUnsafe() (NativePointer, error)
func (f *MetalOwnedTextureFrame) DeviceUnsafe()  (NativePointer, error)
```

Accessor names carry the `Unsafe` suffix, matching the
cross-language convention. They return `ErrInvalidState` after the
callback scope ends.

The C side rejects nested acquires, render updates, resize,
detach, and destroy while a frame is acquired. The binding relies
on those checks and always releases the frame in a `defer` after
the callback returns.

## Native Memory

Per-call temporary storage uses Go-allocated buffers, byte slices,
and `C.CString`-allocated strings released before return. cgo
pins Go-allocated slices for the duration of the C call.

Object-owned native storage uses `C.malloc` / `C.free` paired with
a Go owner that frees in `Close()` or in an explicit teardown
path. This covers callback `user_data` cells (see Callbacks) and
`const char*` values mbgl retains across calls.

Large explicit buffers reused across renders use caller-owned Go
slices passed to readback functions. The binding writes into the
slice and returns; no per-render cgo allocation.

`runtime.Pinner` is reserved for cases where the binding must
store a pointer to Go-allocated memory in a C struct that mbgl
reads across multiple cgo calls without going through a
`cgo.Handle`. Most callback state goes through `cgo.Handle` and
does not require a Pinner.

## Callbacks

The C API exposes process-global, runtime-scoped, and
request-scoped callbacks. Each is plumbed through a `//export`'d
Go trampoline:

```go
//export mlnGoLogTrampoline
func mlnGoLogTrampoline(userData unsafe.Pointer, severity C.uint32_t,
    event C.uint32_t, code C.int64_t, message *C.char) C.uint32_t {
    defer func() { _ = recover() }()
    if userData == nil { return 0 }
    h := cgo.Handle(*(*uintptr)(userData))
    cb := h.Value().(LogCallback)
    // ... invoke cb, return status ...
    _ = cb
    return 0
}
```

The trampoline lives in a Go file that uses cgo's `//export`
directive. The C side requires a function pointer with the C ABI's
typed signature (`mln_log_callback`). Because cgo's auto-generated
`_cgo_export.h` declares the trampoline with `char*` parameters
rather than `const char*`, the typed-cast accessors live in a
small companion C translation unit:

```c
// trampolines.c — compiled with the binding
#include "maplibre_native_c.h"
#include "_cgo_export.h"

mln_log_callback mlnGoGetLogTrampoline(void) {
    return (mln_log_callback)mlnGoLogTrampoline;
}
```

User data passes through cgo as a small C-allocated cell
containing a `runtime/cgo.Handle` value. The cell gives mbgl a
stable native address; the handle keeps the Go callback closure
reachable across the cgo boundary:

```go
hp := C.malloc(C.size_t(unsafe.Sizeof(uintptr(0))))
*(*uintptr)(hp) = uintptr(cgo.NewHandle(callback))

var t C.mln_resource_transform
t.size      = C.uint32_t(unsafe.Sizeof(t))
t.callback  = C.mlnGoGetTransformTrampoline()
t.user_data = hp
```

The owning binding type (e.g. `RuntimeHandle`) frees both the cell
and the handle in `Close()` after the C side is destroyed.

Callbacks recover panics at the trampoline boundary and convert
them to the documented C callback behaviour ("no rewrite", OK
status, or similar). Panics never unwind through cgo.

Callbacks may run on MapLibre worker, network, logging, or render
threads. Implementations must be thread-safe, return quickly, and
must not call back into the binding. Borrowed C-side request
fields are copied into Go values before the Go callback returns
when the binding needs them later.

A handled resource request uses a Go object that owns the
provider's reference to the C request handle. It enforces
one-shot completion and exactly-once release. Completion and
cancellation checks may run from any goroutine when the C API
allows it.

## Constants

The C API's enum values flow into typed Go constants. The binding
generates this table from the C headers via a small generator
committed to the repository:

```text
internal/cmd/gen-constants/main.go     extracts MLN_* enum values
constants_gen.go                       generator output, committed
```

Regenerate when upstream pulls. Generator output is a single Go
file; treat successful generation as the header parsability check.
Hand-written enums beyond the constants table are not introduced.

## Worked Example

Render one still image using the owner goroutine helper. The
helper handles `runtime.LockOSThread` and the runtime event pump;
the example focuses on handle lifecycle and the render-still
flow.

```go
package main

import (
    "fmt"
    "log"
    "time"

    maplibre "github.com/maplibre/maplibre-native-go"
)

func main() {
    owner := maplibre.StartOwner()
    defer owner.Stop()

    var (
        rt   *maplibre.RuntimeHandle
        m    *maplibre.MapHandle
        sess *maplibre.RenderSessionHandle
    )

    if err := owner.Run(func() error {
        var err error
        rt, err = maplibre.NewRuntime(maplibre.RuntimeOptions{})
        if err != nil { return err }
        m, err = rt.NewMap(maplibre.MapOptions{Width: 256, Height: 256})
        if err != nil { return err }
        sess, err = m.AttachOwnedTexture(256, 256, 1.0)
        if err != nil { return err }
        return m.SetStyleJSON(`{"version":8,"sources":{},"layers":[]}`)
    }); err != nil {
        log.Fatal(err)
    }
    defer owner.Run(func() error { return sess.Close() })
    defer owner.Run(func() error { return m.Close() })
    defer owner.Run(func() error { return rt.Close() })

    deadline := time.Now().Add(10 * time.Second)
    if err := owner.PumpEvents(rt, deadline, func(ev maplibre.Event) bool {
        return ev.Source == m && ev.Type == maplibre.EventStyleLoaded
    }); err != nil {
        log.Fatal(err)
    }

    if err := owner.Run(func() error { return m.RequestStillImage() }); err != nil {
        log.Fatal(err)
    }
    if err := owner.PumpEvents(rt, deadline, func(ev maplibre.Event) bool {
        return ev.Source == m && ev.Type == maplibre.EventStillImageFinished
    }); err != nil {
        log.Fatal(err)
    }

    var (
        rgba []byte
        info maplibre.TextureImageInfo
    )
    if err := owner.Run(func() error {
        var err error
        rgba, info, err = sess.ReadPremultipliedRGBA8()
        return err
    }); err != nil {
        log.Fatal(err)
    }
    fmt.Printf("rendered %dx%d (%d bytes)\n", info.Width, info.Height, len(rgba))
}
```

The direct handle API is also available without the helper.
Callers using it own the `runtime.LockOSThread` window, the
`mln_runtime_run_once` pump, and the event drain loop themselves.

Adapter packages above the binding wrap one or both layers with
`context.Context` cancellation, parallel-runtime pools, and
framework integration. Such adapters are out of scope for the
binding.

## Testing

`go test -race ./...` runs the test suite against a real built
`libmaplibre-native-c`. The binding does not mock the C ABI.

Tests cover the language-adaptation invariants: idempotent
`Close`, `errors.Is` sentinel matching, diagnostic capture under
goroutine migration, callback panic recovery, one-shot resource
request completion, owner-helper start and stop ordering, and
`PumpEvents` deadline behaviour. C ABI tests cover native
behaviour; binding-level tests prove the Go layer preserves it.
````
