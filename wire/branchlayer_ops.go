package wire

// branchlayer_ops.go — opsbranch.Env on the App (BRANCH_LAYERS_SPEC 13):
// every verb finds the layer wrapper of the repo's project folder
// (branchlayer.go) and asks its git clone (adapters/backend/git layer.go,
// layerland.go). Land, drop, restore, purge and rename push at once
// (explicit remote acts; each output says it pushed, devco D-614); promote
// and the isolate/share choice write locally and reach the remote with
// lore sync.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsbranch "github.com/thesatellite-ai/lore/ops/branch"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsbranch.Env = (*App)(nil)

// noLayerFolder is the refusal when the repo has no project folder in a
// git store: branch layers live only there.
func noLayerFolder(top string) error {
	return errs.New(errs.NotFound, "the notes of "+top+" are not in a project folder of a git store, so it has no branch notes").
		WithHint("branch layers apply to a repo linked to a store folder (lore org link --org <org> --project <folder>); a repo's own .lore/data already follows its branches")
}

// noLayerFolderLine is lore branch list's line in a repo with no project
// folder in a git store: no branch notes, and why.
func noLayerFolderLine(top string) string {
	return "no branch notes: the notes of " + top + " are not in a project folder of a git store (a repo's own .lore/data already follows its branches; branch layers apply to a repo linked with lore org link --org <org> --project <folder>)"
}

// branchTarget is the layer wrapper of the repo's project folder and its
// store's name.
func (a *App) branchTarget(ctx context.Context) (*state, *layerBackend, store.Source, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	for _, name := range st.eng.Stores() {
		if lb := layerOf(st.byName[name]); lb != nil {
			return st, lb, name, nil
		}
	}
	return st, nil, "", noLayerFolder(st.repoTop)
}

// keyFor is the layer key of branch (the checked-out one when "").
func keyFor(lb *layerBackend, branch string) (branchlayer.Key, error) {
	k := lb.key
	if branch != "" {
		k.Branch = branch
	}
	if lb.code.Detached && branch == "" {
		return k, errs.New(errs.Detached, "the code repo "+lb.code.Top+" has a detached HEAD: name the branch").WithHint("--branch <branch>")
	}
	if err := k.Check(); err != nil {
		return k, errs.Wrap(errs.InvalidInput, "the branch name", err)
	}
	return k, nil
}

// requireBranch refuses an empty --branch on a verb that changes a layer.
func requireBranch(verb, branch string) error {
	if strings.TrimSpace(branch) == "" {
		return errs.New(errs.InvalidInput, "lore branch "+verb+" needs --branch").WithHint("lore branch " + verb + " --branch <branch> (lore branch list names them)")
	}
	return nil
}

// tableCounts turns per-table counts into the output list.
func tableCounts(m map[row.Table]int) []opsbranch.Count {
	out := make(map[string]int, len(m))
	for t, n := range m {
		out[string(t)] = n
	}
	return opsbranch.Counts(out)
}

// rowCounts counts rows per table.
func rowCounts(rows []store.Row) []opsbranch.Count {
	out := map[string]int{}
	for _, r := range rows {
		out[string(r.Table)]++
	}
	return opsbranch.Counts(out)
}

// ageOf is "3d", "5h", "12m" since t ("" for a zero time).
func ageOf(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d >= 24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/time.Minute)) + "m"
}

