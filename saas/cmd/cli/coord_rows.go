// coord_rows.go — where a coordination command reads and writes task rows.
//
// The row side of every fenced write (claim, release, holder done/note,
// human revoke/cancel/reassign) must be pushed in ONE atomic push with the
// signals ref (F7, TEAM-036), so it cannot use the normal "write lore.db,
// export at the end of the command" path: a write that lost its lease would
// still be exported and pushed by the next live sync. Instead the row is
// staged on the live ref — a commit built on the local live ref WITHOUT
// moving it — pushed together with the signals ref, and only after the
// remote confirmed both is the local live ref moved; the end-of-command sync
// pass then shows the row in lore.db (live wins).
//
// Reads always come from a fresh fetch + integrate, because a decision on a
// stale row would overwrite a teammate's edit.
//
// Interim note: the commit is built here from live's public pieces
// (LocalTree, NewVersion, RowPath, DefaultMeta). The lore-live inbox has a
// request (R1) for live.Ref.Stage/CommitStaged; when it lands this file
// switches to it and loses its copy of the first-write ref files.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dbent/gen/ent"
	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/live"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/constants"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/schema"
)

// Tables of the rows coordination writes.
const (
	tableTasks    = "tasks"
	tableMissions = "missions"
)

// Other table names the team commands branch on, taken from the canonical
// entity list so a table name is spelled in exactly one place.
const (
	tableProjects  = string(constants.EntityProject)
	tableTags      = string(constants.EntityTag)
	tableDecisions = string(constants.EntityDecision)
	tableHandoffs  = string(constants.EntityHandoff)
)

// Keys of a task document this file reads.
const (
	keyMergedAtDoc  = "merged_at"
	keyUpdatedAtDoc = "updated_at"
)

// tableOf picks the table for a task or mission id.
func tableOf(id string) string {
	if coord.IsMission(id) {
		return tableMissions
	}
	return tableTasks
}

// coordTarget is where one task's coordination happens: its row store and
// the signals ref and folder its claim lives on (the repo's own, or the
// linked store project's for a client task, D8).
type coordTarget struct {
	Rows    coord.Rows
	Signals *signals.Store
	Folder  string
	// Repo is the caller's repo name (the default --repo of task next).
	Repo string
	// close releases the DB handles.
	close func()
}

// Close releases the target's resources.
func (t coordTarget) Close() {
	if t.close != nil {
		t.close()
	}
}

// errRowsNotWired is the honest refusal where rows cannot be staged.
func errRowsNotWired() error {
	return errcodes.New(errcodes.NotImplemented, "task rows cannot be staged on the live ref here").
		WithHint("coordination needs live rows: a git repo with lore initialised and LORE_LIVE not set to 0")
}

// openCoordTarget resolves where task id is coordinated from this checkout
// (D8, §7.5):
//   - a repo without store links: the repo's own live ref and signals ref;
//   - a linked repo, id "" (task next) or an id that is not one of this
//     repo's rows: the linked store project (its live ref and signals ref);
//   - a linked CLIENT repo's own task: the claim on the store's signals ref,
//     the row in the client repo with agent details kept internal;
//   - a linked OWN repo's own task: everything in the repo (§7.6).
func openCoordTarget(ctx context.Context, w *coordWorld, id string) (coordTarget, error) {
	if w.LinkUnknown {
		return coordTarget{}, errcodes.New(errcodes.LinkUnknown, "cannot tell whether this repo is a client repo; refusing to write coordination data").
			WithHint("`lore whoami` shows the link status; fetch the store and retry")
	}
	if len(w.Links) == 0 {
		return openRepoTarget(ctx, w)
	}
	if id != "" {
		if repoT, err := openRepoTarget(ctx, w); err == nil {
			found, gerr := repoOwnsRow(ctx, repoT, id)
			if gerr == nil && found {
				if w.Role != orgresolve.RoleClient {
					return repoT, nil
				}
				return openClientTarget(ctx, w, repoT)
			}
			repoT.Close()
		}
	}
	return openStoreTarget(ctx, w)
}

