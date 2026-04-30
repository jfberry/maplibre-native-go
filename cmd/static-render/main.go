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
	"flag"
	"image"
	"image/png"
	"log"
	"os"
	"strings"
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

	if err := loadStyle(m, *style); err != nil {
		log.Fatalf("load style: %v", err)
	}
	if _, err := m.WaitForEvent(*loadTimeout, func(e maplibre.Event) bool {
		return e.Type == maplibre.EventStyleLoaded || e.Type == maplibre.EventMapLoadingFailed
	}); err != nil {
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

	rgba, w, h, _, err := m.RenderStillImage(sess, *frameTimeout)
	if err != nil {
		log.Fatalf("RenderStillImage: %v", err)
	}

	// maplibre returns premultiplied RGBA; PNG consumers usually want
	// non-premultiplied. Unpremultiply in place into an NRGBA image.
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	unpremultiply(img.Pix, rgba)

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

// unpremultiply converts premultiplied RGBA bytes to non-premultiplied,
// writing into dst (which must be the same length as src). Pixels with
// alpha 0 or 255 short-circuit since the math is identity.
func unpremultiply(dst, src []byte) {
	for i := 0; i < len(src); i += 4 {
		r, g, b, a := src[i], src[i+1], src[i+2], src[i+3]
		if a == 0 || a == 255 {
			dst[i+0] = r
			dst[i+1] = g
			dst[i+2] = b
			dst[i+3] = a
			continue
		}
		// c' = c * 255 / a, rounded.
		dst[i+0] = byte((uint32(r)*255 + uint32(a)/2) / uint32(a))
		dst[i+1] = byte((uint32(g)*255 + uint32(a)/2) / uint32(a))
		dst[i+2] = byte((uint32(b)*255 + uint32(a)/2) / uint32(a))
		dst[i+3] = a
	}
}

func loadStyle(m *maplibre.Map, style string) error {
	switch {
	case strings.HasPrefix(style, "{"):
		return m.SetStyleJSON(style)
	case strings.Contains(style, "://"):
		return m.SetStyleURL(style)
	default:
		return m.SetStyleURL("file://" + style)
	}
}
