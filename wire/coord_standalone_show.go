package wire

// coord_standalone_show.go — standaloneTeam's opstask.ShowDetailer
// (cmd/lore, P3-SIGCOORD-6): what `task show` adds for a store task of a
// linked repo (LORE_TEAM_SPEC §7.5 step 5, BUG-D3): the after list, gate,
// time box, attempts and outcome as fields, and the claim on the store's
// signals ref as this clone sees it now (v0.1.17 saas coord_show.go). A
// checkout without links, or a row the linked store project does not
// hold, adds nothing: the core showed all there is.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

var _ opstask.ShowDetailer = standaloneTeam{}

// ShowDetail implements opstask.ShowDetailer.
func (t standaloneTeam) ShowDetail(ctx context.Context, taskID string) (opstask.ShowDetail, bool, error) {
	h := standaloneCoord(t)
	w, err := h.openWorld(ctx)
	if err != nil || !w.linked() {
		return opstask.ShowDetail{}, false, nil // no coordination world here: the row is all there is
	}
	if err = w.useStore(ctx); err != nil {
		return opstask.ShowDetail{}, false, err
	}
	d, ok, err := engineRows{a: h.a}.Get(ctx, taskID)
	if err != nil || !ok {
		return opstask.ShowDetail{}, false, err
	}
	cv := &coordView{a: t.a, w: w, labels: map[string]string{}}
	source := w.folder
	if w.sit.Org != nil {
		source = w.sit.Org.Name + "/" + w.folder
	}
	claim, err := storeClaimText(ctx, w.signalsStore(), w.folder, taskID)
	if err != nil {
		return opstask.ShowDetail{}, false, err
	}
	return opstask.ShowDetail{Fields: cv.showFields(ctx, d, source), Tail: claim}, true, nil
}

// showFields are v0.1.17's store-task fields the core's row view does not
// print; empty values are skipped by the printer.
func (cv *coordView) showFields(ctx context.Context, d coord.Doc, source string) []opstask.DetailField {
	s := coord.Summarize(d)
	repo := s.RepoID
	if repo != "" {
		repo = engineRows{a: cv.a}.RepoName(repo)
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
		{Name: "repo", Value: repo}, {Name: "role", Value: s.Role}, {Name: "assigned", Value: cv.label(ctx, s.Assignee)},
		{Name: "after", Value: after}, {Name: "gate", Value: s.Gate}, {Name: "timebox", Value: timebox},
		{Name: "attempts", Value: attempts}, {Name: "branch", Value: s.WorkBranch}, {Name: "pr", Value: pr},
		{Name: "commit", Value: s.DoneCommit}, {Name: "merged", Value: merged}, {Name: "source", Value: source},
	}
}

// storeClaimText is the claim on task id as this clone sees it now
// (v0.1.17 claimText).
func storeClaimText(ctx context.Context, st *signals.Store, folder, id string) (string, error) {
	v, err := st.Read(ctx)
	if err != nil {
		return "", coordErr(err)
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
	fmt.Fprintf(&b, "  claim:    %s, held by %s (session %s", state, holderName(s), s.Session)
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