// repoOwnsRow reports whether id is one of this repo's own rows. A core
// table's row is read through the core, which also answers from the linked
// store; only a row in the repo's own store counts (coreRepoOwns).
func repoOwnsRow(ctx context.Context, repoT coordTarget, id string) (bool, error) {
	_, found, err := repoT.Rows.Get(ctx, id)
	if err != nil || !found {
		return false, err
	}
	if _, isLive := repoT.Rows.(*liveRows); isLive && coreRowTable(tableOf(id)) {
		return coreRepoOwns(ctx, tableOf(id), id)
	}
	return true, nil
}

// openRepoTarget is the repo's own live ref + signals ref.
func openRepoTarget(ctx context.Context, w *coordWorld) (coordTarget, error) {
	root := liveProjectRoot(w.Dir)
	s := liveFor(ctx, root)
	if root == "" || s == nil {
		return coordTarget{}, errRowsNotWired()
	}
	var f commonFlags
	rctx, client, err := resolveContext(&f)
	if err != nil {
		return coordTarget{}, err
	}
	// P2B-OLD-3: a private-cache handle beside the command's main one, so a
	// live apply here never waits forever on the shared-cache lock.
	db, err := openPrivateLoreDB(rctx.DBPath, false)
	if err != nil {
		_ = client.Close() // the open error is the one to report
		return coordTarget{}, err
	}
	folder := s.folderFor(root)
	projectID, err := resolveProjectID(ctx, rctx.ProjectID)
	if err != nil {
		_ = client.Close() // the project error is the one to report
		_ = db.Close()     // the project error is the one to report
		return coordTarget{}, err
	}
	// A mount not registered as a repo (E_REPO_NOT_FOUND) leaves the rows
	// unscoped ("" = any repo) rather than failing a coordination command:
	// the repo id only narrows which rows a checkout sees first.
	repoID, _ := resolveRepoID(ctx, projectID, rctx.RepoMount)
	rows := &liveRows{s: s, folder: folder, db: db, client: client, projectID: projectID, repoID: repoID, actor: w.ActorID}
	return coordTarget{
		Rows: rows, Signals: w.repoSignals(), Folder: folder, Repo: rctx.RepoMount,
		close: func() { _ = db.Close(); _ = client.Close() }, // end of command: close errors are not actionable
	}, nil
}

// env builds the coord.Env for a target.
func (w *coordWorld) env(t coordTarget) coord.Env {
	return coord.Env{Signals: t.Signals, Folder: t.Folder, Rows: t.Rows, Caller: w.Caller(), Agents: w.Agents, Held: w.held(),
		Urgency: urgencyWeights(context.Background()), // OV15B-URG (coord_views.go); policySet reads the process's core app
		Time:    claimTimeSpans(context.Background())}
}

// Built-in time.long_span / time.coalesce_gap (TQ2 10h, TQ4 2m) for a
// policy that leaves the key unset.
const (
	claimLongSpanDefault    = 10 * time.Hour
	claimCoalesceGapDefault = 2 * time.Minute
)

// claimTimeSpans are the repo's time.long_span / time.coalesce_gap for the
// claim spans the coordination writer closes (OV7-6, FA-LTASK-3: the same
// keys task start/stop read). No policy, or a key not set, leaves the
// zero value: coord then uses the built-in 10h / 2m.
func claimTimeSpans(ctx context.Context) coord.TimeSpans {
	set, ok := policySet(ctx)
	if !ok {
		return coord.TimeSpans{}
	}
	// a key not set keeps its built-in default (the policy default is the same)
	out := coord.TimeSpans{LongSpan: claimLongSpanDefault, CoalesceGap: claimCoalesceGapDefault, Read: true}
	if n, isSet := set.Limit(policy.KeyTimeLongSpan); isSet {
		out.LongSpan = time.Duration(n) * time.Minute
	}
	if n, isSet := set.Limit(policy.KeyTimeCoalesceGap); isSet {
		out.CoalesceGap = time.Duration(n) * time.Minute
	}
	return out
}

