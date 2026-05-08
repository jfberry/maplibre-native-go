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

Higher-level Go integrations (tile servers, render workers, GPU compositors,
SDL/Cocoa hosts) should be able to build on this layer. Such integrations will
own request scheduling, output encoding, on-disk caches, and application-level
map objects while delegating native calls to this binding.

The binding uses [cgo](https://pkg.go.dev/cmd/cgo) for the unsafe bridge.
It targets a stable, well-tested cgo build with explicit OS-thread pinning,
no FFI auto-generators, and a small handwritten codegen step for the
constants table only. The binding does not expose `unsafe.Pointer`,
`*C.foo`, or any other cgo type to consumers except where the C API itself
requires a borrowed backend-native handle (Metal device, Vulkan image, etc.).

## Package And API Shape

The binding lives under one importable package. The recommended public path is:

```text
github.com/maplibre/maplibre-native-go
```

Sub-packages may exist for examples, but the public API is one package
imported as `maplibre`. Idiomatic Go does not use a `Handle` suffix; the type
itself communicates its role.

```text
maplibre.Runtime         long-lived, dispatched, Closeable
maplibre.Map             long-lived, dispatched, Closeable
maplibre.RenderSession   long-lived, dispatched, Closeable
maplibre.Projection      long-lived, dispatched, Closeable
maplibre.Session         high-level bundle (Runtime + Map + RenderSession)
```

Java-owned values, descriptors, events, copied data, and one-shot snapshots
are plain structs with no `*Handle` suffix:

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
strip trailing `_options` / `_t` suffixes where Go readability benefits
(`MLN_RUNTIME_EVENT_MAP_STYLE_LOADED` becomes `EventStyleLoaded`).

## Binding Layers

The binding is layered, but the layers all live in the same Go package — Go
does not need separate internal packages for cgo isolation, and the public
surface is small enough that a single package keeps the API navigable.

```text
cgo.go              package-level CFLAGS / LDFLAGS / pkg-config wiring
trampolines.c       small C bridge for //export'd Go callbacks (see "Native Callbacks")
constants_gen.go    auto-generated typed constants for every C enum
errors.go           Status, *Error, sentinels
runtime.go          Runtime + dispatcher
map.go              Map + lifecycle + style loaders + render-still flow
texture.go          RenderSession + readback
texture_metal_darwin.go    backend-specific attach (build-tagged)
texture_vulkan_linux.go    backend-specific attach (build-tagged)
session.go          high-level bundle
projection.go       projection helpers
events.go           Event + payload decode
log.go              process-global log callback
resource.go         URL transform + resource provider callbacks
```

`internal/` sub-packages are not required and are avoided for ergonomic
imports.

The binding is **handwritten** following this spec. Two narrow exceptions:

1. **Constants are auto-generated** from the C headers. A small generator
   (Go source-based or shell-based) extracts `MLN_*` enum values and emits
   typed Go constants. Regenerate when upstream pulls. See "Codegen Scope".

2. **Callback trampoline cast helpers** live in `trampolines.c` (a C
   translation unit compiled alongside the cgo code) so the `//export`'d Go
   functions can be referenced as typed C function pointers without
   cgo's auto-generated `_cgo_export.h` declarations colliding with
   per-file preamble re-declarations.

Auto-generating the whole binding (e.g. with c-for-go) is **not the
recommended path**. The C ABI's combination of `**T` out-pointers,
struct-cache wrapper semantics, callback trampoline requirements, modern-Go
strict typing on bitmask flag enums, and goroutine-vs-OS-thread mismatch
produce auto-gen output that is strictly more dangerous than the C ABI
itself for naive Go consumers. Handwritten is the path. See `RECOMMENDATIONS.md`
for header-shape changes that would make auto-gen more viable in the future.

## Go Version

Target Go 1.22 or newer.

The binding uses `runtime/cgo.Handle` (Go 1.17+), `runtime.Pinner` for any
explicit pin operations, and `errors.Join`/multi-`%w` formatting (Go 1.20+).

Explicit minimum is enforced via `go.mod`'s `go` directive.

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
    Message string  // mbgl thread-local diagnostic, captured at failure site
}

