//go:build darwin

package machine

import (
	"bytes"
	"os/exec"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// ioregPath is the absolute path of macOS's ioreg (no PATH lookup).
const ioregPath = "/usr/sbin/ioreg"

// platformUUIDKey is the ioreg property holding the hardware UUID.
const platformUUIDKey = `"IOPlatformUUID"`

// osID returns the IOPlatformUUID. Why a subprocess: no pure-Go API reads
// it and the build is CGO-free (PROVENANCE_DESIGN 6.2).
func osID() (string, error) {
	out, err := exec.Command(ioregPath, "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", errs.Wrap(errs.NotFound, "machine: ioreg did not answer", err)
	}
	return parseIoreg(out)
}

// parseIoreg finds `"IOPlatformUUID" = "<uuid>"` in ioreg's output.
func parseIoreg(out []byte) (string, error) {
	for line := range bytes.SplitSeq(out, []byte("\n")) {
		s := string(line)
		_, after, ok := strings.Cut(s, platformUUIDKey)
		if !ok {
			continue
		}
		_, v, ok := strings.Cut(after, "=")
		if !ok {
			continue
		}
		if v = strings.Trim(strings.TrimSpace(v), `"`); v != "" {
			return v, nil
		}
	}
	return "", errs.New(errs.NotFound, "machine: ioreg shows no IOPlatformUUID")
}