// liveRows is coord.Rows over this repo's live ref (live wins) with the
// project's branch data (lore.db) as the fallback.
type liveRows struct {
	s         *liveSession
	folder    string
	db        *sql.DB
	client    *ent.Client
	projectID string
	// repoID is this checkout's repo in the project ("" when none).
	repoID string
	actor  string
}

// MergedCheck runs the lazy done → merged check (F9) for this repo.
func (r *liveRows) MergedCheck(ctx context.Context) {
	lazyMergedCheck(ctx, r.projectID, r.repoID)
}

// refresh fetches and integrates the live ref (F3), ignoring the per-command
// fetch cache: a coordination decision must see the remote as it is now.
func (r *liveRows) refresh(ctx context.Context) (live.Tree, error) {
	if r.s.blocked != nil {
		return nil, errcodes.New(errcodes.ReadOnly, "the remote live ref was rewritten; lore stopped writing to it").WithCause(r.s.blocked)
	}
	if r.s.ref.Remote != nil && r.s.autopush {
		r.s.fetch(ctx, r.s.clk.Now())
		if r.s.fetchErr != nil {
			return nil, &signals.OfflineError{Reason: "live ref fetch failed", Err: r.s.fetchErr}
		}
	}
	r.s.integrate(ctx)
	if r.s.blocked != nil {
		return nil, errcodes.New(errcodes.ReadOnly, "the remote live ref was rewritten; lore stopped writing to it").WithCause(r.s.blocked)
	}
	_, tree, err := r.s.ref.LocalTree(ctx)
	return tree, err
}

// Get implements coord.Rows: the live file when there is one (a tombstone
// means deleted), else the branch row from lore.db.
func (r *liveRows) Get(ctx context.Context, id string) (coord.Doc, bool, error) {
	if coreRowTable(tableOf(id)) {
		// The core owns the row (task_corerows.go); with live mode on, its
		// live copy is read through the core's live overlay (live_core.go)
		// after a fresh fetch + integrate: a decision on a stale row would
		// overwrite a teammate's edit.
		if r.s != nil {
			if _, err := r.refresh(ctx); err != nil {
				return nil, false, err
			}
		}
		return coreDoc(ctx, tableOf(id), id)
	}
	tree, err := r.refresh(ctx)
	if err != nil {
		return nil, false, err
	}
	files, _ := tree.Rows(r.folder)
	if f, ok := files[live.RowKey{Table: tableOf(id), ID: id}]; ok {
		if f.Deleted() {
			return nil, false, nil
		}
		return coord.Doc(f.Body), true, nil
	}
	branch, okBranch, err := r.branchDoc(ctx, tableOf(id), id)
	if err != nil {
		return nil, false, err
	}
	main, okMain := r.mainDoc(ctx, tableOf(id), id)
	d, ok := newerDoc(branch, okBranch, main, okMain)
	return d, ok, nil
}

// branchDoc is the row as this checkout's branch data has it (lore.db).
func (r *liveRows) branchDoc(ctx context.Context, table, id string) (coord.Doc, bool, error) {
	b, ok, err := lsync.RowDoc(ctx, r.db, table, id)
	if err != nil || !ok {
		return nil, false, err
	}
	d, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return nil, false, fmt.Errorf("decode %s: %w", id, err)
	}
	return coord.Doc(d), true, nil
}

// mainDoc is the row on the fetched default branch, its durable home
// (fixed rule 1). A row that landed and left live exists only there for a
// clone whose checkout is older; a coordination decision must never act on
// the stale branch copy (it would reopen a finished task).
// Any read or decode failure means "no main copy".
func (r *liveRows) mainDoc(ctx context.Context, table, id string) (coord.Doc, bool) {
	target := "refs/remotes/" + r.s.ref.RemoteName + "/" + r.s.defaultBranch(ctx)
	b, ok, err := r.s.ref.Repo.ReadFile(ctx, target, mainDataPath(r.folder, table+"/"+id+".json"))
	if err != nil || !ok {
		return nil, false // nothing fetched yet, or not landed: no main copy
	}
	d, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return nil, false
	}
	return coord.Doc(d), true
}