// codeState is merged or orphaned for an open layer of this repo, with
// why; "" when it is plainly open. A branch that still exists is merged
// only when every copy of it this clone sees (local, origin's) is in
// origin/<default> past the split commit, and the commits the notes last
// saw (the code head of the newest write) are in the default branch too,
// by commit or by patch (devco V-9: a branch reset to main's tip has no
// commit of its own left, yet its own work never merged). A branch gone
// from both places is merged when the last code head a write saw is in
// origin/<default> and past the split; otherwise it is orphaned: deleted
// unmerged, or squash-merged, which lore cannot tell apart without the
// host, so the person (or the CI step) decides. Only a clone that wrote
// the notes itself (l.Local) can tell a deleted branch from one that
// lives only on its author's laptop (devco V-7): elsewhere it stays open.
func codeState(ctx context.Context, lb *layerBackend, l git.Layer) (string, string) {
	if l.Key.RepoKey != lb.key.RepoKey {
		return "", "" // another repo of the folder: its branches are not here
	}
	top, def, b := lb.code.Top, lb.code.Default, l.Key.Branch
	main := "refs/remotes/origin/" + def
	if !liveRefExists(ctx, top, main) {
		return "", "" // no fetched default branch: nothing can be told
	}
	merged := func(tip string) bool {
		if tip == "" || tip == l.Facts.Split {
			return false
		}
		_, err := liveGit(ctx, top, "merge-base", "--is-ancestor", tip, main)
		return err == nil
	}
	var tips []string
	for _, ref := range []string{"refs/heads/" + b, "refs/remotes/origin/" + b} {
		if out, err := liveGit(ctx, top, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
			tips = append(tips, strings.TrimSpace(string(out)))
		}
	}
	landHint := b + " merged into " + def + ": land its notes with lore branch land --branch " + b
	if len(tips) > 0 {
		for _, tip := range tips {
			if !merged(tip) {
				return "", ""
			}
		}
		if !ownWorkIn(ctx, top, main, l.Facts.Split, l.Facts.CodeHead) {
			return "", ""
		}
		return opsbranch.StateMerged, landHint
	}
	if merged(l.Facts.CodeHead) {
		return opsbranch.StateMerged, landHint
	}
	if !l.Local {
		return "", ""
	}
	return opsbranch.StateOrphaned, b + " is gone (deleted without merging, or squash-merged): land it (lore branch land --branch " + b + ") or drop it (lore branch drop --branch " + b + ")"
}

// ownWorkIn reports whether the branch's own commits up to head (the code
// head a note was written at, past split) are all in main, by commit or
// as the same patch (a rebased merge). It is true when nothing can be
// told: no head recorded, the head is the split, or this clone lacks the
// head commit (a teammate never fetched it).
func ownWorkIn(ctx context.Context, top, main, split, head string) bool {
	if head == "" || head == split {
		return true
	}
	if _, err := liveGit(ctx, top, "cat-file", "-e", head+"^{commit}"); err != nil {
		return true
	}
	if _, err := liveGit(ctx, top, "merge-base", "--is-ancestor", head, main); err == nil {
		return true
	}
	args := []string{"cherry", main, head}
	if split != "" {
		if _, err := liveGit(ctx, top, "cat-file", "-e", split+"^{commit}"); err == nil {
			args = append(args, split)
		}
	}
	out, err := liveGit(ctx, top, args...)
	if err != nil {
		return true
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "+") {
			return false // a commit of the branch whose change main does not have
		}
	}
	return true
}

// BranchList implements opsbranch.Env.
func (a *App) BranchList(ctx context.Context, in opsbranch.ListIn) (opsbranch.ListOut, error) {
	st, lb, name, err := a.branchTarget(ctx)
	if lb == nil && st != nil {
		// devco:D-388-branchlist: a listing with nothing to list is an empty
		// list, as every read in a repo with no store folder answers (a
		// repo's own .lore/data keeps no branch layers); the line says why.
		return opsbranch.ListOut{Layers: []opsbranch.Layer{}, Lines: []string{noLayerFolderLine(st.repoTop)}}, nil
	}
	if err != nil {
		return opsbranch.ListOut{}, err
	}
	if _, ferr := lb.gb.LayerFetch(ctx); ferr != nil {
		return opsbranch.ListOut{}, ferr
	}
	repoKey := lb.key.RepoKey
	if in.All {
		repoKey = ""
	}
	entries, err := lb.gb.LayerList(ctx, repoKey, in.Trash)
	if err != nil {
		return opsbranch.ListOut{}, err
	}
	now := a.opts.Wall()
	out := opsbranch.ListOut{Store: string(name), Folder: lb.key.Folder}
	for _, e := range entries {
		l := opsbranch.Layer{Branch: e.Layer.Key.Branch, Repo: e.Layer.Facts.Repo, Folder: e.Layer.Key.Folder, State: opsbranch.StateOpen,
			Rows: tableCounts(e.Counts), Age: ageOf(e.Layer.Facts.Started, now), Tip: e.Layer.Tip, Trash: e.Trash}
		if !e.Layer.Facts.Started.IsZero() {
			l.Started = e.Layer.Facts.Started.UTC().Format(time.RFC3339)
		}
		if e.Trash != "" {
			l.State = string(e.Layer.Facts.State)
			l.Age = ageOf(e.Trashed, now)
		} else if s, why := codeState(ctx, lb, e.Layer); s != "" {
			l.State, l.Why = s, why
			out.Lines = append(out.Lines, why)
		}
		out.Layers = append(out.Layers, l)
	}
	out.Count = len(out.Layers) // op.PageFlags: Invoke cuts the page (layers are rows)
	if out.Count > 0 {
		if offer := a.ciOffer(ctx, lb); offer != "" {
			out.Lines = append(out.Lines, offer)
		}
	}
	return out, nil
}

