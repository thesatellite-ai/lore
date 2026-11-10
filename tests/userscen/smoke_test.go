//go:build userscen

package userscen

import "testing"

// TestSmoke_VersionRuns proves the harness builds and drives the binary.
func TestSmoke_VersionRuns(t *testing.T) {
	w := newWorld(t)
	a := w.Human("person", "person@example.com")
	a.Lore(w.Root, "version").OK(t)
}
