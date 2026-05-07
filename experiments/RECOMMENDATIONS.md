# C ABI shape recommendations from a Go consumer

This is feedback to maplibre-native-c maintainers based on real
experience writing both a hand-written idiomatic Go binding
(`maplibre-native-go`, ~3000 LOC) and a c-for-go auto-generated one
(see `experiments/cforgo`). Each recommendation is tied to a concrete
pain point we hit, with a suggested mitigation.

The recommendations are **additive** — none would break existing C
or C++ consumers. They reduce friction for Go (and other languages
with strict type systems and managed runtimes — Rust, Swift, and
Kotlin/Java would benefit similarly).

## Tier 1: Substantial impact for any Go binding

### 1. Bitmask types should not be enums

**Pain.** mbgl declares bitmask flags as enums:

```c
typedef enum mln_camera_option_field : uint32_t {
  MLN_CAMERA_OPTION_CENTER  = 1u << 0u,
  MLN_CAMERA_OPTION_ZOOM    = 1u << 1u,
  ...
} mln_camera_option_field;
```

In Go, this generates a typed enum:

```go
type CameraOptionField uint32
const (
  CameraOptionCenter CameraOptionField = uint32(1) << uint32(0)  // ← rejected
)
```

Modern Go (≥1.17 strict typing) rejects this. The typed constant
declaration requires the RHS to be assignment-compatible with the
type. `uint32(1)` is `uint32`, not `CameraOptionField`. c-for-go
auto-gen produces broken code; hand-written bindings have to spell
out `CameraOptionField(1) << X` manually.

**Suggested mitigation.** Distinguish *enums* (mutually exclusive
values) from *bitmasks* (composable flags). Use plain typedefs +
constants for bitmasks:

```c
typedef uint32_t mln_camera_option_field;
#define MLN_CAMERA_OPTION_CENTER ((mln_camera_option_field) 1u << 0u)
#define MLN_CAMERA_OPTION_ZOOM   ((mln_camera_option_field) 1u << 1u)
```

Or `static const uint32_t MLN_CAMERA_OPTION_* = ...;`. Both work
cleanly across every Go binding tool — the constants emit as
clean Go const declarations of type `CameraOptionField`.

**Where this hits today.** `mln_camera_option_field`,
`mln_animation_option_field`, `mln_camera_fit_option_field`,
`mln_bound_option_field`, `mln_free_camera_option_field`,
`mln_projection_mode_field`, `mln_log_severity_mask`,
`mln_runtime_option_flag`, `mln_resource_provider_decision`-style
unions, and ~10 others. Roughly 60-80 mask declarations.

---

### 2. Avoid C23 typed-enum syntax in public headers

**Pain.** mbgl uses C23 typed-enum syntax for explicit width:

```c
typedef enum mln_status : int32_t {
  MLN_STATUS_OK = 0,
  ...
};
```

c-for-go's parser is C99 only. SWIG, cgo's auto-discovery in some
configurations, older Clang front-ends, and any binding tool built
on a pre-C23 parser fail outright. Our PoC required preprocessing
the headers with `sed` to strip the `: int32_t` annotation before
c-for-go could parse them.

**Suggested mitigation.** Use a plain enum + static_assert for
width:

```c
typedef enum mln_status {
  MLN_STATUS_OK = 0,
  ...
} mln_status;
_Static_assert(sizeof(mln_status) == 4, "mln_status width must be 32-bit");
```

The compiler's natural enum sizing already lands on 32-bit for the
range mbgl uses. The `_Static_assert` catches drift without
requiring C23.

**Where this hits today.** Every typed enum in the headers — ~25
declarations across `base.h`, `runtime.h`, `map.h`, `style.h`,
`query.h`, `logging.h`, etc.

---

### 3. Make struct `size` field discoverable

**Pain.** Almost every options/event/descriptor struct starts with:

```c
typedef struct mln_runtime_options {
  uint32_t size;  // caller fills this with sizeof(struct)
  ...
};
```

The `mln_xxx_options_default()` initializers populate `size`, but
the natural Go pattern is `opts := RuntimeOptions{Width: 256}` —
constructing a struct from scratch and only setting fields you care
about. With the `size` requirement, that pattern breaks: `size: 0`
makes mbgl reject the call as `INVALID_ARGUMENT`.

