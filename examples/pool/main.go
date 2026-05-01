// Command pool demonstrates a pool of Sessions, each on its own
// dedicated OS thread, to render multiple styles concurrently.
//
// Each Session owns one Runtime, so a pool of N Sessions parallelises
// across N OS threads. This is the recommended shape for any service
// that needs to render multiple maps in parallel — keep one Session
// per worker goroutine, and dispatch render requests to whichever
// Session is free.
//
// Two demonstrations:
//
//   - Concurrent rendering: each session renders 3 frames at slightly
//     different camera positions; total wall time should be roughly
//     1/N the serial time.
//
//   - In-place style swap: after the first batch, every Session swaps
//     to a different style (in parallel) without rebuilding. The
//     second batch renders the new style.
//
// Run:
//
//	go run ./examples/pool -workers=4 -frames=3 \
//	  -style1='{"version":8,...}' -style2='{"version":8,...}' -o=out
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	maplibre "github.com/jfberry/maplibre-native-go"
)

const (
	defaultStyle1 = `{"version":8,"sources":{},"layers":[{"id":"bg","type":"background","paint":{"background-color":"#0033CC"}}]}`
	defaultStyle2 = `{"version":8,"sources":{},"layers":[{"id":"bg","type":"background","paint":{"background-color":"#FFAA00"}}]}`
)

type renderRequest struct {
	id     int
	camera maplibre.Camera
}

type renderResult struct {
	id     int
	worker int
	rgba   []byte
	width  int
	height int
	err    error
}

func main() {
	style1 := flag.String("style1", defaultStyle1, "first style (URL, file path, or JSON)")
	style2 := flag.String("style2", defaultStyle2, "second style (URL, file path, or JSON)")
	workers := flag.Int("workers", 4, "number of parallel sessions")
	frames := flag.Int("frames", 3, "frames per worker per style")
	width := flag.Uint("w", 256, "logical width")
	height := flag.Uint("h", 256, "logical height")
	output := flag.String("o", "", "output directory for PNGs (empty: don't save)")
	timeout := flag.Duration("timeout", 30*time.Second, "overall timeout")
	flag.Parse()

	log.SetFlags(log.Lmicroseconds)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	pool, err := newPool(ctx, *workers, maplibre.SessionOptions{
		Map:   maplibre.MapOptions{Width: uint32(*width), Height: uint32(*height), ScaleFactor: 1},
		Style: *style1,
	})
	if err != nil {
		log.Fatalf("newPool: %v", err)
	}
	defer pool.Close()
	log.Printf("pool ready: %d sessions, style1 loaded", *workers)

	// Build a request batch that exercises every worker at least frames
	// times. Sweep camera positions so consecutive renders aren't
	// trivially-identical (would still hit the same tile cache, but
	// camera state is now per-worker).
	total := *workers * *frames
	requests := make([]renderRequest, total)
	for i := range requests {
		requests[i] = renderRequest{
			id: i,
			camera: maplibre.Camera{
				Fields:    maplibre.CameraFieldCenter | maplibre.CameraFieldZoom,
				Latitude:  float64(i) * 0.5,
				Longitude: float64(i) * 0.7,
				Zoom:      float64(2 + i%6),
			},
		}
	}

	// Pass 1: style1.
	t0 := time.Now()
	results := pool.Run(ctx, requests)
	d1 := time.Since(t0)
	report("style1", *workers, results, d1)
	if *output != "" {
		savePNGs(*output, "style1", results)
	}

	// In-place swap to style2 across all workers in parallel.
	t1 := time.Now()
	if err := pool.SetStyleAll(ctx, *style2); err != nil {
		log.Fatalf("SetStyleAll: %v", err)
	}
	d2 := time.Since(t1)
	log.Printf("style2 swapped in across %d sessions in %s", *workers, d2.Round(time.Millisecond))

	t2 := time.Now()
	results = pool.Run(ctx, requests)
	d3 := time.Since(t2)
	report("style2", *workers, results, d3)
	if *output != "" {
		savePNGs(*output, "style2", results)
	}
}

// pool dispatches render requests across N Sessions running on N
// OS threads. Each Session is owned by exactly one worker goroutine.
type pool struct {
	sessions []*maplibre.Session
}

