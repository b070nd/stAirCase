//go:build !barriers

package barrier

// Hit marks a point a drill may hold the process at. In this build it does nothing.
func Hit(string) {}
