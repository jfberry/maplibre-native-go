// Command poc exercises the maplibre-native-go bindings end to end:
// runtime -> map -> style -> camera -> Metal texture session -> render ->
// acquire/release frame -> teardown.
//
// Flags:
//
//	-style    URL or inline JSON for the map style. Default: empty style.
//	-lat,-lon,-zoom,-bearing,-pitch  Camera target.
//	-w,-h     Logical map dimensions in pixels.
//	-scale    Backing-texture scale factor (1 or 2 typical).
//	-timeout  Style-load timeout.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	maplibre "github.com/jfberry/maplibre-native-go"
)

func main() {
	style := flag.String("style", "", "style URL, file path, or inline JSON (empty = empty style)")
	lat := flag.Float64("lat", 0, "camera latitude")
	lon := flag.Float64("lon", 0, "camera longitude")
	zoom := flag.Float64("zoom", 0, "camera zoom")
	bearing := flag.Float64("bearing", 0, "camera bearing")
	pitch := flag.Float64("pitch", 0, "camera pitch")
	width := flag.Uint("w", 512, "logical map width")
	height := flag.Uint("h", 512, "logical map height")
	scale := flag.Float64("scale", 1, "scale factor")
	timeout := flag.Duration("timeout", 5*time.Second, "style load timeout")
	flag.Parse()

	log.SetFlags(log.Lmicroseconds)
	log.Printf("ABI v%d", maplibre.ABIVersion())

	rt, err := maplibre.NewRuntime(maplibre.RuntimeOptions{})
	if err != nil {
		log.Fatalf("NewRuntime: %v", err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			log.Printf("Runtime.Close: %v", err)
		}
	}()
	log.Printf("runtime ready")

	m, err := rt.NewMap(maplibre.MapOptions{
		Width:       uint32(*width),
		Height:      uint32(*height),
		ScaleFactor: *scale,
	})
	if err != nil {
		log.Fatalf("NewMap: %v", err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			log.Printf("Map.Close: %v", err)
		}
	}()
	log.Printf("map ready (%dx%d @%v)", *width, *height, *scale)

	if err := loadStyle(m, *style); err != nil {
		log.Fatalf("loadStyle: %v", err)
	}
	loadCtx, loadCancel := context.WithTimeout(context.Background(), *timeout)
	defer loadCancel()
	if _, err := m.WaitForEvent(loadCtx, func(e maplibre.Event) bool {
		log.Printf("event: %s code=%d msg=%q", e.Type, e.Code, e.Message)
		return e.Type == maplibre.EventStyleLoaded || e.Type == maplibre.EventMapLoadingFailed
	}); err != nil {
		log.Fatalf("waiting for STYLE_LOADED: %v", err)
	}
	log.Printf("style loaded")

	cam := maplibre.Camera{
		Fields:    maplibre.CameraFieldCenter | maplibre.CameraFieldZoom | maplibre.CameraFieldBearing | maplibre.CameraFieldPitch,
		Latitude:  *lat,
		Longitude: *lon,
		Zoom:      *zoom,
		Bearing:   *bearing,
		Pitch:     *pitch,
	}
	if err := m.JumpTo(cam); err != nil {
		log.Fatalf("JumpTo: %v", err)
	}
	got, _ := m.GetCamera()
	log.Printf("camera: lat=%.4f lon=%.4f zoom=%.2f bearing=%.1f pitch=%.1f",
		got.Latitude, got.Longitude, got.Zoom, got.Bearing, got.Pitch)

	sess, err := attachSession(m, uint32(*width), uint32(*height), *scale)
	if err != nil {
		log.Fatalf("attachSession: %v", err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			log.Printf("TextureSession.Close: %v", err)
		}
	}()
	log.Printf("metal texture session attached")

	renderCtx, renderCancel := context.WithTimeout(context.Background(), *timeout)
	defer renderCancel()
	frame, err := m.RenderStill(renderCtx, sess)
	if err != nil {
		log.Fatalf("RenderStill: %v", err)
	}
	log.Printf("still rendered")
	log.Printf("frame: gen=%d id=%d %dx%d @%v fmt=%d texture=%p device=%p",
		frame.Generation, frame.FrameID, frame.Width, frame.Height,
		frame.ScaleFactor, frame.PixelFormat, frame.Texture, frame.Device)

	if err := sess.ReleaseFrame(frame); err != nil {
		log.Fatalf("ReleaseFrame: %v", err)
	}
	if err := sess.Detach(); err != nil {
		log.Fatalf("Detach: %v", err)
	}
	log.Printf("done")
}

// loadStyle accepts a URL, a filesystem path, an inline JSON document, or "".
func loadStyle(m *maplibre.Map, style string) error {
	if style == "" {
		return m.SetStyleJSON(`{"version":8,"sources":{},"layers":[]}`)
	}
	return m.LoadStyle(style)
}