// newPool spins up workers Sessions in parallel. If any one fails, all
// already-built sessions are closed and the error is returned.
func newPool(ctx context.Context, workers int, opts maplibre.SessionOptions) (*pool, error) {
	if workers < 1 {
		return nil, fmt.Errorf("workers must be >= 1, got %d", workers)
	}

	type result struct {
		idx int
		s   *maplibre.Session
		err error
	}
	results := make(chan result, workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			s, err := maplibre.NewSession(ctx, opts)
			results <- result{idx: i, s: s, err: err}
		}(i)
	}

	sessions := make([]*maplibre.Session, workers)
	var firstErr error
	for i := 0; i < workers; i++ {
		r := <-results
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		sessions[r.idx] = r.s
	}
	if firstErr != nil {
		for _, s := range sessions {
			if s != nil {
				_ = s.Close()
			}
		}
		return nil, fmt.Errorf("newPool: %w", firstErr)
	}
	return &pool{sessions: sessions}, nil
}

// Run distributes requests across the pool, one render per worker at a
// time, and returns the results in request-id order. Errors are surfaced
// per-result rather than aborting the batch.
func (p *pool) Run(ctx context.Context, requests []renderRequest) []renderResult {
	out := make([]renderResult, len(requests))
	jobs := make(chan renderRequest, len(requests))
	for _, req := range requests {
		jobs <- req
	}
	close(jobs)

	var wg sync.WaitGroup
	for i, sess := range p.sessions {
		wg.Add(1)
		go func(workerID int, sess *maplibre.Session) {
			defer wg.Done()
			for req := range jobs {
				if err := sess.JumpTo(req.camera); err != nil {
					out[req.id] = renderResult{id: req.id, worker: workerID, err: fmt.Errorf("JumpTo: %w", err)}
					continue
				}
				rgba, w, h, err := sess.Render(ctx)
				out[req.id] = renderResult{id: req.id, worker: workerID, rgba: rgba, width: w, height: h, err: err}
			}
		}(i, sess)
	}
	wg.Wait()
	return out
}

// SetStyleAll swaps every session to the new style in parallel and
// blocks until each one's STYLE_LOADED has fired. This is the in-place
// rotate path: existing Maps are reused, no Runtime/Map rebuild.
func (p *pool) SetStyleAll(ctx context.Context, style string) error {
	errs := make(chan error, len(p.sessions))
	for _, s := range p.sessions {
		go func(s *maplibre.Session) {
			errs <- s.SetStyle(ctx, style)
		}(s)
	}
	var firstErr error
	for range p.sessions {
		if err := <-errs; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close tears down all sessions. Errors from individual closes are logged
// but do not stop the rest.
func (p *pool) Close() {
	for i, s := range p.sessions {
		if err := s.Close(); err != nil {
			log.Printf("session %d Close: %v", i, err)
		}
	}
}

func report(label string, workers int, results []renderResult, elapsed time.Duration) {
	failed := 0
	for _, r := range results {
		if r.err != nil {
			log.Printf("[%s] result %d (worker %d) FAIL: %v", label, r.id, r.worker, r.err)
			failed++
		}
	}
	ok := len(results) - failed
	log.Printf("[%s] %d frames in %s across %d workers: %.1f fps (%d ok, %d failed)",
		label, len(results), elapsed.Round(time.Millisecond), workers,
		float64(len(results))/elapsed.Seconds(), ok, failed)
}

func savePNGs(dir, prefix string, results []renderResult) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("MkdirAll: %v", err)
		return
	}
	for _, r := range results {
		if r.err != nil {
			continue
		}
		img := image.NewNRGBA(image.Rect(0, 0, r.width, r.height))
		unpremultiply(img.Pix, r.rgba)
		path := filepath.Join(dir, fmt.Sprintf("%s-%03d-w%d.png", prefix, r.id, r.worker))
		f, err := os.Create(path)
		if err != nil {
			log.Printf("Create %s: %v", path, err)
			continue
		}
		if err := png.Encode(f, img); err != nil {
			log.Printf("png.Encode %s: %v", path, err)
		}
		_ = f.Close()
	}
	log.Printf("[%s] PNGs written to %s", prefix, dir)
}

// unpremultiply converts premultiplied RGBA to non-premultiplied.
func unpremultiply(dst, src []byte) {
	for i := 0; i < len(src); i += 4 {
		r, g, b, a := src[i], src[i+1], src[i+2], src[i+3]
		if a == 0 || a == 255 {
			dst[i+0], dst[i+1], dst[i+2], dst[i+3] = r, g, b, a
			continue
		}
		dst[i+0] = byte((uint32(r)*255 + uint32(a)/2) / uint32(a))
		dst[i+1] = byte((uint32(g)*255 + uint32(a)/2) / uint32(a))
		dst[i+2] = byte((uint32(b)*255 + uint32(a)/2) / uint32(a))
		dst[i+3] = a
	}
}