// BranchShow implements opsbranch.Env.
func (a *App) BranchShow(ctx context.Context, in opsbranch.ShowIn) (opsbranch.ShowOut, error) {
	_, lb, _, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.ShowOut{}, err
	}
	key, err := keyFor(lb, in.Branch)
	if err != nil {
		return opsbranch.ShowOut{}, err
	}
	l, err := lb.gb.LayerOpen(ctx, key)
	if err != nil {
		return opsbranch.ShowOut{}, err
	}
	onMain, err := bornOnShared(ctx, lb, key.Branch)
	if err != nil {
		return opsbranch.ShowOut{}, err
	}
	var hint string
	if len(onMain) > 0 {
		hint = opsbranch.OnMainHint(key.Branch)
	}
	if !l.Exists() {
		if len(onMain) > 0 {
			// D-392: a landed (or adopted) branch has no open notes, but
			// its rows on the shared notes still answer to its name
			return opsbranch.ShowOut{Rows: []opsbranch.ShowRow{}, OnMain: onMain, Hint: hint,
				Layer: opsbranch.Layer{Branch: key.Branch, Folder: key.Folder, State: opsbranch.StateLanded, Rows: []opsbranch.Count{}}}, nil
		}
		return opsbranch.ShowOut{}, errs.New(errs.NotFound, "no notes of "+key.Branch+" in "+key.Folder).WithHint("lore branch list shows the branches that have some")
	}
	rows, err := lb.gb.LayerRows(ctx, l)
	if err != nil {
		return opsbranch.ShowOut{}, err
	}
	out := opsbranch.ShowOut{OnMain: onMain, Hint: hint, Base: l.Base, Split: l.Facts.Split, Head: l.Facts.CodeHead, By: l.Facts.By,
		Layer: opsbranch.Layer{Branch: key.Branch, Repo: l.Facts.Repo, Folder: key.Folder, State: opsbranch.StateOpen, Rows: rowCounts(rows), Tip: l.Tip}}
	if !l.Facts.Started.IsZero() {
		out.Layer.Started = l.Facts.Started.UTC().Format(time.RFC3339)
	}
	if s, _ := codeState(ctx, lb, l); s != "" {
		out.Layer.State = s
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Table != rows[j].Table {
			return rows[i].Table < rows[j].Table
		}
		return rows[i].ID.String() < rows[j].ID.String()
	})
	for _, r := range rows {
		out.Rows = append(out.Rows, opsbranch.ShowRow{Table: string(r.Table), ID: r.ID.String(), Title: titleOf(r), Deleted: r.Deleted})
	}
	return out, nil
}

// bornOnShared counts, per table, the live note rows of the folder's
// shared notes (the store branch, layers left out) created on branch:
// what a land or an adopt put there (D-392). The tables are the ones
// `lore trash add --born-branch` sweeps, so the count and the trash
// agree.
func bornOnShared(ctx context.Context, lb *layerBackend, branch string) ([]opsbranch.Count, error) {
	shared, _, err := lb.inner.Read(withoutLayers(ctx), "")
	if err != nil {
		return nil, err
	}
	notes := opstrash.NoteTables()
	var born []store.Row
	for _, r := range shared {
		if r.Deleted || !slices.Contains(notes, r.Table) {
			continue
		}
		m, derr := r.Decode()
		if derr != nil || m.Deleted || trash.Is(m) {
			continue
		}
		if origin.Branch(m.Origin) == branch {
			born = append(born, r)
		}
	}
	return rowCounts(born), nil
}

