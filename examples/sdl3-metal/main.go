// SDL3 + Metal + maplibre interactive example.
//
// Opens an SDL3 window with a Metal layer, runs a maplibre map off-screen
// into a texture session sharing the window's MTLDevice, and samples the
// resulting texture into the window each frame via a tiny render pass.
//
// Demonstrates the binding's full lifecycle outside cmd/poc and cmd/bench:
// runtime / map / style / camera / texture session / render-still / external
// device sharing / per-frame composite.
//
// Build requires SDL3 from Homebrew (`brew install sdl3`) so the cgo
// directives below resolve via pkg-config.
package main

/*
#cgo darwin pkg-config: sdl3
#cgo darwin CFLAGS: -fobjc-arc
#cgo darwin LDFLAGS: -framework Metal -framework QuartzCore -framework Foundation

#include <stdlib.h>
#include <stdint.h>
#include <SDL3/SDL.h>
#include <SDL3/SDL_metal.h>

// compositor_darwin.m exports.
typedef struct mln_compositor mln_compositor;
mln_compositor *compositor_create(void *layer);
void           *compositor_device(mln_compositor *c);
void            compositor_resize(mln_compositor *c, double width, double height);
int             compositor_draw(mln_compositor *c, void *texture);
const char     *compositor_last_error(mln_compositor *c);
void            compositor_destroy(mln_compositor *c);

// SDL3's SDL_Event is a union; cgo can't index into it ergonomically. The
// first 4 bytes are always Uint32 type; this helper returns it.
static inline uint32_t mln_event_type(SDL_Event *e) { return e->type; }
*/
import "C"

import (
	"flag"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"
	"unsafe"

	maplibre "github.com/jfberry/maplibre-native-go"
)

func init() {
	// Cocoa AppKit requires its event loop on the main OS thread. Go's main
	// goroutine starts there but may be moved by the scheduler; lock it.
	runtime.LockOSThread()
}

func sdlError() error {
	return fmt.Errorf("SDL: %s", C.GoString(C.SDL_GetError()))
}

func main() {
	style := flag.String("style", "", "style URL/path/JSON (default empty style)")
	lat := flag.Float64("lat", 55.07, "camera latitude")
	lon := flag.Float64("lon", -3.58, "camera longitude")
	zoom := flag.Float64("zoom", 8, "camera zoom")
	width := flag.Int("w", 800, "logical window width")
	height := flag.Int("h", 600, "logical window height")
	loadTimeout := flag.Duration("load-timeout", 15*time.Second, "style load timeout")
	frameTimeout := flag.Duration("frame-timeout", 10*time.Second, "RenderStill timeout")
	flag.Parse()

	log.SetFlags(log.Lmicroseconds)

	if !C.SDL_Init(C.SDL_INIT_VIDEO) {
		log.Fatalf("SDL_Init: %v", sdlError())
	}
	defer C.SDL_Quit()

	title := C.CString("maplibre-native-go SDL3 metal example")
	defer C.free(unsafe.Pointer(title))

	const flags = C.SDL_WINDOW_METAL | C.SDL_WINDOW_RESIZABLE | C.SDL_WINDOW_HIGH_PIXEL_DENSITY
	window := C.SDL_CreateWindow(title, C.int(*width), C.int(*height), flags)
	if window == nil {
		log.Fatalf("SDL_CreateWindow: %v", sdlError())
	}
	defer C.SDL_DestroyWindow(window)

	view := C.SDL_Metal_CreateView(window)
	if view == nil {
		log.Fatalf("SDL_Metal_CreateView: %v", sdlError())
	}
	defer C.SDL_Metal_DestroyView(view)

	layer := C.SDL_Metal_GetLayer(view)
	if layer == nil {
		log.Fatalf("SDL_Metal_GetLayer: %v", sdlError())
	}

	comp := C.compositor_create(layer)
	if comp == nil {
		log.Fatal("compositor_create: failed")
	}
	defer C.compositor_destroy(comp)

	device := C.compositor_device(comp)
	if device == nil {
		log.Fatal("compositor_device: nil")
	}

	rt, err := maplibre.NewRuntime(maplibre.RuntimeOptions{})
	if err != nil {
		log.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	m, err := rt.NewMap(maplibre.MapOptions{
		Width: uint32(*width), Height: uint32(*height), ScaleFactor: 1,
	})
	if err != nil {
		log.Fatalf("NewMap: %v", err)
	}
	defer m.Close()

	if err := loadStyle(m, *style); err != nil {
		log.Fatalf("load style: %v", err)
	}
	if _, err := m.WaitForEvent(*loadTimeout, func(e maplibre.Event) bool {
		return e.Type == maplibre.EventStyleLoaded || e.Type == maplibre.EventMapLoadingFailed
	}); err != nil {
		log.Fatalf("waiting for STYLE_LOADED: %v", err)
	}
	log.Printf("style loaded")

	if err := m.JumpTo(maplibre.Camera{
		Fields:    maplibre.CameraFieldCenter | maplibre.CameraFieldZoom,
		Latitude:  *lat, Longitude: *lon, Zoom: *zoom,
	}); err != nil {
		log.Fatalf("JumpTo: %v", err)
	}

	sess, err := m.AttachMetalTextureWithDevice(unsafe.Pointer(device),
		uint32(*width), uint32(*height), 1)
	if err != nil {
		log.Fatalf("AttachMetalTextureWithDevice: %v", err)
	}
	defer sess.Close()

	frame, renders, err := m.RenderStill(sess, *frameTimeout)
	if err != nil {
		log.Fatalf("RenderStill: %v", err)
	}
	log.Printf("rendered: %d render calls; texture %dx%d gen=%d",
		renders, frame.Width, frame.Height, frame.Generation)
	defer sess.ReleaseFrame(frame)

	C.compositor_resize(comp, C.double(frame.Width), C.double(frame.Height))

	log.Printf("compositing; close window to exit")
	var ev C.SDL_Event
	running := true
	for running {
		for C.SDL_PollEvent(&ev) {
			switch C.mln_event_type(&ev) {
			case C.SDL_EVENT_QUIT, C.SDL_EVENT_WINDOW_CLOSE_REQUESTED:
				running = false
			}
		}
		if rc := C.compositor_draw(comp, frame.Texture); rc != 0 {
			log.Printf("compositor_draw rc=%d: %s", rc,
				C.GoString(C.compositor_last_error(comp)))
		}
		C.SDL_Delay(16)
	}
}

func loadStyle(m *maplibre.Map, style string) error {
	switch {
	case style == "":
		return m.SetStyleJSON(`{"version":8,"sources":{},"layers":[]}`)
	case strings.HasPrefix(style, "{"):
		return m.SetStyleJSON(style)
	case strings.Contains(style, "://"):
		return m.SetStyleURL(style)
	default:
		return m.SetStyleURL("file://" + style)
	}
}
