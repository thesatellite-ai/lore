// live_doctor.go — the live rows' debug tools (LORE_TEAM_SPEC.md §9.4):
// doctor rows for F1–F8, `lore explain <id>` (every copy of a row, which
// one won and why, the land decision) and `--trace` (every git command
// with its timing).
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/live"
)

// doctorLive adds one line per live-rows check to the doctor report. Only
// real problems are warnings (they degrade health); facts every team with
// live rows has (rows only in live) are notes.
func doctorLive(ctx context.Context, projectRoot string, r *doctorReport) {
	s := liveFor(ctx, projectRoot)
	if s == nil {
		return
	}
	warn := func(msg string) {
		r.Warnings = append(r.Warnings, "live: "+msg)
		if r.Status == doctorHealthy {
			r.Status = doctorDegraded
		}
	}
	note := func(msg string) { r.Notes = append(r.Notes, "live: "+msg) }
	if s.ref.Remote != nil {
		st, err := gitremote.EnsureTrackingRefspec(ctx, s.repo, s.ref.RemoteName)
		switch {
		case err != nil:
			warn("cannot check the fetch refspec: " + err.Error())
		case len(st.Unsafe) > 0:
			warn("a fetch refspec writes straight into refs/lore/* and can overwrite unpushed live writes on a plain git fetch: " + strings.Join(st.Unsafe, ", ") + " (remove it; lore uses refs/lore-remote/)")
		default:
			note("tracking refspec installed, not forced into refs/lore/*")
		}
	}
	if why := readLiveNoteText(s.stateDir, liveNoteRefused); why != "" {
		warn("the remote refused a push to refs/lore/live, so live rows stay on this machine (the host may allow only branches and tags): " + why)
	}
	if s.blocked != nil {
		warn("the remote live ref was deleted or rewritten; writes are stopped until `lore live restore`")
	}
	if at := live.FetchStamp(s.stateDir); !at.IsZero() {
		note("last fetch " + s.clk.Now().Sub(at).Round(time.Second).String() + " ago")
	}
	snap, err := s.ref.Snapshot(ctx)
	if err == nil && snap.Ahead() {
		p, _ := s.ref.Pending() // display only: max(len, 1) below still reports the unpushed state
		note(fmt.Sprintf("%d live write(s) not pushed yet", max(len(p), 1)))
	}
	folder := s.folderFor(projectRoot)
	plan, err := s.plan(ctx, folder, nil)
	if err != nil {
		warn("read live ref: " + err.Error())
		return
	}
	branch := s.defaultBranch(ctx)
	only, old, waiting, conflicts := 0, 0, 0, 0
	now := s.clk.Now()
	for _, p := range plan {
		if p.File.Deleted() {
			continue
		}
		only++
		if p.File.Block.Conflict {
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
		note(liveOnlyNote(only, branch))
	}
	if waiting > 0 {
		why := "the next live write or `lore sync` lands them"
		if readLiveNoteText(s.stateDir, liveNoteProtected) != "" {
			why = liveProtectedWhy(branch)
		}
		note(fmt.Sprintf("%d row(s) waiting to land; %s", waiting, why))
	}
	if old > 0 {
		warn(liveOldWarn(old, branch))
	}
	if conflicts > 0 {
		warn(fmt.Sprintf("%d live row(s) carry conflict markers and cannot land (`lore sync conflicts`)", conflicts))
	}
	if n, err := s.ref.CommitCount(ctx); err == nil && n > 0 {
		_, tree, _ := s.ref.LocalTree(ctx) // display only: an unreadable tree shows epoch 0
		note(fmt.Sprintf("live ref: %d commit(s) since the last compaction (epoch %d; automatic past %d)", n, tree.Epoch(), s.cfg.CompactAfter))
	}
	doctorLiveHooks(ctx, s, warn, note)
	if out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--is-shallow-repository"}}); err == nil && strings.TrimSpace(string(out)) == "true" {
		warn("shallow clone: landing and merged checks need the default branch's history")
	}
}

// The doctor and explain lines below name the repo's real default branch
// (liveSession.defaultBranch: default_branch: in config, else origin/HEAD,
// else main), never a hardcoded "main", the same way printLandOutcome does.

