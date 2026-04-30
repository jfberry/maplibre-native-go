// Command render-worker is a long-lived subprocess that renders maps on
// demand via a binary frame protocol on stdin/stdout.
//
// Wire format (matches rampardos-render-worker-rs and the Node worker):
//
//	| 1 byte type | 4 bytes BE uint32 length | length bytes payload |
//
// Frame types:
//
//	'H' handshake (worker -> orchestrator) JSON {pid, style}, sent once at startup
//	'R' request   (orchestrator -> worker) JSON {zoom, center: [lng,lat], width, height, bearing?, pitch?}
//	'K' ok        (worker -> orchestrator) raw premultiplied RGBA bytes (width*height*4*ratio^2)
//	'E' error     (worker -> orchestrator) UTF-8 error message
//
// The same orchestrator that drives the Node and Rust workers in
// rampardos-rust-poc/rampardos-render-worker-rs will accept this Go
// worker as a drop-in third backend; only its --bench arg list needs to
// gain a `--go-binary <path>` flag.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	maplibre "github.com/jfberry/maplibre-native-go"
)

const (
	frameRequest   byte = 'R'
	frameOK        byte = 'K'
	frameError     byte = 'E'
	frameHandshake byte = 'H'
	maxFrameSize        = 64 * 1024 * 1024
)

type renderRequest struct {
	Zoom    float64    `json:"zoom"`
	Center  [2]float64 `json:"center"` // [lng, lat]
	Width   uint32     `json:"width"`
	Height  uint32     `json:"height"`
	Bearing float64    `json:"bearing,omitempty"`
	Pitch   float64    `json:"pitch,omitempty"`
}

type handshakeMsg struct {
	PID   int    `json:"pid"`
	Style string `json:"style"`
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > maxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", len(payload), maxFrameSize)
	}
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func readFrame(r io.Reader) (typ byte, payload []byte, err error) {
	var hdr [5]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	typ = hdr[0]
	length := binary.BigEndian.Uint32(hdr[1:5])
	if length > maxFrameSize {
		return typ, nil, fmt.Errorf("frame payload too large: %d > %d", length, maxFrameSize)
	}
	payload = make([]byte, length)
	if length > 0 {
		if _, err = io.ReadFull(r, payload); err != nil {
			return typ, nil, err
		}
	}
	return typ, payload, nil
}

func main() {
	stylePath := flag.String("style-path", "", "absolute path to style JSON (required)")
	mbtiles := flag.String("mbtiles", "", "absolute path to mbtiles (accepted for CLI compat; the style references it via mbtiles:// URL)")
	stylesDir := flag.String("styles-dir", "", "absolute path to styles root (CLI compat)")
	fontsDir := flag.String("fonts-dir", "", "absolute path to fonts root (CLI compat)")
	styleID := flag.String("style-id", "go", "style identifier reported in handshake")
	ratio := flag.Uint("ratio", 1, "pixel ratio (DPI multiplier)")
	loadTimeout := flag.Duration("load-timeout", 30*time.Second, "style load timeout")
	frameTimeout := flag.Duration("frame-timeout", 30*time.Second, "per-render timeout")
	flag.Parse()

	// Logs to stderr so they don't collide with the stdout frame stream.
	log.SetOutput(os.Stderr)

	_ = mbtiles
	_ = stylesDir
	_ = fontsDir

	if *stylePath == "" {
		fail("--style-path is required")
	}

	if err := run(*stylePath, *styleID, uint32(*ratio), *loadTimeout, *frameTimeout); err != nil {
		fail(fmt.Sprintf("worker: %v", err))
	}
}

func fail(msg string) {
	// Best-effort error frame so the orchestrator sees a structured failure.
	_ = writeFrame(os.Stdout, frameError, []byte(msg))
	log.Printf("render-worker-go startup failed: %s", msg)
	os.Exit(1)
}

