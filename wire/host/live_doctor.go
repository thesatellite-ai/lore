package host

// live_doctor.go — the live-rows rows of `lore doctor` for a binary built
// from the root module (LORE_TEAM_SPEC §9.4; ported in part from saas/cmd/cli
// live_doctor.go doctorLive, P3-MODEB-7). Only real problems are warnings
// (they degrade health); facts every team with live rows has (rows only in
// live, writes not pushed) are notes:
//
//	warning: the remote refused refs/lore/live, the remote live ref was
//	         rewritten, rows past live.max_age not copied, conflict markers
//	note:    last fetch, writes not pushed, rows only in live, rows waiting
//	         to land (and why: a protected default branch rides on the push)
//
//	warning: an unsafe fetch refspec into refs/lore/*, missing post-commit /
//	         pre-push blocks, a shallow clone
//	note:    the tracking refspec, the hook blocks, the live ref's commits
//	         since the last compaction

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/live"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	"github.com/thesatellite-ai/lore/wire"
)

// liveDoctorPrefix starts every live doctor line.
const liveDoctorPrefix = "live: "

// doctorStoresOnlyNote is ops/doctor's note for a binary without lore.db
// checks (its Run adds it only when no Host is set; this Host replaces it).
const doctorStoresOnlyNote = "this build checks the stores only (no lore.db checks)"

// liveDoctor is the doctor Host LiveHook installs: the inner Host's base
// (or ops/doctor's default one) plus the live rows of the cwd project.
type liveDoctor struct {
	h     *liveHost
	inner opsdoctor.Host
}

// Base implements opsdoctor.Host.
func (d liveDoctor) Base(ctx context.Context, fix bool) (opsdoctor.Base, error) {
	base := opsdoctor.Base{Status: opsdoctor.StatusHealthy, DBOK: true, Notes: []string{doctorStoresOnlyNote}}
	if d.inner != nil {
		var err error
		if base, err = d.inner.Base(ctx, fix); err != nil {
			return base, err
		}
	}
	s, folder, err := liveSync{h: d.h}.here(ctx)
	if err != nil || s == nil {
		return base, nil // not a lore project in a git repo: no live rows to report
	}
	warn := func(msg string) {
		base.Warnings = append(base.Warnings, liveDoctorPrefix+msg)
		if base.Status == opsdoctor.StatusHealthy {
			base.Status = opsdoctor.StatusDegraded
		}
	}
	note := func(msg string) { base.Notes = append(base.Notes, liveDoctorPrefix+msg) }
	if s.ref.Remote != nil {
		// plain doctor reads: it looks at the refspec and adds none
		// (owner bug 6); --fix adds it
		look := gitremote.InspectTrackingRefspec
		if fix {
			look = gitremote.EnsureTrackingRefspec
		}
		st, rerr := look(ctx, s.repo, s.ref.RemoteName)
		switch {
		case rerr != nil:
			warn("cannot check the fetch refspec: " + rerr.Error())
		case len(st.Unsafe) > 0:
			warn("a fetch refspec writes straight into refs/lore/* and can overwrite unpushed live writes on a plain git fetch: " + strings.Join(st.Unsafe, ", ") + " (remove it; lore uses refs/lore-remote/)")
		case !st.Present:
			note("tracking refspec not configured, so a plain `git fetch` does not fetch live rows (lore's own fetch does); `lore sync` or `lore doctor --fix` adds it")
		default:
			note("tracking refspec installed, not forced into refs/lore/*")
		}
	}
	if why := s.noteText(liveNoteRefused); why != "" {
		warn("the remote refused a push to refs/lore/live, so live rows stay on this machine (the host may allow only branches and tags): " + why)
	}
	if s.blocked != nil {
		warn("the remote live ref was deleted or rewritten; writes are stopped until `lore live restore`")
	}
	now := s.clk.Now()
	if at := live.FetchStamp(s.stateDir); !at.IsZero() {
		note("last fetch " + now.Sub(at).Round(time.Second).String() + " ago")
	}
	if snap, serr := s.ref.Snapshot(ctx); serr == nil && snap.Ahead() {
		p, _ := s.ref.Pending() // display only: max(len, 1) still reports the unpushed state
		note(fmt.Sprintf("%d live write(s) not pushed yet", max(len(p), 1)))
	}
	plan, err := s.plan(ctx, folder, nil)
	if err != nil {
		warn("read live ref: " + err.Error())
		return base, nil
	}
	branch := s.defaultBranch(ctx)
	only, old, waiting, conflicts := 0, 0, 0, 0
	for _, p := range plan {
		if p.File.Deleted() {
			continue
		}
		only++
		if p.File.Block.Conflict || isConflict(p) {
			conflicts++
		}
		if p.Decision.Action != live.LandStay {
			waiting++
		}
		since := p.File.Block.LastCopyAt
		if since.IsZero() {
			since = p.File.Block.LiveSince
		}
		if !since.IsZero() && now.Sub(since) > s.cfg.MaxAge && !p.Leave {
			old++
		}
	}
	if only > 0 {
		note(fmt.Sprintf("%d row(s) exist only in live (forks, ZIP downloads and shallow clones do not carry refs/lore/*; open rows reach %s at live.max_age)", only, branch))
	}
	if waiting > 0 {
		why := "the next live write or `lore sync` lands them"
		if s.noteText(liveNoteProtected) != "" {
			why = branch + " is protected: they ride on your next push"
		}
		note(fmt.Sprintf("%d row(s) waiting to land; %s", waiting, why))
	}
	if old > 0 {
		warn(fmt.Sprintf("%d live row(s) older than live.max_age not copied to %s yet (run `lore sync`)", old, branch))
	}
	if conflicts > 0 {
		warn(fmt.Sprintf("%d live row(s) carry conflict markers and cannot land (`lore sync conflicts`)", conflicts))
	}
	if n, cerr := s.ref.CommitCount(ctx); cerr == nil && n > 0 {
		_, tree, _ := s.ref.LocalTree(ctx) // display only: an unreadable tree shows epoch 0
		note(fmt.Sprintf("live ref: %d commit(s) since the last compaction (epoch %d; automatic past %d)", n, tree.Epoch(), s.cfg.CompactAfter))
	}
	s.doctorHooks(ctx, branch, fix, warn, note)
	if out, serr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--is-shallow-repository"}}); serr == nil && strings.TrimSpace(string(out)) == "true" {
		warn("shallow clone: landing and merged checks need the default branch's history")
	}
	return base, nil
}

