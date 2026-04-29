//go:build linux

package maplibre

import "testing"

func attachSmokeSession(_ *testing.T, m *Map) (*TextureSession, error) {
	return m.AttachVulkanTexture(256, 256, 1)
}
