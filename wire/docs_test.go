package wire

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/surface/docs"
)

// referenceDir holds the committed generated reference, relative to this
// package.
const referenceDir = "../docs/reference"

// TestReferenceIsCurrent is the drift test of spec 9.6: the committed
// command/error reference and OpenAPI file must equal what the real
// registry renders. A changed Summary, flag, route or error code fails
// here until `lore spec docs && lore spec openapi` is run.
func TestReferenceIsCurrent(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	docs.CheckStale(t, a.Registry(),
		filepath.Join(referenceDir, "cli-commands.md"),
		filepath.Join(referenceDir, "openapi.json"),
	)
}
