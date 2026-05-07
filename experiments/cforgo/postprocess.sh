#!/usr/bin/env bash
# Post-processing patches that c-for-go can't apply via YAML alone.
# Each fix has a comment explaining why c-for-go gets it wrong.
set -e
cd "$(dirname "$0")/cforgo"

# (1) Identifier collision between a struct tag and an enum value that
# share the same C name modulo case. mbgl has both a struct
# `mln_runtime_event_offline_region_response_error` (a typed payload)
# and an enum value `MLN_RUNTIME_EVENT_OFFLINE_REGION_RESPONSE_ERROR`.
# C uses separate namespaces for these; Go does not. Rename the
# struct type everywhere to add a `Payload` suffix; leave the
# constant in const.go alone.
for f in types.go cgo_helpers.go; do
  perl -pi -e 's/\bRuntimeEventOfflineRegionResponseError\b(?!Payload)/RuntimeEventOfflineRegionResponseErrorPayload/g' "$f"
done

# (2) Bitmask typed constants. c-for-go emits
#   FooMaskA  FooMask = uint32(1) << uint32(0)
# which modern Go rejects: the typed constant declaration needs the
# RHS to be assignment-compatible with FooMask. Rewrite LHS-side
# uint32(1) to use the actual mask type. The shift count is left
# alone — Go's strict typing accepts an untyped uint32 shift amount.
perl -i -pe '
  s/(\s+\w+\s+(\w+)\s*=\s*)uint32\(1\)(\s*<<\s*(?:uint32\([0-9]+\)|\w+))/$1$2(1)$3/g
' const.go

# (3) The same bitmask pattern shows up where the shift count is a
# named constant (e.g. LogSeverityMaskInfo = uint32(1) << LogSeverityInfo).
# Already handled by the regex above thanks to the alternative branch.

# (3) Parameter shadowing the return type: c-for-go uses C parameter
# names verbatim (PascalCase), so when a function takes a parameter
# named `status` and returns `Status`, the parameter shadows the
# type and `(Status)(__ret)` becomes a function call. Rename the
# offending parameter to `arg` in the affected function.
perl -i -pe '
  s/func NetworkStatusSet\(Status uint32\) Status/func NetworkStatusSet(arg uint32) Status/;
  s/cStatus, cStatusAllocMap := \(C\.uint32_t\)\(Status\)/cStatus, cStatusAllocMap := (C.uint32_t)(arg)/;
' cforgo.go

# (4) c-for-go wraps every C struct with a Go struct carrying a
# hidden `refXXXXXXXX *C.mln_foo` cache. PassRef short-circuits on a
# non-nil cache and ignores Go-side field changes — which means any
# struct returned from a *Default() function (or any C return-by-value)
# can't be modified and re-submitted to the C side. Deref() copies
# values from the cache to Go fields but doesn't clear the cache,
# so PassRef still returns the stale C pointer. From an external
# package the cache field is unreachable.
#
# Patch: append a Reset method on every wrapper struct that clears the
# cache. Callers must call Reset() between Default() and any
# field-modify-then-pass usage.
cat >> cgo_helpers.go <<'GOEOF'

// Reset clears the internal C reference cache. Required between a
// *Default()-style read and any field modification + pass back to a
// C function. Without this, PassRef returns a stale (often dangling)
// cache pointer to the original C-side temporary and silently
// ignores Go-side field changes.
//
// This is a workaround for a c-for-go limitation, not part of
// the original generated API.
func (x *RuntimeOptions) Reset() { x.refc28b8f85 = nil }
func (x *MapOptions) Reset()     { x.ref8e9c84cf = nil }
GOEOF

# Discover the actual cache-field suffix per type — c-for-go names it
# `ref` plus an 8-char-or-so hash, varying per struct. The two added
# above cover RuntimeOptions and MapOptions; for a real binding you'd
# generate Reset() for every struct in cgo_helpers.go.

remaining=$(grep -c 'uint32(1) <<' const.go || true)
echo "post-process complete; $remaining bitmask issues remain (should be 0)"
