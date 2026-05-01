package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

import (
	"fmt"
	"time"
	"unsafe"
)

// EventPayloadType mirrors mln_runtime_event_payload_type. Most events
// carry no payload (PayloadNone); the few that do are decoded into
// typed structs by PollEvent and exposed via Event.Payload.
type EventPayloadType uint32

const (
	PayloadNone              EventPayloadType = EventPayloadType(C.MLN_RUNTIME_EVENT_PAYLOAD_NONE)
	PayloadRenderFrame       EventPayloadType = EventPayloadType(C.MLN_RUNTIME_EVENT_PAYLOAD_RENDER_FRAME)
	PayloadRenderMap         EventPayloadType = EventPayloadType(C.MLN_RUNTIME_EVENT_PAYLOAD_RENDER_MAP)
	PayloadStyleImageMissing EventPayloadType = EventPayloadType(C.MLN_RUNTIME_EVENT_PAYLOAD_STYLE_IMAGE_MISSING)
	PayloadTileAction        EventPayloadType = EventPayloadType(C.MLN_RUNTIME_EVENT_PAYLOAD_TILE_ACTION)
)

// RenderMode mirrors mln_render_mode. Reported in render observer
// events to distinguish a partial render (some content still
// pending) from a full render (all visible content drawn).
type RenderMode uint32

const (
	RenderModePartial RenderMode = RenderMode(C.MLN_RENDER_MODE_PARTIAL)
	RenderModeFull    RenderMode = RenderMode(C.MLN_RENDER_MODE_FULL)
)

func (m RenderMode) String() string {
	switch m {
	case RenderModePartial:
		return "PARTIAL"
	case RenderModeFull:
		return "FULL"
	}
	return fmt.Sprintf("UNKNOWN(%d)", uint32(m))
}

// TileOperation mirrors mln_tile_operation — the lifecycle stage a
// tile is at when mbgl emits a TILE_ACTION event.
type TileOperation uint32

const (
	TileOpRequestedFromCache   TileOperation = TileOperation(C.MLN_TILE_OPERATION_REQUESTED_FROM_CACHE)
	TileOpRequestedFromNetwork TileOperation = TileOperation(C.MLN_TILE_OPERATION_REQUESTED_FROM_NETWORK)
	TileOpLoadFromNetwork      TileOperation = TileOperation(C.MLN_TILE_OPERATION_LOAD_FROM_NETWORK)
	TileOpLoadFromCache        TileOperation = TileOperation(C.MLN_TILE_OPERATION_LOAD_FROM_CACHE)
	TileOpStartParse           TileOperation = TileOperation(C.MLN_TILE_OPERATION_START_PARSE)
	TileOpEndParse             TileOperation = TileOperation(C.MLN_TILE_OPERATION_END_PARSE)
	TileOpError                TileOperation = TileOperation(C.MLN_TILE_OPERATION_ERROR)
	TileOpCancelled            TileOperation = TileOperation(C.MLN_TILE_OPERATION_CANCELLED)
	TileOpNull                 TileOperation = TileOperation(C.MLN_TILE_OPERATION_NULL)
)

func (o TileOperation) String() string {
	switch o {
	case TileOpRequestedFromCache:
		return "REQUESTED_FROM_CACHE"
	case TileOpRequestedFromNetwork:
		return "REQUESTED_FROM_NETWORK"
	case TileOpLoadFromNetwork:
		return "LOAD_FROM_NETWORK"
	case TileOpLoadFromCache:
		return "LOAD_FROM_CACHE"
	case TileOpStartParse:
		return "START_PARSE"
	case TileOpEndParse:
		return "END_PARSE"
	case TileOpError:
		return "ERROR"
	case TileOpCancelled:
		return "CANCELLED"
	case TileOpNull:
		return "NULL"
	}
	return fmt.Sprintf("UNKNOWN(%d)", uint32(o))
}

// Payload is implemented by every concrete event-payload type. Use a
// type switch or assertion on Event.Payload to decode:
//
//	switch p := ev.Payload.(type) {
//	case *RenderFramePayload:
//	    fmt.Println("frame", p.Stats.FrameCount)
//	case *TileActionPayload:
//	    fmt.Println("tile", p.Tile, "op", p.Operation)
//	}
type Payload interface {
	payloadType() EventPayloadType
}

// RenderingStats mirrors mln_rendering_stats. Times are converted from
// the underlying double-seconds into time.Duration for ergonomics.
type RenderingStats struct {
	EncodingTime       time.Duration
	RenderingTime      time.Duration
	FrameCount         int64
	DrawCallCount      int64
	TotalDrawCallCount int64
}

