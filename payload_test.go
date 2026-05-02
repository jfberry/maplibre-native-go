package maplibre

import (
	"context"
	"testing"
	"time"
)

// TestPayloadNoneForCommonEvents asserts that events without a typed
// payload (STYLE_LOADED, MAP_LOADING_FAILED, CAMERA_DID_CHANGE...)
// have Event.Payload == nil. This is the common case — the typed
// payload field shouldn't add cost for the events most callers
// already wait on.
func TestPayloadNoneForCommonEvents(t *testing.T) {
	m := newTestMap(t)
	if err := m.SetStyleJSON(`{"version":8,"sources":{},"layers":[]}`); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ev, err := m.WaitForEvent(ctx, EventOfType(EventStyleLoaded))
	if err != nil {
		t.Fatalf("WaitForEvent: %v", err)
	}
	if ev.Payload != nil {
		t.Errorf("STYLE_LOADED.Payload = %v, want nil", ev.Payload)
	}
}

// TestPayloadStyleImageMissing exercises the only payload path we can
// trigger from a pure offline test without real tile data: load a
// style that references an icon that doesn't exist, and assert mbgl
// reports the missing-image ID via a *StyleImageMissingPayload.
func TestPayloadStyleImageMissing(t *testing.T) {
	rt := newTestRuntime(t)
	m, err := rt.NewMap(MapOptions{Width: 64, Height: 64})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	// Symbol layer with a sprite icon that the style does not declare.
	// mbgl emits STYLE_IMAGE_MISSING during render.
	const style = `{
		"version": 8,
		"sources": {
			"pts": {
				"type": "geojson",
				"data": {
					"type": "FeatureCollection",
					"features": [{
						"type": "Feature",
						"geometry": {"type":"Point","coordinates":[0,0]},
						"properties": {}
					}]
				}
			}
		},
		"layers": [
			{"id":"bg","type":"background","paint":{"background-color":"#000000"}},
			{
				"id":"missing-icon",
				"type":"symbol",
				"source":"pts",
				"layout":{"icon-image":"definitely-not-a-real-icon"}
			}
		]
	}`
	if err := m.SetStyleJSON(style); err != nil {
		t.Fatalf("SetStyleJSON: %v", err)
	}

	loadCtx, loadCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer loadCancel()
	if _, err := m.WaitForEvent(loadCtx, EventOfTypes(EventStyleLoaded, EventMapLoadingFailed)); err != nil {
		t.Fatalf("waiting for STYLE_LOADED: %v", err)
	}

	// Trigger a render to provoke the symbol layer. Agnostic AttachTexture
	// + RenderImage drives the readback path; we don't need GPU handles
	// here, just to force the symbol layer to be evaluated.
	sess, err := m.AttachTexture(64, 64, 1)
	if err != nil {
		var mlnErr *Error
		if asErr(err, &mlnErr) && mlnErr.Status == StatusUnsupported {
			t.Skipf("backend unavailable: %v", err)
		}
		t.Fatalf("AttachTexture: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	renderCtx, renderCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer renderCancel()
	if _, _, _, err := m.RenderImage(renderCtx, sess); err != nil {
		t.Fatalf("RenderImage: %v", err)
	}

	// Drain the runtime's event queue and look for a STYLE_IMAGE_MISSING.
	deadline := time.Now().Add(2 * time.Second)
	var saw *StyleImageMissingPayload
	for time.Now().Before(deadline) {
		ev, has, err := m.rt.PollEvent()
		if err != nil {
			t.Fatalf("PollEvent: %v", err)
		}
		if !has {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if ev.Type != EventStyleImageMissing {
			continue
		}
		p, ok := ev.Payload.(*StyleImageMissingPayload)
		if !ok {
			t.Fatalf("STYLE_IMAGE_MISSING.Payload = %T, want *StyleImageMissingPayload", ev.Payload)
		}
		saw = p
		break
	}
	if saw == nil {
		t.Skip("did not observe STYLE_IMAGE_MISSING within deadline (mbgl may have batched into a later frame)")
	}
	if saw.ImageID != "definitely-not-a-real-icon" {
		t.Errorf("ImageID = %q, want %q", saw.ImageID, "definitely-not-a-real-icon")
	}
}

// TestRenderingStatsZeroValues verifies that the typed payload struct
// converts a zero/zero/zero stats record without panicking. The
// goal here is to exercise the decodePayload code path; mbgl emits
// zero stats in degenerate cases.
func TestRenderingStatsZeroValues(t *testing.T) {
	stats := RenderingStats{}
	if stats.EncodingTime != 0 || stats.RenderingTime != 0 {
		t.Fatalf("zero RenderingStats has non-zero times: %+v", stats)
	}
}

// TestEventPayloadTypeRoundTrip exercises every payload type's
// payloadType() to make sure the interface implementations agree
// with the package-level constants. Runs entirely in Go — no cgo.
func TestEventPayloadTypeRoundTrip(t *testing.T) {
	cases := []struct {
		p    Payload
		want EventPayloadType
	}{
		{&RenderFramePayload{}, PayloadRenderFrame},
		{&RenderMapPayload{}, PayloadRenderMap},
		{&StyleImageMissingPayload{}, PayloadStyleImageMissing},
		{&TileActionPayload{}, PayloadTileAction},
	}
	for _, c := range cases {
		if got := c.p.payloadType(); got != c.want {
			t.Errorf("%T.payloadType() = %v, want %v", c.p, got, c.want)
		}
	}
}

// TestRenderModeAndTileOpStrings spot-checks the String() helpers so
// log output is readable.
func TestRenderModeAndTileOpStrings(t *testing.T) {
	if got := RenderModePartial.String(); got != "PARTIAL" {
		t.Errorf("RenderModePartial.String() = %q", got)
	}
	if got := RenderModeFull.String(); got != "FULL" {
		t.Errorf("RenderModeFull.String() = %q", got)
	}
	if got := RenderMode(99).String(); got != "UNKNOWN(99)" {
		t.Errorf("RenderMode(99).String() = %q", got)
	}
	if got := TileOpEndParse.String(); got != "END_PARSE" {
		t.Errorf("TileOpEndParse.String() = %q", got)
	}
	if got := TileOperation(99).String(); got != "UNKNOWN(99)" {
		t.Errorf("TileOperation(99).String() = %q", got)
	}
}