// newerDoc picks the newer of two copies of one row by updated_at (the
// canonical fixed-width layout orders as text). A tie goes to the default
// branch: it only ever receives landed content, so an equal stamp there is
// at least as new as the branch copy (and two writes in one second, or a
// fixed test clock, must not reopen a task main already holds as done).
func newerDoc(branch coord.Doc, okBranch bool, main coord.Doc, okMain bool) (coord.Doc, bool) {
	switch {
	case okBranch && okMain:
		if branch.Str(keyUpdatedAtDoc) > main.Str(keyUpdatedAtDoc) {
			return branch, true
		}
		return main, true
	case okMain:
		return main, true
	}
	return branch, okBranch
}

// List returns every task of the project. Tasks are core-owned: their row
// files, read through the core in every attached store, are the only copy
// that counts (task_corerows.go).
func (r *liveRows) List(ctx context.Context) ([]coord.Doc, error) {
	return coreDocs(ctx, tableTasks)
}

// RepoName maps a repo id to its mount name (what --repo matches).
func (r *liveRows) RepoName(repoID string) string {
	d, ok, err := coreDoc(context.Background(), schema.TableRepos, repoID)
	if err != nil || !ok || d.Str(fieldMountName) == "" {
		return repoID
	}
	return d.Str(fieldMountName)
}

// Stage implements coord.Rows: one companion, one commit for all rows.
// Rows of a core-owned table (tasks, missions, comments) are written
// through the core after the signals push (task_corerows.go); any other
// row is staged on the live ref as before.
func (r *liveRows) Stage(_ context.Context, rows ...coord.RowWrite) ([]signals.Companion, error) {
	var core, live []coord.RowWrite
	for _, w := range rows {
		if coreRowTable(coreTableFor(w)) {
			core = append(core, w)
		} else {
			live = append(live, w)
		}
	}
	var out []signals.Companion
	if len(live) > 0 {
		out = append(out, &stagedLiveRows{spot: r.spot(), rows: live})
	}
	if len(core) > 0 {
		if r.s != nil && r.s.ref.Remote != nil && !liveDisabled() {
			// Live mode: the rows join the signals push (same remote).
			out = append(out, &coreLiveCompanion{rows: core, refresh: func(ctx context.Context) error {
				_, err := r.refresh(ctx)
				return err
			}})
		} else {
			out = append(out, &coreCompanion{rows: core})
		}
	}
	return out, nil
}

// spot is this repo's live ref and project folder for staging.
func (r *liveRows) spot() liveSpot {
	return liveSpot{ref: r.s.ref, folder: r.folder, clk: r.s.clk, actor: r.actor, wrote: func() { r.s.wrote++ }}
}

// liveSpot is one project folder on one live ref: where staged rows go.
type liveSpot struct {
	ref    *live.Ref
	folder string
	clk    clock.Clock
	actor  string
	// wrote is told after each applied staging (end-of-command counts).
	wrote func()
}

// stagedLiveRows are row versions waiting for the push.
type stagedLiveRows struct {
	spot liveSpot
	rows []coord.RowWrite
	// old/new are the local live ref before and after, set by Prepare.
	old, new string
}