// titleOf is a row's title, name or the start of its body (60 runes).
func titleOf(r store.Row) string {
	m, err := r.Decode()
	if err != nil {
		return ""
	}
	for _, f := range []string{"title", "name", "body", "text"} {
		if v, ok := m.Fields[f].(value.Text); ok && v.String() != "" {
			s := strings.Join(strings.Fields(v.String()), " ")
			if rs := []rune(s); len(rs) > 60 {
				s = string(rs[:60]) + "…"
			}
			return s
		}
	}
	return ""
}

// BranchMode implements opsbranch.Env.
func (a *App) BranchMode(ctx context.Context, in opsbranch.ModeIn) (opsbranch.ModeOut, error) {
	_, lb, name, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.ModeOut{}, err
	}
	branch := in.Branch
	if branch == "" {
		branch = lb.code.Branch
	}
	out := opsbranch.ModeOut{Branch: branch, Repo: lb.code.Remote, Folder: lb.key.Folder, Store: string(name)}
	if branch == "" {
		out.Branch, out.Mode = "(detached HEAD)", string(branchlayer.Shared)
		out.Why = "a detached HEAD has no branch: reads show the shared notes, writes are refused (E_DETACHED) unless LORE_LAYER=off (or --no-layer on an add or edit)"
		return out, nil
	}
	// the settings this command's reads, writes and cache path follow
	// (pinned at open, branchmode.go), so mode never disagrees with them
	if lb.decideErr != nil {
		return out, lb.decideErr
	}
	d := branchMode(branch, lb.code.Default, lb.settings)
	out.Mode, out.Step, out.Why = string(d.Mode), int(d.Step), d.Why(lb.code.Default)
	off, err := envLayerSetting()
	if err != nil {
		return out, err
	}
	if off {
		out.Why += " (LORE_LAYER=off: this shell writes to the shared notes anyway)"
	}
	return out, nil
}

// landSubject is the landing commit's subject: "lore: land feat/x
// (github.com/acme/app #123): 2 tasks, 1 rule".
func landSubject(branch, repo string, pr int, rows []opsbranch.Count) string {
	where := repo
	if pr > 0 {
		where += " #" + strconv.Itoa(pr)
	}
	return "lore: land " + branch + " (" + where + "): " + opsbranch.RowsText(rows)
}

// BranchLand implements opsbranch.Env.
func (a *App) BranchLand(ctx context.Context, in opsbranch.LandIn) (opsbranch.LandOut, error) {
	if err := requireBranch("land", in.Branch); err != nil {
		return opsbranch.LandOut{}, err
	}
	if in.Store != "" || in.Folder != "" || in.Repo != "" {
		return a.landWithoutConfig(ctx, in)
	}
	_, lb, _, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.LandOut{}, err
	}
	key, err := keyFor(lb, in.Branch)
	if err != nil {
		return opsbranch.LandOut{}, err
	}
	var out opsbranch.LandOut
	if in.Into != "" {
		if _, ferr := lb.gb.LayerFetch(ctx); ferr != nil {
			return opsbranch.LandOut{}, ferr
		}
		out, err = landInto(ctx, lb.gb, key, in)
	} else {
		out, err = landLayer(ctx, lb.gb, key, lb.code.Remote, in)
	}
	if err == nil && !in.DryRun {
		forgetLayerCache(a.opts.Home, lb, key.Branch) // devco:V-17
	}
	return out, err
}

