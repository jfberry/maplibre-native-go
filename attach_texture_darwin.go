//go:build darwin

package maplibre

// AttachTexture attaches a texture session using the platform-default
// backend. On darwin this is Metal (AttachMetalTexture).
//
// Use the explicit AttachMetalTexture / AttachVulkanTexture forms if you
// need to pass a specific device or Vulkan context.
func (m *Map) AttachTexture(width, height uint32, scaleFactor float64) (*TextureSession, error) {
	return m.AttachMetalTexture(width, height, scaleFactor)
}
