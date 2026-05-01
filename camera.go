package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

// CameraField is a bitmask of which fields a Camera carries when used as a
// jump command. Mirrors mln_camera_option_field.
type CameraField uint32

const (
	CameraFieldCenter  CameraField = 1 << 0
	CameraFieldZoom    CameraField = 1 << 1
	CameraFieldBearing CameraField = 1 << 2
	CameraFieldPitch   CameraField = 1 << 3
)

// Camera mirrors mln_camera_options.
//
// When passed to JumpTo, only fields indicated by Fields are applied; the
// others are ignored. When returned from GetCamera, all fields are populated.
type Camera struct {
	Fields    CameraField
	Latitude  float64
	Longitude float64
	Zoom      float64
	Bearing   float64
	Pitch     float64
}

// ScreenPoint mirrors mln_screen_point.
type ScreenPoint struct {
	X float64
	Y float64
}

// toC fills a C.mln_camera_options from cam.Fields and the typed
// fields. The caller passes a value initialised with
// mln_camera_options_default() so size is set correctly.
func (cam Camera) toC() C.mln_camera_options {
	ccam := C.mln_camera_options_default()
	ccam.fields = C.uint32_t(cam.Fields)
	ccam.latitude = C.double(cam.Latitude)
	ccam.longitude = C.double(cam.Longitude)
	ccam.zoom = C.double(cam.Zoom)
	ccam.bearing = C.double(cam.Bearing)
	ccam.pitch = C.double(cam.Pitch)
	return ccam
}

func cameraFromC(ccam C.mln_camera_options) Camera {
	return Camera{
		Fields:    CameraField(ccam.fields),
		Latitude:  float64(ccam.latitude),
		Longitude: float64(ccam.longitude),
		Zoom:      float64(ccam.zoom),
		Bearing:   float64(ccam.bearing),
		Pitch:     float64(ccam.pitch),
	}
}

// GetCamera returns the current camera snapshot.
func (m *Map) GetCamera() (Camera, error) {
	if m == nil {
		return Camera{}, errClosed("Map.GetCamera", "map")
	}
	var out Camera
	err := m.rt.runOnOwner("Map.GetCamera", func() error {
		if m.ptr == nil {
			return errClosed("Map.GetCamera", "map")
		}
		ccam := C.mln_camera_options_default()
		if status := C.mln_map_get_camera(m.ptr, &ccam); status != C.MLN_STATUS_OK {
			return statusError("mln_map_get_camera", status)
		}
		out = cameraFromC(ccam)
		return nil
	})
	return out, err
}

// JumpTo applies a camera jump command. Only fields indicated by cam.Fields
// are used.
func (m *Map) JumpTo(cam Camera) error {
	if m == nil {
		return errClosed("Map.JumpTo", "map")
	}
	return m.rt.runOnOwner("Map.JumpTo", func() error {
		if m.ptr == nil {
			return errClosed("Map.JumpTo", "map")
		}
		ccam := cam.toC()
		if status := C.mln_map_jump_to(m.ptr, &ccam); status != C.MLN_STATUS_OK {
			return statusError("mln_map_jump_to", status)
		}
		return nil
	})
}

// MoveBy pans the camera by a screen-space delta.
func (m *Map) MoveBy(deltaX, deltaY float64) error {
	if m == nil {
		return errClosed("Map.MoveBy", "map")
	}
	return m.rt.runOnOwner("Map.MoveBy", func() error {
		if m.ptr == nil {
			return errClosed("Map.MoveBy", "map")
		}
		if status := C.mln_map_move_by(m.ptr, C.double(deltaX), C.double(deltaY)); status != C.MLN_STATUS_OK {
			return statusError("mln_map_move_by", status)
		}
		return nil
	})
}

// ScaleBy zooms the camera by a multiplicative factor about an optional
// screen-space anchor (nil anchors center the zoom on the viewport).
func (m *Map) ScaleBy(scale float64, anchor *ScreenPoint) error {
	if m == nil {
		return errClosed("Map.ScaleBy", "map")
	}
	return m.rt.runOnOwner("Map.ScaleBy", func() error {
		if m.ptr == nil {
			return errClosed("Map.ScaleBy", "map")
		}
		var anchorC *C.mln_screen_point
		var ap C.mln_screen_point
		if anchor != nil {
			ap.x = C.double(anchor.X)
			ap.y = C.double(anchor.Y)
			anchorC = &ap
		}
		if status := C.mln_map_scale_by(m.ptr, C.double(scale), anchorC); status != C.MLN_STATUS_OK {
			return statusError("mln_map_scale_by", status)
		}
		return nil
	})
}

// RotateBy rotates the camera based on two screen-space points.
func (m *Map) RotateBy(first, second ScreenPoint) error {
	if m == nil {
		return errClosed("Map.RotateBy", "map")
	}
	return m.rt.runOnOwner("Map.RotateBy", func() error {
		if m.ptr == nil {
			return errClosed("Map.RotateBy", "map")
		}
		f := C.mln_screen_point{x: C.double(first.X), y: C.double(first.Y)}
		s := C.mln_screen_point{x: C.double(second.X), y: C.double(second.Y)}
		if status := C.mln_map_rotate_by(m.ptr, f, s); status != C.MLN_STATUS_OK {
			return statusError("mln_map_rotate_by", status)
		}
		return nil
	})
}

// PitchBy applies a pitch delta.
func (m *Map) PitchBy(pitch float64) error {
	if m == nil {
		return errClosed("Map.PitchBy", "map")
	}
	return m.rt.runOnOwner("Map.PitchBy", func() error {
		if m.ptr == nil {
			return errClosed("Map.PitchBy", "map")
		}
		if status := C.mln_map_pitch_by(m.ptr, C.double(pitch)); status != C.MLN_STATUS_OK {
			return statusError("mln_map_pitch_by", status)
		}
		return nil
	})
}

// CancelTransitions cancels any active camera transitions.
func (m *Map) CancelTransitions() error {
	if m == nil {
		return errClosed("Map.CancelTransitions", "map")
	}
	return m.rt.runOnOwner("Map.CancelTransitions", func() error {
		if m.ptr == nil {
			return errClosed("Map.CancelTransitions", "map")
		}
		if status := C.mln_map_cancel_transitions(m.ptr); status != C.MLN_STATUS_OK {
			return statusError("mln_map_cancel_transitions", status)
		}
		return nil
	})
}
