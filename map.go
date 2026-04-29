package maplibre

/*
#include "maplibre_native_abi.h"
*/
import "C"

// Map owns a maplibre-native map handle bound to the runtime that created it.
// It is owner-thread affine to the runtime's dispatcher.
type Map struct {
	rt  *Runtime
	ptr *C.mln_map
}

// MapOptions configures NewMap. Width and Height are logical dimensions;
// physical render size is multiplied by ScaleFactor.
type MapOptions struct {
	Width       uint32
	Height      uint32
	ScaleFactor float64
}

// NewMap creates a map on the runtime owner thread.
//
// The runtime must be live; if it has been closed, the call returns an Error
// with StatusInvalidArgument.
func (r *Runtime) NewMap(opts MapOptions) (*Map, error) {
	if r == nil || r.ptr == nil {
		return nil, &Error{
			Status:  StatusInvalidArgument,
			Op:      "Runtime.NewMap",
			Message: "runtime is closed",
		}
	}

	m := &Map{rt: r}
	var err error
	r.d.do(func() {
		copts := C.mln_map_options_default()
		if opts.Width > 0 {
			copts.width = C.uint32_t(opts.Width)
		}
		if opts.Height > 0 {
			copts.height = C.uint32_t(opts.Height)
		}
		if opts.ScaleFactor > 0 {
			copts.scale_factor = C.double(opts.ScaleFactor)
		}

		var out *C.mln_map
		status := C.mln_map_create(r.ptr, &copts, &out)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_create", status)
			return
		}
		m.ptr = out
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

// Close destroys the map handle.
//
// Idempotent: safe to call on a nil Map or one already closed.
func (m *Map) Close() error {
	if m == nil || m.ptr == nil {
		return nil
	}
	var err error
	m.rt.d.do(func() {
		status := C.mln_map_destroy(m.ptr)
		if status != C.MLN_STATUS_OK {
			err = statusError("mln_map_destroy", status)
			return
		}
		m.ptr = nil
	})
	return err
}
