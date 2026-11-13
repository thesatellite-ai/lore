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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// ruleDefaultReadThrough names F1's default-branch read-through rule in
// explain's "wins" line (v0.1.17 live_wire.go wording).
const ruleDefaultReadThrough = "default-branch read-through (F1)"

// Explain implements opssync.Env (`lore explain <id>`).
func (l liveSync) Explain(ctx context.Context, in opssync.ExplainIn) (opssync.ExplainOut, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return opssync.ExplainOut{}, errs.New(errs.InvalidInput, "name a row id: lore explain <id>")
	}
	if _, perr := ids.ParseAny(id); perr != nil { // a typo is not "found nowhere"
		return opssync.ExplainOut{}, errs.Wrap(errs.InvalidID, "explain: "+strconv.Quote(id)+" is not a row id (like mem_…, rul_…, tsk_…)", perr).
			WithHint("copy the id from `lore <noun> list` or `lore search --query <words>`")
	}
	s, folder, err := l.here(ctx)
	if err != nil {
		return opssync.ExplainOut{}, err
	}
	if s == nil {
		return opssync.ExplainOut{}, errs.New(errs.Unsupported, liveUnsupported)
	}
	out, err := s.explainLines(ctx, folder, id)
	if err != nil {
		return opssync.ExplainOut{}, err
	}
	out.Items = []string{id}
	return out, nil
}

// explainStatePurged is a default-branch copy's state when the branch's
// purge list names the row (deleted on purpose, not missing).
const explainStatePurged = "deleted_on_purpose"

// explainLeaveLine is explain's land line for a finished row.
func explainLeaveLine(branch string) string {
	return "leaves live on the next sync (finished, " + branch + " holds it)"
}

// explainLandsLine is explain's land line for a row the next landing
// writes to the default branch: a write never lands by itself (U-2), so it
// names what does, never "land: land: finished".
func explainLandsLine(branch, reason string) string {
	return "lands on " + branch + " on the next `lore sync` or your `git push` of it (" + reason + ")"
}

// explainWaitsLine is explain's land line while the default branch is protected.
func explainWaitsLine(branch string) string {
	return "waits: " + branch + " is protected; rides on your next push"
}

// explainPeekHint points at `lore sync peek` for an id found nowhere.
func explainPeekHint(remote, branch string) string {
	return "it may have landed on " + branch + " while this branch is older: `lore sync peek --ref " + remote + "/" + branch + "`"
}

// shortCommit is a commit id cut to shortSHALen for display.
func shortCommit(sha string) string {
	if len(sha) > shortSHALen {
		return sha[:shortSHALen]
	}
	return sha
}

