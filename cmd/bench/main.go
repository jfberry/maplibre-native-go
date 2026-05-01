// Command bench renders many frames against a real style/mbtiles to measure
// per-frame timing and steady-state RSS.
//
// The camera pans across a configurable grid between frames, forcing tile
// churn so the benchmark stresses the resource pipeline, not just shader
// re-execution. Output covers warmup vs end RSS, p50/p99 frame time, and
// total frames-per-second.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	maplibre "github.com/jfberry/maplibre-native-go"
)

func main() {
	style := flag.String("style", "", "style URL, file path, or inline JSON (required)")
	lat := flag.Float64("lat", 55.07, "starting latitude")
	lon := flag.Float64("lon", -3.58, "starting longitude")
	zoom := flag.Float64("zoom", 8, "starting zoom")
	bearing := flag.Float64("bearing", 0, "starting bearing")
	pitch := flag.Float64("pitch", 0, "starting pitch")
	width := flag.Uint("w", 512, "logical width")
	height := flag.Uint("h", 512, "logical height")
	scale := flag.Float64("scale", 1, "scale factor")
	warmup := flag.Int("warmup", 5, "warmup frames before measurement")
	frames := flag.Int("frames", 100, "frames to render after warmup")
	dLat := flag.Float64("dlat", 0.01, "lat delta per measured frame")
	dLon := flag.Float64("dlon", 0.01, "lon delta per measured frame")
	frameTimeout := flag.Duration("frame-timeout", 10*time.Second, "per-frame RenderStill timeout")
	loadTimeout := flag.Duration("load-timeout", 15*time.Second, "style-load timeout")
	verbose := flag.Bool("v", false, "log per-frame timings")
	flag.Parse()

	if *style == "" {
		log.Fatalf("--style is required")
	}

	log.SetFlags(log.Lmicroseconds)
	log.Printf("ABI v%d", maplibre.ABIVersion())

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
	loadCtx, loadCancel := context.WithTimeout(context.Background(), *loadTimeout)
	defer loadCancel()
	if _, err := m.WaitForEvent(loadCtx, func(e maplibre.Event) bool {
		return e.Type == maplibre.EventStyleLoaded || e.Type == maplibre.EventMapLoadingFailed
	}); err != nil {
		log.Fatalf("waiting for STYLE_LOADED: %v", err)
	}
	log.Printf("style loaded")

	sess, err := attachSession(m, uint32(*width), uint32(*height), *scale)
	if err != nil {
		log.Fatalf("attachSession: %v", err)
	}
	defer sess.Close()

	jump := func(i int) error {
		return m.JumpTo(maplibre.Camera{
			Fields:    maplibre.CameraFieldCenter | maplibre.CameraFieldZoom | maplibre.CameraFieldBearing | maplibre.CameraFieldPitch,
			Latitude:  *lat + float64(i)*(*dLat),
			Longitude: *lon + float64(i)*(*dLon),
			Zoom:      *zoom,
			Bearing:   *bearing,
			Pitch:     *pitch,
		})
	}

	renderOne := func(i int) (dur time.Duration, err error) {
		if err = jump(i); err != nil {
			return 0, fmt.Errorf("JumpTo: %w", err)
		}
		t0 := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), *frameTimeout)
		frame, err := m.RenderStill(ctx, sess)
		cancel()
		dur = time.Since(t0)
		if err != nil {
			return dur, err
		}
		if relErr := sess.ReleaseFrame(frame); relErr != nil {
			return dur, fmt.Errorf("ReleaseFrame: %w", relErr)
		}
		return dur, nil
	}

	// Warmup: get sprite/glyph/initial-tile churn out of the way.
	log.Printf("warmup: %d frames", *warmup)
	for i := 0; i < *warmup; i++ {
		if _, err := renderOne(i); err != nil {
			log.Fatalf("warmup frame %d: %v", i, err)
		}
	}
	rssWarmup := maxRSSKB()
	log.Printf("warmup done; max-rss=%s", fmtKB(rssWarmup))

	// Measured run.
	timings := make([]time.Duration, 0, *frames)
	rssSamples := []int64{rssWarmup}

	start := time.Now()
	for i := 0; i < *frames; i++ {
		dur, err := renderOne(*warmup + i)
		if err != nil {
			log.Fatalf("frame %d: %v", i, err)
		}
		timings = append(timings, dur)
		if *verbose {
			log.Printf("frame %d: %s", i, dur)
		}
		if (i+1)%(maxInt(*frames/10, 1)) == 0 {
			rssSamples = append(rssSamples, maxRSSKB())
		}
	}
	elapsed := time.Since(start)
	rssEnd := maxRSSKB()

	sort.Slice(timings, func(i, j int) bool { return timings[i] < timings[j] })
	p := func(q float64) time.Duration {
		idx := int(float64(len(timings)-1) * q)
		return timings[idx]
	}

	log.Printf("=== bench results ===")
	log.Printf("frames           = %d", *frames)
	log.Printf("total            = %s", elapsed.Round(time.Millisecond))
	log.Printf("fps              = %.1f", float64(*frames)/elapsed.Seconds())
	log.Printf("frame p50        = %s", p(0.5).Round(time.Microsecond))
	log.Printf("frame p90        = %s", p(0.9).Round(time.Microsecond))
	log.Printf("frame p99        = %s", p(0.99).Round(time.Microsecond))
	log.Printf("frame max        = %s", timings[len(timings)-1].Round(time.Microsecond))
	log.Printf("max-rss warmup   = %s", fmtKB(rssWarmup))
	log.Printf("max-rss end      = %s", fmtKB(rssEnd))
	log.Printf("max-rss delta    = %s", fmtKBDelta(rssEnd-rssWarmup))
	log.Printf("max-rss progression: %v", samplesProgression(rssSamples))

	var goMS runtime.MemStats
	runtime.ReadMemStats(&goMS)
	log.Printf("go heap          = %s (sys=%s)", fmtKB(int64(goMS.HeapInuse)/1024), fmtKB(int64(goMS.Sys)/1024))
}

// maxRSSKB returns peak RSS in KB. On Darwin getrusage Maxrss is bytes; on
// Linux it's kilobytes.
func maxRSSKB() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	if isDarwin() {
		return int64(ru.Maxrss) / 1024
	}
	return int64(ru.Maxrss)
}

func isDarwin() bool { return runtime.GOOS == "darwin" }

func fmtKB(kb int64) string {
	if kb >= 1024 {
		return fmt.Sprintf("%.1f MiB", float64(kb)/1024)
	}
	return fmt.Sprintf("%d KiB", kb)
}

func fmtKBDelta(kb int64) string {
	sign := "+"
	if kb < 0 {
		sign = ""
	}
	return sign + fmtKB(kb)
}

func samplesProgression(samples []int64) string {
	parts := make([]string, len(samples))
	for i, s := range samples {
		parts[i] = fmtKB(s)
	}
	return strings.Join(parts, " -> ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
