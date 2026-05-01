// trampolines.c centralises the casts from //exported Go functions to
// the typed callback pointers MapLibre's C ABI expects. cgo auto-emits
// extern declarations for //exported Go functions into _cgo_export.h
// using their literal Go signatures (e.g. char* instead of const char*),
// so doing the casts in a separate translation unit that includes both
// _cgo_export.h and maplibre_native_c.h keeps the per-file Go preambles
// free of conflicting declarations.

#include "maplibre_native_c.h"
#include "_cgo_export.h"

mln_log_callback mlnGoGetLogTrampoline(void) {
    return (mln_log_callback)mlnGoLogTrampoline;
}

mln_resource_transform_callback mlnGoGetTransformTrampoline(void) {
    return (mln_resource_transform_callback)mlnGoResourceTransformTrampoline;
}