// RenderFramePayload accompanies EventRenderFrameFinished.
type RenderFramePayload struct {
	Mode             RenderMode
	NeedsRepaint     bool
	PlacementChanged bool
	Stats            RenderingStats
}

func (*RenderFramePayload) payloadType() EventPayloadType { return PayloadRenderFrame }

// RenderMapPayload accompanies EventRenderMapFinished.
type RenderMapPayload struct {
	Mode RenderMode
}

func (*RenderMapPayload) payloadType() EventPayloadType { return PayloadRenderMap }

// StyleImageMissingPayload accompanies EventStyleImageMissing. Use
// the ID to feed mbgl a sprite via a future image-loader API; for
// now the field is informational.
type StyleImageMissingPayload struct {
	ImageID string
}

func (*StyleImageMissingPayload) payloadType() EventPayloadType { return PayloadStyleImageMissing }

// TileID mirrors mln_tile_id — the canonical XY/Z plus overscale
// metadata mbgl uses to identify a tile.
type TileID struct {
	OverscaledZ uint32
	Wrap        int32
	CanonicalZ  uint32
	CanonicalX  uint32
	CanonicalY  uint32
}

func (t TileID) String() string {
	return fmt.Sprintf("z%d/%d/%d (wrap=%d, overscaled=%d)", t.CanonicalZ, t.CanonicalX, t.CanonicalY, t.Wrap, t.OverscaledZ)
}

// TileActionPayload accompanies EventTileAction.
type TileActionPayload struct {
	Operation TileOperation
	Tile      TileID
	SourceID  string
}

func (*TileActionPayload) payloadType() EventPayloadType { return PayloadTileAction }

// decodePayload copies the borrowed C payload into a typed Go value
// before the C buffer goes stale on the next poll. Called from
// PollEvent inside the dispatcher.
func decodePayload(payloadType uint32, payload unsafe.Pointer, size C.size_t) Payload {
	if payload == nil || size == 0 {
		return nil
	}
	switch EventPayloadType(payloadType) {
	case PayloadRenderFrame:
		if size < C.size_t(unsafe.Sizeof(C.mln_runtime_event_render_frame{})) {
			return nil
		}
		p := (*C.mln_runtime_event_render_frame)(payload)
		return &RenderFramePayload{
			Mode:             RenderMode(p.mode),
			NeedsRepaint:     bool(p.needs_repaint),
			PlacementChanged: bool(p.placement_changed),
			Stats: RenderingStats{
				EncodingTime:       time.Duration(float64(p.stats.encoding_time) * float64(time.Second)),
				RenderingTime:      time.Duration(float64(p.stats.rendering_time) * float64(time.Second)),
				FrameCount:         int64(p.stats.frame_count),
				DrawCallCount:      int64(p.stats.draw_call_count),
				TotalDrawCallCount: int64(p.stats.total_draw_call_count),
			},
		}
	case PayloadRenderMap:
		if size < C.size_t(unsafe.Sizeof(C.mln_runtime_event_render_map{})) {
			return nil
		}
		p := (*C.mln_runtime_event_render_map)(payload)
		return &RenderMapPayload{Mode: RenderMode(p.mode)}
	case PayloadStyleImageMissing:
		if size < C.size_t(unsafe.Sizeof(C.mln_runtime_event_style_image_missing{})) {
			return nil
		}
		p := (*C.mln_runtime_event_style_image_missing)(payload)
		out := &StyleImageMissingPayload{}
		if p.image_id != nil && p.image_id_size > 0 {
			out.ImageID = C.GoStringN(p.image_id, C.int(p.image_id_size))
		}
		return out
	case PayloadTileAction:
		if size < C.size_t(unsafe.Sizeof(C.mln_runtime_event_tile_action{})) {
			return nil
		}
		p := (*C.mln_runtime_event_tile_action)(payload)
		out := &TileActionPayload{
			Operation: TileOperation(p.operation),
			Tile: TileID{
				OverscaledZ: uint32(p.tile_id.overscaled_z),
				Wrap:        int32(p.tile_id.wrap),
				CanonicalZ:  uint32(p.tile_id.canonical_z),
				CanonicalX:  uint32(p.tile_id.canonical_x),
				CanonicalY:  uint32(p.tile_id.canonical_y),
			},
		}
		if p.source_id != nil && p.source_id_size > 0 {
			out.SourceID = C.GoStringN(p.source_id, C.int(p.source_id_size))
		}
		return out
	}
	return nil
}