Hand-written Go bindings work around it via `unsafe.Sizeof(C.mln_xxx{})`.
That's fine, but every options struct construction needs the
boilerplate.

c-for-go auto-gen can't surface `unsafe.Sizeof` because of package
boundaries. Consumers end up hardcoding `Size: 32` and breaking
when the struct grows on a future release. This actually happened
during our PoC — we'd had to look up the C struct, count bytes,
and hardcode.

**Suggested mitigation.** Pick one of:

A. **Treat `size == 0` as "use the binary's view"** — i.e. mbgl
   substitutes `sizeof(struct)` whenever it sees zero. Old callers
   keep working; new callers can omit the size dance.

B. **Expose a `mln_xxx_options_size()` function** that returns
   `sizeof(struct)`. Auto-gen tools surface the function trivially
   and consumers do `Size: int(maplibre.RuntimeOptionsSize())`.

C. **Provide a preprocessor constant** — `#define MLN_RUNTIME_OPTIONS_SIZE 32`
   alongside the struct. cgo + auto-gen tools both pick it up.

(A) is the most consumer-friendly; (B) is the easiest to implement.

**Where this hits today.** ~30 options/descriptor/event structs.

---

### 4. Avoid identifier collisions across namespaces

**Pain.** mbgl has both:

- A struct tag `mln_runtime_event_offline_region_response_error`
- An enum value `MLN_RUNTIME_EVENT_OFFLINE_REGION_RESPONSE_ERROR`

Both case-fold to the same Go identifier (`RuntimeEventOfflineRegionResponseError`).
C uses separate namespaces for struct tags and enum members; Go
collapses them. Auto-gen tools emit two declarations; the build
fails. Hand-written bindings have to rename one or the other.

**Suggested mitigation.** Suffix payload struct tags so they can
never collide with the corresponding event-type enum value:

```c
typedef struct mln_runtime_event_offline_region_response_error_payload { ... };
```

vs. the enum value `MLN_RUNTIME_EVENT_OFFLINE_REGION_RESPONSE_ERROR`.

This convention scales: every struct that's the payload for an
event of the same name should add `_payload` to its tag.

**Where this hits today.** At least the one struct above; possibly
others as the payload set grows.

---

### 5. Bundle error message with status, not thread-local

**Pain.** mbgl uses a thread-local string:

```c
mln_status status = mln_runtime_create(...);
if (status != MLN_STATUS_OK) {
  const char* msg = mln_thread_last_error_message();
}
```

In Go, **goroutines migrate between OS threads** unless explicitly
pinned via `runtime.LockOSThread()`. Between the failing call and
`mln_thread_last_error_message()`, the goroutine could move to a
different OS thread that has no error stored — or worse, a stale
one from an unrelated call.

Our hand-written binding works around this by routing every C call
through a dispatcher goroutine pinned to one OS thread. Inside the
dispatcher's runOnOwner closure, we call `mln_thread_last_error_message`
synchronously after the failing call, before the goroutine yields.
That's correct but it means **the binding has to enforce dispatcher
discipline on every error path** — easy to forget for a feature
add, and silently produces empty error messages.

**Suggested mitigation.** Provide an optional output parameter for
the diagnostic message on every status-returning call:

```c
typedef struct mln_status_extended {
  mln_status status;
  char message[256];  // or pointer + length pair
  size_t message_len;
} mln_status_extended;

MLN_API mln_status mln_runtime_create(
  const mln_runtime_options* options,
  mln_runtime** out_runtime,
  mln_status_extended* out_status_ex  // optional; pass NULL to skip
);
```

The thread-local `mln_thread_last_error_message()` stays for
backward compat. New callers get the message tied to the call,
not to the producing thread. Auto-gen and hand-written bindings
both benefit equally.

Alternative: every status-returning function takes a `char*
out_msg, size_t out_msg_cap, size_t* out_msg_len` triple at the end.
Less elegant but minimally invasive.

---

## Tier 2: Improves binding ergonomics

### 6. Document `run_once` cadence expectations

