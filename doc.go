// Package maplibre provides Go bindings for the maplibre-native-ffi C
// ABI. It targets server-side and headless map rendering: load a
// style, point a camera, hand back a buffer of premultiplied RGBA
// bytes.
//
// # Quick start
//
//	import (
//	    "context"
//	    "time"
//	    maplibre "github.com/jfberry/maplibre-native-go"
//	)
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//
//	sess, err := maplibre.NewSession(ctx, maplibre.SessionOptions{
//	    Map:   maplibre.MapOptions{Width: 512, Height: 512, ScaleFactor: 1},
//	    Style: "https://demotiles.maplibre.org/style.json",
//	})
//	if err != nil { return err }
//	defer sess.Close()
//
//	if err := sess.JumpTo(maplibre.Camera{
//	    Fields:    maplibre.CameraFieldCenter | maplibre.CameraFieldZoom,
//	    Latitude:  55.07, Longitude: -3.58, Zoom: 6,
//	}); err != nil { return err }
//
//	rgba, w, h, err := sess.Render(ctx)
//	if err != nil { return err }
//	// rgba is w*h*4 bytes of premultiplied RGBA, top-left origin.
//
// Session bundles the three handles you almost always want together:
// Runtime, Map, RenderSession. Use it whenever you don't need
// finer-grained control. For multiple concurrent render workers, see
// examples/pool — one Session per OS thread.
//
// # Concept map
//
//   - Runtime owns one OS thread (the "owner thread"). All native ABI
//     calls into the underlying maplibre handle dispatch through that
//     thread. From Go, every method on every type is safe to call from
//     any goroutine — calls serialise behind the dispatcher.
//
//   - Map is created on a Runtime. It owns the style, camera, and
//     projection state. Multiple Maps can share one Runtime, but
//     parallelism comes from spawning multiple Runtimes since each is
//     pinned to a single OS thread.
//
//   - RenderSession attaches a backend-specific render target (Metal
//     on darwin, Vulkan on linux) to a Map. Resize is supported in
//     place. Use Map.AttachTexture for the platform default, or
//     AttachMetalTexture / AttachVulkanTexture for explicit backend
//     choice and device injection.
//
//   - Session is a high-level bundle of Runtime + Map + RenderSession.
//     NewSession blocks until the style is loaded, in-place SetStyle
//     swaps reuse the same handles, and Render / RenderInto return
//     RGBA bytes directly.
//
//   - Projection is a standalone helper that snapshots a Map's
//     transform. Convert coordinates and fit cameras without touching
//     the live render loop.
//
// # Errors
//
// Every native error is reported as *Error wrapping a Status. Use
// errors.Is to match by status:
//
//	if errors.Is(err, maplibre.ErrInvalidState) { ... }
//	if errors.Is(err, maplibre.ErrTimeout)      { ... }
//	if errors.Is(err, context.DeadlineExceeded) { ... } // context-driven timeouts
//
// or errors.As for richer inspection:
//
//	var mlnErr *maplibre.Error
//	if errors.As(err, &mlnErr) {
//	    log.Printf("op=%s status=%s msg=%s", mlnErr.Op, mlnErr.Status, mlnErr.Message)
//	}
//
// # Cancellation
//
// Operations that wait on mbgl-internal events (WaitForEvent,
// RenderStill, RenderImage, NewSession) take a context.Context. On
// ctx.Done() they return ctx.Err() wrapped in ErrTimeout, so both
// errors.Is(err, ErrTimeout) and errors.Is(err, context.DeadlineExceeded)
// match.
//
// # Threading model
//
// One Runtime = one OS thread. Within a Runtime, every native ABI
// call serialises on the dispatcher goroutine, so multiple goroutines
// may share one Runtime without locking — but they will not render
// in parallel. For parallel rendering, spawn N Runtimes (or N
// Sessions, which is the same thing with a higher-level API) and
// distribute work across them. examples/pool demonstrates the
// pattern.
//
// Process-global state (network reachability, log callback) lives
// outside any Runtime. See GetNetworkStatus / SetNetworkStatus and
// InstallLogCallback / ClearLogCallback / SetLogAsyncSeverityMask.
//
// # Resource customisation
//
// Runtime.SetResourceURLTransform installs a runtime-scoped URL
// rewrite for HTTP/HTTPS resources, applied before mbgl's online
// file source dispatches each request. Must be called before any
// Map is created on the runtime; see the doc comment on the method
// for the lifetime contract.
//
// Built-in schemes (file://, asset://, mbtiles://, pmtiles://) are
// resolved by mbgl's MainResourceLoader and do not go through the
// transform; nested PMTiles network range requests do.
//
// # Build requirements
//
// The binding links against libmaplibre-native-c via pkg-config.
// Provide PKG_CONFIG_PATH pointing at the directory containing
// maplibre-native-c.pc. The repo's Makefile sets this up against
// $MLN_FFI_DIR; see README.md for details.
//
// # Build tags
//
//   - mln_debug — installs runtime finalizers on Runtime, Map,
//     RenderSession, Session, and Projection that print a warning
//     to stderr if the resource was garbage-collected without a
//     prior Close. Off by default; use during development.
package maplibre
