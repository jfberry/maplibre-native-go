package maplibre

/*
#include "maplibre_native_abi.h"
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

// GetCamera returns the current camera snapshot.
func (m *Map) GetCamera() (Camera, error) {
	var out Camera
	var err error
	m.rt.d.do(func() {
		ccam := C.mln_camera_options_default()
		status := C.mln_map_get_camera(m.ptr, &ccam)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_get_camera", status)
			return
		}
		out = Camera{
			Fields:    CameraField(ccam.fields),
			Latitude:  float64(ccam.latitude),
			Longitude: float64(ccam.longitude),
			Zoom:      float64(ccam.zoom),
			Bearing:   float64(ccam.bearing),
			Pitch:     float64(ccam.pitch),
		}
	})
	return out, err
}

// JumpTo applies a camera jump command. Only fields indicated by cam.Fields
// are used.
func (m *Map) JumpTo(cam Camera) error {
	var err error
	m.rt.d.do(func() {
		ccam := C.mln_camera_options_default()
		ccam.fields = C.uint32_t(cam.Fields)
		ccam.latitude = C.double(cam.Latitude)
		ccam.longitude = C.double(cam.Longitude)
		ccam.zoom = C.double(cam.Zoom)
		ccam.bearing = C.double(cam.Bearing)
		ccam.pitch = C.double(cam.Pitch)
		status := C.mln_map_jump_to(m.ptr, &ccam)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_jump_to", status)
		}
	})
	return err
}

// MoveBy pans the camera by a screen-space delta.
func (m *Map) MoveBy(deltaX, deltaY float64) error {
	var err error
	m.rt.d.do(func() {
		status := C.mln_map_move_by(m.ptr, C.double(deltaX), C.double(deltaY))
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_move_by", status)
		}
	})
	return err
}

// ScaleBy zooms the camera by a multiplicative factor about an optional
// screen-space anchor (nil anchors center the zoom on the viewport).
func (m *Map) ScaleBy(scale float64, anchor *ScreenPoint) error {
	var err error
	m.rt.d.do(func() {
		var anchorC *C.mln_screen_point
		var ap C.mln_screen_point
		if anchor != nil {
			ap.x = C.double(anchor.X)
			ap.y = C.double(anchor.Y)
			anchorC = &ap
		}
		status := C.mln_map_scale_by(m.ptr, C.double(scale), anchorC)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_scale_by", status)
		}
	})
	return err
}

// RotateBy rotates the camera based on two screen-space points.
func (m *Map) RotateBy(first, second ScreenPoint) error {
	var err error
	m.rt.d.do(func() {
		f := C.mln_screen_point{x: C.double(first.X), y: C.double(first.Y)}
		s := C.mln_screen_point{x: C.double(second.X), y: C.double(second.Y)}
		status := C.mln_map_rotate_by(m.ptr, f, s)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_rotate_by", status)
		}
	})
	return err
}

// PitchBy applies a pitch delta.
func (m *Map) PitchBy(pitch float64) error {
	var err error
	m.rt.d.do(func() {
		status := C.mln_map_pitch_by(m.ptr, C.double(pitch))
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_pitch_by", status)
		}
	})
	return err
}

// CancelTransitions cancels any active camera transitions.
func (m *Map) CancelTransitions() error {
	var err error
	m.rt.d.do(func() {
		status := C.mln_map_cancel_transitions(m.ptr)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_cancel_transitions", status)
		}
	})
	return err
}
