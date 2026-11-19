//go:build !loretest

package app

// pinVersion is the running release: a release build lets no environment
// variable change the version it pins the workflow to.
func pinVersion(version string) string { return version }
