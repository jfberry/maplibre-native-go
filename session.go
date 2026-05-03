package maplibre

import (
	"context"
	"fmt"
)

// SessionOptions configures NewSession.
//
// Style accepts the same shapes as the lower-level loaders:
//   - inline JSON (any string starting with `{`) -> SetStyleJSON
//   - URL with scheme (e.g. https://, file://, asset://, mbtiles://) -> SetStyleURL
//   - filesystem path -> rewritten to "file://<path>" then SetStyleURL
//
// Width/Height/ScaleFactor are forwarded to MapOptions and to the
// initial AttachTexture call. ScaleFactor zero-defaults to 1.0.
type SessionOptions struct {
	Runtime RuntimeOptions
	Map     MapOptions
	Style   string
}

// Session bundles a Runtime + Map + RenderSession behind a single
// handle. It is the recommended entry point for the common case of
// "render maps from this style on a dedicated thread."
//
// One Session owns one OS thread (via its Runtime), so create N
// Sessions for N parallel renderers. See examples/pool for that pattern.
//
// Thread-safety: all Session methods are safe to call from any
// goroutine. Concurrent calls into the same Session serialize through
// the underlying Runtime's dispatcher, so callers should partition work
// across multiple Sessions for parallelism rather than calling one
// Session from many goroutines.
type Session struct {
	rt *Runtime
	m  *Map
	rs *RenderSession
}

// NewSession creates a Runtime, a Map, attaches a platform-default
// RenderSession, loads the style, and blocks until the style is
// loaded (STYLE_LOADED) or the load fails (MAP_LOADING_FAILED), or ctx
// is cancelled.
//
// On error all partially-constructed resources are torn down; the
// returned Session is nil. On success the caller owns the returned
// Session and must Close() it.
func NewSession(ctx context.Context, opts SessionOptions) (*Session, error) {
	if opts.Map.Width == 0 || opts.Map.Height == 0 {
		return nil, &Error{
			Status:  StatusInvalidArgument,
			Op:      "NewSession",
			Message: fmt.Sprintf("Map.Width and Map.Height must be non-zero (got %dx%d)", opts.Map.Width, opts.Map.Height),
		}
	}
	if opts.Map.ScaleFactor <= 0 {
		opts.Map.ScaleFactor = 1
	}
	if opts.Style == "" {
		return nil, &Error{
			Status:  StatusInvalidArgument,
			Op:      "NewSession",
			Message: "Style is required",
		}
	}

	rt, err := NewRuntime(opts.Runtime)
	if err != nil {
		return nil, fmt.Errorf("NewSession: NewRuntime: %w", err)
	}
	// On any failure after this point, tear down what we built so far.
	s := &Session{rt: rt}
	cleanup := func() {
		if s.rs != nil {
			_ = s.rs.Close()
		}
		if s.m != nil {
			_ = s.m.Close()
		}
		_ = rt.Close()
	}

	m, err := rt.NewMap(opts.Map)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("NewSession: NewMap: %w", err)
	}
	s.m = m

	rs, err := m.AttachTexture(opts.Map.Width, opts.Map.Height, opts.Map.ScaleFactor)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("NewSession: AttachTexture: %w", err)
	}
	s.rs = rs

	if err := s.loadStyleAndWait(ctx, opts.Style); err != nil {
		cleanup()
		return nil, err
	}
	trackForLeak(s, "Session", func() bool { return s.rt != nil || s.m != nil || s.rs != nil })
	return s, nil
}

// Map exposes the underlying Map for callers that need camera control,
// projection helpers, or other map-level APIs not surfaced on Session.
func (s *Session) Map() *Map { return s.m }

// Runtime exposes the underlying Runtime for callers that need to
// install a log callback or read network status.
func (s *Session) Runtime() *Runtime { return s.rt }

// RenderSession exposes the underlying render-target session for
// callers that need AcquireFrame / ReleaseFrame access to GPU handles
// directly.
func (s *Session) RenderSession() *RenderSession { return s.rs }

