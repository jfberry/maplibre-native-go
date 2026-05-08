---
title: Go Bindings
description: Design rules for safe low-level Go bindings.
sidebar:
  order: 4
---

## Scope

The Go binding is a safe low-level binding over the public C API. It exposes
the C API's runtime, map, render session, event, callback, and render target
model with Go ownership, error, memory, and thread-safety conventions.

Higher-level Go integrations like tile servers, render workers, GPU
compositors, and SDL/Cocoa hosts should be able to build on this layer. Such
integrations will own request scheduling, output encoding, on-disk caches,
and application-level map objects while delegating native calls to this
binding.

The binding uses [cgo](https://pkg.go.dev/cmd/cgo) for the unsafe bridge.
It targets stable cgo on Go 1.22 or newer with explicit OS-thread pinning
and a small handwritten C translation unit for callback trampoline casts.

## Package And API Shape

The binding lives under one importable package, imported as `maplibre`:

```text
github.com/maplibre/maplibre-native-go
```

Long-lived native objects use plain Go type names without a `Handle` suffix.
Idiomatic Go does not need a suffix to mark closeable types.

```text
maplibre.Runtime
maplibre.Map
maplibre.RenderSession
maplibre.Projection
maplibre.Session
```

Go-owned values, descriptors, events, copied data, and one-shot snapshots
are plain structs:

```text
maplibre.Camera
maplibre.MapOptions
maplibre.AnimationOptions
maplibre.TextureImageInfo
maplibre.LatLng
maplibre.ScreenPoint
maplibre.Event
maplibre.RenderingStats
maplibre.JSONSnapshot
```

Keep public Go names close to the C concepts. Drop the `mln_` prefix and
strip `_options` / `_t` suffixes where Go readability benefits.
`MLN_RUNTIME_EVENT_MAP_STYLE_LOADED` becomes `EventStyleLoaded`.
`mln_camera_options` becomes `Camera`.

## Binding Layers

The binding ships as a single Go package. Internal organisation:

```text
cgo.go               package-level pkg-config + cgo CFLAGS / LDFLAGS
trampolines.c        C bridge between //export'd Go functions and the C
                     ABI's typed callback function pointers
constants_gen.go     auto-generated typed constants for every C enum
errors.go            Status, *Error, sentinels
runtime.go           Runtime + dispatcher
map.go               Map + lifecycle + style loaders + render-still flow
texture.go           RenderSession + native readback
texture_metal_darwin.go    backend-specific attach
texture_vulkan_linux.go    backend-specific attach
session.go           high-level bundle (Runtime + Map + RenderSession)
projection.go        projection helpers
events.go            Event + payload decode
log.go               process-global log callback
resource.go          URL transform + resource provider callbacks
```

Constants are generated from the C headers. A small generator extracts
every `MLN_*` enum value and emits typed Go constants. Generator output
is committed to the repository and regenerated when upstream pulls.
Treat successful generation as the header parsability check.

The rest of the binding is handwritten. Public types and methods do not
expose `unsafe.Pointer`, `*C.foo`, or any other cgo type to consumers
except where the C API itself requires a borrowed backend-native handle.

## Go Version And Build

Target Go 1.22 or newer. The binding uses `runtime/cgo.Handle` (Go 1.17+),
typed-cgo casts, multi-`%w` error wrapping (Go 1.20+), and `errors.Join`.

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

Build tags:

```text
mln_debug    installs leak-detection finalizers on every owned handle.
             Off by default. Used during development; not for release.
```

Platform-specific files use `//go:build darwin` and `//go:build linux`
for backend-specific render-target attach (Metal on darwin, Vulkan on
linux).

The package compiles cleanly with `-race`. The binding's design — every
shared cgo pointer touched only inside the dispatcher closure — holds
against `-race` and `checkptr`.

## Status And Diagnostics

Status-returning C calls become Go methods that return `error`. A non-OK
status produces an `*Error`:

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
    Op      string  // tag of failing operation, e.g. "Map.SetStyleJSON"
    Message string  // mbgl thread-local diagnostic captured at failure site
}