// doctorHooks checks that the ride-along blocks are in the hook folder git
// uses. A read only looks (owner bug 6). `lore fix` / `lore doctor --fix`
// install the missing ones (owner item 37: the fix of this check is a set-up
// step, announced as a fixed line), unless the hook folder is in the work
// tree of a client repo, whose tracked files lore never changes: then the
// warning says so and never points at lore fix. A failed install names its
// error, so the cause is not lost on stderr.
func (s *liveSession) doctorHooks(ctx context.Context, branch string, fix bool, warn, note func(string)) {
	dir, err := s.hooksDir(ctx)
	if err != nil {
		return
	}
	missing := missingRideHooks(dir)
	if len(missing) == 0 {
		note("post-commit and pre-push hook blocks installed")
		return
	}
	what := "git hook block missing in " + strings.Join(missing, ", ") + " (rows cannot ride along on a protected " + branch + ")"
	if s.clientHooksInWorkTree(ctx, dir) {
		warn(what + "; not installed: " + dir + " is in the work tree of a client repo, whose tracked files lore never changes; add the blocks by hand if this repo should carry rows on its pushes")
		return
	}
	if !fix {
		warn(what + "; run `lore fix` (or `lore sync`) to install it; a read never does")
		return
	}
	for _, h := range missing {
		if ierr := ensureHookBlock(dir, h, rideHookMarker, rideHookLines[h]); ierr != nil {
			warn(what + "; installing it failed: " + ierr.Error())
			return
		}
	}
	// the fixing run's line, which the re-check no longer gives: the
	// doctor lists it as fixed
	warn(what + "; installed now in " + dir)
}

// missingRideHooks are the ride-along hooks (post-commit, pre-push) in
// dir that do not hold lore's block.
func missingRideHooks(dir string) []string {
	var missing []string
	for _, h := range []string{hookPostCommit, hookPrePush} {
		b, rerr := os.ReadFile(filepath.Clean(filepath.Join(dir, h)))
		if rerr != nil || !strings.Contains(string(b), rideHookMarker) {
			missing = append(missing, h)
		}
	}
	return missing
}

// clientHooksInWorkTree reports whether the repo is marked a client repo
// (git config lore.clientrepo, wire/clientmark.go) and its hook folder is
// inside the work tree (a core.hooksPath into a tracked folder), where
// installing a block would change a file of theirs.
func (s *liveSession) clientHooksInWorkTree(ctx context.Context, dir string) bool {
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", wire.ClientRepoGitKey}})
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return false
	}
	rel, rerr := filepath.Rel(s.top, dir)
	if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false // outside the work tree (a folder of the person's own)
	}
	first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	return first != ".git"
}