// SetStyleURL swaps the active style by URL and blocks until the new
// style finishes loading or fails. The Map handle is reused — this is
// significantly cheaper than rebuilding the Session.
func (s *Session) SetStyleURL(ctx context.Context, url string) error {
	if s == nil || s.m == nil {
		return errClosed("Session.SetStyleURL", "session")
	}
	if err := s.m.SetStyleURL(url); err != nil {
		return err
	}
	return s.waitForStyle(ctx)
}

// SetStyleJSON swaps the active style from inline JSON and blocks until
// the new style loads or fails.
func (s *Session) SetStyleJSON(ctx context.Context, json string) error {
	if s == nil || s.m == nil {
		return errClosed("Session.SetStyleJSON", "session")
	}
	if err := s.m.SetStyleJSON(json); err != nil {
		return err
	}
	return s.waitForStyle(ctx)
}

// SetStyle accepts the same forms as SessionOptions.Style and routes
// to SetStyleURL / SetStyleJSON.
func (s *Session) SetStyle(ctx context.Context, style string) error {
	if s == nil || s.m == nil {
		return errClosed("Session.SetStyle", "session")
	}
	return s.loadStyleAndWait(ctx, style)
}

// Resize updates the render session and the next render's logical
// dimensions. Camera state is preserved.
func (s *Session) Resize(width, height uint32, scaleFactor float64) error {
	if s == nil || s.rs == nil {
		return errClosed("Session.Resize", "session")
	}
	if scaleFactor <= 0 {
		scaleFactor = 1
	}
	return s.rs.Resize(width, height, scaleFactor)
}

// JumpTo applies the camera and returns when the dispatcher has
// committed it (mbgl applies it synchronously inside the call).
func (s *Session) JumpTo(cam Camera) error {
	if s == nil || s.m == nil {
		return errClosed("Session.JumpTo", "session")
	}
	return s.m.JumpTo(cam)
}

// RenderStill returns the next still frame's GPU handle. The caller is
// responsible for ReleaseFrame; prefer Render / RenderInto if you only
// need the pixel bytes.
func (s *Session) RenderStill(ctx context.Context) (TextureFrame, error) {
	if s == nil || s.m == nil {
		return TextureFrame{}, errClosed("Session.RenderStill", "session")
	}
	return s.m.RenderStill(ctx, s.rs)
}

// Render produces an RGBA image, allocating a fresh slice. Width and
// height are returned in physical pixels (logical * scaleFactor).
func (s *Session) Render(ctx context.Context) (rgba []byte, width, height int, err error) {
	if s == nil || s.m == nil {
		return nil, 0, 0, errClosed("Session.Render", "session")
	}
	return s.m.RenderImage(ctx, s.rs)
}

// RenderInto produces an RGBA image into dst. Returns physical width
// and height. dst must be at least width*height*4 bytes.
func (s *Session) RenderInto(ctx context.Context, dst []byte) (width, height int, err error) {
	if s == nil || s.m == nil {
		return 0, 0, errClosed("Session.RenderInto", "session")
	}
	return s.m.RenderImageInto(ctx, s.rs, dst)
}

// Close tears down the session in dependency order: RenderSession ->
// Map -> Runtime. Idempotent; safe on a nil Session.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	if s.rs != nil {
		if err := s.rs.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.rs = nil
	}
	if s.m != nil {
		if err := s.m.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.m = nil
	}
	if s.rt != nil {
		if err := s.rt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.rt = nil
	}
	return firstErr
}

// loadStyleAndWait routes via Map.LoadStyle and blocks on the
// resulting STYLE_LOADED / MAP_LOADING_FAILED event.
func (s *Session) loadStyleAndWait(ctx context.Context, style string) error {
	if err := s.m.LoadStyle(style); err != nil {
		return err
	}
	return s.waitForStyle(ctx)
}

// waitForStyle blocks until STYLE_LOADED for this session's map, or
// returns an error on MAP_LOADING_FAILED.
func (s *Session) waitForStyle(ctx context.Context) error {
	ev, err := s.m.WaitForEvent(ctx, EventOfTypes(EventStyleLoaded, EventMapLoadingFailed))
	if err != nil {
		return fmt.Errorf("waiting for STYLE_LOADED: %w", err)
	}
	if ev.Type == EventMapLoadingFailed {
		return eventErr("Session.waitForStyle", ev)
	}
	return nil
}
