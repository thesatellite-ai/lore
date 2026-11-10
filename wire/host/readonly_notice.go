package host

// readonly_notice.go — FORMAT_EVOLUTION rule 2 on reads: a read command
// run by a lore that may not write the project prints one stderr line and
// succeeds (wire.App.ReadOnlyNotice holds the text). CLI only: a REST
// request (run.Cmd nil) gets no line, and the notice never reaches stdout,
// so --json stays clean.

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/thesatellite-ai/lore/op"
)

// HookReadOnlyNotice names the read-only notice hook.
const HookReadOnlyNotice = "read-only-notice"

// ReadOnlyNoticeHook prints notice(ctx) on w before the first CLI read
// operation of the process, when it is not "". notice is called lazily
// (the app is built after the hooks), at most once.
func ReadOnlyNoticeHook(w io.Writer, notice func(context.Context) string) Hook {
	var once sync.Once
	return Hook{Name: HookReadOnlyNotice, Before: func(ctx context.Context, run *Run) error {
		if run.Cmd == nil || run.Entry.Spec().Access != op.AccessRead {
			return nil
		}
		once.Do(func() {
			if line := notice(ctx); line != "" {
				_, _ = fmt.Fprintln(w, line) // stderr gone: nothing left to report to
			}
		})
		return nil
	}}
}
