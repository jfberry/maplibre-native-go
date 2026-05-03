//go:build darwin

package maplibre

import "testing"

func attachSmokeSession(_ *testing.T, m *Map, w, h uint32, scale float64) (*RenderSession, error) {
	return m.AttachMetalTexture(w, h, scale)
}