// liveOnlyNote is the doctor note for rows that exist only in live.
func liveOnlyNote(n int, branch string) string {
	return fmt.Sprintf("%d row(s) exist only in live (forks, ZIP downloads and shallow clones do not carry refs/lore/*; open rows reach %s at live.max_age)", n, branch)
}

// liveProtectedWhy says why rows wait when the default branch is protected.
func liveProtectedWhy(branch string) string {
	return branch + " is protected: they ride on your next push"
}

// liveOldWarn is the doctor warning for live rows past live.max_age.
func liveOldWarn(n int, branch string) string {
	return fmt.Sprintf("%d live row(s) older than live.max_age not copied to %s yet (run `lore sync`)", n, branch)
}

// liveHooksMissingWarn is the doctor warning for missing hook blocks.
func liveHooksMissingWarn(missing []string, branch string) string {
	return "git hook block missing in " + strings.Join(missing, ", ") + " (rows cannot ride along on a protected " + branch + "); run any lore command (such as `lore sync`) to install it"
}

// explainLeaveLine is explain's land line for a finished row.
func explainLeaveLine(branch string) string {
	return "leaves live on the next sync (finished, " + branch + " holds it)"
}

// explainWaitsLine is explain's land line while the default branch is protected.
func explainWaitsLine(branch string) string {
	return "waits: " + branch + " is protected; rides on your next push"
}

// doctorLiveHooks checks the post-commit and pre-push blocks (F6).
func doctorLiveHooks(ctx context.Context, s *liveSession, warn, note func(string)) {
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}})
	if err != nil {
		return
	}
	dir := strings.TrimSpace(string(out))
	var missing []string
	for _, h := range []string{hookPostCommit, hookPrePush} {
		b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, h)))
		if err != nil || !strings.Contains(string(b), liveHookMarker) {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		warn(liveHooksMissingWarn(missing, s.defaultBranch(ctx)))
		return
	}
	note("post-commit and pre-push hook blocks installed")
}

// ---------------------------------------------------------------- explain

// explain prints F1's view of one id.
func (s *liveSession) explain(ctx context.Context, w *strings.Builder, folder, id string) error {
	_, local, err := s.ref.LocalTree(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "read live ref").WithCause(err)
	}
	rows, _ := local.Rows(folder)
	var key live.RowKey
	var lf *live.File
	for k, f := range rows {
		if k.ID == id {
			key, lf = k, &f
			break
		}
	}
	snap, _ := s.ref.Snapshot(ctx) // display only (`lore live explain`): unreadable refs show as none
	fmt.Fprintf(w, "%s in %s (folder %s)\n", id, s.toplevel, folder)
	if at := live.FetchStamp(s.stateDir); !at.IsZero() {
		fmt.Fprintf(w, "  fetched %s ago\n", s.clk.Now().Sub(at).Round(time.Second))
	}
	if lf != nil {
		state := "row"
		if lf.Deleted() {
			state = "tombstone"
		}
		fmt.Fprintf(w, "  live (local %s): %s, live since %s, hlc %s", shortSHA(snap.Local), state, lf.Block.LiveSince.Format(time.RFC3339), lf.Block.HLC)
		if !lf.Block.LastCopyAt.IsZero() {
			fmt.Fprintf(w, ", last copy %s", lf.Block.LastCopyAt.Format(time.RFC3339))
		}
		if lf.Block.Conflict {
			w.WriteString(", CONFLICT")
		}
		w.WriteString("\n")
		if snap.Tracking != snap.Local {
			fmt.Fprintf(w, "  live on the remote (tracking %s) differs: %d write(s) not pushed\n", shortSHA(snap.Tracking), max(1, len(mustPending(s))))
		}
	} else {
		fmt.Fprintln(w, "  live: not present")
	}
	table := key.Table
	if table == "" {
		table = s.findTable(ctx, folder, id)
	}
	branchPath := mainDataPath(folder, table+"/"+id+".json")
	if table != "" {
		if b, ok, _ := s.repo.ReadFile(ctx, "HEAD", branchPath); ok { // display only: a read error shows as absent
			fmt.Fprintf(w, "  this branch (HEAD): %s (%d bytes)\n", branchPath, len(b))
		} else {
			fmt.Fprintln(w, "  this branch (HEAD): not present")
		}
		mv := s.mainView(ctx, folder)
		if b := mv.body(ctx, live.RowKey{Table: table, ID: id}); b != nil {
			fmt.Fprintf(w, "  %s: present\n", strings.TrimPrefix(mv.ref, "refs/remotes/"))
		} else if mv.purged[id] {
			fmt.Fprintf(w, "  %s: deleted on purpose (_purged.json)\n", strings.TrimPrefix(mv.ref, "refs/remotes/"))
		} else {
			fmt.Fprintf(w, "  %s: not present\n", strings.TrimPrefix(mv.ref, "refs/remotes/"))
		}
	}
	onMainOnly := false
	if lf == nil && table == "" {
		if b, ok := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}}); ok == nil && strings.TrimSpace(string(b)) == s.defaultBranch(ctx) {
			mv := s.mainView(ctx, folder)
			if t := s.findTableAt(ctx, mv.ref, folder, id); t != "" {
				onMainOnly = true
				fmt.Fprintf(w, "  %s: present (%s)\n", strings.TrimPrefix(mv.ref, "refs/remotes/"), t)
			}
		}
	}
	switch {
	case onMainOnly:
		fmt.Fprintln(w, "  wins: the fetched default branch's copy, by the "+ruleDefaultReadThrough+" rule (you are on the default branch and have not pulled it yet)")
	case lf != nil && lf.Deleted():
		fmt.Fprintln(w, "  wins: the live tombstone (rule 4): the row is deleted; lookup stops here")
	case lf != nil:
		fmt.Fprintln(w, "  wins: the live copy (rule 4: if a row id is in live, live wins)")
	case table != "":
		fmt.Fprintln(w, "  wins: this branch's copy (not live)")
	default:
		fmt.Fprintln(w, "  not found in live or on this branch; "+livePeekHint(s.ref.RemoteName, s.defaultBranch(ctx), ""))
	}
	if lf != nil {
		plan, err := s.plan(ctx, folder, nil)
		if err == nil {
			for _, p := range plan {
				if p.Key.ID != id {
					continue
				}
				what := string(p.Decision.Action) + ": " + p.Decision.Reason
				if p.Leave {
					what = explainLeaveLine(s.defaultBranch(ctx))
				} else if p.Decision.Action != live.LandStay && readLiveNoteText(s.stateDir, liveNoteProtected) != "" {
					what = explainWaitsLine(s.defaultBranch(ctx))
				}
				fmt.Fprintln(w, "  land: "+what)
			}
		}
	}
	return nil
}

