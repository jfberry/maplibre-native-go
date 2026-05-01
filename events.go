package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

import "fmt"

// EventType mirrors mln_runtime_event_type. Events flow through the
// runtime's event queue regardless of which Map (or Runtime) they
// originated on; the Source field of Event identifies the originating
// Map (or nil for runtime-source events).
//
// Values are typed against the C constants so a drift in upstream
// values fails to compile.
type EventType uint32

const (
	EventCameraWillChange      EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_CAMERA_WILL_CHANGE)
	EventCameraIsChanging      EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_CAMERA_IS_CHANGING)
	EventCameraDidChange       EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_CAMERA_DID_CHANGE)
	EventStyleLoaded           EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_STYLE_LOADED)
	EventMapLoadingStarted     EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_LOADING_STARTED)
	EventMapLoadingFinished    EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_LOADING_FINISHED)
	EventMapLoadingFailed      EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_LOADING_FAILED)
	EventMapIdle               EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_IDLE)
	EventRenderUpdateAvailable EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_RENDER_UPDATE_AVAILABLE)
	EventRenderError           EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_RENDER_ERROR)
	EventStillImageFinished    EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_STILL_IMAGE_FINISHED)
	EventStillImageFailed      EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_STILL_IMAGE_FAILED)
	EventRenderFrameStarted    EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_RENDER_FRAME_STARTED)
	EventRenderFrameFinished   EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_RENDER_FRAME_FINISHED)
	EventRenderMapStarted      EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_RENDER_MAP_STARTED)
	EventRenderMapFinished     EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_RENDER_MAP_FINISHED)
	EventStyleImageMissing     EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_STYLE_IMAGE_MISSING)
	EventTileAction            EventType = EventType(C.MLN_RUNTIME_EVENT_MAP_TILE_ACTION)
)

func (e EventType) String() string {
	switch e {
	case EventCameraWillChange:
		return "CAMERA_WILL_CHANGE"
	case EventCameraIsChanging:
		return "CAMERA_IS_CHANGING"
	case EventCameraDidChange:
		return "CAMERA_DID_CHANGE"
	case EventStyleLoaded:
		return "STYLE_LOADED"
	case EventMapLoadingStarted:
		return "MAP_LOADING_STARTED"
	case EventMapLoadingFinished:
		return "MAP_LOADING_FINISHED"
	case EventMapLoadingFailed:
		return "MAP_LOADING_FAILED"
	case EventMapIdle:
		return "MAP_IDLE"
	case EventRenderUpdateAvailable:
		return "RENDER_UPDATE_AVAILABLE"
	case EventRenderError:
		return "RENDER_ERROR"
	case EventStillImageFinished:
		return "STILL_IMAGE_FINISHED"
	case EventStillImageFailed:
		return "STILL_IMAGE_FAILED"
	case EventRenderFrameStarted:
		return "RENDER_FRAME_STARTED"
	case EventRenderFrameFinished:
		return "RENDER_FRAME_FINISHED"
	case EventRenderMapStarted:
		return "RENDER_MAP_STARTED"
	case EventRenderMapFinished:
		return "RENDER_MAP_FINISHED"
	case EventStyleImageMissing:
		return "STYLE_IMAGE_MISSING"
	case EventTileAction:
		return "TILE_ACTION"
	}
	return fmt.Sprintf("UNKNOWN(%d)", uint32(e))
}

// Event is the high-level Go representation of mln_runtime_event. Source
// identifies the originating Map for map-source events, or is nil for
// runtime-source events. Payload is non-nil for events that carry
// typed extras: EventRenderFrameFinished -> *RenderFramePayload,
// EventRenderMapFinished -> *RenderMapPayload, EventStyleImageMissing
// -> *StyleImageMissingPayload, EventTileAction -> *TileActionPayload.
// All other event types have Payload == nil.
type Event struct {
	Type    EventType
	Code    int32
	Source  *Map // nil if event source is the runtime itself
	Message string
	Payload Payload
}

// EventOfType returns a predicate matching events with the given type
// and ignoring source. Convenience for WaitForEvent:
//
//	rt.WaitForEvent(ctx, maplibre.EventOfType(maplibre.EventStyleLoaded))
func EventOfType(t EventType) func(Event) bool {
	return func(e Event) bool { return e.Type == t }
}

// EventOfTypes returns a predicate matching events whose type is in any
// of the supplied set.
func EventOfTypes(ts ...EventType) func(Event) bool {
	return func(e Event) bool {
		for _, t := range ts {
			if e.Type == t {
				return true
			}
		}
		return false
	}
}