func (e *Error) Error() string { /* "Op: STATUS_NAME: message" */ }
func (e *Error) Is(target error) bool { /* matches by Status */ }
```

The binding ships sentinel `*Error` values for `errors.Is` matching:

```go
var (
    ErrInvalidArgument = &Error{Status: StatusInvalidArgument}
    ErrInvalidState    = &Error{Status: StatusInvalidState}
    ErrWrongThread     = &Error{Status: StatusWrongThread}
    ErrUnsupported     = &Error{Status: StatusUnsupported}
    ErrNativeError     = &Error{Status: StatusNativeError}
    ErrTimeout         = errors.New("maplibre: timeout waiting for runtime event")
)

if errors.Is(err, maplibre.ErrInvalidState) { ... }
```

The binding reads the C thread-local diagnostic inside the dispatcher
closure that produced the failing status, before the goroutine yields:

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

Closed handles produce `StatusInvalidState`. The resource was valid; the
call is invalid given the resource's state. A small `errClosed(op, what
string) error` helper produces these consistently:

```go
func errClosed(op, what string) error {
    return &Error{Status: StatusInvalidState, Op: op,
                  Message: what + " is closed"}
}
```

The Go layer checks Go-owned state such as nil receivers, closed
wrappers, and active callback-scoped borrows. The C API validates
native arguments and native state.

## Owned Handles And Lifecycle

Every long-lived C-owned opaque handle maps to a Go struct with a
`Close() error` method.

A handle stores:

- the native pointer
- parent handles needed for native validity
- open or closed state
- optional debug leak context

```go
type Runtime struct { /* dispatcher + ptr */ }
func NewRuntime(opts RuntimeOptions) (*Runtime, error)
func (r *Runtime) Close() error

type Map struct { /* parent runtime + ptr */ }
func (r *Runtime) NewMap(opts MapOptions) (*Map, error)
func (m *Map) Close() error
```

Conventions:

`Close()` is idempotent. Calling on a nil receiver returns nil. Calling
twice returns nil the second time.

Parents stay reachable. A `Map` retains a `*Runtime` field; a
`RenderSession` retains a `*Map`. Garbage collecting a parent before
its children is impossible by reachability.

Closing a parent before children is an error. `Runtime.Close()` returns
`ErrInvalidState` when live maps remain. The binding does not cascade-
close children — the caller is responsible for the close order. The
recommended idiom is `defer x.Close()` in reverse order of creation:

```go
rt, _ := maplibre.NewRuntime(maplibre.RuntimeOptions{})
defer rt.Close()

m, _ := rt.NewMap(maplibre.MapOptions{Width: 256, Height: 256})
defer m.Close()

sess, _ := m.AttachTexture(256, 256, 1)
defer sess.Close()
```

`Close()` is callable from any goroutine. The destroy call routes through
the dispatcher; goroutine-of-call doesn't matter to correctness.

`Close()` is the single lifecycle operation. The binding does not
expose `IsClosed()`, lifecycle-observation methods, `Close(ctx)`
variants, or any other way to inspect or vary the destroy path.
Callers detect closure by the error returned from the next operation
on a closed handle (`errors.Is(err, ErrInvalidState)`).

Under the `mln_debug` build tag, the binding installs
`runtime.SetFinalizer` on each handle that prints to stderr if the
handle is garbage-collected with the underlying native handle still
live. The finalizer reports the leak only; it does not destroy
thread-affine native handles. The finalizer is a no-op in release
builds.

## OS Thread Pinning And Dispatcher

Goroutines migrate between OS threads at the Go scheduler's discretion.
The C API requires owner-thread affinity for runtime, map, projection,
and render-session calls; a non-owner thread call returns
`MLN_STATUS_WRONG_THREAD`. Every cgo call from an unpinned goroutine is
potentially on a different OS thread than the previous call.

Every `Runtime` owns one OS thread via a dedicated dispatcher goroutine:

```go
type Runtime struct {
    d   *dispatcher  // owns one OS thread via runtime.LockOSThread
    ptr *C.mln_runtime
}

