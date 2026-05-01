//go:build !mln_debug

package maplibre

// trackForLeak is a no-op in release builds. The mln_debug build tag
// swaps in a real implementation that prints leak warnings to stderr.
func trackForLeak(_ any, _ string, _ func() bool) {}
