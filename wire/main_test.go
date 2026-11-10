package wire

import (
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
)

// TestMain starts every SQLite cache that wire's tests create from an empty
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
	sqlite.UseEmptyTemplates()
	os.Exit(m.Run())
}