func (r *Runtime) runOnOwner(op string, fn func() error) error {
    // dispatch fn onto the runtime's OS thread; block until fn returns.
}
```

The dispatcher loops on a command channel, calling each command
synchronously on its locked OS thread:

```go
func (d *dispatcher) loop() {
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()
    for {
        select {
        case fn := <-d.cmds: fn()
        case <-d.quit: return
        }
    }
}
```

Every Map, RenderSession, and Projection method routes through its
parent Runtime's dispatcher. Callers may use any handle from any
goroutine — the dispatcher serializes calls onto the right OS thread.

Calls that the C API documents as not owner-thread-affine bypass the
dispatcher and call C directly from any goroutine:

```text
mln_log_set_callback / mln_log_clear_callback / mln_log_set_async_severity_mask
mln_network_status_get / mln_network_status_set
mln_projected_meters_for_lat_lng / mln_lat_lng_for_projected_meters
mln_resource_request_complete / mln_resource_request_cancelled / mln_resource_request_release
```

The Go layer does not duplicate owner-thread validation for ordinary
calls. Native `MLN_STATUS_WRONG_THREAD` results become `*Error` values
with `Status == StatusWrongThread`. Go type boundaries align with C
owner concepts:

```text
Runtime         runtime owner thread in C
Map             map owner thread in C
RenderSession   session owner thread in C
Projection      projection owner thread in C
```

Render session owner threads currently match map owner threads.
This leaves room for a future C API that exposes render sessions
owned by a render thread distinct from the runtime owner thread. The
Go binding would create a separate dispatcher for such sessions
when the C API supports it. If Go needs to inspect owner threads
directly, add a C getter.

A single dispatcher op may make multiple C calls to amortize the
goroutine-to-OS-thread hop cost. Wait operations are structured so
that one dispatcher op pumps `mln_runtime_run_once`, drains the
runtime's event queue, and services any render-update events
inline. The binding pumps `run_once` at the dispatcher hop cadence
during active waits so that event delivery is bounded by mbgl's own
work, not by the binding's poll loop.

The wait loop returns to Go-side control between dispatcher ops to
honour `ctx` cancellation. When the event queue drains without a
match, the loop yields; when the queue is producing events, the
loop re-enters the dispatcher immediately.

## Context And Cancellation

Every binding operation that waits on a C-side event takes a
`context.Context` as its first parameter, following Go convention.
Operations that issue one C call and return synchronously do not take
a context; the cgo call is bounded by mbgl's own owner-thread cost.

The binding does not expose deadline parameters (`time.Duration`),
cancellation channels (`<-chan struct{}`), or polling-loop primitives
in the public API. `context.Context` is the single cancellation
primitive.

```go
func (r *Runtime) WaitForEvent(ctx context.Context, match func(Event) bool) (Event, error)
func (m *Map) WaitForEvent(ctx context.Context, match func(Event) bool) (Event, error)
func (m *Map) RenderStill(ctx context.Context, sess *RenderSession) (TextureFrame, error)
func (m *Map) RenderImage(ctx context.Context, sess *RenderSession) (rgba []byte, w, h int, err error)
func (m *Map) RenderImageInto(ctx context.Context, sess *RenderSession, dst []byte) (w, h int, err error)
func (s *Session) Render(ctx context.Context) (rgba []byte, w, h int, err error)
func NewSession(ctx context.Context, opts SessionOptions) (*Session, error)
```

When `ctx` is cancelled or its deadline passes during a wait, the
operation returns:

```go
fmt.Errorf("%w: %w", ErrTimeout, ctx.Err())
```

Callers can match either `errors.Is(err, ErrTimeout)` or
`errors.Is(err, context.DeadlineExceeded)`.

## Options And Transparent Structs

C option structs are Go structs with public fields. The zero value
plus literal construction is the only construction pattern. The
binding does not expose:

- builder methods (`opts.WithWidth(512)`, `opts.WithHeight(512)`)
- setter methods (`opts.SetWidth(512)`)
- immutable `with...` constructors (`opts.WithMode(MapModeStatic)`)
- `New<Type>(...)` constructors that just fill fields a literal could
  set directly

Plain Go structs expose their exported fields. The binding does not
add `GetWidth()` / `Width()` accessor methods on plain structs. Methods
are reserved for behaviour that requires native dispatch.

```go
opts := maplibre.MapOptions{
    Width:       512,
    Height:      512,
    ScaleFactor: 1.0,
    Mode:        maplibre.MapModeStatic,
}
m, err := rt.NewMap(opts)
```

Field-mask structs use a typed bitmask field plus explicit fields:

```go
type Camera struct {
    Fields    CameraField
    Latitude  float64
    Longitude float64
    Zoom      float64
    Bearing   float64
    Pitch     float64
}

