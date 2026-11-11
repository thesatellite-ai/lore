package wire

import (
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
)

// TestMain first removes the CI variables (ciMarkers) the test process
// inherited: under them lore behaves as in CI on purpose (redacted output,
// findings hidden, a CI session per run, no host name in the profile), so a
// test that wants CI sets them itself. Without this every such test failed
// on a CI runner and passed on a workstation.
//
// It then starts every SQLite cache that wire's tests create from an empty
// template built once per test process (sqlite.UseEmptyTemplates) instead of
// running the full table DDL for each new cache: most of a test world's
// start-up cost. The cache is derived data and Open still syncs the copied
// file against the registry, so a test sees the same cache either way
// (adapters/cache/sqlite/template_test.go pins that).
//
// It composes with wire/internal/isolate: an isolated test's child process
// is this same test binary, so it passes through this TestMain too and gets
// the same templates.
func TestMain(m *testing.M) {
	for _, k := range ciMarkers {
		_ = os.Unsetenv(k)
	}
	sqlite.UseEmptyTemplates()
	os.Exit(m.Run())
}
