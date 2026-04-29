//go:build !darwin

package maplibre

// withAutoreleasePool is a no-op on non-darwin platforms. Vulkan does not
// require an autorelease pool around cgo calls.
func withAutoreleasePool(fn func()) {
	fn()
}
