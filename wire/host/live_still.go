package host

// live_still.go — runs that leave the clone's .git exactly as it was
// (devco K-9, K-15).
//
// A read's live session normally takes the live lock (.git/lore/live.lock),
// fetches refs/lore/* when due (.git/lore/fetch-stamp, new objects, the
// tracking ref) and integrates the tracking ref (refs/lore/live and the
// .git/lore/lore-live.seen* marks). Two kinds of run must do none of it:
//
//	dry run            "changes nothing": not even lore's own state in .git
//	                   (lore fix --all --dry-run wrote lore-live.seen* into
//	                   a 0.1.17-format repo it says it leaves alone); plain
//	                   lore fix in such a repo leaves it alone the same way
//	CI form            lore check leaks --ci, render --check/--report/--trend,
//	                   and any read on a CI runner (CI, GITHUB_ACTIONS, …):
//	                   ids and structure from the checkout only, so no
//	                   fetch from the network and no write in .git
//
// Such a run reads the refs the clone already has, as they are. A CI run
// also skips the "git is not set up for lore's row files … lore sync sets
// up …" note: a CI job never runs lore sync, so the line is noise in every
// log (gitwire.go readWiring).

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/op"
	opscheck "github.com/thesatellite-ai/lore/ops/check"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	"github.com/thesatellite-ai/lore/wire"
)

// leaksCIFlag is lore check leaks' --ci (opscheck.LeaksIn.CI).
const leaksCIFlag = "ci"

// stillRun reports whether run must leave .git as it is (see the file
// comment): a dry run, a CI-form run, or plain lore fix in a repo still on
// the 0.1.17 format (D15: it only says `lore fix --migrate` and leaves the
// repo as it is; lore fix --all ran it in every such repo it skipped).
func stillRun(run *Run) bool {
	if run.Entry == nil {
		return false
	}
	return dryRun(run) || ciRun(run) || (run.OldRows && run.Entry.Spec().Name == opsfix.OpRun)
}

// ciRun reports whether run is a CI-form command: lore check leaks --ci,
// one of render's read forms (renderReads), or any read while a CI marker
// is set in the environment (wire.InCIEnv).
func ciRun(run *Run) bool {
	if run.Entry == nil {
		return false
	}
	return leaksCIRun(run) || renderReads(run) || (readRun(run) && wire.InCIEnv())
}

// leaksCIRun reports whether run is `lore check leaks --ci` (CLI flag or
// REST body). An input it cannot read is the CI form, the side that
// changes less.
func leaksCIRun(run *Run) bool {
	if run.Entry.Spec().Name != opscheck.OpLeaks {
		return false
	}
	if run.Cmd != nil {
		if f, err := run.Cmd.Flags().GetBool(leaksCIFlag); err == nil {
			return f
		}
		return slices.Contains(run.Args, "--"+leaksCIFlag)
	}
	if d, ok := run.Decoder.(op.JSONDecoder); ok && len(strings.TrimSpace(string(d.Body))) > 0 {
		var in opscheck.LeaksIn
		return json.Unmarshal(d.Body, &in) != nil || in.CI
	}
	return false
}

// envGitOptionalLocks is git's switch for the locks it takes only to save
// work for later: "0" keeps `git status` from refreshing and rewriting the
// index (.git/index) while it reads (git(1) GIT_OPTIONAL_LOCKS).
const envGitOptionalLocks = "GIT_OPTIONAL_LOCKS"

// stillGit makes every git this CLI process starts read without the
// optional index refresh, so a still run's `git status` calls leave
// .git/index as it was (devco K-15: render --check and doctor rewrote it in
// a CI clone). Only a CLI run sets it: the process runs that one command,
// whereas `lore api` serves many requests from one process environment.
func stillGit(run *Run) {
	if run.Cmd == nil {
		return
	}
	_ = os.Setenv(envGitOptionalLocks, "0") // only an invalid name fails
}
