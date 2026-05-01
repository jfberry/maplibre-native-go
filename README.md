# maplibre-native-go

Go bindings for the [maplibre-native-ffi](https://github.com/sargunv/maplibre-native-ffi) C ABI over [MapLibre Native](https://github.com/maplibre/maplibre-native).

**Status: experimental.** The upstream ABI is unstable (`mln_abi_version() == 0`) and these bindings track it directly. Pin to a specific upstream commit and expect breaking changes between bumps.

**Tested against maplibre-native-ffi commit** [`f1d0008`](https://github.com/sargunv/maplibre-native-ffi/commit/f1d00086e0da85617edc1ce5281b4c5f4e5938e1). CI builds against this exact commit; bumping it is intentional.

## What works today

- Runtime / Map / TextureSession lifecycle
- Style URL + inline style JSON
- Camera ops: `GetCamera`, `JumpTo`, `MoveBy`, `ScaleBy`, `RotateBy`, `PitchBy`, `CancelTransitions`
- Runtime event polling (`Runtime.PollEvent`, `Runtime.WaitForEvent`, `Map.WaitForEvent` filter wrapper)
- Metal texture session on macOS — `AttachMetalTexture` / `AttachMetalTextureWithDevice`, resize, render, acquire/release frame, detach, destroy
- Vulkan texture session on Linux — `AttachVulkanTexture` (default Mesa lavapipe context internally) / `AttachVulkanTextureWithContext` (caller-supplied `VkInstance`/`VkPhysicalDevice`/`VkDevice`/`VkQueue`)
- `Map.RenderStill` — drives a single static-mode render via `mln_map_request_still_image` and returns the acquired frame. Sub-millisecond p50 against tile-cached styles on Apple Silicon Metal.
- Stress benchmark (`cmd/bench`) demonstrating stable steady-state RSS

## What's missing

- **CPU pixel readback.** The texture-session API hands back GPU handles (`id<MTLTexture>`, future `VkImage`). To extract RGBA bytes today consumers must do their own GPU-to-CPU copy. Tracked upstream: [sargunv/maplibre-native-ffi#9](https://github.com/sargunv/maplibre-native-ffi/issues/9).
- **Vulkan / Linux** texture session — the upstream ABI supports Vulkan; the Go bindings haven't wired it yet.
- **Resource provider / log / transform callbacks.** `file://` and `mbtiles://` URLs go through built-in native sources, so most static-render workloads don't need these.
- **Style mutation (M6 upstream).** No runtime image add/remove, layer/source insertion, or property setters yet.
- **Windows.** Upstream is macOS+Linux only.

## Requirements

- Go 1.23+
- A built `libmaplibre-native-c` from [maplibre-native-ffi](https://github.com/sargunv/maplibre-native-ffi). The library lives at `$MLN_FFI_DIR/build/libmaplibre-native-c.dylib` (macOS) or `.so` (Linux), with a pkg-config file at `$MLN_FFI_DIR/build/pkgconfig/maplibre-native-c.pc`. Build it with `cd $MLN_FFI_DIR && mise run build` (the upstream tooling expects `mise` + `pixi`).
- macOS 13+ with the Metal framework available, **or** Linux with Vulkan (`apt-get install libvulkan-dev mesa-vulkan-drivers vulkan-tools` for a CPU-only Mesa lavapipe deploy).
- (For `examples/sdl3-metal` only) SDL3 from Homebrew: `brew install sdl3`.

## Build

The Makefile points pkg-config at `$MLN_FFI_DIR/build/pkgconfig` and adds an rpath via `CGO_LDFLAGS`. Default `MLN_FFI_DIR` is `$HOME/dev/maplibre-native-ffi`:

```bash
# Build the native library (one-time per upstream commit)
make native

# Build everything else
make build

# Run the test suite
make test
```

To use a different checkout:

```bash
MLN_FFI_DIR=/path/to/maplibre-native-ffi make build
```

To `go run` / `go test` outside the Makefile, source the env first:

```bash
eval "$(make env)"
go test ./...
```

You may see one `ld: warning: duplicate -rpath ... ignored` during the link. It's harmless — Go's cgo applies `CGO_LDFLAGS` at both the package and binary link phases.

### Real-asset smoke test

A `TestSmokeRealAssets` test exercises the full lifecycle (style + tiles + sprite + glyphs + render-until-idle) against assets you point at. Skipped by default; enable by setting `MLN_TEST_STYLE`:

```bash
eval "$(make env)"
MLN_TEST_STYLE="file:///abs/path/to/style.prepared.json" \
  go test -v -run TestSmokeRealAssets ./...
```

Optional env: `MLN_TEST_LAT`, `MLN_TEST_LON`, `MLN_TEST_ZOOM`, `MLN_TEST_TIMEOUT`.

## Quick start

```go
package main

import (
	"log"
	"time"

	maplibre "github.com/jfberry/maplibre-native-go"
)

func main() {
	rt, err := maplibre.NewRuntime(maplibre.RuntimeOptions{})
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	m, err := rt.NewMap(maplibre.MapOptions{
		Width: 512, Height: 512, ScaleFactor: 1,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer m.Close()

	if err := m.SetStyleURL("file:///path/to/style.prepared.json"); err != nil {
		log.Fatal(err)
	}
	if _, err := m.WaitForEvent(15*time.Second, func(e maplibre.Event) bool {
		return e.Type == maplibre.EventStyleLoaded
	}); err != nil {
		log.Fatal(err)
	}

	if err := m.JumpTo(maplibre.Camera{
		Fields:    maplibre.CameraFieldCenter | maplibre.CameraFieldZoom,
		Latitude:  55.07, Longitude: -3.58, Zoom: 10,
	}); err != nil {
		log.Fatal(err)
	}

	sess, err := m.AttachMetalTexture(512, 512, 1)
	if err != nil {
		log.Fatal(err)
	}
	defer sess.Close()

	frame, err := m.RenderStill(sess, 10*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer sess.ReleaseFrame(frame)

	log.Printf("frame %dx%d at gen=%d, texture=%p",
		frame.Width, frame.Height, frame.Generation, frame.Texture)
}
```

`frame.Texture` is a borrowed `id<MTLTexture>` valid until `ReleaseFrame` is called. To get RGBA bytes today you need to do a Metal `blit -> host-visible buffer -> memcpy` yourself. Once [sargunv/maplibre-native-ffi#9](https://github.com/sargunv/maplibre-native-ffi/issues/9) lands this becomes a single ABI call.

## Threading model

The bindings own one OS thread per runtime via `runtime.LockOSThread` and serialize every ABI call through it. You can call `Runtime`, `Map`, and `TextureSession` methods from any goroutine; they dispatch to the owner thread automatically. The dispatcher pumps `mln_runtime_run_once` between commands at 8 ms intervals so async tile loads and resource fetches make progress.

On macOS each dispatched call is wrapped in `objc_autoreleasePoolPush`/`Pop` because the dispatcher goroutine is not the macOS main thread and has no implicit autorelease pool. Without this, Metal's command-buffer pool deadlocks on first render against any non-trivial style.

## Performance

A worst-case stress benchmark (200 frames, panning across Scotland at zoom 8, klokantech-basic over OpenMapTiles, every frame loads ~1100 fresh tiles) on Apple M-series:

```
frame p50        = 301ms
frame p90        = 602ms
frame p99        = 998ms
max-rss warmup   = 54.8 MiB
max-rss end      = 66.0 MiB
max-rss progression: 54.8 → 60.3 → 61.7 → 66.0 → 66.0 → 66.0 → 66.0 → 66.0 → 66.0 → 66.0 → 66.0
```

RSS plateaus at the native cache cap and stays flat for the rest of the run. Production static-render workloads (one viewport per request, no animation) will be faster than these numbers.

Run your own:

```bash
make bench MLN_FFI_DIR=... # adjust flags in cmd/bench/main.go via -flag=...
```

## Examples

- [`cmd/poc`](cmd/poc/main.go) — full lifecycle demo, prints frame info from a single render.
- [`cmd/bench`](cmd/bench/main.go) — stress benchmark, prints p50/p99 frame time and RSS progression.
- [`examples/sdl3-metal`](examples/sdl3-metal/main.go) — interactive SDL3 window with a Metal compositor that samples maplibre's offscreen texture into a `CAMetalLayer`. Requires `brew install sdl3`. Run with:

  ```bash
  eval "$(make env)"
  go run ./examples/sdl3-metal \
    -style=file:///abs/path/to/style.prepared.json \
    -lat=55.07 -lon=-3.58 -zoom=8
  ```

## License

[BSD 3-Clause](LICENSE), matching upstream maplibre-native-ffi.