// landLayer lands key's layer through gb.
func landLayer(ctx context.Context, gb *git.Backend, key branchlayer.Key, repo string, in opsbranch.LandIn) (opsbranch.LandOut, error) {
	out := opsbranch.LandOut{Branch: key.Branch, Folder: key.Folder, StoreBranch: gb.Branch(), DryRun: in.DryRun}
	req, err := landRequestOf(key, in)
	if err != nil {
		return out, err
	}
	dry := req
	dry.DryRun, dry.Predict = true, in.DryRun
	plan, err := gb.LayerLand(ctx, dry)
	if err != nil {
		return out, err
	}
	out.Rows, out.Kept, out.Dropped = rowCounts(plan.Rows), rowCounts(plan.Kept), rowCounts(plan.Dropped)
	if plan.AlreadyLanded != "" || in.DryRun {
		out.AlreadyLanded = plan.AlreadyLanded
		return out, nil
	}
	if in.Review {
		if req.Exclude, err = reviewRows(append(append([]store.Row{}, plan.Rows...), plan.Kept...), req); err != nil {
			return out, err
		}
	}
	trailers := []string{"Lore-Land-Branch: " + key.Branch, "Lore-Land-Repo: " + repo}
	if in.PR > 0 {
		trailers = append(trailers, "Lore-Land-PR: "+strconv.Itoa(in.PR))
	}
	req.Trailers = trailers
	req.Subject = landSubject(key.Branch, repo, in.PR, out.Rows)
	res, err := gb.LayerLand(ctx, req)
	if err != nil {
		return out, err
	}
	out.Rows, out.Kept, out.Dropped = rowCounts(res.Rows), rowCounts(res.Kept), rowCounts(res.Dropped)
	out.Commit, out.AlreadyLanded, out.Trash, out.Pushed, out.Merged, out.Rebased = res.Commit, res.AlreadyLanded, res.Trash, res.Pushed, len(res.Merged), res.Rebased
	return out, nil
}

// landWithoutConfig is the CI form of land: --store, --folder and --repo
// name the layer; the store is cloned (or its clone reused) under the lore
// home without any config (BRANCH_LAYERS 17).
func (a *App) landWithoutConfig(ctx context.Context, in opsbranch.LandIn) (opsbranch.LandOut, error) {
	if in.Store == "" || in.Folder == "" || in.Repo == "" {
		return opsbranch.LandOut{}, errs.New(errs.InvalidInput, "--store, --folder and --repo go together").
			WithHint("lore branch land --store <url> --folder <folder> --repo <github.com/owner/repo> --branch <branch>")
	}
	code, err := normalizeRepoArg(ctx, in.Repo)
	if err != nil {
		return opsbranch.LandOut{}, err
	}
	dir, _, err := git.CloneDirOf(a.opts.Home, in.Store, "")
	if err != nil {
		return opsbranch.LandOut{}, err
	}
	gb, err := git.OpenClone(ctx, in.Store, dir, in.Folder, store.OpenOptions{}, git.WithMerger(mergerForLayers()))
	if err != nil {
		return opsbranch.LandOut{}, err
	}
	defer func() { _ = gb.Close() }()
	key := branchlayer.Key{StoreBranch: gb.Branch(), Folder: in.Folder, RepoKey: layerRepoKey(ctx, gb, code), Branch: in.Branch}
	if err := key.Check(); err != nil {
		return opsbranch.LandOut{}, errs.Wrap(errs.InvalidInput, "the layer", err)
	}
	out, err := landLayer(ctx, gb, key, code.Remote, in)
	if errs.Has(err, errs.NotFound) {
		return opsbranch.LandOut{Branch: in.Branch, Folder: in.Folder, StoreBranch: gb.Branch(), Nothing: true},
			nil // CI: a branch that wrote no notes has nothing to land (exit 0)
	}
	return out, err
}

// BranchDrop implements opsbranch.Env.
func (a *App) BranchDrop(ctx context.Context, in opsbranch.DropIn) (opsbranch.DoneOut, error) {
	if err := requireBranch("drop", in.Branch); err != nil {
		return opsbranch.DoneOut{}, err
	}
	_, lb, _, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	key, err := keyFor(lb, in.Branch)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	l, err := lb.gb.LayerOpen(ctx, key)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	if !l.Exists() {
		return opsbranch.DoneOut{}, errs.New(errs.NotFound, "no notes of "+key.Branch+" in "+key.Folder).WithHint("lore branch list shows the branches that have some")
	}
	rows, err := lb.gb.LayerRows(ctx, l)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	what := opsbranch.RowsText(rowCounts(rows))
	if in.DryRun {
		return opsbranch.DoneOut{OK: true, Lines: []string{"would drop the notes of " + key.Branch + " (" + what + ") to the trash; nothing changed"}}, nil
	}
	trash, err := lb.gb.LayerTrash(ctx, key, branchlayer.StateDropped, "", true)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	forgetLayerCache(a.opts.Home, lb, key.Branch) // devco:V-17
	return opsbranch.DoneOut{OK: true, Lines: []string{"✓ dropped the notes of " + key.Branch + " (" + what + ") to the trash (" + trash + "; " + opsbranch.PushedText(true) + "); lore branch restore --branch " + key.Branch + " brings them back"}}, nil
}