**Pain.** `mln_runtime_run_once()` "Runs one pending owner-thread
task. If no task is pending, the call returns OK without doing
work." We initially called it via an 8 ms tick (matches mbgl's
internal heartbeat). Renders stalled — events sat in the queue for
~8 ms between pumps. After profiling we drove `run_once` at ~10 kHz
during active waits and got a 2-4× throughput improvement.

The cadence requirement isn't documented. A naive Go implementation
that leans on the 8 ms tick is **correct but slow**; only a
well-instrumented binding finds the bottleneck.

**Suggested mitigation.** Either:

A. **Document the cadence expectation** in `runtime_run_once`'s
   doc comment: "Call this in a tight loop while waiting on
   events; mbgl's owner-thread tasks accumulate between pumps and
   slow event delivery proportional to the gap."

B. **Provide a blocking `mln_runtime_run_until(deadline_ns,
   match_predicate)` helper** that loops `run_once + poll_event`
   internally. Bindings call it once per render step; the cadence
   is mbgl's responsibility, not the binding's.

(B) is more invasive but eliminates a whole class of "binding too
slow" issues across all consumer languages.

---

### 7. Output-buffer variants for borrowed event strings

**Pain.** `mln_runtime_event.message` is documented as "valid until
the next mln_runtime_poll_event() call for the same runtime or
until the runtime is destroyed." In Go, this means a copy via
`C.GoStringN(message, message_size)` per event — one allocation
per event, plus the cgo crossing.

For high-volume events (TILE_ACTION can fire hundreds per render),
this allocation churn shows up in profiles.

**Suggested mitigation.** Add an output-buffer variant of poll:

```c
MLN_API mln_status mln_runtime_poll_event_into(
  mln_runtime* runtime,
  mln_runtime_event* out_event,
  bool* out_has_event,
  char* msg_buffer,
  size_t msg_buffer_cap,
  size_t* msg_len
);
```

Caller supplies a reusable byte slice for the message. mbgl copies
into it (or sets `*msg_len = 0` if no message). Bindings reuse one
buffer across thousands of events with zero allocation per event.

---

### 8. Document callback re-entrancy and threading per-callback

**Pain.** mbgl has multiple callback types with different rules:

| Callback | Thread | May call back into C ABI? |
|---|---|---|
| `mln_log_callback` | any (worker, network, owner) | **No** — must not call back |
| `mln_resource_transform_callback` | any worker | **No** |
| `mln_resource_provider_callback` | any worker | Only via `mln_resource_request_handle` API |
| `mln_resource_request_complete` (caller-invoked) | any | Yes |

The current docs cover most of this but the rules are scattered
across each callback's prose. Binding writers (and downstream
consumers using bindings) often miss one — the wrong assumption
deadlocks the mbgl owner thread or crashes via a re-entered
mutex.

**Suggested mitigation.** Add a single table to `logging.h` or the
top of the module covering all callbacks:

```
| Callback                  | Thread       | Re-entrant into C ABI? | Lifetime |
| ------------------------- | ------------ | ---------------------- | -------- |
| mln_log_callback          | any          | No                     | until cleared / replaced |
| mln_resource_transform... | any worker   | No                     | until runtime destroy |
...
```

Bindings can mirror this in their own docs and consumers stop
guessing.

---

### 9. Avoid `*out != NULL` initialization check on constructors

**Pain.** Every constructor checks:

```
- MLN_STATUS_INVALID_ARGUMENT when ... out_runtime is null,
  *out_runtime is not null, ...
```

The intent (catch double-init bugs) is good. But it makes the
caller responsible for initialising the destination to NULL before
the call. In hand-written Go bindings this is fine — we declare
`var out *C.mln_runtime` (zero-valued = NULL) and pass `&out`. In
auto-gen Go bindings, c-for-go renders `**T` as `[][]T` because
the auto-gen translator can't represent the "nil-allowed inner
pointer" idiom. The slot `[][]T{{T{}}}` makes the inner pointer
non-NULL → mbgl rejects with `INVALID_ARGUMENT`. **Every
constructor in the API needs a hand-written shim for c-for-go.**

**Suggested mitigation.** Either drop the `*out != NULL` defensive
check (treat the slot as write-only on entry), or provide
**single-pointer-return constructors** in addition:

```c
// Existing:
MLN_API mln_status mln_runtime_create(const mln_runtime_options* opts,
                                      mln_runtime** out_runtime);

// New, parallel:
MLN_API mln_runtime* mln_runtime_create_or_null(const mln_runtime_options* opts,
                                                mln_status* out_status);
```

Auto-gen tools render `_or_null` cleanly: `func RuntimeCreateOrNull(opts, &status) *Runtime`.
The double-pointer variant stays for callers who need it.

---

## Tier 3: Stylistic / nice-to-have

### 10. Less Objective-C / Vulkan exposure on attach surfaces

**Pain.** `mln_metal_surface_attach` takes a `void* layer` that's
expected to be a `CAMetalLayer*`. In Go, we'd need to either:

- Pull in `<QuartzCore/CAMetalLayer.h>` from cgo (works but
  pollutes the binding with Objective-C runtime types)
- Have the consumer hand-marshal the pointer through a Go SDL/Cocoa
  wrapper

The Vulkan surface attach takes raw `VkInstance` / `VkDevice` /
`VkSurfaceKHR` pointers, which we can pass via `unsafe.Pointer`
but then the binding's GoDoc has to describe what each pointer
expects.

**Suggested mitigation.** This is harder and less critical, but a
"host-supplied surface factory" pattern would decouple language
bindings from platform-specific render APIs. Less urgent than
items 1-9.

---

### 11. Function naming consistency for "default" vs "size"

**Pain.** Some patterns we wished were uniform:

- `mln_xxx_options_default()` — initializes a struct (existing convention, good)
- `mln_xxx_size()` — returns sizeof for binding tools (proposed in #3)
- `mln_xxx_describe()` — returns a `const char*` describing the value (could supersede a per-enum `String()` we hand-write each time)

Right now consumers re-implement `String()` for every enum
(`Status`, `EventType`, `LogSeverity`, `RenderMode`,
`TileOperation`, ...). A native `mln_status_describe(status)`
returning `"INVALID_STATE"` would let bindings forward the C name.

**Suggested mitigation.** Optional `mln_xxx_describe()` returning
borrowed `const char*` for each enum / status type. Not load-
bearing but eliminates 100+ lines of stringer boilerplate per
binding.

---

## What's already good

To balance the asks above, mbgl already gets a lot right for
language bindings:

- **Opaque handle types** (`mln_runtime`, `mln_map`, `mln_render_session`)
  are pure forward declarations. Bindings wrap them as `unsafe.Pointer`
  with no field-access concerns.
- **JSON-as-string for style spec** (`mln_map_set_style_json`) is a
  godsend for Go — JSON manipulation is trivial in Go's stdlib, so
  bindings don't have to mirror the entire style spec as Go types.
- **Poll-based event model** instead of mandatory callbacks fits Go's
  CSP / channels idiom better than callbacks would.
- **Documented thread-affinity rules** (per function, in the doc
  comment) are what allow binding writers to encode the rules in
  the type system. Bindings without that documentation would have
  to test empirically.
- **`MLN_API` visibility annotation** plus pkg-config + .pc file
  shipped in the build directory makes integration trivial.
- **Header split** (`maplibre_native_c/{base,runtime,map,...}.h`) +
  the umbrella include for IWYU — easy to navigate, easy to consume
  selectively.

## Summary

The five items most worth addressing first:

| # | Recommendation | Estimated header churn |
|---|---|---|
| 1 | Bitmask types as plain typedef, not enum | ~60-80 declarations |
| 2 | Avoid C23 typed enums; use `_Static_assert` | ~25 declarations |
| 3 | Discoverable struct sizes (size=0 means default) | ~30 structs, one ABI tweak |
| 4 | Suffix payload struct tags to avoid Go-side collisions | 1+ rename |
| 5 | Bundle error message with status return | One new function variant per status-returning call |

(1) and (2) are pure-syntax: would unblock c-for-go and similar
tools instantly with no behavioral change. (3) and (5) are minor
ABI additions that benefit hand-written bindings most.
Together they'd eliminate ~80% of the post-process patches our PoC
needed.