func (e *Error) Error() string  { /* "Op: STATUS_NAME: message" */ }
func (e *Error) Is(target error) bool  { /* matches by Status */ }
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

Reading the C thread-local diagnostic is a **dispatcher-discipline
requirement** unique to Go. Goroutines migrate between OS threads, so the
diagnostic must be read on the same goroutine that produced the failing
status, before the goroutine yields. The binding reads it inside the
dispatcher closure that produced the status:

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

Closed handles produce `StatusInvalidState` (not `StatusInvalidArgument`):
the resource was valid; the call is invalid given the resource's state.
A small `errClosed(op, what string) error` helper produces these
consistently.

## Owned Handles And Lifecycle

Every long-lived C-owned opaque handle maps to a Go struct with a
`Close() error` method.

```go
type Runtime struct { /* dispatcher + ptr */ }
func NewRuntime(opts RuntimeOptions) (*Runtime, error)
func (r *Runtime) Close() error  // idempotent, nil-receiver safe

type Map struct { /* parent runtime + ptr */ }
func (r *Runtime) NewMap(opts MapOptions) (*Map, error)
func (m *Map) Close() error
```

Conventions:

- **`Close()` is idempotent.** Calling on a nil receiver returns nil.
  Calling twice returns nil the second time.
- **Parents stay reachable.** A `Map` retains a `*Runtime` field; a
  `RenderSession` retains a `*Map`. Garbage collecting a parent before its
  children is impossible by reachability.
- **Closing a parent before children is an error.** `Runtime.Close()`
  returns `ErrInvalidState` when live maps remain. The binding does not
  cascade-close children — the caller is responsible for the close order.
  The recommended idiom is `defer x.Close()` in reverse order of creation.
- **`Close()` from any goroutine.** The destroy call routes through the
  dispatcher; goroutine-of-call doesn't matter to correctness.
- **Optional debug leak tracker.** Under the `mln_debug` build tag, the
  binding installs `runtime.SetFinalizer` on each handle that prints to
  stderr if the handle is garbage-collected with the underlying native
  handle still live. No-op in release builds.

## OS Thread Pinning And Dispatcher

This is the central design constraint of a Go binding for maplibre-native-c.

The C API requires owner-thread affinity for runtime / map / projection /
render-session calls. A non-owner thread call returns
`MLN_STATUS_WRONG_THREAD`. Goroutines, by default, **migrate between OS
threads** at the Go scheduler's discretion — every cgo call from an
unpinned goroutine is potentially on a different OS thread than the
previous call.

The binding solves this by giving every `Runtime` a dedicated OS thread:

```go
type Runtime struct {
    d   *dispatcher  // owns one OS thread via runtime.LockOSThread
    ptr *C.mln_runtime
    // ...
}

func (r *Runtime) runOnOwner(op string, fn func() error) error {
    // dispatch fn onto the runtime's OS thread; block until fn returns.
}
```

The dispatcher is a goroutine that calls `runtime.LockOSThread()` and then
loops on a command channel:

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

Every Map, RenderSession, and Projection call routes through its parent
Runtime's dispatcher. **Callers may use any handle from any goroutine** —
the dispatcher serializes everything onto the right OS thread.

Calls that are **not owner-thread-affine** in the C API (the header
explicitly documents which ones — see `mln_log_*`, `mln_network_status_*`,
`mln_projected_meters_*`, `mln_resource_request_*`) bypass the dispatcher
and call C directly from any goroutine. The binding follows the C API's
threading documentation precisely.

A single dispatcher op may make **multiple C calls** to amortize the
goroutine→OS-thread hop cost. The render-still inner loop, for example,
makes one dispatcher op per iteration that calls
`mln_runtime_run_once` + `mln_runtime_poll_event` (drained until empty)
+ `mln_render_session_render_update` (inline, when a RUA event is drained).
See "Hot-Path Pumping".

## Cancellation And Context

Every binding operation that waits on a C-side event takes a
`context.Context` as its first argument:

```go
func (m *Map) WaitForEvent(ctx context.Context, match func(Event) bool) (Event, error)
func (m *Map) RenderStill(ctx context.Context, sess *RenderSession) (TextureFrame, error)
func (m *Map) RenderImage(ctx context.Context, sess *RenderSession) (rgba []byte, w, h int, err error)
func (s *Session) Render(ctx context.Context) (rgba []byte, w, h int, err error)
```

