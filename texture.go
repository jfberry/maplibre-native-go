package maplibre

import "unsafe"

// TextureFrame is the platform-neutral shape of a frame acquired from a
// texture session. Backend-specific data lives in the borrowed pointers:
// for Metal, Texture is id<MTLTexture> and Device is id<MTLDevice>; for
// Vulkan (when added), Texture is VkImage, Device is VkDevice, and an
// additional image-view handle will be carried in a future field.
//
// The borrowed pointers remain valid only until the frame is released.
type TextureFrame struct {
	Generation  uint64
	Width       uint32
	Height      uint32
	ScaleFactor float64
	FrameID     uint64
	Texture     unsafe.Pointer
	Device      unsafe.Pointer
	PixelFormat uint64
}
