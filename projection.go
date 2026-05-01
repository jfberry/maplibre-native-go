package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

import "unsafe"

// PixelForLatLng converts a geographic coordinate to a screen point
// using the map's current camera and projection. The returned point is
// in logical map pixels with origin at the top-left of the viewport.
func (m *Map) PixelForLatLng(coord LatLng) (ScreenPoint, error) {
	if m == nil {
		return ScreenPoint{}, errClosed("Map.PixelForLatLng", "map")
	}
	var out ScreenPoint
	err := m.rt.runOnOwner("Map.PixelForLatLng", func() error {
		if m.ptr == nil {
			return errClosed("Map.PixelForLatLng", "map")
		}
		var pt C.mln_screen_point
		if status := C.mln_map_pixel_for_lat_lng(m.ptr, coord.toC(), &pt); status != C.MLN_STATUS_OK {
			return statusError("mln_map_pixel_for_lat_lng", status)
		}
		out = screenPointFromC(pt)
		return nil
	})
	return out, err
}

// LatLngForPixel converts a screen point (logical map pixels, origin
// top-left) back to a geographic coordinate using the map's current
// camera and projection.
func (m *Map) LatLngForPixel(point ScreenPoint) (LatLng, error) {
	if m == nil {
		return LatLng{}, errClosed("Map.LatLngForPixel", "map")
	}
	var out LatLng
	err := m.rt.runOnOwner("Map.LatLngForPixel", func() error {
		if m.ptr == nil {
			return errClosed("Map.LatLngForPixel", "map")
		}
		var ll C.mln_lat_lng
		if status := C.mln_map_lat_lng_for_pixel(m.ptr, point.toC(), &ll); status != C.MLN_STATUS_OK {
			return statusError("mln_map_lat_lng_for_pixel", status)
		}
		out = latLngFromC(ll)
		return nil
	})
	return out, err
}