cam := maplibre.Camera{
    Fields:    maplibre.CameraFieldCenter | maplibre.CameraFieldZoom,
    Latitude:  55.07,
    Longitude: -3.58,
    Zoom:      8,
}
m.JumpTo(cam)
```

The binding fills the C struct's `size` field internally inside the
dispatcher closure that consumes the Go struct. Callers do not see or
set ABI bookkeeping fields.

Useful zero values are required. `MapOptions{}` produces a usable
static-mode empty map: `ScaleFactor: 0` is treated as `1.0`. Each
options type's zero-value behavior is documented on the type.

C `*Default()` initializers are not exposed to Go callers. The binding
uses them internally to seed C struct allocation; callers see Go
constructors and Go zero values.

## Native Memory

cgo manages the Go↔C memory boundary. Conventions:

Go strings to C strings:

```go
cs := C.CString(s)
defer C.free(unsafe.Pointer(cs))
```

Allocated and freed inside the dispatcher closure that consumes the
string. The binding never holds a `*C.char` past the C call.

Go byte slices to C buffers: passed directly as
`(*C.uint8_t)(unsafe.Pointer(&b[0]))` plus length. Cgo pins the slice
for the duration of the C call. The binding does not retain
Go-allocated buffers across calls.

C-allocated buffers: `C.malloc` plus matching `C.free`. Used for
callback `user_data` cells and any long-lived C-side string the
binding owns.

Borrowed C strings are copied to Go via `C.GoStringN(ptr, len)`
immediately on receipt. The binding never returns a borrowed C string
to a caller.

Buffers reused across renders use a caller-owned Go slice passed via
`RenderImageInto(ctx, sess, dst)`. The binding writes into `dst`
inside the dispatcher and returns. No cgo allocation per render.

## Native Pointers

Go's `unsafe.Pointer` is the public Go type for backend-native handles
the binding does not own. It is a value, not a memory view.

`unsafe.Pointer` appears in public types only where the C API itself
exposes a borrowed backend-native handle. Field names of borrowed
backend handles use the `Unsafe` suffix to mark the caller-managed
lifetime:

```go
type TextureFrame struct {
    Generation      uint64
    Width           uint32
    Height          uint32
    ScaleFactor     float64
    FrameID         uint64
    TextureUnsafe   unsafe.Pointer  // Metal: id<MTLTexture>; Vulkan: VkImage
    ImageViewUnsafe unsafe.Pointer  // Vulkan only: VkImageView; nil on Metal
    DeviceUnsafe    unsafe.Pointer  // Metal: id<MTLDevice>; Vulkan: VkDevice
    PixelFormat     uint64
    Layout          uint32
}
```

Public methods that accept an `unsafe.Pointer` as a backend handle live
in platform-tagged build files and also use the `Unsafe` suffix on the
parameter or function name where appropriate:

```go
//go:build darwin

func (m *Map) AttachMetalTextureWithDevice(deviceUnsafe unsafe.Pointer,
    width, height uint32, scale float64) (*RenderSession, error)
```

The binding never accepts `unsafe.Pointer` for Go-managed memory.
Slices and strings flow through their typed Go signatures.

## Callback-Scoped Borrows

When the C API exposes a borrowed handle valid only between an acquire
and a release, the binding's idiom is acquire plus `defer` release:

```go
frame, err := m.RenderStill(ctx, sess)
if err != nil { return err }
defer sess.ReleaseFrame(frame)

