# Go-binding shapes for maplibre-native-c

A side-by-side comparison of two approaches to a Go binding, intended
to seed a discussion with upstream about what a "baseline Go writer"
should look like.

```
experiments/
├── cforgo/                 c-for-go auto-gen + the workarounds it needs
│   ├── maplibre.yml        manifest (~50 LOC)
│   ├── postprocess.sh      patches c-for-go can't apply via YAML (~40 LOC)
│   ├── headers/            preprocessed C23 → C99 header tree
│   ├── cforgo/             generated package (~16k LOC)
│   └── example/main.go     usage demo (~110 LOC, including hand-written shims)
└── idiomatic/              hand-written equivalent
    ├── maplibre.go         binding (~330 LOC)
    └── example/main.go     usage demo (~45 LOC)
```

Both demonstrations render the same thing: open a runtime, create a
256×256 map, load an empty style, attach an offscreen texture, render
one still image, read out 256 KiB of premultiplied RGBA. Both are
verified end-to-end against `libmaplibre-native-c` at upstream
`cc799b4`.

## TL;DR

- **c-for-go can technically generate against this header**, but the
  output is unusable without 4 categories of post-processing patches
  and at least one hand-written shim per out-pointer constructor.
- **It is not "free Go bindings."** It is a starting layer that needs
  significant custom code on top — and that custom code looks a lot
  like the idiomatic version below it.
- **The idiomatic 330-LOC version covers the same render path more
  safely**: it encodes thread affinity, lifecycle, errors, and
  cancellation. A consumer doesn't need to know mbgl's threading
  rules or the C ABI's `**T` out-pointer convention.
- Recommendation for upstream: **a hand-written idiomatic Go example
  in the same shape as `examples/zig-map` is the right artifact**.
  c-for-go output is interesting as a separate "raw bindings" layer
  for projects that want to wrap the API differently than upstream
  prescribes — but it's not a substitute for the idiomatic layer.

## Practical issues encountered with c-for-go

These are real, concrete blockers found while running the PoC:

### 1. C23 typed enums

mbgl uses `typedef enum mln_status : int32_t { ... }` (typed enum
syntax from C23). c-for-go's parser is C99 and rejects the `: int32_t`
annotation. **Workaround:** preprocess the headers with sed to strip
the type annotation before invoking c-for-go. See `headers/` and
the regen step.

### 2. Identifier collisions

mbgl has both a struct tag `mln_runtime_event_offline_region_response_error`
and an enum value `MLN_RUNTIME_EVENT_OFFLINE_REGION_RESPONSE_ERROR`.
C separates these into struct/enum namespaces; Go folds them together.
c-for-go emits two declarations with the same Go name; build fails.
**Workaround:** post-process script renames the struct to add a
`Payload` suffix.

### 3. Bitmask flags on typed enum constants

c-for-go emits:

```go
LogSeverityMaskInfo LogSeverityMask = uint32(1) << LogSeverityInfo
```

Modern Go (≥1.17 strict typing) rejects this: the typed constant
declaration requires the RHS to be assignment-compatible with
`LogSeverityMask`, not `uint32`. c-for-go has no rule to fix this.
**Workaround:** post-process regex rewrites every `uint32(1)` to
`<TypeName>(1)` in bitmask declarations.

### 4. Parameter shadowing return type

mbgl's `mln_network_status_set(uint32_t status)` returns `mln_status`.
c-for-go renders this as:

```go
func NetworkStatusSet(Status uint32) Status { ... __v := (Status)(__ret) ... }
```

The parameter `Status uint32` shadows the return type `Status`, so
`(Status)(__ret)` is a type-cast on a function call — invalid.
**Workaround:** post-process renames the offending parameter.

### 5. `**T` out-parameters can't pass NULL

Every constructor in maplibre-native-c uses the
`mln_xxx_create(opts, T** out)` pattern. The C ABI requires
`*out == NULL` on entry (rejects with `INVALID_ARGUMENT` otherwise).
c-for-go renders the parameter as `[][]T`, expecting the caller to
pre-allocate a one-element outer-of-one-element-inner slot:

```go
slot := [][]Runtime{{Runtime{}}}
RuntimeCreate(&opts, slot)
rt := &slot[0][0]
```

But `slot[0][0]` is a zero-valued `Runtime` struct, not nil — the
inner-pointer the C side sees is non-NULL, so the call fails. There
is no way to pass a NULL inner pointer through the `[][]T` shape.

**No YAML workaround exists.** Every constructor needs a hand-written
shim that drops to raw cgo:

```go
func runtimeCreate(...) (*Runtime, error) {
    var out *C.mln_runtime
    if status := C.mln_runtime_create(&cOpts, &out); ...
    return (*Runtime)(unsafe.Pointer(out)), nil
}
```

This applies to: `runtime_create`, `map_create`, `map_projection_create`,
`*_owned_texture_attach`, `*_borrowed_texture_attach`,
`*_surface_attach`, plus several offline-region creators. Roughly
15–20 hand-written shims.

### 6. Wrapper-struct cache pointer goes stale

c-for-go wraps every C struct with a Go struct carrying hidden
`refXXX *C.mln_foo` and `allocsXXX interface{}` cache fields.
`PassRef()` returns the cached pointer if non-nil, even when Go-side
field changes haven't been flushed. The cache is set whenever a
struct is returned from a C call (e.g. `RuntimeOptionsDefault()`).

```go
opts := cforgo.RuntimeOptionsDefault()  // cache points at C-stack temporary
opts.Width = 256                        // ignored — cache still set
RuntimeCreate(&opts, ...)               // passes cached (stale) pointer
```

`Deref()` copies cache → Go fields but doesn't clear the cache.
The cache field is unexported, so external packages can't reset it.

