package maplibre

/*
#include "maplibre_native_c.h"
*/
import "C"

import "fmt"

// LatLng is a geographic coordinate in degrees. Latitude must be finite
// and within [-90, 90]; longitude must be finite. Mirrors mln_lat_lng.
type LatLng struct {
	Latitude  float64
	Longitude float64
}

// EdgeInsets is a screen-space inset in logical map pixels, used when
// fitting visible coordinates into a projection helper. Mirrors
// mln_edge_insets.
type EdgeInsets struct {
	Top    float64
	Left   float64
	Bottom float64
	Right  float64
}

// ProjectedMeters is a Spherical Mercator coordinate in meters. Most
// callers should use LatLng; this exists for the standalone Mercator
// helpers (ProjectedMetersForLatLng / LatLngForProjectedMeters).
// Mirrors mln_projected_meters.
type ProjectedMeters struct {
	Northing float64 // meters north of the equator
	Easting  float64 // meters east of the prime meridian
}

// ProjectionField is a bitmask for ProjectionMode indicating which
// fields are populated when read or which fields to apply when writing.
// Mirrors mln_projection_mode_field.
type ProjectionField uint32

const (
	ProjectionFieldAxonometric ProjectionField = ProjectionField(C.MLN_PROJECTION_MODE_AXONOMETRIC)
	ProjectionFieldXSkew       ProjectionField = ProjectionField(C.MLN_PROJECTION_MODE_X_SKEW)
	ProjectionFieldYSkew       ProjectionField = ProjectionField(C.MLN_PROJECTION_MODE_Y_SKEW)
)

// ProjectionMode controls the live render transform. It does NOT change
// the geographic coordinate model — pixel/lat-lng conversions are
// unaffected. Mirrors mln_projection_mode.
//
// When passed to SetProjectionMode, only the fields indicated by
// Fields are applied. When returned from GetProjectionMode, all fields
// are populated.
type ProjectionMode struct {
	Fields      ProjectionField
	Axonometric bool
	XSkew       float64
	YSkew       float64
}

func (l LatLng) toC() C.mln_lat_lng {
	return C.mln_lat_lng{latitude: C.double(l.Latitude), longitude: C.double(l.Longitude)}
}

func latLngFromC(l C.mln_lat_lng) LatLng {
	return LatLng{Latitude: float64(l.latitude), Longitude: float64(l.longitude)}
}

func (p ScreenPoint) toC() C.mln_screen_point {
	return C.mln_screen_point{x: C.double(p.X), y: C.double(p.Y)}
}

func screenPointFromC(p C.mln_screen_point) ScreenPoint {
	return ScreenPoint{X: float64(p.x), Y: float64(p.y)}
}

func (i EdgeInsets) toC() C.mln_edge_insets {
	return C.mln_edge_insets{
		top:    C.double(i.Top),
		left:   C.double(i.Left),
		bottom: C.double(i.Bottom),
		right:  C.double(i.Right),
	}
}

// ProjectedMetersForLatLng converts a geographic coordinate to its
// Spherical Mercator (EPSG:3857) projected-meter representation. This
// is a pure helper — it does not depend on any Map or Runtime, so any
// goroutine may call it.
func ProjectedMetersForLatLng(coord LatLng) (ProjectedMeters, error) {
	var out C.mln_projected_meters
	if status := C.mln_projected_meters_for_lat_lng(coord.toC(), &out); status != C.MLN_STATUS_OK {
		return ProjectedMeters{}, statusError("mln_projected_meters_for_lat_lng", status)
	}
	return ProjectedMeters{Northing: float64(out.northing), Easting: float64(out.easting)}, nil
}

// LatLngForProjectedMeters is the inverse of ProjectedMetersForLatLng.
// Pure helper; safe from any goroutine.
func LatLngForProjectedMeters(meters ProjectedMeters) (LatLng, error) {
	cm := C.mln_projected_meters{
		northing: C.double(meters.Northing),
		easting:  C.double(meters.Easting),
	}
	var out C.mln_lat_lng
	if status := C.mln_lat_lng_for_projected_meters(cm, &out); status != C.MLN_STATUS_OK {
		return LatLng{}, statusError("mln_lat_lng_for_projected_meters", status)
	}
	return latLngFromC(out), nil
}

// String renders LatLng as "lat,lng" with 6 decimals (~10 cm at the
// equator) for log lines and tests.
func (l LatLng) String() string {
	return fmt.Sprintf("%.6f,%.6f", l.Latitude, l.Longitude)
}