// BranchRestore implements opsbranch.Env.
func (a *App) BranchRestore(ctx context.Context, in opsbranch.RestoreIn) (opsbranch.DoneOut, error) {
	if err := requireBranch("restore", in.Branch); err != nil {
		return opsbranch.DoneOut{}, err
	}
	_, lb, _, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	key, err := keyFor(lb, in.Branch)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	var at time.Time
	if in.At != "" {
		if at, err = time.Parse(branchlayer.StampLayout, in.At); err != nil {
			return opsbranch.DoneOut{}, errs.New(errs.InvalidInput, "--at "+in.At+" is not a trash stamp").WithHint("use the stamp lore branch list --trash shows, like 20261120T091200Z")
		}
	}
	if _, ferr := lb.gb.LayerFetch(ctx); ferr != nil {
		return opsbranch.DoneOut{}, ferr
	}
	landing := ""
	if in.Revert {
		if landing, err = lb.gb.LayerLanded(ctx, key); err != nil {
			return opsbranch.DoneOut{}, err
		}
		if landing == "" {
			_, err = revertLanding(ctx, lb.gb, key, "")
			return opsbranch.DoneOut{}, err
		}
	}
	l, err := lb.gb.LayerRestore(ctx, key, at, true)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	if in.Revert {
		rev, rerr := revertLanding(ctx, lb.gb, key, landing)
		if rerr != nil {
			return opsbranch.DoneOut{}, errs.Prefix("the notes of "+key.Branch+" are back, but reverting their landing failed", rerr)
		}
		rows, rerr := lb.gb.LayerRows(ctx, l)
		if rerr != nil {
			return opsbranch.DoneOut{}, rerr
		}
		return opsbranch.DoneOut{OK: true, Lines: []string{"✓ restored the notes of " + key.Branch + " (" + opsbranch.RowsText(rowCounts(rows)) + ") and reverted their landing on " + lb.gb.Branch() + " (commit " + short7(rev) + "; both " + opsbranch.PushedText(true) + ")"}}, nil
	}
	rows, err := lb.gb.LayerRows(ctx, l)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	return opsbranch.DoneOut{OK: true, Lines: []string{"✓ restored the notes of " + key.Branch + " (" + opsbranch.RowsText(rowCounts(rows)) + "; " + opsbranch.PushedText(true) + "); a landing made before stays on " + lb.gb.Branch() +
		"; lore sync keeps them open until the branch writes again or lore branch drop --branch " + key.Branch + " drops them"}}, nil
}

