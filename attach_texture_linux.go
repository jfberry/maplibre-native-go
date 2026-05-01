//go:build linux

package maplibre

// AttachTexture attaches a texture session using the platform-default
// backend. On linux this is Vulkan (AttachVulkanTexture) using the
// runtime's bundled instance/device/queue.
//
// Use AttachVulkanTextureWithContext if you need to inject your own
// Vulkan instance, device, or queue (e.g. for sharing with a windowing
// system).
func (m *Map) AttachTexture(width, height uint32, scaleFactor float64) (*TextureSession, error) {
	return m.AttachVulkanTexture(width, height, scaleFactor)
}