// frame.TextureUnsafe, frame.DeviceUnsafe, etc. are valid until
// ReleaseFrame returns.
```

The binding does not expose callback-scoped (`WithFrame(fn)`,
`Use(fn)`, etc.) accessors for borrowed handles. Go's `defer` already
gives the caller a tight, panic-safe scope without an additional
function-call layer; introducing a parallel callback-style API for
the same lifetime would split the binding's idiom and confuse
consumers about which to use.

Acquire and release order is enforced by the C ABI (mbgl rejects
nested acquires, render updates, resize, detach, and destroy while a
frame is acquired). The binding does not duplicate that validation in
Go.

## Borrowed Data

Borrowed C data becomes copied Go data unless it is exposed through an
acquire plus release scope.

`PollEvent()` copies the event message and decodes typed payloads
inside the dispatcher closure, before the next poll invalidates the
C-side storage:

```go
func (r *Runtime) pollEventLocked() (Event, bool, error) {
    var cev C.mln_runtime_event
    cev.size = C.uint32_t(unsafe.Sizeof(cev))
    var has C.bool
    if status := C.mln_runtime_poll_event(r.ptr, &cev, &has); status != C.MLN_STATUS_OK {
        return Event{}, false, statusError("mln_runtime_poll_event", status)
    }
    if !bool(has) {
        return Event{}, false, nil
    }
    var ev Event
    ev.Type = EventType(cev._type)
    ev.Code = int32(cev.code)
    if cev.message != nil && cev.message_size > 0 {
        ev.Message = C.GoStringN((*C.char)(unsafe.Pointer(cev.message)),
                                 C.int(cev.message_size))
    }
    ev.Payload = decodePayload(uint32(cev.payload_type),
                               unsafe.Pointer(cev.payload), cev.payload_size)
    return ev, true, nil
}
```

Snapshot objects own native snapshot storage and have a matching
`Close() error`. Reads from snapshots return copied Go values; the
binding does not expose free-floating views into C-owned snapshot
memory.

## Events

Polling returns copied Go values. The polling signature follows Go's
`(value, ok, error)` convention — `ok == false` means no event was
available, `err != nil` means the call itself failed:

```go
type Event struct {
    Type    EventType
    Code    int32
    Source  *Map     // nil for runtime-source events
    Message string
    Payload Payload  // typed, see below
}

func (r *Runtime) PollEvent() (Event, bool, error)
func (r *Runtime) WaitForEvent(ctx context.Context, match func(Event) bool) (Event, error)
func (m *Map) WaitForEvent(ctx context.Context, match func(Event) bool) (Event, error)
```

The `Source *Map` field is resolved by the binding from a registry of
live `*Map` wrappers keyed by C handle. The registry is dispatcher-
local — both registration (`NewMap`) and lookup (`PollEvent`) run on
the dispatcher thread, so no lock is required.

A drain helper consumes every queued event with the same copy
semantics as `PollEvent`:

```go
func (r *Runtime) DrainEvents(consumer func(Event) error) error
```

The binding ships convenience predicates:

```go
func EventOfType(t EventType) func(Event) bool
func EventOfTypes(ts ...EventType) func(Event) bool

ev, err := m.WaitForEvent(ctx, maplibre.EventOfType(maplibre.EventStyleLoaded))
```

`WaitForEvent`'s `match` predicate runs inside the dispatcher in the
batched-poll variant. The predicate must be a pure function: calling
back into any binding method from `match` deadlocks the dispatcher.

When a map-originated event's source pointer does not resolve to a
live `*Map` wrapper (e.g. the parent `Map` was closed concurrently),
`Event.Source` is nil and the event still carries copied source kind
and native identity metadata for diagnostics.

The low-level binding preserves event names and payload categories
close to the C API. Translating events into channels, listeners, or
UI state belongs to adapters above this layer.

The C runtime event includes a typed payload. The binding decodes
the payload into a Go-typed `Payload` interface:

```go
type Payload interface { payloadType() EventPayloadType }

type RenderFramePayload struct {
    Mode             RenderMode
    NeedsRepaint     bool
    PlacementChanged bool
    Stats            RenderingStats
}

type RenderMapPayload struct {
    Mode RenderMode
}

type StyleImageMissingPayload struct {
    ImageID string
}