When `ctx` is canceled or its deadline passes during a wait, the operation
returns:

```go
fmt.Errorf("%w: %w", ErrTimeout, ctx.Err())
```

Callers can match either `errors.Is(err, ErrTimeout)` or
`errors.Is(err, context.DeadlineExceeded)`.

Operations that issue one C call and return synchronously (`Map.JumpTo`,
`Map.SetStyleURL`, `RenderSession.Resize`) **do not take a context**.
The cgo call is bounded by mbgl's own owner-thread cost.

## Options And Transparent Structs

C option structs are Go structs with public fields. **Builder methods are
not idiomatic Go**; the zero value plus literal construction is the
expected pattern:

```go
opts := maplibre.MapOptions{
    Width:       512,
    Height:      512,
    ScaleFactor: 1.0,
    Mode:        maplibre.MapModeStatic,
}
m, err := rt.NewMap(opts)
```

Field-mask structs (Camera, ProjectionMode) use a typed bitmask field plus
explicit fields:

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
dispatcher closure that consumes the Go struct. **Callers never see or
set ABI bookkeeping fields.**

Useful zero values: `MapOptions{}` should produce a usable static-mode
empty map (e.g. `ScaleFactor: 0` is treated as `1.0`). Document the
zero-value behavior on each options type.

`*Default()` C initializers are not exposed to Go callers. The binding
uses them internally to seed C struct allocation; callers see Go
constructors and Go zero values.

## Native Memory

cgo manages a stricter Go↔C memory model than Java FFM:

- **Go strings → C strings**: `cs := C.CString(s); defer C.free(unsafe.Pointer(cs))`
  inside the dispatcher closure. The binding never holds a `*C.char` past
  the C call that consumes it.

- **Go byte slices → C buffers**: passed directly as
  `(*C.uint8_t)(unsafe.Pointer(&b[0]))` plus length. Cgo pins the slice
  for the duration of the C call. The binding does not retain
  Go-allocated buffers across calls.

- **C-allocated buffers**: `C.malloc` + matching `C.free`. Used for
  callback `user_data` cells (see "Native Callbacks") and for
  long-lived strings the C side retains.

- **Borrowed C strings**: copied to Go via `C.GoStringN(ptr, len)`
  immediately on receipt. The binding never returns a borrowed C
  string to a caller. See "Borrowed Data".

- **`C.GoBytes` for C buffers** the binding wants to own as a Go slice.

Buffers reused across renders (RGBA readback) are caller-owned Go slices
passed via `RenderImageInto(dst []byte) (w, h int, err error)`. The
binding writes into `dst` inside the dispatcher and returns. No cgo
allocation per render.

## Native Pointers

Go's `unsafe.Pointer` is the public Go type for backend-native handles
that the binding does not own. The binding does not invent a
`NativePointer` wrapper because cgo already exposes `unsafe.Pointer`
with appropriate semantics: a value, not a memory view, no GC tracking.

`unsafe.Pointer` appears in public types only where the C API itself
exposes a borrowed backend-native handle:

```go
type TextureFrame struct {
    Generation  uint64
    Width       uint32
    Height      uint32
    ScaleFactor float64
    FrameID     uint64
    Texture     unsafe.Pointer  // Metal: id<MTLTexture>; Vulkan: VkImage
    ImageView   unsafe.Pointer  // Vulkan only: VkImageView; nil on Metal
    Device      unsafe.Pointer  // Metal: id<MTLDevice>; Vulkan: VkDevice
    PixelFormat uint64
    Layout      uint32
}
```

Pointer fields have backend documentation in field comments and the
type's package doc. Public methods that accept an `unsafe.Pointer` as a
backend handle (`Map.AttachMetalTextureWithDevice(device unsafe.Pointer, ...)`)
require platform-tagged build files (`_darwin.go`, `_linux.go`).

The binding never accepts `unsafe.Pointer` for Go-managed memory. Slices
and strings flow through their typed Go signatures.

## Callback-Scoped Borrows

When the C API exposes a borrowed handle valid only between an acquire
and a release, the binding's idiomatic shape is **acquire + defer
release**:

```go
frame, err := m.RenderStill(ctx, sess)
if err != nil { return err }
defer sess.ReleaseFrame(frame)

// frame.Texture, frame.Device etc. are valid until ReleaseFrame.
```

A callback-scoped form (`session.WithFrame(func(f Frame) error)` that
acquires before and releases after) is acceptable but not preferred.
Go's `defer` already gives the caller a tight, exception-safe scope; an
explicit callback form adds a function-call layer without a meaningful
safety win.

Acquire/release order is enforced by the C ABI (mbgl rejects nested
acquires, render updates, resize, detach, and destroy while a frame is
acquired). The binding does not duplicate that validation in Go.

## Borrowed Data

Borrowed C data becomes copied Go data unless it is exposed through an
acquire+release scope.

`PollEvent()` copies the event message and decodes typed payloads
**inside the dispatcher closure**, before the next poll invalidates the
C-side storage:

```go
func (r *Runtime) pollEventLocked() (Event, bool, error) {
    var cev C.mln_runtime_event
    cev.size = C.uint32_t(unsafe.Sizeof(cev))
    var has C.bool
    C.mln_runtime_poll_event(r.ptr, &cev, &has)
    // ...
    var ev Event
    ev.Message = C.GoStringN((*C.char)(unsafe.Pointer(cev.message)), C.int(cev.message_size))
    ev.Payload = decodePayload(uint32(cev.payload_type), unsafe.Pointer(cev.payload), cev.payload_size)
    return ev, true, nil
}
```

Snapshot objects (`*JSONSnapshot`, `*OfflineRegionSnapshot`) own native
storage and have a matching `Close() error`. Reads from snapshots return
copied Go values; the binding does not expose free-floating views into
C-owned snapshot memory.

## Events

Polling returns copied Go values:

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
live `*Map` wrappers keyed by their C handle. The registry is dispatcher-
local (no lock) since both registration (`NewMap`) and lookup
(`PollEvent`) run on the dispatcher thread.

Convenience predicates:

```go
func EventOfType(t EventType) func(Event) bool
func EventOfTypes(ts ...EventType) func(Event) bool

ev, err := m.WaitForEvent(ctx, maplibre.EventOfType(maplibre.EventStyleLoaded))
```

`WaitForEvent`'s `match` predicate **runs inside the dispatcher** in the
batched-poll variant (see "Hot-Path Pumping"). The function MUST be a
pure predicate: calling back into any binding method from `match`
deadlocks the dispatcher. Document this on the function and on
`EventOfType` / `EventOfTypes`.

### Event Payloads

The C runtime event includes a typed payload (`mln_runtime_event_payload_*`).
The binding decodes the payload into a Go-typed `Payload` interface:

```go
type Payload interface { payloadType() EventPayloadType }

type RenderFramePayload struct {
    Mode             RenderMode
    NeedsRepaint     bool
    PlacementChanged bool
    Stats            RenderingStats
}

type RenderMapPayload struct { Mode RenderMode }
type StyleImageMissingPayload struct { ImageID string }
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

The C API exposes three callback types that need Go trampolines:

| C callback | Threading | Re-entrant into binding? |
|---|---|---|
| `mln_log_callback` | any (worker / network / owner) | **No** |
| `mln_resource_transform_callback` | any worker | **No** |
| `mln_resource_provider_callback` | any worker | only via `mln_resource_request_handle` |

The binding installs callbacks via `//export`'d Go functions that match
the C ABI signature, plus a small `trampolines.c` translation unit that
exposes typed function pointers:

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

Why a separate `.c` file: cgo's `//export` generates a function with
`char*` parameters in `_cgo_export.h`. The C ABI typedefs use
`const char*`. Casting the function pointer in the same file as
`#include "maplibre_native_c.h"` collides with cgo's auto-generated
extern declaration. A separate translation unit that includes both
headers in the right order resolves the cast cleanly.

User data passes through cgo as a C-allocated cell containing a
`cgo.Handle` value (a `uintptr`-sized opaque integer):