// BranchPurge implements opsbranch.Env.
func (a *App) BranchPurge(ctx context.Context, in opsbranch.PurgeIn) (opsbranch.PurgeOut, error) {
	if (in.Branch == "") == (in.OlderThan == "") {
		return opsbranch.PurgeOut{}, errs.New(errs.InvalidInput, "lore branch purge needs --branch or --older-than (one of them)").
			WithHint("lore branch purge --branch fix-login --confirm, or lore branch purge --older-than 90d --confirm")
	}
	if !in.Confirm {
		args := []string{"branch", "purge"}
		if in.Branch != "" {
			args = append(args, "--branch", in.Branch)
		} else {
			args = append(args, "--older-than", in.OlderThan)
		}
		msg := "purge deletes trashed branch notes for good; they cannot be restored"
		return opsbranch.PurgeOut{}, errs.New(errs.NeedsConfirm, "branch: "+msg).
			WithHint("ask the person, then re-run with --confirm (lore branch list --trash shows what is there)").
			WithQuestion(msg+"?").
			WithNext("lore", append(args, "--confirm")...)
	}
	_, lb, _, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.PurgeOut{}, err
	}
	var key branchlayer.Key
	cutoff := time.Time{}
	if in.Branch != "" {
		if key, err = keyFor(lb, in.Branch); err != nil {
			return opsbranch.PurgeOut{}, err
		}
	} else {
		d, perr := parseAge(in.OlderThan)
		if perr != nil {
			return opsbranch.PurgeOut{}, perr
		}
		cutoff = time.Now().Add(-d)
	}
	if _, ferr := lb.gb.LayerFetch(ctx); ferr != nil {
		return opsbranch.PurgeOut{}, ferr
	}
	refs, err := lb.gb.LayerPurge(ctx, key, cutoff, true)
	if err == nil && len(refs) > 0 {
		forgetLayerCache(a.opts.Home, lb, key.Branch) // devco:V-17
	}
	return opsbranch.PurgeOut{Purged: refs}, err
}

// parseAge reads "90d" or a Go duration ("720h").
func parseAge(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		if days, err := strconv.Atoi(n); err == nil && days >= 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, errs.New(errs.InvalidInput, "--older-than "+s+" is not an age").WithHint("use days (90d) or hours (720h)")
	}
	return d, nil
}

// BranchPromote implements opsbranch.Env.
func (a *App) BranchPromote(ctx context.Context, in opsbranch.PromoteIn) (opsbranch.DoneOut, error) {
	branch := ""
	if top := gitTopLevel(ctx, a.projectDir()); top != "" {
		branch = readCodeBranch(ctx, top).Branch
	}
	want, err := parseIDs(in.IDs, branch)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	if len(want) == 0 {
		return opsbranch.DoneOut{}, errs.New(errs.InvalidInput, "lore branch promote needs --ids").WithHint("lore branch show lists this branch's rows")
	}
	_, lb, _, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	key, err := keyFor(lb, "")
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	res, err := lb.gb.LayerPromote(ctx, key, want)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	lb.mu.Lock()
	lb.rows = nil
	lb.mu.Unlock()
	return opsbranch.DoneOut{OK: true, Lines: []string{fmt.Sprintf("✓ put %d row(s) of %s into the shared notes of %s (commit %s on %s; lore sync pushes it)", len(res.Rows), key.Branch, key.Folder, short7(res.Commit), lb.gb.Branch())}}, nil
}

// BranchChoose implements opsbranch.Env: lore branch isolate|share add (or
// with --undo remove) the branch in branches.isolated / branches.shared of
// the project folder's policy, through policy.set.
func (a *App) BranchChoose(ctx context.Context, in opsbranch.ChoiceIn, isolate bool) (opsbranch.DoneOut, error) {
	verb := "share"
	k := policy.KeyBranchesShared
	if isolate {
		verb, k = "isolate", policy.KeyBranchesIsolated
	}
	if err := requireBranch(verb, in.Branch); err != nil {
		return opsbranch.DoneOut{}, err
	}
	if strings.Contains(in.Branch, ",") {
		return opsbranch.DoneOut{}, errs.New(errs.InvalidInput, "a branch name may not hold a comma here").WithHint("name one branch")
	}
	st, lb, name, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	set, err := st.eng.Policy(withoutLayers(ctx), name)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	items := set.Strings(k)
	has := slices.Contains(items, in.Branch)
	switch {
	case in.Undo && !has:
		return opsbranch.DoneOut{OK: true, Lines: []string{in.Branch + " has no " + verb + " choice in " + lb.key.Folder + "; nothing changed"}}, nil
	case in.Undo:
		items = slices.DeleteFunc(items, func(s string) bool { return s == in.Branch })
	case has:
		return opsbranch.DoneOut{OK: true, Lines: []string{in.Branch + " is already chosen to " + verb + " in " + lb.key.Folder + "; nothing changed"}}, nil
	default:
		items = append(items, in.Branch)
	}
	if len(items) == 0 {
		err = invokePolicy(withoutLayers(ctx), a, opssettings.OpPolicyUnset,
			opssettings.PolicyUnsetIn{Key: string(k), To: string(name), Reason: "lore branch " + verb + " --branch " + in.Branch + " --undo"})
	} else {
		err = invokePolicy(withoutLayers(ctx), a, opssettings.OpPolicySet,
			opssettings.PolicySetIn{Key: string(k), Value: strings.Join(items, ","), To: string(name)})
	}
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	line := "✓ " + in.Branch + " now writes to its own notes for everyone in " + lb.key.Folder + " (policy " + string(k) + "; lore sync shares the choice)"
	if !isolate {
		line = "✓ " + in.Branch + " now writes to the shared notes for everyone in " + lb.key.Folder + " (policy " + string(k) + "; lore sync shares the choice)"
	}
	if in.Undo {
		line = "✓ removed the " + verb + " choice of " + in.Branch + " in " + lb.key.Folder + " (policy " + string(k) + ")"
	}
	return opsbranch.DoneOut{OK: true, Lines: []string{line}}, nil
}