type TileActionPayload struct {
    Operation TileOperation
    Tile      TileID
    SourceID  string
}
```

A type switch on `ev.Payload` recovers the specific payload:

```go
switch p := ev.Payload.(type) {
case *RenderFramePayload:
    log.Printf("frame: %v", p.Stats.RenderingTime)
case *TileActionPayload:
    log.Printf("tile %s op=%s", p.Tile, p.Operation)
}
```

Decoding happens inside the dispatcher closure that polled the event;
borrowed C memory is copied before the next poll.

## Native Callbacks

The binding installs callbacks via `//export`'d Go functions that match
the C ABI signature, plus a small `trampolines.c` translation unit that
exposes typed function pointers to the C ABI:

```c
// trampolines.c — compiled with the binding
#include "maplibre_native_c.h"
#include "_cgo_export.h"

mln_log_callback mlnGoGetLogTrampoline(void) {
    return (mln_log_callback)mlnGoLogTrampoline;
}

mln_resource_transform_callback mlnGoGetTransformTrampoline(void) {
    return (mln_resource_transform_callback)mlnGoResourceTransformTrampoline;
}
```

User data passes through cgo as C-allocated storage containing a
`runtime/cgo.Handle` value. The C-allocated storage gives the
binding a stable native address to use as `user_data`; the handle
keeps the Go callback closure reachable across the cgo boundary.
The trampoline reads the handle from the storage and dispatches to
the registered Go function:

```go
// C calls into the //export'd Go trampoline.
//export mlnGoResourceTransformTrampoline
func mlnGoResourceTransformTrampoline(userData unsafe.Pointer, kind C.uint32_t,
    url *C.char, out *C.mln_resource_transform_response) C.mln_status {
    h := cgo.Handle(*(*uintptr)(userData))
    cb := h.Value().(URLTransform)
    // invoke cb, set out->url, return status
}
```

The `Runtime` owns the storage and the handle for the runtime's
lifetime. Both are freed during `Runtime.Close()` after the C side
is destroyed.

A single Go-registered log callback is process-global, matching the C
API. Resource transform and provider callbacks are runtime-scoped.

Threading and re-entrancy rules carry forward from the C API into the
Go callback documentation. Resource provider and resource transform
callbacks may run on worker or network threads. Implementations must
not call back into any binding method; the dispatcher's command
channel is single-buffered and a re-entrant call deadlocks. This
restriction is documented on every public callback type.

Callbacks catch Go panics at the trampoline boundary, log to stderr,
and return the appropriate "no rewrite" or OK status. Panics never
unwind through cgo.

Borrowed request fields (URL bytes, headers, prior data) are copied
to Go values before the Go callback method returns when the binding
needs them later. The C ABI's borrowed pointers do not escape the
callback scope.

A handled resource request uses a Go object that owns the provider's
reference to the C request handle. It enforces one-shot completion
and exactly-once release. Completion and cancellation checks may run
from any goroutine when the C API allows it.

## Render Sessions And Render Targets

`RenderSession` represents one attached render target for one map. The
session owner thread is the map owner thread.

The binding exposes three attach paths. Backend-agnostic attach
supports the readback path but not backend-specific frame access:

```go
// Backend-agnostic; uses the platform-default backend; supports the
// readback path but NOT backend-specific AcquireFrame.
func (m *Map) AttachTexture(width, height uint32, scale float64) (*RenderSession, error)
```

Backend-specific attach supports both readback and `AcquireFrame`:

```go
//go:build darwin

func (m *Map) AttachMetalTexture(width, height uint32, scale float64) (*RenderSession, error)
func (m *Map) AttachMetalTextureWithDevice(device unsafe.Pointer,
    width, height uint32, scale float64) (*RenderSession, error)
```

```go
//go:build linux

type VulkanContext struct {
    Instance            unsafe.Pointer
    PhysicalDevice      unsafe.Pointer
    Device              unsafe.Pointer
    GraphicsQueue       unsafe.Pointer
    GraphicsQueueFamily uint32
}

func (m *Map) AttachVulkanTexture(width, height uint32, scale float64) (*RenderSession, error)
func (m *Map) AttachVulkanTextureWithContext(ctx VulkanContext,
    width, height uint32, scale float64) (*RenderSession, error)
```

