package host

// writeloc.go — WriteLocationHook: a command that wrote rows names the
// files it wrote, on standard error after it finished (plain files sweep
// 3.4, 2026-11-12: "every write names the store/folder/file it changed").
// stdout and --json stay as they were; a read gets no log at all.

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire"
)

// HookWriteLocation is WriteLocationHook's name.
const HookWriteLocation = "write-location"

// WriteLocationHook prints where each writing command's rows went.
func WriteLocationHook() Hook { return writeLocationHook(os.Stderr) }

// writeLocationHook prints to w (tests pass a buffer).
func writeLocationHook(w io.Writer) Hook {
	return Hook{Name: HookWriteLocation, Around: func(ctx context.Context, run *Run, next Next) (op.Result, error) {
		if run.Entry == nil || run.Entry.Spec().OnlyReads() || os.Getenv(EnvSyncQuiet) == envOn {
			return next(ctx)
		}
		ctx, log := wire.WithWriteLog(ctx)
		res, err := next(ctx)
		if err == nil {
			for _, l := range wire.WrittenLines(log.Files()) {
				_, _ = fmt.Fprintln(w, l)
			}
		}
		return res, err
	}}
}