// policyIn is the input of the policy operation a branch choice runs.
type policyIn interface {
	opssettings.PolicySetIn | opssettings.PolicyUnsetIn
}

// invokePolicy runs policy.set or policy.unset inside the current
// operation (the outer branch verb already passed the Runner's checks).
func invokePolicy[T policyIn](ctx context.Context, a *App, name string, in T) error {
	e, ok := a.reg.Lookup(name)
	if !ok {
		return errs.New(errs.Internal, "branch: "+name+" is not registered")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return errs.Wrap(errs.Internal, "branch: encode the policy", err)
	}
	dec, err := e.Decode(op.JSONDecoder{Body: body})
	if err != nil {
		return err
	}
	_, err = e.Invoke(ctx, dec)
	return err
}

// errNoRepo is a --repo that is no repo address.
var errNoRepo = errors.New("not a repo address")

// normalizeRepoArg normalizes --repo (github.com/acme/app, a url) the way
// a checkout's origin is (readCodeRepo): an ssh alias url
// (git@<alias>:acme/app.git) names the layers of github.com/acme/app
// (devco D-532), and the alias spelling is kept as PlainRemote so layers
// made under its key before D-532 still land.
func normalizeRepoArg(ctx context.Context, raw string) (codeRepo, error) {
	norm, err := orgcfg.ResolveRemote(ctx, "", raw)
	if err != nil || norm == "" {
		return codeRepo{}, errs.Wrap(errs.InvalidInput, "--repo "+raw, errors.Join(errNoRepo, err)).WithHint("--repo github.com/<owner>/<repo>")
	}
	return codeRepo{Remote: norm, PlainRemote: orgcfg.PlainRemote(raw, norm)}, nil
}

// mergerForLayers is the row merger a config-less store clone (the CI
// form of land) merges with: logic/merge over the built-in tables.
func mergerForLayers() git.Merger {
	reg, err := builtinRegistry()
	if err != nil {
		return nil
	}
	return merge.Driver{Registry: reg}
}

// BranchRename implements opsbranch.Env.
func (a *App) BranchRename(ctx context.Context, in opsbranch.RenameIn) (opsbranch.DoneOut, error) {
	if err := requireBranch("rename", in.From); err != nil {
		return opsbranch.DoneOut{}, err
	}
	if strings.TrimSpace(in.To) == "" || in.To == in.From {
		return opsbranch.DoneOut{}, errs.New(errs.InvalidInput, "lore branch rename needs --to, a name other than --from").WithHint("lore branch rename --from <old> --to <new>")
	}
	_, lb, _, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	from, err := keyFor(lb, in.From)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	to, err := keyFor(lb, in.To)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	if _, ferr := lb.gb.LayerFetch(ctx); ferr != nil {
		return opsbranch.DoneOut{}, ferr
	}
	l, err := lb.gb.LayerRename(ctx, from, to, true)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	rows, err := lb.gb.LayerRows(ctx, l)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	return opsbranch.DoneOut{OK: true, Lines: []string{"✓ the notes of " + in.From + " (" + opsbranch.RowsText(rowCounts(rows)) + ") now belong to " + in.To + " (" + opsbranch.PushedText(true) + ")"}}, nil
}