```go
// Go side: register a callback
hp := C.malloc(C.size_t(unsafe.Sizeof(uintptr(0))))
*(*uintptr)(hp) = uintptr(cgo.NewHandle(callback))
var t C.mln_resource_transform
t.size      = C.uint32_t(unsafe.Sizeof(t))
t.callback  = C.mlnGoGetTransformTrampoline()
t.user_data = hp

// C → Go trampoline
//export mlnGoResourceTransformTrampoline
func mlnGoResourceTransformTrampoline(userData unsafe.Pointer, kind C.uint32_t,
    url *C.char, out *C.mln_resource_transform_response) C.mln_status {
    h := cgo.Handle(*(*uintptr)(userData))
    cb := h.Value().(URLTransform)
    // ... invoke cb, set out->url ...
}
```

The C-allocated cell exists to dodge the race detector's `checkptr`
guard on `unsafe.Pointer(uintptr(handle))`: with `-race`, that
conversion fails because `cgo.Handle` is not a real pointer. The cell
gives cgo a real address to point at; the trampoline dereferences it to
recover the handle.

The Runtime owns the cell and the handle for the runtime's lifetime;
both are freed during `Runtime.Close()` after the C side is destroyed.

A single Go-registered log callback is process-global (matches the C
API). Resource transform and provider callbacks are runtime-scoped.

Callbacks **must not** call back into any binding method. The
dispatcher's command channel is single-buffered and a re-entrant call
deadlocks. This restriction is documented on every public callback type.

Go panics inside a callback are caught at the trampoline boundary,
logged to stderr, and converted to the appropriate "no rewrite" / OK
status. Panics never unwind through cgo.

## Render Sessions And Render Targets

`RenderSession` represents one attached render target. Three attach paths,
all return a `*RenderSession`:

```go
// Backend-agnostic; uses the platform-default backend; supports the
// readback path but NOT backend-specific AcquireFrame.
func (m *Map) AttachTexture(width, height uint32, scale float64) (*RenderSession, error)

// Platform-specific; supports both readback and AcquireFrame.
//go:build darwin
func (m *Map) AttachMetalTexture(width, height uint32, scale float64) (*RenderSession, error)
func (m *Map) AttachMetalTextureWithDevice(device unsafe.Pointer, width, height uint32, scale float64) (*RenderSession, error)

//go:build linux
func (m *Map) AttachVulkanTexture(width, height uint32, scale float64) (*RenderSession, error)
func (m *Map) AttachVulkanTextureWithContext(ctx VulkanContext, width, height uint32, scale float64) (*RenderSession, error)
```

The agnostic `AttachTexture` is the recommended path for static rendering.
Backend-specific forms exist for callers that need to share a device with
another GPU consumer or sample the rendered MTLTexture/VkImage directly.

Render flow uses the native readback when GPU handles aren't needed:

```go
// One call: request still image, pump events until STILL_IMAGE_FINISHED,
// native readback into a fresh Go slice.
rgba, w, h, err := m.RenderImage(ctx, sess)

// Same with caller-supplied buffer for reuse across renders.
w, h, err := m.RenderImageInto(ctx, sess, dst)

// GPU-handle path: attach with a backend-specific function, render, take
// the borrowed handle, release explicitly.
frame, err := m.RenderStill(ctx, sess)
defer sess.ReleaseFrame(frame)
```

Surface attach (direct present to CAMetalLayer / VkSurfaceKHR) and
borrowed-texture attach (mbgl renders into a caller-owned MTLTexture /
VkImage) are out of scope for the initial binding. Add them when a
consumer needs them.

## Hot-Path Pumping

The static-render path requires aggressive `mln_runtime_run_once` cadence.
mbgl's RunLoop only progresses when the owner thread calls `run_once`;
events sit in the queue between pumps. A naive 8 ms tick produces ~125 Hz
render-step throughput. The binding should pump at the dispatcher's hop
cadence (~10 kHz) during active waits.

The recommended primitive is a single dispatcher op that pumps and
drains:

```go
// pumpAndPollForRender runs ONE dispatcher op that:
//   - calls mln_runtime_run_once
//   - drains every queued event in the runtime's event queue
//   - for each event with Source == m, services RENDER_UPDATE_AVAILABLE
//     inline (no extra dispatcher hop) by calling
//     mln_render_session_render_update
//   - returns the first terminal event for m (STILL_IMAGE_FINISHED,
//     STILL_IMAGE_FAILED, MAP_LOADING_FAILED, RENDER_ERROR)
//   - returns "productive=true" if any event was drained
func (m *Map) pumpAndPollForRender(sess *RenderSession) (Event, bool, bool, error)
```

