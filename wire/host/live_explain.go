package host

// live_explain.go — `lore explain <id>` on cmd/lore (LORE_TEAM_SPEC 9,
// F1 "Debug"; port of v0.1.17 saas/cmd/cli live_doctor.go explain): every
// copy of one row id and which one wins, in lookup order
//
//	live ref (local) ─▶ this branch (HEAD) ─▶ origin/<default>
//	                 ─▶ wins: <rule> ─▶ land: <decision of the landing plan>
//
// Display only: unreadable refs and files show as absent, never as an
// error, except the live ref itself (nothing can be explained without it).
// Every line names the repo's real default branch (liveSession.
// defaultBranch), never a hardcoded "main".

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// ruleDefaultReadThrough names F1's default-branch read-through rule in
// explain's "wins" line (v0.1.17 live_wire.go wording).
const ruleDefaultReadThrough = "default-branch read-through (F1)"

// Explain implements opssync.Env (`lore explain <id>`).
func (l liveSync) Explain(ctx context.Context, in opssync.ExplainIn) (opssync.DoneOut, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return opssync.DoneOut{}, errs.New(errs.InvalidInput, "name a row id: lore explain <id>")
	}
	s, folder, err := l.here(ctx)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	if s == nil {
		return opssync.DoneOut{}, errs.New(errs.Unsupported, liveUnsupported)
	}
	lines, err := s.explainLines(ctx, folder, id)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	return opssync.DoneOut{Items: []string{id}, Lines: lines}, nil
}

// explainLeaveLine is explain's land line for a finished row.
func explainLeaveLine(branch string) string {
	return "leaves live on the next sync (finished, " + branch + " holds it)"
}

// explainWaitsLine is explain's land line while the default branch is protected.
func explainWaitsLine(branch string) string {
	return "waits: " + branch + " is protected; rides on your next push"
}

// explainPeekHint points at `lore sync peek` for an id found nowhere.
func explainPeekHint(remote, branch string) string {
	return "it may have landed on " + branch + " while this branch is older: `lore sync peek " + remote + "/" + branch + "`"
}

// shortCommit is a commit id cut to shortSHALen for display.
func shortCommit(sha string) string {
	if len(sha) > shortSHALen {
		return sha[:shortSHALen]
	}
	return sha
}

// explainLines is F1's view of one id in folder.
func (s *liveSession) explainLines(ctx context.Context, folder, id string) ([]string, error) {
	_, local, err := s.ref.LocalTree(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "read live ref", err)
	}
	rows, _ := local.Rows(folder) // undecodable files say nothing about id
	var key live.RowKey
	var lf *live.File
	for k, f := range rows {
		if k.ID == id {
			key, lf = k, &f
			break
		}
	}
	snap, _ := s.ref.Snapshot(ctx) // display only: unreadable refs show as none
	branch := s.defaultBranch(ctx)
	lines := []string{fmt.Sprintf("%s in %s (folder %s)", id, s.top, folder)}
	if at := live.FetchStamp(s.stateDir); !at.IsZero() {
		lines = append(lines, fmt.Sprintf("  fetched %s ago", s.clk.Now().Sub(at).Round(time.Second)))
	}
	if lf != nil {
		state := "row"
		if lf.Deleted() {
			state = "tombstone"
		}
		line := fmt.Sprintf("  live (local %s): %s, live since %s, hlc %s", shortCommit(snap.Local), state, lf.Block.LiveSince.Format(time.RFC3339), lf.Block.HLC)
		if !lf.Block.LastCopyAt.IsZero() {
			line += ", last copy " + lf.Block.LastCopyAt.Format(time.RFC3339)
		}
		if lf.Block.Conflict {
			line += ", CONFLICT"
		}
		lines = append(lines, line)
		if snap.Tracking != snap.Local {
			p, _ := s.ref.Pending() // display only: an unreadable list still shows one waiting
			lines = append(lines, fmt.Sprintf("  live on the remote (tracking %s) differs: %d write(s) not pushed", shortCommit(snap.Tracking), max(1, len(p))))
		}
	} else {
		lines = append(lines, "  live: not present")
	}
	mainRef := "refs/remotes/" + s.ref.RemoteName + "/" + branch
	mainName := strings.TrimPrefix(mainRef, "refs/remotes/")
	table := key.Table
	if table == "" {
		table = s.findTableAt(ctx, "HEAD", folder, id)
	}
	if table != "" {
		branchPath := mainDataPath(folder, table+"/"+id+".json")
		if b, ok, _ := s.repo.ReadFile(ctx, "HEAD", branchPath); ok { // display only: a read error shows as absent
			lines = append(lines, fmt.Sprintf("  this branch (HEAD): %s (%d bytes)", branchPath, len(b)))
		} else {
			lines = append(lines, "  this branch (HEAD): not present")
		}
		purged, _ := s.mainPurged(ctx, mainRef, folder) // display only: an unreadable purge list shows as none
		if _, ok, _ := s.repo.ReadFile(ctx, mainRef, branchPath); ok {
			lines = append(lines, "  "+mainName+": present")
		} else if purged[id] {
			lines = append(lines, "  "+mainName+": deleted on purpose ("+purgedFileName+")")
		} else {
			lines = append(lines, "  "+mainName+": not present")
		}
	}
	onMainOnly := false
	if lf == nil && table == "" {
		if b, herr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}}); herr == nil && strings.TrimSpace(string(b)) == branch {
			if t := s.findTableAt(ctx, mainRef, folder, id); t != "" {
				onMainOnly = true
				lines = append(lines, fmt.Sprintf("  %s: present (%s)", mainName, t))
			}
		}
	}
	switch {
	case onMainOnly:
		lines = append(lines, "  wins: the fetched default branch's copy, by the "+ruleDefaultReadThrough+" rule (you are on the default branch and have not pulled it yet)")
	case lf != nil && lf.Deleted():
		lines = append(lines, "  wins: the live tombstone (rule 4): the row is deleted; lookup stops here")
	case lf != nil:
		lines = append(lines, "  wins: the live copy (rule 4: if a row id is in live, live wins)")
	case table != "":
		lines = append(lines, "  wins: this branch's copy (not live)")
	default:
		lines = append(lines, "  not found in live or on this branch; "+explainPeekHint(s.ref.RemoteName, branch))
	}
	if lf != nil {
		if plan, perr := s.plan(ctx, folder, nil); perr == nil { // display only: no plan line when main's purge list is unreadable
			for _, p := range plan {
				if p.Key.ID != id {
					continue
				}
				what := string(p.Decision.Action) + ": " + p.Decision.Reason
				if p.Leave {
					what = explainLeaveLine(branch)
				} else if p.Decision.Action != live.LandStay && s.noteText(liveNoteProtected) != "" {
					what = explainWaitsLine(branch)
				}
				lines = append(lines, "  land: "+what)
			}
		}
	}
	return lines, nil
}

// findTableAt is the table of id in rev's .lore/data of folder ("" when
// rev has no such row file; one ls-tree).
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
