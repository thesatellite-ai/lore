// format_gate.go — the format gate on the old binary's own write paths and
// the read-only notice (FORMAT_EVOLUTION rule 2, "read-only on every
// path"). Core writes are gated in the engine; these are the paths the old
// code still owns:
//
//	signals claim/renew/release ─► signals.Store.Gate ─► repoFormatGate(top)  (repo ref)
//	                                                  └► live ref CheckFloor   (store ref)
//	live write command ─► liveGate ─► live CheckFloor (features + floor) ─► repoFormatGate(top)
//	read command ─► readOnlyNotice ─► one stderr line, never on --json stdout
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/formatgate"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/live"
)

// repoVerdict checks the checkout's .lore/data/_meta.json (top holds .lore)
// against this binary. ok is false outside a project or before `lore init`
// wrote the file (FMT-ROOT: nothing declared, nothing gated). An
// unreadable file is the fail-closed E_OLD_LORE error.
func repoVerdict(top string) (formatgate.Verdict, bool, error) {
	if top == "" {
		return formatgate.Verdict{}, false, nil
	}
	d, exists, err := rowfs.ReadRepoFormat(top)
	if err != nil || !exists {
		return formatgate.Verdict{}, false, err
	}
	return formatgate.Check(version, d), true, nil
}

// repoFormatGate refuses a write (E_OLD_LORE) when this lore may not write
// the project at top; nil when it may or nothing is declared.
func repoFormatGate(top string) error {
	v, ok, err := repoVerdict(top)
	if err != nil || !ok {
		return err
	}
	return v.Err()
}

// repoSignalsGate is the signals.Store.Gate of a code repo's own signals
// ref: the repo's _meta.json decides (the signals belong to that project).
func repoSignalsGate(dir string) func(context.Context) error {
	return func(context.Context) error { return repoFormatGate(liveProjectRoot(dir)) }
}

// storeSignalsGate is the Gate of a store branch's signals ref: the store
// live ref's _live/meta.json decides (features + floor, the same Check).
func storeSignalsGate(ref *live.Ref) func(context.Context) error {
	return func(ctx context.Context) error {
		if ref == nil {
			return nil
		}
		return oldLoreLive(ref.CheckFloor(ctx, version))
	}
}

// oldLoreLive maps the live package's ErrBelowFloor to E_OLD_LORE with the
// lore upgrade hint (FORMAT_EVOLUTION 8.5); other errors pass unchanged.
func oldLoreLive(err error) error {
	if err == nil || !errors.Is(err, live.ErrBelowFloor) {
		return err
	}
	return errcodes.New(errcodes.OldLore, "this lore may not write here (read-only)").
		WithCause(err).WithHint("run `" + BinaryName + " upgrade`; reading still works")
}

// readNoticeVerb reports whether a command named name is a read that gets
// the read-only notice: the live read verbs minus restore (a trash
// restore writes, and the write gate already refuses it).
func readNoticeVerb(name string) bool { return liveReadVerbs[name] && name != "restore" }

// readOnlyNoticeOnce makes the notice print at most once per process (one
// command is one process).
var readOnlyNoticeOnce sync.Once

// readOnlyLine is the notice text for a verdict that is not writable
// ("" when it is): which features or floor block, and the fix.
func readOnlyLine(v formatgate.Verdict) string {
	if v.Writable {
		return ""
	}
	var why string
	switch {
	case len(v.Unknown) > 0:
		why = "feature " + strings.Join(v.Unknown, ", ") + " is unknown to this lore"
		if v.Floor != "" {
			why += "; needs lore " + v.Floor
		}
	default:
		why = "needs lore " + v.Floor + "; this is " + normVersion(v.Binary)
	}
	return "lore: read-only here (" + why + "); reading works, writing is off until you run `" + BinaryName + " upgrade`"
}

// readOnlyNotice prints the read-only line on w (stderr) once per command
// when this lore may not write the project at top. It never writes to
// stdout, so --json output stays clean (FORMAT_EVOLUTION rule 2, "Read
// commands print one line on stderr and succeed"). A read error is not
// the read command's business: the notice is skipped.
func readOnlyNotice(w io.Writer, top string) {
	v, ok, err := repoVerdict(top)
	if err != nil || !ok {
		return
	}
	line := readOnlyLine(v)
	if line == "" {
		return
	}
	readOnlyNoticeOnce.Do(func() { _, _ = fmt.Fprintln(w, line) }) // a failed stderr write has nowhere to go
}

// workflowsDir is where GitHub keeps CI workflows (the place a CI pin of
// lore lives, FORMAT_EVOLUTION section 4).
var workflowsDir = filepath.Join(".github", "workflows")

// doctorCIPins warns for every lore version a workflow under
// .github/workflows/ pins below the project's min_lore_version: that CI
// runs a read-only lore, and fail-closed checks there fail (FORMAT_EVOLUTION
// section 4 "CI on an old pinned lore version"). Called from doctor.go.
func doctorCIPins(repoTop string, r *doctorReport) {
	if repoTop == "" {
		return
	}
	_, m, ok, err := findProjectMeta(repoTop)
	if err != nil || !ok || m.MinLoreVersion == "" {
		return
	}
	wf := os.DirFS(filepath.Join(repoTop, workflowsDir)) // reads stay inside the workflows folder
	for _, pat := range []string{"*.yml", "*.yaml"} {
		files, _ := fs.Glob(wf, pat) // only ErrBadPattern, and the patterns are fixed
		for _, f := range files {
			b, rerr := fs.ReadFile(wf, f)
			if rerr != nil {
				continue
			}
			for _, p := range formatgate.CIPinsBelow(formatgate.CIPins(string(b)), m.MinLoreVersion) {
				r.Warnings = append(r.Warnings, fmt.Sprintf("%s:%d pins lore %s, below this project's min_lore_version %s: that CI is read-only here; pin %s or later (`%s upgrade --version` in the workflow)",
					path.Join(filepath.ToSlash(workflowsDir), f), p.Line, p.Version, m.MinLoreVersion, m.MinLoreVersion, BinaryName))
				degrade(r)
			}
		}
	}
}
