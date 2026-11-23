package wire

import (
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/internal/testenv"
)

// The CI variables (ciMarkers) the test process inherited are already gone
// when TestMain runs: detect_testbinary.go drops them in every test binary
// that links wire (under them lore behaves as in CI on purpose: redacted
// output, findings hidden, a CI session per run, no host name in the
// profile, reads without the live fetch), so a test that wants CI sets them
// itself.
//
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
	// the macOS temp-folder shape on every system (devco D-413): $TMPDIR
	// is a symlink to the real folder, as macOS /var links to
	// /private/var, so a path compared without model/fspath fails here too
	cleanup := testenv.LinkTemp("lore-wire-")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
