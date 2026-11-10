package wire

// setupdebug.go — where the setup page writes its debug log
// (surface/setupweb/debuglog.go): beside the machine's local state.

import (
	"path/filepath"

	"github.com/thesatellite-ai/lore/surface/setupweb"
)

// SetupDebugLogPath is <home>/state/setup-debug.log.
func SetupDebugLogPath(home string) string {
	return filepath.Join(home, localStateDir, setupweb.DebugLogFile)
}
