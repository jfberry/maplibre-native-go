// Command static-render renders one map view to a PNG file.
//
//	static-render \
//	  -style=file:///abs/path/to/style.prepared.json \
//	  -lat=55.07 -lon=-3.58 -zoom=10 \
//	  -w=512 -h=512 -o=out.png
//
// Demonstrates the typical static-render production pattern: open
// runtime, create map, load style, jump camera, attach texture session,
// RenderStillImage, encode PNG, exit. Backend (Metal/Vulkan) is selected
// at build time via the per-platform attach_*.go files.
package main

import (
	"context"
	"flag"
	"image"
	"image/png"
	"log"
	"os"
	"time"

	maplibre "github.com/jfberry/maplibre-native-go"
)

func main() {
	style := flag.String("style", "", "style URL/path/JSON (required)")
	lat := flag.Float64("lat", 0, "camera latitude")
	lon := flag.Float64("lon", 0, "camera longitude")
	zoom := flag.Float64("zoom", 0, "camera zoom")
	bearing := flag.Float64("bearing", 0, "camera bearing")
	pitch := flag.Float64("pitch", 0, "camera pitch")
	width := flag.Uint("w", 512, "logical width")
	height := flag.Uint("h", 512, "logical height")
	scale := flag.Float64("scale", 1, "scale factor (1 or 2)")
	output := flag.String("o", "out.png", "output PNG path")
	loadTimeout := flag.Duration("load-timeout", 15*time.Second, "style load timeout")
	frameTimeout := flag.Duration("frame-timeout", 30*time.Second, "render-still timeout")
	flag.Parse()

	if *style == "" {
		log.Fatalf("--style is required")
	}

	rt, err := maplibre.NewRuntime(maplibre.RuntimeOptions{})
	if err != nil {
		log.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	m, err := rt.NewMap(maplibre.MapOptions{
		Width: uint32(*width), Height: uint32(*height), ScaleFactor: *scale,
	})
	if err != nil {
		log.Fatalf("NewMap: %v", err)
	}
	defer m.Close()

	if err := m.LoadStyle(*style); err != nil {
		log.Fatalf("load style: %v", err)
	}
	loadCtx, loadCancel := context.WithTimeout(context.Background(), *loadTimeout)
	defer loadCancel()
	if _, err := m.WaitForEvent(loadCtx, maplibre.EventOfTypes(maplibre.EventStyleLoaded, maplibre.EventMapLoadingFailed)); err != nil {
		log.Fatalf("waiting for STYLE_LOADED: %v", err)
	}

	if err := m.JumpTo(maplibre.Camera{
		Fields: maplibre.CameraFieldCenter | maplibre.CameraFieldZoom |
			maplibre.CameraFieldBearing | maplibre.CameraFieldPitch,
		Latitude:  *lat,
		Longitude: *lon,
		Zoom:      *zoom,
		Bearing:   *bearing,
		Pitch:     *pitch,
	}); err != nil {
		log.Fatalf("JumpTo: %v", err)
	}

	sess, err := attachSession(m, uint32(*width), uint32(*height), *scale)
	if err != nil {
		log.Fatalf("attachSession: %v", err)
	}
	defer sess.Close()

	renderCtx, renderCancel := context.WithTimeout(context.Background(), *frameTimeout)
	defer renderCancel()
	rgba, w, h, err := m.RenderImage(renderCtx, sess)
	if err != nil {
		log.Fatalf("RenderImage: %v", err)
	}

	// maplibre returns premultiplied RGBA; PNG consumers usually want
	// non-premultiplied. Unpremultiply in place into an NRGBA image.
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	maplibre.UnpremultiplyRGBA(img.Pix, rgba)

	f, err := os.Create(*output)
	if err != nil {
		log.Fatalf("Create: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatalf("png.Encode: %v", err)
	}
	log.Printf("wrote %s (%dx%d)", *output, w, h)
}

