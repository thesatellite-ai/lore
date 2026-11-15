package host

import (
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
)

// TestMain runs every test of this package with the macOS temp-folder
// shape (devco D-413): $TMPDIR is a symlink to the real folder, as macOS
// /var links to /private/var, so a path compared without model/fspath
// fails on the Linux gate too.
func TestMain(m *testing.M) {
	cleanup := testenv.LinkTemp("lore-host-")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
