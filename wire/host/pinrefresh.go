package host

// pinrefresh.go — the project's auto-pinned lore-sync-merge workflow and a
// newer running release (E-FATES-5). Since devco:D-742 no command rewrites
// the workflow as a side effect: it is a tracked file, and `lore row move`
// silently moving its pin (0.1.18 -> 0.1.23, new checksum) put a change the
// person never asked for into their next commit. One place decides who
// writes it:
//
//	lore sync install-action   writes it (reposync.Install: the person's command)
//	lore doctor --fix          moves the pin (doctorWorkflowPin → reposync.RefreshPin)
//	every other command        writes nothing; a writing command or `lore sync`
//	                           that finds the pin stale prints one stderr line:
//
//	lore: .github/workflows/lore-sync-merge.yml pins lore v0.1.18; run `lore sync install-action` to update it to v0.1.23
//
//	operation succeeded ─▶ CLI run of a writing op or `lore sync`, not
//	   install-action or doctor (they handle the file themselves), no --json,
//	   LORE_SYNC / LORE_SYNC_GIT not "0" ─▶ reposync.PreviewPin(project)
//	   ─▶ stale: the line above (quiet under LORE_SYNC_QUIET=1) · left alone
//	   with a reason (hand-edited, unreadable): that reason, always
//
// PreviewPin leaves every user-pinned (`--lore-version`) file, every foreign
// file, a development build and a downgrade alone, exactly as the write
// does. Once per process, so a command that runs several operations says it
// once.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/reposync"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// HookPinRefresh names the hook in errors and tests.
const HookPinRefresh = "pin-refresh"

// The line's prefix, its quiet switch (v0.1.17's sync_wire.go envSyncQuiet
// = "1"; own names so this file builds on its own), the one command that
// writes the workflow, and the CLI flag that makes stdout a JSON document.
const (
	pinNoticePrefix   = "lore: "
	pinQuietEnv       = "LORE_SYNC_QUIET"
	pinQuietOn        = "1"
	pinInstallCommand = "lore sync install-action"
	pinJSONFlag       = "json"
	// pinStaleMarker is in every stale-pin line and in no note line.
	pinStaleMarker = "; run `" + pinInstallCommand + "` to update it to "
	// pinDoctorExtra is what doctor adds to the stale line: its own fix.
	pinDoctorExtra = " (or `lore doctor --fix`)"
)

// PinRefreshHook checks the workflow pin of the project holding workDir
// against version after every qualifying operation (see the file comment)
// and prints the stale-pin line on w. It never writes the workflow
// (devco:D-742).
func PinRefreshHook(workDir, version string, w io.Writer) Hook {
	var once sync.Once
	return Hook{Name: HookPinRefresh, Around: func(ctx context.Context, run *Run, next Next) (op.Result, error) {
		res, err := next(ctx)
		if err != nil || !NeedsPinRefresh(run) {
			return res, err
		}
		once.Do(func() { notePinStale(ctx, workDir, version, w) })
		return res, err
	}}
}

// NeedsPinRefresh reports whether run checks the workflow pin: a CLI
// command (REST requests of `lore api` print nothing), not under --json
// (stdout is a JSON document there; `lore doctor` reports the pin), only
// for an operation that writes rows or for `lore sync`, never for
// install-action or doctor (each handles the file itself), and not under
// LORE_SYNC=0 (live rows off) or LORE_SYNC_GIT=0 (lore leaves git and its
// tracked wiring alone). The check only reads, so --read-only and dry runs
// get the line too.
func NeedsPinRefresh(run *Run) bool {
	if run == nil || run.Cmd == nil || run.Entry == nil || jsonOutput(run) {
		return false
	}
	if os.Getenv(EnvSync) == envOff || os.Getenv(EnvSyncGit) == envOff {
		return false
	}
	spec := run.Entry.Spec()
	switch spec.Name {
	case opssync.OpInstallAction, opsdoctor.OpRun:
		return false
	}
	return spec.Access == op.AccessWrite || spec.Name == opssync.OpRun
}

// jsonOutput reports whether the CLI command was run with --json.
func jsonOutput(run *Run) bool {
	f := run.Cmd.Flags().Lookup(pinJSONFlag)
	return f != nil && f.Value.String() == "true"
}

// notePinStale prints the stale-pin line (or why lore leaves the file
// alone) for the project holding workDir. Best effort: the workflow only
// matters in CI, so a failure is a line, never a failed command.
func notePinStale(ctx context.Context, workDir, version string, w io.Writer) {
	root := liveProjectRoot(workDir)
	if root == "" {
		return // not a lore project in a git checkout: no workflow of its own
	}
	line := pinStaleLine(ctx, root, version, "")
	if line == "" || (pinIsStale(line) && os.Getenv(pinQuietEnv) == pinQuietOn) {
		return
	}
	_, _ = fmt.Fprintln(w, pinNoticePrefix+line) // stderr hint; nowhere to report a failed write
}

// pinIsStale tells a stale-pin line from a note (hand-edited, unreadable).
func pinIsStale(line string) bool { return strings.Contains(line, pinStaleMarker) }

// pinStaleLine is the text (without prefix) for the workflow of the project
// at root against version: "<file> pins lore <old>; run `lore sync
// install-action` to update it to <new>" plus extra, "<file>: <why it is
// left alone>", or "" when there is nothing to say. It writes nothing.
func pinStaleLine(ctx context.Context, root, version, extra string) string {
	r, err := reposync.PreviewPin(ctx, root, version)
	if err != nil {
		return "could not check the lore-sync-merge workflow: " + err.Error()
	}
	switch {
	case r.To != "":
		return fmt.Sprintf("%s pins lore %s%s%s%s", reposync.DisplayPath(root, r.Path), r.From, pinStaleMarker, r.To, extra)
	case r.Note != "":
		return reposync.DisplayPath(root, r.Path) + ": " + r.Note
	}
	return ""
}

// doctorWorkflowPin is doctor's part (devco:D-742): a stale auto-pinned
// workflow is a warning naming `lore sync install-action`; `lore doctor
// --fix` moves the pin (reposync.RefreshPin, which leaves a user-pinned or
// hand-edited file alone) and lists it as fixed.
func doctorWorkflowPin(ctx context.Context, workDir, version string, fix bool, warn func(string)) {
	root := liveProjectRoot(workDir)
	if root == "" {
		return
	}
	line := pinStaleLine(ctx, root, version, pinDoctorExtra)
	if line == "" {
		return
	}
	if !fix || !pinIsStale(line) {
		warn(line)
		return
	}
	r, err := reposync.RefreshPin(ctx, root, version)
	switch {
	case err != nil:
		warn(line + "; updating it failed: " + err.Error())
	case r.To != "":
		// the fixing run's line, which the re-check no longer gives
		warn(fmt.Sprintf("%s pinned lore %s; updated now to %s (commit it)", reposync.DisplayPath(root, r.Path), r.From, r.To))
	case r.Note != "":
		warn(reposync.DisplayPath(root, r.Path) + ": " + r.Note)
	}
}

// pinVersion is the release the workflow pins (LiveConfig.PinVersion, else
// the binary's Version).
func (h *liveHost) pinVersion() string {
	if h.cfg.PinVersion != "" {
		return h.cfg.PinVersion
	}
	return h.cfg.Version
}