The wait loop calls this in a tight cycle:

```go
timer := time.NewTimer(pollInterval)
defer timer.Stop()
for {
    ev, found, productive, err := m.pumpAndPollForRender(sess)
    if err != nil { return err }
    if found { /* terminal */ return resolve(ev) }
    if productive { continue }  // immediate retry — work was done
    select {
    case <-ctx.Done():
        return fmt.Errorf("%w: %w", ErrTimeout, ctx.Err())
    case <-timer.C:
        timer.Reset(pollInterval)
    }
}
```

The same primitive shape applies to `Runtime.WaitForEvent` (an
`r.pumpAndMatch(predicate)` op). `Map.WaitForEvent` filters by
`ev.Source == m` on top.

`pollInterval` is `100 * time.Microsecond`. The `time.NewTimer` is
reused (do not use `time.After`, which allocates a fresh runtime timer
per call and stays in the heap until it fires).

This batching is the difference between 200 fps and 1500 fps on small
static renders.

## Unsafe Escape Hatches

Go uses `unsafe.Pointer` for backend-native handles already; no
additional escape hatch type is needed. Naming convention for fields
that expose backend handles: the field name describes the handle
("Texture", "Device", "ImageView") with type `unsafe.Pointer` and a
field comment documenting which backend's type the pointer represents.

The package does not expose `*C.foo` types. `unsafe.Pointer` is the
binding's public wire format for opaque handles.

## Constants And Enums

Every C enum becomes a typed Go alias of `uint32` (or `int32` for
signed enums) with a `const` block of typed values:

```go
type Status int32

const (
    StatusOK              Status = 0
    StatusInvalidArgument Status = -1
    // ...
)

func (s Status) String() string {
    switch s { /* explicit switch */ }
    return fmt.Sprintf("UNKNOWN(%d)", int32(s))
}
```

Bitmask flags are typed bitmask values:

```go
type CameraField uint32

const (
    CameraFieldCenter  CameraField = 1 << 0
    CameraFieldZoom    CameraField = 1 << 1
    CameraFieldBearing CameraField = 1 << 2
    CameraFieldPitch   CameraField = 1 << 3
)
```

Note that the binding writes the bitmask values as `1 << 0` rather than
`uint32(1) << 0` — Go's typed constants accept untyped shifts cleanly.
This is independent of how the C header declares the values.

## Codegen Scope

**Auto-generate the constants table only.** Everything else is
handwritten following this spec.

A small generator (Go source-based or shell-based) extracts every
`MLN_*` enum value from the C headers and emits typed Go constants in
`constants_gen.go`:

```go
//go:generate go run ./internal/cmd/gen-constants $MLN_FFI_DIR/include
```

Regenerate when upstream pulls. The generator output is committed to
the repository (no `go generate` step in normal builds).

Why not full FFI auto-gen (e.g. c-for-go):

- The C ABI's `**T` out-pointer convention (`*out_runtime` must be NULL
  on entry) doesn't translate cleanly through any FFI auto-gen tool.
  Every constructor needs a handwritten cgo shim. With ~15 constructors
  in the API, that's most of the value the auto-gen would have provided.
- The wrapper-struct cache pattern in c-for-go invalidates Go-side field
  modifications across return-by-value boundaries (`*Default()`
  returning a struct with a stale C cache pointer). Workarounds require
  unexported field access or post-process patches.
- Modern Go's strict typing rejects c-for-go's bitmask flag emission
  (`uint32(1) << X` for an enum-typed flag). Post-process required.
- Cgo `//export` declarations conflict with c-for-go's per-file
  preamble forward declarations for callback function pointers. Requires
  a separate translation unit (which we already write by hand for
  `trampolines.c`).
- C23 typed-enum syntax in the headers breaks c-for-go's C99 parser
  entirely; preprocessing required.

