//go:build !linux && !darwin && !windows

package machine

import "github.com/thesatellite-ai/lore/model/errs"

// osID: no machine id source is known for this OS, said plainly instead
// of guessing one.
func osID() (string, error) {
	return "", errs.New(errs.NotFound, "machine: no machine id source on this operating system")
}