The render flow uses native readback when GPU handles are not needed.
Readback paths return a `TextureImageInfo` with copied metadata so
callers can read width, height, stride, and total byte length without
crossing cgo again:

```go
type TextureImageInfo struct {
    Width      int  // physical pixels
    Height     int  // physical pixels
    Stride     int  // bytes per row
    ByteLength int  // total buffer size
}

// One call: request still image, pump events until STILL_IMAGE_FINISHED,
// native readback into a fresh Go slice.
rgba, info, err := m.RenderImage(ctx, sess)

// Same with caller-supplied buffer for reuse across renders.
info, err := m.RenderImageInto(ctx, sess, dst)

// GPU-handle path: attach with a backend-specific function, render, take
// the borrowed handle, release explicitly.
frame, err := m.RenderStill(ctx, sess)
defer sess.ReleaseFrame(frame)
```

Surface descriptors and caller-owned texture descriptors contain
backend-native handles. The binding treats those handles as borrowed.
The caller keeps backend objects valid and synchronized for the
lifetime documented by the C API.

## Unsafe Escape Hatches

Backend interop requires raw native handles in specific render-target
APIs. Unsafe accessors are limited to those APIs and use an `Unsafe`
suffix on the field or parameter name to mark caller-managed
lifetime.

```go
type TextureFrame struct {
    TextureUnsafe   unsafe.Pointer  // Metal: id<MTLTexture>; Vulkan: VkImage
    DeviceUnsafe    unsafe.Pointer  // Metal: id<MTLDevice>; Vulkan: VkDevice
    ImageViewUnsafe unsafe.Pointer  // Vulkan only: VkImageView
}
```

Unsafe field documentation states the scope in which the returned
native handle is valid (e.g. "valid until ReleaseFrame returns") and
which backend-native type the pointer represents. Unsafe accessors
do not transfer ownership.

The binding does not expose `*C.foo` types. `unsafe.Pointer` is the
binding's wire format for opaque handles in public API surfaces.

## Constants And Enums

Every C enum becomes a typed Go alias of `uint32` or `int32` with a
`const` block of typed values:

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

func (s Status) String() string {
    switch s {
    case StatusOK:              return "OK"
    case StatusInvalidArgument: return "INVALID_ARGUMENT"
    // ...
    }
    return fmt.Sprintf("UNKNOWN(%d)", int32(s))
}
```

Bitmask flags are typed bitmask values. The binding writes shifts
without untyped intermediates:

```go
type CameraField uint32

const (
    CameraFieldCenter  CameraField = 1 << 0
    CameraFieldZoom    CameraField = 1 << 1
    CameraFieldBearing CameraField = 1 << 2
    CameraFieldPitch   CameraField = 1 << 3
)
```

Output values that may grow across C API versions use stable unknown-
value representations. `String()` methods return `UNKNOWN(<value>)`
for unrecognized values rather than panicking.

## Testing

Test the Go adaptation layer rather than duplicating the full C ABI
test suite. C ABI tests prove native behavior. Go tests prove that
Go ownership, copying, callbacks, and errors preserve that behavior at
the cgo boundary.

`go test ./...` runs against a real built `libmaplibre-native-c`. Tests
do not mock the C ABI. The full test suite runs with `-race`.

Coverage targets:

```text
Smoke           Runtime / Map / Session create-render-close
Lifecycle       Idempotent Close, nil-receiver safety, parent-with-live-
                children rejection, double-close
Errors          errors.Is sentinel matching, errors.As to *Error
Cancellation    context.WithTimeout produces ErrTimeout +
                context.DeadlineExceeded from waits
Threading       Concurrent use of one Session from multiple goroutines
                serializes correctly through the dispatcher
Payload decode  Every event payload type round-trips
Callbacks       Log callback receives records under both synchronous
                and async masks; URL transform invoked with right kind/url
```

Add regression tests when the Go layer owns a lifetime or threading
invariant that the C API cannot express on its own, such as releasing
a texture frame after a panicking callback, preserving parent handles
while child handles are reachable, or recovering panics inside an
upcall trampoline without unwinding through cgo.
