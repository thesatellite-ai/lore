package host

import (
	"fmt"
	"os"

	"github.com/thesatellite-ai/lore/wire"
)

// NoticeHook prints the core's one-line notices (wire.Options.Notice, e.g.
// LiveNote) on stderr, so stdout (--json, piped output) stays the
// command's own; the old binary's coreNotice.
func NoticeHook() Hook {
	return Hook{Name: "notice", Options: func(o *wire.Options) {
		o.Notice = func(msg string) { _, _ = fmt.Fprintln(os.Stderr, msg) } // stderr gone: nothing left to report to
	}}
}