func run(stylePath, styleID string, ratio uint32, loadTimeout, frameTimeout time.Duration) error {
	// Handshake first so the orchestrator can fail fast on later setup errors
	// vs handshake-never-arrived.
	hs := handshakeMsg{PID: os.Getpid(), Style: styleID}
	hsJSON, err := json.Marshal(hs)
	if err != nil {
		return fmt.Errorf("marshalling handshake: %w", err)
	}
	if err := writeFrame(os.Stdout, frameHandshake, hsJSON); err != nil {
		return fmt.Errorf("writing handshake: %w", err)
	}

	rt, err := maplibre.NewRuntime(maplibre.RuntimeOptions{})
	if err != nil {
		return fmt.Errorf("NewRuntime: %w", err)
	}
	defer rt.Close()

	// Map dimensions are picked at attach time. We start with 1x1 and resize
	// on first request; cheaper than a guess that's almost certainly wrong.
	m, err := rt.NewMap(maplibre.MapOptions{Width: 1, Height: 1, ScaleFactor: float64(ratio)})
	if err != nil {
		return fmt.Errorf("NewMap: %w", err)
	}
	defer m.Close()

	if err := loadStyle(m, stylePath); err != nil {
		return fmt.Errorf("loading style: %w", err)
	}
	if _, err := m.WaitForEvent(loadTimeout, func(e maplibre.Event) bool {
		return e.Type == maplibre.EventStyleLoaded || e.Type == maplibre.EventMapLoadingFailed
	}); err != nil {
		return fmt.Errorf("waiting for STYLE_LOADED: %w", err)
	}

	sess, err := attachSession(m, 1, 1, float64(ratio))
	if err != nil {
		return fmt.Errorf("attachSession: %w", err)
	}
	defer sess.Close()

	w := &worker{
		m:       m,
		sess:    sess,
		ratio:   ratio,
		timeout: frameTimeout,
	}

	for {
		typ, payload, err := readFrame(os.Stdin)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("readFrame: %w", err)
		}
		if typ != frameRequest {
			msg := fmt.Sprintf("unexpected frame type: %c", typ)
			if err := writeFrame(os.Stdout, frameError, []byte(msg)); err != nil {
				return err
			}
			continue
		}

		rgba, rerr := w.handle(payload)
		if rerr != nil {
			msg := []byte(rerr.Error())
			if err := writeFrame(os.Stdout, frameError, msg); err != nil {
				return err
			}
			continue
		}
		if err := writeFrame(os.Stdout, frameOK, rgba); err != nil {
			return err
		}
	}
}

type worker struct {
	m       *maplibre.Map
	sess    *maplibre.TextureSession
	ratio   uint32
	width   uint32
	height  uint32
	buf     []byte
	timeout time.Duration
}

func (w *worker) handle(payload []byte) ([]byte, error) {
	var req renderRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("parsing request JSON: %w", err)
	}
	if req.Width == 0 || req.Height == 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d", req.Width, req.Height)
	}

	if req.Width != w.width || req.Height != w.height {
		if err := w.sess.Resize(req.Width, req.Height, float64(w.ratio)); err != nil {
			return nil, fmt.Errorf("Resize(%dx%d): %w", req.Width, req.Height, err)
		}
		w.width = req.Width
		w.height = req.Height
		// Allocate buffer at physical size (logical * ratio).
		needed := int(req.Width) * int(req.Height) * 4 * int(w.ratio) * int(w.ratio)
		if cap(w.buf) < needed {
			w.buf = make([]byte, needed)
		} else {
			w.buf = w.buf[:needed]
		}
	}

	if err := w.m.JumpTo(maplibre.Camera{
		Fields: maplibre.CameraFieldCenter | maplibre.CameraFieldZoom |
			maplibre.CameraFieldBearing | maplibre.CameraFieldPitch,
		Latitude:  req.Center[1],
		Longitude: req.Center[0],
		Zoom:      req.Zoom,
		Bearing:   req.Bearing,
		Pitch:     req.Pitch,
	}); err != nil {
		return nil, fmt.Errorf("JumpTo: %w", err)
	}

	gw, gh, _, err := w.m.RenderStillImageInto(w.sess, w.timeout, w.buf)
	if err != nil {
		return nil, fmt.Errorf("RenderStillImageInto: %w", err)
	}
	want := int(req.Width) * int(w.ratio)
	wantH := int(req.Height) * int(w.ratio)
	if gw != want || gh != wantH {
		return nil, fmt.Errorf("render size mismatch: got %dx%d, want %dx%d", gw, gh, want, wantH)
	}

	// Copy out — caller will write the bytes to the wire frame and we want
	// the buffer back for the next render.
	out := make([]byte, len(w.buf))
	copy(out, w.buf)
	return out, nil
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