Auto-generating just the constants table sidesteps all of these issues
and tracks upstream automatically. See `experiments/cforgo/` in the
maplibre-native-go repository for a full PoC and `RECOMMENDATIONS.md`
for header-shape changes that would make broader auto-gen viable in
the future.

## Build And Toolchain

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
`maplibre-native-c.pc`. A small `Makefile` in the binding's repository
provides convenience targets (`make build`, `make test`) that set up
the path against `$MLN_FFI_DIR`.

Build tags:

- **`mln_debug`**: installs leak-detection finalizers on every owned
  handle. Off by default. Used during development; not for release
  builds.

Platform-specific files:

- **`_darwin.go`** / **`_linux.go`**: backend-specific render-target
  attach (Metal / Vulkan). Each platform file is gated by `//go:build`.

The package compiles cleanly with `-race`. The race detector enforces
the Go memory model on goroutines that share data with the dispatcher;
the binding's design (every shared cgo pointer touched only inside the
dispatcher closure) holds against `-race` and `checkptr`.

## Testing

`go test ./...` runs the full test suite against a real built
`libmaplibre-native-c`. Tests do not mock the C ABI.

Coverage targets:

- **Smoke tests**: `Runtime` create/destroy, `Map` create/destroy with
  empty style, `Session` end-to-end render, `Pool` parallel renders.
- **Lifecycle**: idempotent `Close()`, nil-receiver safety,
  parent-with-live-children rejection, double-close.
- **Errors**: `errors.Is(err, ErrInvalidState)` matching, `errors.As`
  to `*Error`, sentinel comparison.
- **Cancellation**: `context.WithTimeout` produces `ErrTimeout` +
  `context.DeadlineExceeded` from waits.
- **Threading**: tests run with `-race`; concurrent use of one Session
  from multiple goroutines must serialize correctly through the
  dispatcher.
- **Payload decode**: every event payload type round-trips correctly
  (`*RenderFramePayload`, `*RenderMapPayload`, `*StyleImageMissingPayload`,
  `*TileActionPayload`).
- **Callbacks**: log callback receives records under both synchronous
  and async masks; URL transform is invoked with the right kind/url.

Add regression tests when a Go-layer invariant (dispatcher discipline,
ctx cancellation, leak-tracker behavior) is added or changed.

## Documentation

The package ships a top-level `doc.go` with:

- **Quick start**: a 30-line example using `Session` to render an empty
  style.
- **Concept map**: one-paragraph descriptions of `Runtime`, `Map`,
  `RenderSession`, `Session`, `Projection`.
- **Errors**: `errors.Is` and `errors.As` patterns; sentinel list.
- **Cancellation**: `context.Context` semantics; double-error wrap.
- **Threading model**: one OS thread per Runtime; goroutine-safe;
  pool of N Runtimes for parallelism.
- **Resource customisation**: when to use `SetResourceURLTransform`.
- **Build requirements**: `pkg-config`, `MLN_FFI_DIR`.
- **Build tags**: `mln_debug`.

Each public type carries a doc comment that begins with the type name.
Each public method documents its threading rules where relevant
(callbacks: "must not re-enter", `WaitForEvent`'s `match`: "pure
predicate") and its cancellation behavior.

The repository's top-level `README.md` covers consumer-facing usage:
installation, Hello World, the `examples/` directory layout, links to
GoDoc.

## Examples

The `examples/` directory in the repository ships at least:

- **examples/static-render** (`cmd/static-render`): renders one static
  map view to a PNG file. The 80-line consumer-side equivalent of
  `mapnik-shape-test`-style demos.
- **examples/pool**: pool of N `Session` instances rendering concurrently
  on N OS threads. Demonstrates parallelism + in-place style swap.
- **examples/render-worker** (`cmd/render-worker`): long-lived subprocess
  speaking a binary frame protocol on stdin/stdout, drop-in compatible
  with multi-language tile-server harnesses.
- **examples/sdl3-metal**: optional, demonstrates host-renderer
  integration with `AttachMetalTextureWithDevice` sharing a
  `CAMetalLayer`'s device.
- **examples/bench**: micro-benchmark loop measuring frame timing and
  steady-state RSS against a real style.

Examples live in their own modules under `examples/` so consumers can
copy one as a starting point without pulling in a transitive dependency
on the whole binding's test fixtures.