// PixelsForLatLngs converts an array of geographic coordinates to
// screen points in one ABI call. Returns a fresh slice of equal length.
// Empty input returns an empty slice without crossing the ABI.
func (m *Map) PixelsForLatLngs(coords []LatLng) ([]ScreenPoint, error) {
	if m == nil {
		return nil, errClosed("Map.PixelsForLatLngs", "map")
	}
	if len(coords) == 0 {
		return nil, nil
	}
	out := make([]ScreenPoint, len(coords))
	err := m.rt.runOnOwner("Map.PixelsForLatLngs", func() error {
		if m.ptr == nil {
			return errClosed("Map.PixelsForLatLngs", "map")
		}
		// Build C arrays. They must outlive the ABI call but not the
		// Go closure, so plain Go slices backed by C-typed elements
		// are fine — Go's GC won't move them while we hold pointers
		// to them inside this synchronous call.
		cIn := make([]C.mln_lat_lng, len(coords))
		for i, c := range coords {
			cIn[i] = c.toC()
		}
		cOut := make([]C.mln_screen_point, len(coords))
		if status := C.mln_map_pixels_for_lat_lngs(
			m.ptr,
			(*C.mln_lat_lng)(unsafe.Pointer(&cIn[0])),
			C.size_t(len(cIn)),
			(*C.mln_screen_point)(unsafe.Pointer(&cOut[0])),
		); status != C.MLN_STATUS_OK {
			return statusError("mln_map_pixels_for_lat_lngs", status)
		}
		for i, p := range cOut {
			out[i] = screenPointFromC(p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// LatLngsForPixels is the inverse of PixelsForLatLngs.
func (m *Map) LatLngsForPixels(points []ScreenPoint) ([]LatLng, error) {
	if m == nil {
		return nil, errClosed("Map.LatLngsForPixels", "map")
	}
	if len(points) == 0 {
		return nil, nil
	}
	out := make([]LatLng, len(points))
	err := m.rt.runOnOwner("Map.LatLngsForPixels", func() error {
		if m.ptr == nil {
			return errClosed("Map.LatLngsForPixels", "map")
		}
		cIn := make([]C.mln_screen_point, len(points))
		for i, p := range points {
			cIn[i] = p.toC()
		}
		cOut := make([]C.mln_lat_lng, len(points))
		if status := C.mln_map_lat_lngs_for_pixels(
			m.ptr,
			(*C.mln_screen_point)(unsafe.Pointer(&cIn[0])),
			C.size_t(len(cIn)),
			(*C.mln_lat_lng)(unsafe.Pointer(&cOut[0])),
		); status != C.MLN_STATUS_OK {
			return statusError("mln_map_lat_lngs_for_pixels", status)
		}
		for i, ll := range cOut {
			out[i] = latLngFromC(ll)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetProjectionMode returns the map's current axonometric render
// transform. All fields of the returned ProjectionMode are populated.
func (m *Map) GetProjectionMode() (ProjectionMode, error) {
	if m == nil {
		return ProjectionMode{}, errClosed("Map.GetProjectionMode", "map")
	}
	var out ProjectionMode
	err := m.rt.runOnOwner("Map.GetProjectionMode", func() error {
		if m.ptr == nil {
			return errClosed("Map.GetProjectionMode", "map")
		}
		cmode := C.mln_projection_mode_default()
		if status := C.mln_map_get_projection_mode(m.ptr, &cmode); status != C.MLN_STATUS_OK {
			return statusError("mln_map_get_projection_mode", status)
		}
		out = ProjectionMode{
			Fields:      ProjectionField(cmode.fields),
			Axonometric: bool(cmode.axonometric),
			XSkew:       float64(cmode.x_skew),
			YSkew:       float64(cmode.y_skew),
		}
		return nil
	})
	return out, err
}

// SetProjectionMode applies the fields indicated by mode.Fields to the
// map's render transform. Unspecified fields keep their current values.
// This does not change the geographic coordinate model.
func (m *Map) SetProjectionMode(mode ProjectionMode) error {
	if m == nil {
		return errClosed("Map.SetProjectionMode", "map")
	}
	return m.rt.runOnOwner("Map.SetProjectionMode", func() error {
		if m.ptr == nil {
			return errClosed("Map.SetProjectionMode", "map")
		}
		cmode := C.mln_projection_mode_default()
		cmode.fields = C.uint32_t(mode.Fields)
		cmode.axonometric = C.bool(mode.Axonometric)
		cmode.x_skew = C.double(mode.XSkew)
		cmode.y_skew = C.double(mode.YSkew)
		if status := C.mln_map_set_projection_mode(m.ptr, &cmode); status != C.MLN_STATUS_OK {
			return statusError("mln_map_set_projection_mode", status)
		}
		return nil
	})
}

// RequestRepaint asks a continuous-mode map to repaint. Returns
// StatusInvalidState wrapped in *Error when called on a static or tile
// map; those modes drive rendering through Map.RenderStill instead.
func (m *Map) RequestRepaint() error {
	if m == nil {
		return errClosed("Map.RequestRepaint", "map")
	}
	return m.rt.runOnOwner("Map.RequestRepaint", func() error {
		if m.ptr == nil {
			return errClosed("Map.RequestRepaint", "map")
		}
		if status := C.mln_map_request_repaint(m.ptr); status != C.MLN_STATUS_OK {
			return statusError("mln_map_request_repaint", status)
		}
		return nil
	})
}

// Projection is a standalone projection helper. It snapshots the map's
// transform at creation; later changes to the map's camera or
// projection do NOT update the helper. Useful for camera fitting
// (SetVisibleCoordinates) or coordinate conversions without touching
// the live map.
//
// Projection is owner-thread affine to its source map's Runtime. All
// methods dispatch through that Runtime, so callers may use Projection
// from any goroutine.
type Projection struct {
	rt  *Runtime
	ptr *C.mln_map_projection
}

// NewProjection creates a standalone projection helper from this map's
// current transform. The helper owns projection and camera state only —
// not style, resources, or render targets.
func (m *Map) NewProjection() (*Projection, error) {
	if m == nil {
		return nil, errClosed("Map.NewProjection", "map")
	}
	p := &Projection{rt: m.rt}
	err := m.rt.runOnOwner("Map.NewProjection", func() error {
		if m.ptr == nil {
			return errClosed("Map.NewProjection", "map")
		}
		var out *C.mln_map_projection
		if status := C.mln_map_projection_create(m.ptr, &out); status != C.MLN_STATUS_OK {
			return statusError("mln_map_projection_create", status)
		}
		p.ptr = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Close destroys the projection helper. Idempotent.
func (p *Projection) Close() error {
	if p == nil {
		return nil
	}
	return p.rt.runOnOwner("Projection.Close", func() error {
		if p.ptr == nil {
			return nil
		}
		if status := C.mln_map_projection_destroy(p.ptr); status != C.MLN_STATUS_OK {
			return statusError("mln_map_projection_destroy", status)
		}
		p.ptr = nil
		return nil
	})
}

// GetCamera returns the projection helper's current camera snapshot.
func (p *Projection) GetCamera() (Camera, error) {
	if p == nil {
		return Camera{}, errClosed("Projection.GetCamera", "projection")
	}
	var out Camera
	err := p.rt.runOnOwner("Projection.GetCamera", func() error {
		if p.ptr == nil {
			return errClosed("Projection.GetCamera", "projection")
		}
		ccam := C.mln_camera_options_default()
		if status := C.mln_map_projection_get_camera(p.ptr, &ccam); status != C.MLN_STATUS_OK {
			return statusError("mln_map_projection_get_camera", status)
		}
		out = Camera{
			Fields:    CameraField(ccam.fields),
			Latitude:  float64(ccam.latitude),
			Longitude: float64(ccam.longitude),
			Zoom:      float64(ccam.zoom),
			Bearing:   float64(ccam.bearing),
			Pitch:     float64(ccam.pitch),
		}
		return nil
	})
	return out, err
}

// SetCamera applies the fields indicated by cam.Fields to the helper's
// camera. Other fields keep their current values.
func (p *Projection) SetCamera(cam Camera) error {
	if p == nil {
		return errClosed("Projection.SetCamera", "projection")
	}
	return p.rt.runOnOwner("Projection.SetCamera", func() error {
		if p.ptr == nil {
			return errClosed("Projection.SetCamera", "projection")
		}
		ccam := C.mln_camera_options_default()
		ccam.fields = C.uint32_t(cam.Fields)
		ccam.latitude = C.double(cam.Latitude)
		ccam.longitude = C.double(cam.Longitude)
		ccam.zoom = C.double(cam.Zoom)
		ccam.bearing = C.double(cam.Bearing)
		ccam.pitch = C.double(cam.Pitch)
		if status := C.mln_map_projection_set_camera(p.ptr, &ccam); status != C.MLN_STATUS_OK {
			return statusError("mln_map_projection_set_camera", status)
		}
		return nil
	})
}

// SetVisibleCoordinates updates the helper camera so all coords are
// visible within padding (logical pixels, top/left/bottom/right).
// Read the resulting camera back with GetCamera. Returns
// StatusInvalidArgument if coords is empty.
func (p *Projection) SetVisibleCoordinates(coords []LatLng, padding EdgeInsets) error {
	if p == nil {
		return errClosed("Projection.SetVisibleCoordinates", "projection")
	}
	if len(coords) == 0 {
		return &Error{
			Status:  StatusInvalidArgument,
			Op:      "Projection.SetVisibleCoordinates",
			Message: "coords must be non-empty",
		}
	}
	return p.rt.runOnOwner("Projection.SetVisibleCoordinates", func() error {
		if p.ptr == nil {
			return errClosed("Projection.SetVisibleCoordinates", "projection")
		}
		cIn := make([]C.mln_lat_lng, len(coords))
		for i, c := range coords {
			cIn[i] = c.toC()
		}
		cPad := padding.toC()
		if status := C.mln_map_projection_set_visible_coordinates(
			p.ptr,
			(*C.mln_lat_lng)(unsafe.Pointer(&cIn[0])),
			C.size_t(len(cIn)),
			cPad,
		); status != C.MLN_STATUS_OK {
			return statusError("mln_map_projection_set_visible_coordinates", status)
		}
		return nil
	})
}

// PixelForLatLng converts a coordinate using the helper's snapshot.
func (p *Projection) PixelForLatLng(coord LatLng) (ScreenPoint, error) {
	if p == nil {
		return ScreenPoint{}, errClosed("Projection.PixelForLatLng", "projection")
	}
	var out ScreenPoint
	err := p.rt.runOnOwner("Projection.PixelForLatLng", func() error {
		if p.ptr == nil {
			return errClosed("Projection.PixelForLatLng", "projection")
		}
		var pt C.mln_screen_point
		if status := C.mln_map_projection_pixel_for_lat_lng(p.ptr, coord.toC(), &pt); status != C.MLN_STATUS_OK {
			return statusError("mln_map_projection_pixel_for_lat_lng", status)
		}
		out = screenPointFromC(pt)
		return nil
	})
	return out, err
}

// LatLngForPixel converts a screen point using the helper's snapshot.
func (p *Projection) LatLngForPixel(point ScreenPoint) (LatLng, error) {
	if p == nil {
		return LatLng{}, errClosed("Projection.LatLngForPixel", "projection")
	}
	var out LatLng
	err := p.rt.runOnOwner("Projection.LatLngForPixel", func() error {
		if p.ptr == nil {
			return errClosed("Projection.LatLngForPixel", "projection")
		}
		var ll C.mln_lat_lng
		if status := C.mln_map_projection_lat_lng_for_pixel(p.ptr, point.toC(), &ll); status != C.MLN_STATUS_OK {
			return statusError("mln_map_projection_lat_lng_for_pixel", status)
		}
		out = latLngFromC(ll)
		return nil
	})
	return out, err
}
