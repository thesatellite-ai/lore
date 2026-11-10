package host

// pinrefresh.go — after an upgrade, the project's auto-pinned
// lore-sync-merge workflow follows the running release (E-FATES-5; v0.1.17
// sync_wire.go ensureGitWiring → refreshSyncActionPin, now
// adapters/reposync RefreshPin), with v0.1.17's notice:
//
//	lore sync: updated .github/workflows/lore-sync-merge.yml to lore v0.1.19 (was v0.1.18) — commit it
//
//	operation succeeded ─▶ CLI run, not --read-only, writes rows or is
//	   `lore sync`, LORE_SYNC / LORE_SYNC_GIT not "0" ─▶ RefreshPin(project)
//	   ─▶ moved: notice (quiet under LORE_SYNC_QUIET=1) · left alone with a
//	   reason (hand-edited, unreadable): warning, always
//
// A read-only command never runs it: the workflow is a tracked file and a
// read must not change one (the git wiring after a read, 33d1c7c9, touches
// only the clone's own config and hooks plus .gitattributes). RefreshPin
// itself leaves every user-pinned (`--lore-version`) file, every foreign
// file, a development build and a downgrade alone. It runs after the
// operation, so a refused write changes no file. Once per process.

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/reposync"
	"github.com/thesatellite-ai/lore/op"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// HookPinRefresh names the hook in errors and tests.
const HookPinRefresh = "pin-refresh"

// The notice's prefix and its quiet switch, v0.1.17's (sync_wire.go
// syncLogPrefix, envSyncQuiet = "1"). Own names so this file builds on its
// own; the git wiring's notices use the same values.
const (
	pinNoticePrefix = "lore sync: "
	pinQuietEnv     = "LORE_SYNC_QUIET"
	pinQuietOn      = "1"
)

// PinRefreshHook refreshes the workflow pin of the project holding workDir
// to version after every qualifying operation (see the file comment),
// printing its notice on w.
func PinRefreshHook(workDir, version string, w io.Writer) Hook {
	var once sync.Once
	return Hook{Name: HookPinRefresh, Around: func(ctx context.Context, run *Run, next Next) (op.Result, error) {
		res, err := next(ctx)
		if err != nil || !NeedsPinRefresh(run) {
			return res, err
		}
		once.Do(func() { refreshPin(ctx, workDir, version, w) })
		return res, err
	}}
}

// NeedsPinRefresh reports whether run may move the workflow pin: a CLI
// command (REST requests of `lore api` leave tracked files to the person's
// own commands), never under --read-only or LORE_READ_ONLY=1, only for an
// operation that writes rows or for `lore sync` (read access, v0.1.17 ran
// the wiring after its pass), and not under LORE_SYNC=0 (live rows off,
// pin left alone; rows are still written to .lore/data, BUGFIX-05) or
// LORE_SYNC_GIT=0 (lore leaves git and its tracked wiring alone).
func NeedsPinRefresh(run *Run) bool {
	if run == nil || run.Cmd == nil || run.Entry == nil || run.ReadOnly {
		return false
	}
	if os.Getenv(EnvSync) == envOff || os.Getenv(EnvSyncGit) == envOff {
		return false
	}
	spec := run.Entry.Spec()
	return spec.Access == op.AccessWrite || spec.Name == opssync.OpRun
}

// refreshPin runs reposync.RefreshPin for the project holding workDir and
// prints the outcome. Best effort: the workflow only matters in CI, so a
// failure is a warning, never a failed command.
func refreshPin(ctx context.Context, workDir, version string, w io.Writer) {
	root := liveProjectRoot(workDir)
	if root == "" {
		return // not a lore project in a git checkout: no workflow of its own
	}
	r, err := reposync.RefreshPin(ctx, root, version)
	if err != nil {
		r = reposync.PinRefresh{Note: "could not check the lore-sync-merge workflow: " + err.Error()}
	}
	if r.To != "" && os.Getenv(pinQuietEnv) != pinQuietOn {
		_, _ = fmt.Fprintf(w, "%supdated %s to lore %s (was %s) — commit it\n", pinNoticePrefix, reposync.DisplayPath(root, r.Path), r.To, r.From) // stderr notice; nowhere to report a failed write
	}
	if r.Note != "" {
		_, _ = fmt.Fprintln(w, pinNoticePrefix+reposync.DisplayPath(root, r.Path)+": "+r.Note) // as above
	}
}
