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
type EventType uint32

const (
	EventCameraWillChange      EventType = 1
	EventCameraIsChanging      EventType = 2
	EventCameraDidChange       EventType = 3
	EventStyleLoaded           EventType = 4
	EventMapLoadingStarted     EventType = 5
	EventMapLoadingFinished    EventType = 6
	EventMapLoadingFailed      EventType = 7
	EventMapIdle               EventType = 8
	EventRenderUpdateAvailable EventType = 9
	EventRenderError           EventType = 10
	EventStillImageFinished    EventType = 11
	EventStillImageFailed      EventType = 12
	EventRenderFrameStarted    EventType = 13
	EventRenderFrameFinished   EventType = 14
	EventRenderMapStarted      EventType = 15
	EventRenderMapFinished     EventType = 16
	EventStyleImageMissing     EventType = 17
	EventTileAction            EventType = 18
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
// runtime-source events. Detailed payloads (render-frame stats,
// tile-action details, style-image-missing IDs) are not exposed on this
// struct in v1; if you need them, file an issue with the use case.
type Event struct {
	Type    EventType
	Code    int32
	Source  *Map // nil if event source is the runtime itself
	Message string
}