func mustPending(s *liveSession) []live.PendingEntry {
	// Display only: an unreadable pending list shows as zero waiting
	// writes; `lore live doctor` reports the read failure separately.
	p, _ := s.ref.Pending()
	return p
}

// findTable finds the table of an id that is not live, from HEAD's
// .lore/data (one ls-tree).
func (s *liveSession) findTable(ctx context.Context, folder, id string) string {
	return s.findTableAt(ctx, "HEAD", folder, id)
}

// findTableAt is findTable on any revision.
func (s *liveSession) findTableAt(ctx context.Context, rev, folder, id string) string {
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"ls-tree", "-r", "--name-only", rev, "--", mainDataPath(folder, "")}})
	if err != nil {
		return ""
	}
	for _, p := range strings.Split(string(out), "\n") {
		if strings.HasSuffix(p, "/"+id+".json") {
			return filepath.Base(filepath.Dir(p))
		}
	}
	return ""
}

// ---------------------------------------------------------------- trace

// envTrace = "1" is --trace (§1).
const envTrace = "LORE_TRACE"

// tracingGit prints every git command lore runs on live refs, with its
// duration and outcome, to stderr. Arguments are git ref and path names,
// never row content (that goes through stdin and files), so nothing secret
// is printed.
type tracingGit struct{ inner gitref.Git }

func (g tracingGit) Run(ctx context.Context, inv gitref.Invocation) ([]byte, error) {
	start := time.Now()
	out, err := g.inner.Run(ctx, inv)
	status := "ok"
	if err != nil {
		status = "error"
		if code, ok := gitref.ExitCode(err); ok {
			status = fmt.Sprintf("exit %d", code)
		}
	}
	fmt.Fprintf(os.Stderr, "trace: git %s (%s, %s)\n", strings.Join(inv.Args, " "), time.Since(start).Round(time.Millisecond), status)
	return out, err
}

func liveTraceOn() bool { return os.Getenv(envTrace) == envValueOn }
