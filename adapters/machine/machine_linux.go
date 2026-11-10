//go:build linux

package machine

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// linuxIDFiles are read in order (systemd first, then the older D-Bus
// copy some distributions still ship alone).
var linuxIDFiles = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// osID returns the first non-empty Linux machine id.
func osID() (string, error) {
	for _, p := range linuxIDFiles {
		b, err := os.ReadFile(filepath.Clean(p)) // p is one of the fixed system paths above
		if err == nil && strings.TrimSpace(string(b)) != "" {
			return string(b), nil
		}
	}
	return "", errs.New(errs.NotFound, "machine: no machine id in /etc/machine-id or /var/lib/dbus/machine-id")
}
