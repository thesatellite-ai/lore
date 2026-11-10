package layers

import (
	"os"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// Tests here run `go list`, which starts git to stamp version info. Run
// from a git hook (`task check` in pre-push), the process inherits GIT_DIR
// and friends, git then reads the hook's repository instead of this module
// and go list fails (issue #6). Remove them from the whole test process once, before
// any test runs; a test that wants one sets it with t.Setenv.
func init() {
	for _, name := range gitenv.Names() {
		if err := os.Unsetenv(name); err != nil {
			panic("unset " + name + ": " + err.Error()) // only an invalid name fails, and Names has none
		}
	}
}
