// coord_show.go — the CLI's opstask.ShowDetailer: what `task show` adds
// for a store task (LORE_TEAM_SPEC §7.5 step 5, lore-docs BUG-D3), from the
// coordination machinery (the store project's rows, its notes and the
// signals ref): the after list, gate, time box, attempts and outcome, and
// the claim as this clone sees it now (the notes are the core's own). v0.1.17 printed these in
// its own `task show` (task_from.go showCoordTask, removed with the old
// cobra groups in 4a8e307); the core's show prints the row's own fields
// and asks this for the rest.
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"

	opstask "github.com/thesatellite-ai/lore/ops/task"
)

var _ opstask.ShowDetailer = cliTeam{}

// ShowDetail implements opstask.ShowDetailer; the op calls it only for a
// row the core read from a store. ok is false when this checkout has no
// links or its link status is unknown (no coordination store to read), or
// the linked store project does not hold the id (a personal or topic store
// row): the core already showed all there is. A linked store that cannot
// be read is an error.
func (cliTeam) ShowDetail(ctx context.Context, taskID string) (opstask.ShowDetail, bool, error) {
	w, err := openCoordWorld(ctx)
	if err != nil || len(w.Links) == 0 {
		// No coordination world (no git, no org): the row the core read
		// is all there is, as v0.1.17's showStoreTask fell back.
		return opstask.ShowDetail{}, false, nil
	}
	// The core read the row from a store (the op asks only then), so the
	// coordination target is the linked store project. openCoordTarget's
	// repo-first lookup is not used: it answers from the repo's lore.db,
	// which may still carry a copy of a store row, and would then show the
	// repo's signals ref instead of the store's claim.
	if w.LinkUnknown {
		return opstask.ShowDetail{}, false, nil
	}
	t, err := openStoreTarget(ctx, w)
	if err != nil {
		return opstask.ShowDetail{}, false, coreErr(err)
	}
	defer t.Close()
	d, ok, err := t.Rows.Get(ctx, taskID)
	if err != nil {
		return opstask.ShowDetail{}, false, coreErr(coordError(err))
	}
	if !ok {
		return opstask.ShowDetail{}, false, nil
	}
	labels := &actorLabels{}
	defer labels.close()
	out := opstask.ShowDetail{Fields: coordShowFields(ctx, t, d, coordSourceLabel(w, t), labels)}
	// The notes are the core's (ops/task notesOn lists every attached
	// store's comments on the task); the claim is the host's.
	claim, err := claimText(ctx, t.Signals, t.Folder, taskID)
	if err != nil {
		return opstask.ShowDetail{}, false, coreErr(err)
	}
	out.Tail = claim
	return out, true, nil
}

// coordSourceLabel names where a store row was read: <org>/<folder>.
func coordSourceLabel(w *coordWorld, t coordTarget) string {
	if w.Org != nil {
		return w.Org.Name + "/" + t.Folder
	}
	return t.Folder
}

// coordShowFields are v0.1.17's store-task show fields the core's row view
// does not print (title, status, priority, commitment, mission and done by
// it prints itself). Empty values are skipped by the printer.
func coordShowFields(ctx context.Context, t coordTarget, d coord.Doc, source string, labels *actorLabels) []opstask.DetailField {
	s := coord.Summarize(d)
	repo := s.RepoID
	if l, ok := t.Rows.(coordLister); ok && repo != "" {
		repo = l.RepoName(s.RepoID)
	}
	var after string
	if len(s.After) > 0 {
		deps := make([]string, 0, len(s.After))
		for _, dep := range s.After {
			deps = append(deps, fmt.Sprintf("%s (until %s)", dep.TaskID, dep.Until))
		}
		after = strings.Join(deps, ", ")
	}
	var timebox, attempts, pr, merged string
	if s.Timebox > 0 {
		timebox = shortDuration(s.Timebox)
	}
	if s.Attempts > 0 {
		attempts = strconv.Itoa(s.Attempts)
	}
	if s.DonePR > 0 {
		pr = "#" + strconv.Itoa(s.DonePR)
	}
	if !s.MergedAt.IsZero() {
		merged = s.MergedAt.UTC().Format(time.RFC3339)
	}
	return []opstask.DetailField{
		{Name: "repo", Value: repo}, {Name: "role", Value: s.Role}, {Name: "assigned", Value: labels.label(ctx, s.Assignee)},
		{Name: "after", Value: after}, {Name: "gate", Value: s.Gate}, {Name: "timebox", Value: timebox},
		{Name: "attempts", Value: attempts}, {Name: "branch", Value: s.WorkBranch}, {Name: "pr", Value: pr},
		{Name: "commit", Value: s.DoneCommit}, {Name: "merged", Value: merged}, {Name: "source", Value: source},
	}
}

// claimText is the claim on task id in folder of the signals ref st as
// this clone sees it now (printClaimOn's lines).
func claimText(ctx context.Context, st *signals.Store, folder, id string) (string, error) {
	v, err := st.Read(ctx)
	if err != nil {
		return "", coordError(err)
	}
	p := signals.ClaimPath(folder, id)
	s, ok := v.Board[p]
	var b strings.Builder
	b.WriteString("\n")
	if !ok {
		fmt.Fprintf(&b, "  claim:    none on %s\n", st.Ref)
		return b.String(), nil
	}
	_, active := v.Active(p)
	state := "active"
	if !active {
		state = "expired (as seen by this clone)"
	}
	fmt.Fprintf(&b, "  claim:    %s, held by %s (session %s", state, holderLabel(s), s.Session)
	if s.Host != "" {
		fmt.Fprintf(&b, ", %s", s.Host)
	}
	fmt.Fprintf(&b, ")\n  token:    %s…  epoch %d\n  expires:  %s  renewed_seq %d\n", s.ClaimToken[:min(len(s.ClaimToken), coord.TokenPrefixLen)], s.ClaimEpoch,
		s.Expires.UTC().Format(time.RFC3339), s.RenewedSeq)
	if s.WorkBranch != "" {
		fmt.Fprintf(&b, "  branch:   %s\n", s.WorkBranch)
	}
	if len(s.Owns) > 0 {
		fmt.Fprintf(&b, "  owns:     %s\n", strings.Join(s.Owns, ", "))
	}
	return b.String(), nil
}
