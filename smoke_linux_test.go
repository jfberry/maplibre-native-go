//go:build linux

package maplibre

import "testing"

func attachSmokeSession(_ *testing.T, m *Map, w, h uint32, scale float64) (*TextureSession, error) {
	return m.AttachVulkanTexture(w, h, scale)
}
