//go:build !loretest

package locktrace

// record does nothing in a release build (see the package comment).
func record(Event, string) {}
