# Gaps In The Go Binding Conventions Doc

Feedback on
[`development/bindings-go/`](https://maplibre.org/maplibre-native-ffi/development/bindings-go/)
after a hands-on attempt to use it as the only specification source for
producing a Go binding.

The page is intentionally thin — it cross-references the language-agnostic
[`development/bindings/`](https://maplibre.org/maplibre-native-ffi/development/bindings/)
page for the cross-language model and adds Go-specific decisions on top.
The cross-language page is well-developed; the Go-specific page in its
current form leaves a contextless implementer (human or LLM) without
enough material to produce a buildable, working binding. This document
lists what's missing and closes with a proposed replacement page that
fills those gaps without changing any of upstream's design decisions
(thin layer, no internal dispatcher, caller-driven threading).

The framing assumes ergonomic concerns — `context.Context` cancellation,
internal dispatcher goroutines, render-pump loops, `Session`-style
bundles, parallel pools — live in **adapter packages above** this
binding, written by downstream consumers. The binding doc only needs to
help an implementer get a thin, correct, low-level layer in place.

## Issues

### 1. Build setup is undocumented

A buildable Go cgo package needs at minimum:

- `pkg-config` integration
- `CFLAGS` / `LDFLAGS` directives
- `PKG_CONFIG_PATH` setup expectations for consumers
- A way to flip leak-reporting finalizers on for development

None of these are in the current page. An LLM working only from the doc
has no way to get past the first build attempt.

### 2. Platform-tagged files are not described

Metal-only (darwin) and Vulkan-only (linux) attach paths require Go
build tags and platform-specific `#cgo` LDFLAGS. Without explicit
guidance, an LLM either fails the build on whichever platform it
forgets, or wires both paths into one file and breaks both.

### 3. cgo.Handle plumbing through `void* user_data` is not addressed

The page says "use `runtime/cgo.Handle` for callback state." It does
not say *how* a `cgo.Handle` (an opaque uintptr-sized integer)
becomes mbgl's `void* user_data` parameter. The naive
`unsafe.Pointer(uintptr(handle))` cast violates the race detector's
`checkptr` guard. The conventional resolution — store the handle in
a small C-allocated cell that mbgl sees as a real pointer, and have
the trampoline dereference it — is non-obvious and Go-specific. An
LLM hits this and fights it for a while.

### 4. The cgo `//export` cast-helper file pattern is not addressed

cgo's auto-generated `_cgo_export.h` declares `//export`'d Go
functions with `char*` parameters. mbgl's typed callback function
pointers expect `const char*`. Casting in the same Go-file `import "C"`
preamble produces `conflicting types` errors at build time. The
conventional resolution is a small `.c` translation unit that includes
both `_cgo_export.h` and `maplibre_native_c.h` and exposes typed-cast
accessors. The page does not mention this; an LLM hits the conflict
and may misdiagnose it as a bug in the headers.

### 5. Diagnostic-message timing in the no-marshal model needs explicit treatment

`mln_thread_last_error_message()` is thread-local. Goroutines migrate
across OS threads at the Go scheduler's discretion. With the binding's
"no marshal" decision, the binding must read the diagnostic
**immediately after the failing C call, in the same goroutine, before
any operation that could yield**. The current page mentions caller
thread responsibility for owner-thread affinity but does not extend
that guidance to diagnostic capture, which is a different, smaller,
more easily-overlooked window.

### 6. Public-surface shape in the no-dispatcher model is not pinned

The page rules out internal call marshalling but does not show what
`RuntimeHandle`'s public API looks like under that constraint.
Concretely: does `RuntimeHandle` expose `RunOnce()` and `PollEvent()`
directly so the caller drives the pump loop? Does it expose any
`WaitForX` helpers, and if so do they assume the caller has already
called `runtime.LockOSThread`? Without a pinned public surface, two
implementations following the page will diverge meaningfully.

### 7. The `*Error` shape is not pinned

The cross-language page says "Map each C status category to a stable,
idiomatic public error representation." Idiomatic Go is `error` plus
sentinel matching via `errors.Is`, but the specific shape of the
error type (struct fields, sentinel values, `Is` method) varies.
Pinning this in the Go-specific page keeps bindings consistent across
implementers.

### 8. Handle struct layout is not pinned

What's inside `RuntimeHandle`? Native pointer + parent + closed flag
(atomic? mutex?) + finalizer-context? An LLM produces something
plausible but two LLMs will produce two different shapes. A short
example struct definition resolves this.

### 9. Idempotent + nil-safe `Close()` is not pinned

`Close()` should be callable on a nil receiver and on an
already-closed handle without error. The page mentions release once
makes later release calls no-ops, but doesn't extend to nil-receiver
safety, which is a Go convention worth stating.

### 10. Constants generation is not addressed

C enum values flow into typed Go constants. The page does not say
whether to handwrite them, generate them, or where the generator's
output lives. With ~200 enum values across the C API, this is a real
choice that bindings will have to make and that should be pinned.

### 11. `runtime.Pinner` usage is mentioned but not shown

The page lists `runtime.Pinner` in resources but doesn't show when
the binding should reach for it. With `cgo.Handle` covering most
callback state and cgo's automatic per-call pinning covering most
buffers, Pinner is a relatively rare tool. A one-line statement of
the case ("use Pinner when retaining a Go-allocated buffer in a C
struct field across multiple C calls") would clarify.

### 12. There is no end-to-end example

The Java conventions page opens with a five-line example showing
runtime → map → render-session lifecycle. The Go page has none. A
30-line example anchoring every other decision (struct shape, error
return, owner-thread pinning, render-still flow, native readback,
close order) would be more useful than any individual section.

### 13. Strings handling is not Go-specific

The cross-language page covers UTF-8 / NUL rejection. Go-specific
strings choices — `C.GoStringN` over `C.GoString` when length is
known, byte-slice-to-`*C.char` conversion via `unsafe.Pointer(&b[0])`,
`C.CString` + `defer C.free` for borrowed Go-string inputs — would
prevent inconsistent string handling across calls.

### 14. The adapter-layer / wrapper-layer relationship is not framed

Upstream's "no internal dispatcher" decision implicitly defers
ergonomic concerns (Context cancellation, render-pump loops,
goroutine-friendly handle access, parallel pools) to wrappers above
the binding. Stating this explicitly in the page sets reader
expectations and prevents implementers from accidentally pulling
ergonomic concerns into the binding to "make it more usable."

## Proposed replacement page

The text below is a direct candidate for replacing
`docs/src/content/docs/development/bindings-go.md`. It keeps every
upstream design decision, fills the gaps above, and stays at a
length comparable to the existing Java FFM conventions page.

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

The Go binding is a thin, low-level layer over the public C API. It
exposes the C API's runtime, map, render session, event, callback,
and render target model with Go ownership, error, memory, and
threading rules. It does not internalise threading, cancellation,
goroutine-friendly handle access, or parallel-renderer scheduling;
those concerns belong in adapter packages above this binding,
written by downstream consumers.

The binding uses `cgo` over the public C headers and keeps raw C
declarations private. It targets Go 1.21 or newer for
`runtime.Pinner` and the modern `runtime/cgo.Handle` API.

## Package And Build

The binding ships as one Go module:

```text
github.com/maplibre/maplibre-native-go
```

Public types live in package `maplibre`. Internal C wiring stays in
the same package via cgo's `import "C"` mechanism; downstream
adapters consume only the public surface.

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

The binding compiles cleanly with `-race`. cgo pointer rules
(`runtime.Pinner` for retained Go pointers, `cgo.Handle` for callback
state, C-owned storage for strings stored on the C side past the
returning call) keep the package within the race detector's
`checkptr` rules.

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

func (e *Error) Error() string { /* "Op: STATUS_NAME: message" */ }
func (e *Error) Is(target error) bool { /* matches by Status */ }
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
migrate across OS threads. The binding reads the diagnostic
immediately after the failing C call, in the same goroutine, before
any cgo call or channel operation that could yield:

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

`statusError` is called inline at every status-checking site. The
binding does not defer diagnostic capture across function returns.

## Owned Handles

Every long-lived C-owned opaque handle maps to a Go struct with an
explicit `Close() error` method:

```go
type RuntimeHandle struct {
    ptr *C.mln_runtime  // private; nil after Close
}

func NewRuntime(opts RuntimeOptions) (*RuntimeHandle, error)
func (r *RuntimeHandle) Close() error  // idempotent, nil-receiver safe
```

A handle stores the native pointer, parent handles required for
native validity, open-or-closed state, and optional debug leak
context. `Close()` is callable on a nil receiver and on an already-
closed handle, returning nil in both cases.

Parents stay reachable while children are live. `MapHandle` keeps
its `*RuntimeHandle` reachable; `RenderSessionHandle` keeps its
`*MapHandle` reachable. Garbage-collecting a parent before its
children is impossible by reachability.

`MapProjectionHandle` is created from a `MapHandle`, owns a
standalone transform snapshot, and releases via
`mln_map_projection_destroy`. After creation it does not depend on
the source `MapHandle` for native validity; map camera or projection
changes after the snapshot do not update it.

Closing a parent before its children returns `ErrInvalidState`. The
binding does not cascade-close.

Under the `mln_debug` build tag, `runtime.SetFinalizer` reports
leaked handles to stderr. The finalizer reports only; it does not
destroy thread-affine native handles, which would race with the
GC thread.

## Owner Threads

Go goroutines do not preserve OS-thread identity. Callers use
`runtime.LockOSThread` when they need deterministic owner-thread
affinity. The low-level binding preserves caller execution:
ordinary calls run on the calling goroutine and return
`StatusWrongThread` when they reach the wrong owner thread. The
binding does not silently marshal ordinary calls to a different
goroutine or OS thread.

Public type boundaries align with C owner concepts:

```text
RuntimeHandle         runtime owner thread in C
MapHandle             map owner thread in C
MapProjectionHandle   projection owner thread in C
RenderSessionHandle   session owner thread in C
```

Adapter layers above this binding may run a per-runtime goroutine
pinned via `runtime.LockOSThread` and route the binding's calls
through it; the binding does not provide that machinery.

## Strings

Encode Go strings as UTF-8 at the C boundary.

For `const char*` inputs, allocate with `C.CString` and free
immediately:

```go
cs := C.CString(s)
defer C.free(unsafe.Pointer(cs))
// ... pass cs to C, return ...
```

The binding does not retain `*C.char` pointers past the C call that
consumes them.

For explicit-length `mln_string_view` inputs, pass the byte slice
directly:

```go
cs := (*C.char)(unsafe.Pointer(&b[0]))
size := C.size_t(len(b))
```

For `const char*` outputs whose length is known, copy with
`C.GoStringN(ptr, length)` rather than `C.GoString(ptr)` to avoid an
unnecessary `strlen` scan.

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

Field names exposing borrowed backend handles use the `Unsafe`
suffix, matching the cross-language convention:

```go
type MetalOwnedTextureFrame struct {
    TextureUnsafe NativePointer  // id<MTLTexture>
    DeviceUnsafe  NativePointer  // id<MTLDevice>
}
```

## Native Memory

Per-call temporary storage uses Go-allocated buffers, byte slices,
and `C.CString`-allocated strings released before return. Cgo pins
Go-allocated slices for the duration of the C call.

Object-owned native storage uses `C.malloc` / `C.free` paired with a
Go owner that frees in `Close()` or in an explicit teardown path.
Used for callback `user_data` cells (see Callbacks) and for
`const char*` values mbgl retains across calls.

Large explicit buffers reused across renders use caller-owned Go
slices passed to readback functions. The binding writes into the
slice and returns; no per-render cgo allocation.

`runtime.Pinner` is reserved for cases where the binding must store
a pointer to Go-allocated memory in a C struct that mbgl reads
across multiple cgo calls without going through a `cgo.Handle`. Most
callback state goes through `cgo.Handle` and does not require a
Pinner.

## Callbacks

The C API exposes process-global, runtime-scoped, and request-scoped
callbacks. Each is plumbed through a `//export`'d Go trampoline:

```go
//export mlnGoLogTrampoline
func mlnGoLogTrampoline(userData unsafe.Pointer, severity C.uint32_t,
    event C.uint32_t, code C.int64_t, message *C.char) C.uint32_t {
    defer recoverPanic("log callback")
    if userData == nil { return 0 }
    h := cgo.Handle(*(*uintptr)(userData))
    cb := h.Value().(LogCallback)
    return cb(/* ... */)
}
```

The trampoline lives in a Go file that uses cgo's `//export`
directive. The C side requires a function pointer with the C ABI's
typed signature (`mln_log_callback`). Because cgo's auto-generated
`_cgo_export.h` declares the trampoline with `char*` parameters
rather than `const char*`, the typed-cast accessors live in a small
companion C translation unit:

```c
// trampolines.c — compiled with the binding
#include "maplibre_native_c.h"
#include "_cgo_export.h"

mln_log_callback mlnGoGetLogTrampoline(void) {
    return (mln_log_callback)mlnGoLogTrampoline;
}
```

User data passes through cgo as a small C-allocated cell containing
a `runtime/cgo.Handle` value. The cell gives mbgl a stable native
address; the handle keeps the Go callback closure reachable across
the cgo boundary:

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

Callbacks recover panics at the trampoline boundary and convert them
to the documented C callback behaviour ("no rewrite", OK status, or
similar). Panics never unwind through cgo.

Callbacks may run on MapLibre worker, network, logging, or render
threads. Implementations must be thread-safe, return quickly, and
must not call back into binding methods that themselves issue C
calls; the cross-language doc's re-entrancy rules apply.

A handled resource request uses a Go object that owns the provider's
reference to the C request handle. It enforces one-shot completion
and exactly-once release. Completion and cancellation checks may
run from any goroutine when the C API allows it.

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

```go
package main

import (
    "fmt"
    "log"
    "runtime"

    maplibre "github.com/maplibre/maplibre-native-go"
)

func main() {
    // The binding does not marshal calls. Pin this goroutine to the
    // OS thread that becomes the runtime / map / render-session
    // owner thread.
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()

    rt, err := maplibre.NewRuntime(maplibre.RuntimeOptions{})
    if err != nil { log.Fatal(err) }
    defer rt.Close()

    m, err := rt.NewMap(maplibre.MapOptions{Width: 256, Height: 256})
    if err != nil { log.Fatal(err) }
    defer m.Close()

    if err := m.SetStyleJSON(`{"version":8,"sources":{},"layers":[]}`); err != nil {
        log.Fatal(err)
    }

    sess, err := m.AttachOwnedTexture(256, 256, 1.0)
    if err != nil { log.Fatal(err) }
    defer sess.Close()

    // Static-render flow: request, pump run_once + drain events,
    // readback. Caller owns the loop in the low-level binding.
    if err := m.RequestStillImage(); err != nil { log.Fatal(err) }
    for {
        if err := rt.RunOnce(); err != nil { log.Fatal(err) }
        ev, ok, err := rt.PollEvent()
        if err != nil { log.Fatal(err) }
        if !ok { continue }
        if ev.Source == m && ev.Type == maplibre.EventStillImageFinished { break }
        if ev.Type == maplibre.EventStillImageFailed ||
           ev.Type == maplibre.EventMapLoadingFailed {
            log.Fatalf("render failed: %v", ev)
        }
    }

    rgba, info, err := sess.ReadPremultipliedRGBA8()
    if err != nil { log.Fatal(err) }
    fmt.Printf("rendered %dx%d (%d bytes)\n", info.Width, info.Height, len(rgba))
}
```

Adapter packages above the binding can wrap this loop in an
ergonomic API with `context.Context` cancellation, an internal
dispatcher goroutine, and concurrent rendering across multiple
runtimes. Such adapters are out of scope for the low-level binding.

## Testing

`go test -race ./...` runs the test suite against a real built
`libmaplibre-native-c`. The binding does not mock the C ABI.

Tests cover the language-adaptation invariants: idempotent `Close`,
`errors.Is` sentinel matching, diagnostic capture under goroutine
migration scenarios, callback panic recovery, and one-shot resource
request completion. C ABI tests cover native behaviour;
binding-level tests prove the Go layer preserves it.
````