**Workaround:** don't use `*Default()` at all; construct structs from
scratch and hardcode the `Size` field with the C struct's sizeof
(which c-for-go also doesn't expose). Brittle across architectures.

### 7. No callback support for our callbacks

`mln_log_callback`, `mln_resource_transform_callback`, and
`mln_resource_provider_callback` all need a `//export`'d Go trampoline
plus a `cgo.Handle` for user_data, plus the C-cell-as-user-data dance
to dodge `-race`'s checkptr. c-for-go has callback helpers but they
target simpler callback signatures and don't compose with the
trampoline pattern. Hand-written, every time.

### 8. No idiomatic Go cancellation, no Context

The auto-gen produces functions that the caller drives in a busy
loop. There is no `context.Context` integration, no timer-driven
backoff, no cancellation point. Every consumer reimplements the
same render-still loop, and most of them get it subtly wrong (e.g.
forgetting `mln_runtime_run_once` and wondering why renders never
complete).

### 9. No thread-affinity enforcement

The C ABI returns `MLN_STATUS_WRONG_THREAD` for off-owner-thread
calls. c-for-go output gives the caller no help: every public
function is callable from any goroutine, and getting it wrong is a
runtime crash or `WRONG_THREAD` error far from the line that mis-
threaded. The auto-gen surface is **strictly more dangerous** than
the C ABI itself, because Go encourages goroutine-per-task usage.

### 10. Generated parameter names are PascalCase

`func MapCreate(Runtime *Runtime, Options []MapOptions, OutMap [][]Map)`.
Idiomatic Go uses lowercase parameter names. Cosmetic but it makes
the binding obviously not-Go.

## What we got from c-for-go

To be fair: a useful portion of the binding generates cleanly:

- **Constants** (`StatusOk`, `LogSeverityInfo`, `RuntimeEventMapStyleLoaded`, ...)
  are clean and ready to use.
- **Simple value-only functions** (`MapDestroy`, `MapSetStyleJson`,
  `MapJumpTo`, `RuntimeRunOnce`, `RuntimePollEvent`) work as expected.
- The mechanical translation of struct field names and types is correct.
- Headers track upstream automatically — regen on each upstream pull
  and the generated code stays in sync.

If a downstream Go project wants to wrap maplibre-native-c **its own
way**, having c-for-go output as a starting "raw" layer can save real
time on the constants and simple-function wrapping. The
idiomatic-binding work is what they were going to do anyway.

## What the idiomatic version proves

The hand-written version is **330 LOC of binding** + **45 LOC of
example**, including:

- Full owner-thread dispatcher (encoding the C ABI's threading rule
  so callers don't have to think about it)
- Resource lifecycle: `Close()` on Runtime / Map / RenderSession,
  defer-friendly, idempotent
- Typed `*Error` with `errors.Is(err, ErrInvalidState)` matching
- `context.Context` cancellation on `WaitForStyle` and `RenderImage`
- Aggressive `run_once` pumping (~10 kHz vs the C-ABI default of
  whatever the caller schedules) — same perf trick that gave the
  full binding a 2–4× speedup
- The render-still + native-readback flow as one method call,
  including correct event filtering for the right map

A consumer's example program goes from c-for-go's:

```
- LockOSThread() (hope that holds)
- Hand-written runtimeCreate shim using raw cgo
- Hand-written mapCreate shim using raw cgo
- ev.Size = uint32(C.sizeof_mln_runtime_event)  // mandatory, easy to forget
- Manual run_once + poll + drain loop
- evSlot[0].Deref() before reading
```

to the idiomatic:

```
rt, _ := maplibre.NewRuntime()
m, _ := rt.NewMap(256, 256)
m.SetStyleJSON(...)
m.WaitForStyle(ctx)
sess, _ := m.AttachTexture(256, 256)
rgba, w, h, _ := m.RenderImage(ctx, sess)
```

The full `maplibre-native-go` binding extends this to ~3000 LOC by
covering Sessions, Pool, Projection, LatLng, network/cache/log
surfaces, URL transform, typed event payloads, and the rest of the
ABI. None of those would generate cleanly from c-for-go either.

## Recommendation to upstream

1. **Ship a hand-written idiomatic Go example app** (parallel to
   `examples/zig-map` and `examples/swift-map`). The render-still
   path here is a usable starting point for that.
2. **Don't ship c-for-go output as a "Go binding."** It is not one.
   The shape is wrong (PascalCase params, `[][]T` constructors, no
   thread safety, brittle struct-cache).
3. **Consider linking the existing `maplibre-native-go` binding**
   from upstream's README as the recommended Go consumer. It already
   covers the entire ABI surface idiomatically and tracks upstream
   on each release.
4. **If upstream wants a "raw" layer** for downstream projects that
   wrap differently than the recommended idiomatic binding,
   `c-for-go` could ship that layer — but it should be marked
   "low-level building block, not a usable Go API" and ship with all
   the workarounds documented above.

## Reproducing this PoC

```bash
# Install c-for-go
go install github.com/xlab/c-for-go@latest

# Preprocess C23 typed enums to C99
cd experiments/cforgo
mkdir -p headers/maplibre_native_c
for src in $MLN_FFI_DIR/include/maplibre_native_c/*.h; do
  sed -E 's/(typedef enum [a-zA-Z_]+) : (int|uint)[0-9]+_t \{/\1 {/g' "$src" \
    > "headers/maplibre_native_c/$(basename $src)"
done
sed -E 's/(typedef enum [a-zA-Z_]+) : (int|uint)[0-9]+_t \{/\1 {/g' \
  "$MLN_FFI_DIR/include/maplibre_native_c.h" > headers/maplibre_native_c.h

# Generate
$(go env GOPATH)/bin/c-for-go -ccincl -ccdefs maplibre.yml

# Apply post-process patches that c-for-go can't apply via YAML
./postprocess.sh

# Build & run
cd cforgo && go mod init ... && go build .
cd ../example && go run .
# → "style loaded"

# Idiomatic version
cd ../../idiomatic/example && go run .
# → "style loaded\nrendered 256x256 (262144 bytes)"
```