// Prepare builds the live commit on the local live ref (integrated by the
// decision's reads) without moving any ref.
func (p *stagedLiveRows) Prepare(ctx context.Context) (gitremote.RefUpdate, error) {
	sp := p.spot
	old, tree, err := sp.ref.LocalTree(ctx)
	if err != nil {
		return gitremote.RefUpdate{}, err
	}
	files, _ := tree.Rows(sp.folder)
	ch := gitref.Changes{Put: map[string][]byte{}}
	var names []string
	for _, w := range p.rows {
		table := w.Table
		if table == "" {
			table = tableOf(w.ID)
		}
		key := live.RowKey{Table: table, ID: w.ID}
		var prev *live.File
		if f, ok := files[key]; ok {
			prev = &f
		}
		f := live.NewVersion(prev, map[string]any(w.Doc), sp.clk.Now(), sp.actor)
		b, encErr := f.Encode()
		if encErr != nil {
			return gitremote.RefUpdate{}, encErr
		}
		ch.Put[live.RowPath(sp.folder, key.Table, key.ID)] = b
		names = append(names, key.String())
	}
	if err = addFirstWriteFiles(tree, ch); err != nil {
		return gitremote.RefUpdate{}, err
	}
	sig := sp.ref.Signature
	sig.When = sp.clk.Now()
	c, err := sp.ref.Repo.MakeCommit(ctx, gitref.CommitRequest{Base: old, Changes: ch,
		Message: "lore: live " + strings.Join(names, ", "), Signature: sig})
	if err != nil {
		return gitremote.RefUpdate{}, fmt.Errorf("stage %s: %w", strings.Join(names, ", "), err)
	}
	p.old, p.new = old, c.OID
	return gitremote.RefUpdate{Src: c.OID, Dst: sp.ref.Name, Mode: gitremote.ModeFastForward}, nil
}

// Finalize runs after the remote confirmed the push: move the local live
// ref and its tracking copy to the pushed commit.
func (p *stagedLiveRows) Finalize(ctx context.Context) error {
	sp := p.spot
	if p.new == "" || p.new == p.old {
		return nil
	}
	if err := sp.ref.Repo.UpdateRef(ctx, sp.ref.Name, p.new, p.old); err != nil {
		return fmt.Errorf("move %s: %w", sp.ref.Name, err)
	}
	track := sp.ref.Tracking()
	cur, _, err := sp.ref.Repo.ResolveRef(ctx, track)
	if err == nil {
		_ = sp.ref.Repo.UpdateRef(ctx, track, p.new, cur) // a newer fetch may have moved it: harmless
	}
	if sp.wrote != nil {
		sp.wrote()
	}
	return nil
}

// remoteStagedRows is stagedLiveRows on another remote than the signals
// ref (a client repo's live ref, D8): pushed by itself after the store's
// signals push, retried with a fresh integrate when a teammate's live write
// got there first.
type remoteStagedRows struct {
	stagedLiveRows
}

// remotePushTries bounds PushSelf's integrate-and-retry rounds.
const remotePushTries = 4

// PushSelf implements signals.RemoteCompanion.
func (p *remoteStagedRows) PushSelf(ctx context.Context) error {
	ref := p.spot.ref
	if ref.Remote == nil {
		return nil // no remote (solo): the row stays local, like any live write
	}
	for try := 1; ; try++ {
		res, err := ref.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: p.new, Dst: ref.Name, Mode: gitremote.ModeFastForward}}})
		if err != nil {
			return err
		}
		if res.OK() {
			return nil
		}
		if try >= remotePushTries {
			return fmt.Errorf("%s: %+v", ref.Name, res.Refs)
		}
		if err := ref.Fetch(ctx); err != nil {
			return err
		}
		if _, err := ref.Integrate(ctx); err != nil {
			return err
		}
		if _, err := p.Prepare(ctx); err != nil {
			return err
		}
	}
}

// addFirstWriteFiles adds _live/epoch and _live/meta.json when the ref has
// none yet (the first live write ever), as live.Ref.Write does.
func addFirstWriteFiles(tree live.Tree, ch gitref.Changes) error {
	if _, ok := tree[live.EpochPath]; !ok {
		ch.Put[live.EpochPath] = []byte(strconv.Itoa(live.FirstEpoch) + "\n")
	}
	if _, ok := tree[live.MetaPath]; !ok {
		b, err := live.DefaultMeta().Encode()
		if err != nil {
			return err
		}
		ch.Put[live.MetaPath] = b
	}
	return nil
}