// explainLines is F1's view of one id in folder: the text lines and, for
// --json, the same facts as copies[] (one entry per place looked at), the
// winning place, its rule and the land decision.
func (s *liveSession) explainLines(ctx context.Context, folder, id string) (opssync.ExplainOut, error) {
	out := opssync.ExplainOut{ID: id, Folder: folder, Copies: []opssync.ExplainCopy{}}
	_, local, err := s.ref.LocalTree(ctx)
	if err != nil {
		return out, errs.Wrap(errs.Internal, "read live ref", err)
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
	liveCopy := opssync.ExplainCopy{Place: opssync.ExplainLive, Ref: shortCommit(snap.Local)}
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
		liveCopy.Present, liveCopy.State, liveCopy.HLC = true, state, lf.Block.HLC.String()
		liveCopy.LiveSince = lf.Block.LiveSince.Format(time.RFC3339)
		if !lf.Block.LastCopyAt.IsZero() {
			liveCopy.LastCopyAt = lf.Block.LastCopyAt.Format(time.RFC3339)
		}
		liveCopy.Conflict = lf.Block.Conflict
		if snap.Tracking != snap.Local {
			p, _ := s.ref.Pending() // display only: an unreadable list still shows one waiting
			liveCopy.Unpushed = max(1, len(p))
			lines = append(lines, fmt.Sprintf("  live on the remote (tracking %s) differs: %d write(s) not pushed", shortCommit(snap.Tracking), liveCopy.Unpushed))
		}
	} else {
		lines = append(lines, "  live: not present")
	}
	out.Copies = append(out.Copies, liveCopy)
	mainRef := "refs/remotes/" + s.ref.RemoteName + "/" + branch
	mainName := strings.TrimPrefix(mainRef, "refs/remotes/")
	table := key.Table
	if table == "" {
		table = s.findTableAt(ctx, "HEAD", folder, id)
	}
	if table == "" {
		table = s.findTableInTree(folder, id)
	}
	if table != "" {
		branchPath := mainDataPath(folder, table+"/"+id+".json")
		here := opssync.ExplainCopy{Place: opssync.ExplainBranch, Ref: "HEAD", Path: branchPath}
		if b, ok, _ := s.repo.ReadFile(ctx, "HEAD", branchPath); ok { // display only: a read error shows as absent
			lines = append(lines, fmt.Sprintf("  this branch (HEAD): %s (%d bytes)", branchPath, len(b)))
			here.Present, here.Bytes = true, len(b)
		} else if b, rerr := os.ReadFile(filepath.Join(s.top, filepath.FromSlash(branchPath))); rerr == nil {
			lines = append(lines, fmt.Sprintf("  this branch (working tree, not committed yet): %s (%d bytes)", branchPath, len(b)))
			here = opssync.ExplainCopy{Place: opssync.ExplainWorkTree, Path: branchPath, Present: true, Bytes: len(b)}
		} else {
			lines = append(lines, "  this branch (HEAD): not present")
		}
		out.Copies = append(out.Copies, here)
		onMain := opssync.ExplainCopy{Place: opssync.ExplainDefault, Ref: mainName, Path: branchPath}
		purged, _ := s.mainPurged(ctx, mainRef, folder) // display only: an unreadable purge list shows as none
		if b, ok, _ := s.repo.ReadFile(ctx, mainRef, branchPath); ok {
			lines = append(lines, "  "+mainName+": present")
			onMain.Present, onMain.Bytes = true, len(b)
		} else if purged[id] {
			lines = append(lines, "  "+mainName+": deleted on purpose ("+purgedFileName+")")
			onMain.State = explainStatePurged
		} else {
			lines = append(lines, "  "+mainName+": not present")
		}
		out.Copies = append(out.Copies, onMain)
	}
	onMainOnly := false
	if lf == nil && table == "" {
		if b, herr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}}); herr == nil && strings.TrimSpace(string(b)) == branch {
			if t := s.findTableAt(ctx, mainRef, folder, id); t != "" {
				onMainOnly = true
				lines = append(lines, fmt.Sprintf("  %s: present (%s)", mainName, t))
				out.Copies = append(out.Copies, opssync.ExplainCopy{Place: opssync.ExplainDefault, Ref: mainName, Path: mainDataPath(folder, t+"/"+id+".json"), Present: true})
			}
		}
	}
	switch {
	case onMainOnly:
		out.Winner, out.Reason = opssync.ExplainDefault, "the fetched default branch's copy, by the "+ruleDefaultReadThrough+" rule (you are on the default branch and have not pulled it yet)"
	case lf != nil && lf.Deleted():
		out.Winner, out.Reason = opssync.ExplainLive, "the live tombstone (rule 4): the row is deleted; lookup stops here"
	case lf != nil:
		out.Winner, out.Reason = opssync.ExplainLive, "the live copy (rule 4: if a row id is in live, live wins)"
	case table != "":
		out.Winner, out.Reason = opssync.ExplainBranch, "this branch's copy (not live)"
		for _, c := range out.Copies {
			if c.Place == opssync.ExplainWorkTree {
				out.Winner = opssync.ExplainWorkTree
			}
		}
	default:
		out.Reason = "not found in live or on this branch; " + explainPeekHint(s.ref.RemoteName, branch)
	}
	if out.Winner == "" {
		lines = append(lines, "  "+out.Reason)
	} else {
		lines = append(lines, "  wins: "+out.Reason)
		for i := range out.Copies {
			if out.Copies[i].Place == out.Winner && out.Copies[i].Present {
				out.Copies[i].Winner = true
				break
			}
		}
	}
	if lf != nil {
		if plan, perr := s.plan(ctx, folder, nil); perr == nil { // display only: no plan line when main's purge list is unreadable
			for _, p := range plan {
				if p.Key.ID != id {
					continue
				}
				what := string(p.Decision.Action) + ": " + p.Decision.Reason
				if p.Decision.Action == live.LandFinal {
					what = explainLandsLine(branch, p.Decision.Reason)
				}
				if p.Leave {
					what = explainLeaveLine(branch)
				} else if p.Decision.Action != live.LandStay && s.noteText(liveNoteProtected) != "" {
					what = explainWaitsLine(branch)
				}
				lines = append(lines, "  land: "+what)
				out.Land = opssync.ExplainLand{Action: string(p.Decision.Action), Reason: p.Decision.Reason, Text: what}
			}
		}
	}
	out.Lines = lines
	return out, nil
}

// findTableInTree is the table of id in the working tree's .lore/data of
// folder ("" when no row file has that id): a row just added and not
// committed yet is on this branch too, and explain said "not found in
// live or on this branch" for it (campaign knowledge 2026-11-08). id is a
// parsed row id, so it carries no glob characters.
func (s *liveSession) findTableInTree(folder, id string) string {
	hits, err := filepath.Glob(filepath.Join(s.top, filepath.FromSlash(mainDataPath(folder, "*")), id+".json"))
	if err != nil || len(hits) == 0 {
		return ""
	}
	return filepath.Base(filepath.Dir(hits[0]))
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
