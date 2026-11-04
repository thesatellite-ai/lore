// coord_store.go — coordination on a store project (LORE_TEAM_SPEC D8,
// §7.5, §7.6): a linked repo's group tasks live in the store, so their
// claims go on the store's signals ref (`refs/lore/signals/<branch>`,
// folder of the linked project) and their rows on the store's live ref
// (`refs/lore/live/<branch>`), pushed atomically to the store. A linked
// client repo's OWN task keeps its row in the client repo, but its claim is
// still on the store and the client row never carries agent details (claim
// log, work branch, the agent as done_by) unless the org sets
// `agent_details: client`.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"encoding/json"
	"strconv"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/live"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/mergewatch"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/repourl"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/stores"
)

// storeStateDir is a bare store clone's machine-state folder (§1:
// `<clone>/lore/`).
const storeStateDir = "lore"

// defaultLink is the link a linked repo coordinates through: the one
// marked default, or the only one (TEAM-032).
func defaultLink(links []orgresolve.Link) (orgresolve.Link, error) {
	if len(links) == 1 {
		return links[0], nil
	}
	for _, l := range links {
		if l.Default {
			return l, nil
		}
	}
	return orgresolve.Link{}, errcodes.New(errcodes.InvalidInput, "this repo has several links and none is the default (TEAM-032)").
		WithHint("mark one with `lore org link <org> --project <folder> --default`")
}

// storeSpot is an opened store project.
type storeSpot struct {
	st     *stores.Store
	branch string
	folder string
	ref    *live.Ref
	// sess lands what the command wrote (land runs at the target's Close).
	sess *liveSession
}

// wrote counts a write for land.
func (sp storeSpot) wrote() {
	if sp.sess != nil {
		sp.sess.wrote++
	}
}

// land pushes and lands the store live rows this command wrote.
func (sp storeSpot) land() {
	if sp.sess != nil && sp.sess.wrote > 0 {
		sp.sess.finishStore(withStoreWrite(context.Background()), sp.folder)
	}
}

// openStoreSpot opens the linked store project's clone and live ref.
func openStoreSpot(ctx context.Context, w *coordWorld) (storeSpot, error) {
	if w.Org == nil {
		return storeSpot{}, errcodes.New(errcodes.InvalidInput, "store coordination needs an org")
	}
	link, err := defaultLink(w.Links)
	if err != nil {
		return storeSpot{}, err
	}
	return openStoreSpotFor(ctx, w, w.Org, link.Folder)
}

// openStoreSpotFor opens folder of org's stores (the folder's store
// branch is the org entry's: a staging entry reads its staging branch).
func openStoreSpotFor(ctx context.Context, w *coordWorld, org *lorecfg.Org, folder string) (storeSpot, error) {
	pref, err := findStoreFolder(ctx, w.Config, org, folder)
	if err != nil {
		return storeSpot{}, err
	}
	m, err := newStoreManager(w.Config, org)
	if err != nil {
		return storeSpot{}, storeCLIError(err, "open the store")
	}
	st, err := m.Open(ctx, pref.Store.URL)
	if err != nil {
		return storeSpot{}, storeCLIError(err, "open the store")
	}
	branch := pref.Store.EffectiveBranch()
	// lore-live's store session: the same refs/lore/live/<branch>, and its
	// finishStore lands finished rows on the store branch (F5).
	sess := newStoreLiveSession(ctx, st.Repo, st.Remote, stores.RemoteName, filepath.Join(st.Dir, storeStateDir), branch, liveSignature(w.Ident))
	sess.ref.Clock, sess.clk = w.Clock, w.Clock
	return storeSpot{st: st, branch: branch, folder: folder, ref: sess.ref, sess: sess}, nil
}

// openCoordTargetFrom honours `--from <source>` (fixed rule 5, §1 flags):
// "" is the normal lookup, "repo" this repo's own project, "<org>" or
// "<org>/<folder>" a project of that org entry's stores (the folder
// defaults to this repo's link folder).
func openCoordTargetFrom(ctx context.Context, w *coordWorld, id, from string) (coordTarget, error) {
	switch from {
	case "":
		return openCoordTarget(ctx, w, id)
	case toRepo:
		return openRepoTarget(ctx, w)
	}
	name, folder, _ := strings.Cut(from, "/")
	org := w.Config.FindOrg(name)
	if org == nil {
		return coordTarget{}, errcodes.New(errcodes.InvalidInput, "--from "+from+": no org entry named "+name).
			WithHint("use repo, an org entry name, or <org>/<folder>")
	}
	if folder == "" {
		link, err := defaultLink(w.Links)
		if err != nil {
			return coordTarget{}, errcodes.New(errcodes.InvalidInput, "--from "+from+": name the folder (<org>/<folder>); this repo has no default link")
		}
		folder = link.Folder
	}
	sp, err := openStoreSpotFor(ctx, w, org, folder)
	if err != nil {
		return coordTarget{}, err
	}
	if _, client, err := resolveContext(&commonFlags{}); err == nil {
		_ = client.Close() // opened only for its side effect (hook wiring); nothing was read
	}
	rows := &storeRows{sp: sp, w: w, here: hereRepoName(ctx, w)}
	return coordTarget{Rows: rows, Signals: w.storeSignals(sp), Folder: sp.folder, Repo: rows.here, close: sp.land}, nil
}

// storeSignals is the store branch's signals ref, observed per store.
func (w *coordWorld) storeSignals(sp storeSpot) *signals.Store {
	s := w.signalsStore(sp.st.Repo, sp.st.Remote, signals.StoreRef(sp.branch))
	s.RemoteName = stores.RemoteName
	// What this machine has watched of a store's signals ref lives with the
	// one bare clone every checkout here reads it through (§1: <clone>/lore/),
	// so a fresh checkout or agent clone inherits the watch instead of
	// starting it again; the two-part expiry rule is unchanged.
	s.Seen = signals.SeenStore{Path: filepath.Join(sp.st.Dir, storeStateDir, seenFileName)}
	return s
}

// hereRepoName is this checkout's repo name in a group: the last path
// segment of its remote (github.com/globex/backend → backend).
func hereRepoName(ctx context.Context, w *coordWorld) string {
	remotes, _ := gitRemotes(ctx, w.Dir)
	if len(remotes) == 0 {
		return ""
	}
	if n, err := repourl.Normalize(remotes[0]); err == nil {
		return path.Base(n)
	}
	return path.Base(remotes[0])
}

// openStoreTarget coordinates the linked store project's tasks.
func openStoreTarget(ctx context.Context, w *coordWorld) (coordTarget, error) {
	// Open this checkout's own lore context too, even though the rows are
	// in the store: it runs the per-checkout sync session, which installs
	// lore's git hooks at the end of the command — without it a fresh agent
	// clone whose first command is `task next` never gets the commit-msg
	// hook, and its commits miss the Lore-Task trailer (F9).
	if _, client, err := resolveContext(&commonFlags{}); err == nil {
		_ = client.Close() // opened only for its side effect (hook wiring); nothing was read
	}
	sp, err := openStoreSpot(ctx, w)
	if err != nil {
		return coordTarget{}, err
	}
	rows := &storeRows{sp: sp, w: w, here: hereRepoName(ctx, w)}
	return coordTarget{Rows: rows, Signals: w.storeSignals(sp), Folder: sp.folder, Repo: rows.here, close: sp.land}, nil
}

// storeRows is coord.Rows over a store project: the store's live ref (live
// wins) over the store branch's data, the project's durable home.
type storeRows struct {
	sp   storeSpot
	w    *coordWorld
	here string
}

// storeDataPath is a row file inside a store project folder.
func storeDataPath(folder, table, id string) string {
	p := projresolve.MarkerDir + "/data/" + table + "/" + id + ".json"
	if folder == stores.RootFolder || folder == "" {
		return p
	}
	return folder + "/" + p
}

func (r *storeRows) refresh(ctx context.Context) (live.Tree, error) {
	if r.w.Autopush {
		res, err := r.sp.st.Fetch(ctx, true)
		if err != nil {
			return nil, err
		}
		if res.Offline {
			return nil, &signals.OfflineError{Reason: "store unreachable", Err: res.Err}
		}
	}
	if _, err := r.sp.ref.Integrate(ctx); err != nil {
		return nil, fmt.Errorf("store live ref: %w", err)
	}
	_, tree, err := r.sp.ref.LocalTree(ctx)
	return tree, err
}

func decodeDoc(b []byte) (coord.Doc, error) {
	d, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	return coord.Doc(d), err
}

// Get implements coord.Rows.
func (r *storeRows) Get(ctx context.Context, id string) (coord.Doc, bool, error) {
	tree, err := r.refresh(ctx)
	if err != nil {
		return nil, false, err
	}
	files, bad := tree.Rows(r.sp.folder)
	if err = badRowsError(bad); err != nil {
		return nil, false, err
	}
	if f, ok := files[live.RowKey{Table: tableOf(id), ID: id}]; ok {
		if f.Deleted() {
			return nil, false, nil
		}
		return coord.Doc(f.Body), true, nil
	}
	b, ok, err := r.sp.st.Repo.ReadFile(ctx, stores.TrackingBranch(r.sp.branch), storeDataPath(r.sp.folder, tableOf(id), id))
	if errors.Is(err, gitref.ErrNotFound) {
		return nil, false, nil // the store branch was never fetched: only live rows exist
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s from the store branch: %w", id, err)
	}
	if !ok {
		return nil, false, nil
	}
	d, err := decodeDoc(b)
	if err != nil {
		return nil, false, fmt.Errorf("decode %s: %w", id, err)
	}
	return d, true, nil
}

// storeRowFileExt is the extension of a row file on a store branch (the row
// layout's own constant is unexported in live and lsync).
const storeRowFileExt = ".json"

// badRowsError turns the live rows that failed to decode (live.Tree.Rows'
// bad map) into one error naming them, nil when there are none. A row that
// cannot be read is never treated as absent: an absent task looks free to
// `task next`, an absent claim fence looks unclaimed.
func badRowsError(bad map[string]error) error {
	if len(bad) == 0 {
		return nil
	}
	paths := make([]string, 0, len(bad))
	for p := range bad {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return fmt.Errorf("unreadable live rows on the store's live ref: %s (first: %w)", strings.Join(paths, ", "), bad[paths[0]])
}

// List implements the listing half (task next): branch rows overlaid with
// live ones.
func (r *storeRows) List(ctx context.Context) ([]coord.Doc, error) {
	return r.listTable(ctx, tableTasks)
}

// Notes returns the task's notes (comments rows of the store project),
// oldest first, for `task show` (§7.5 step 5).
func (r *storeRows) Notes(ctx context.Context, taskID string) ([]coord.Doc, error) {
	all, err := r.listTable(ctx, tableComments)
	if err != nil {
		return nil, err
	}
	var out []coord.Doc
	for _, d := range all {
		if d.Str("entity_table") == tableTasks && d.Str("entity_id") == taskID {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Str("created_at") < out[j].Str("created_at") })
	return out, nil
}

// listTable reads every row of one table of the store project: the store
// branch's files overlaid with the live ref (live wins, a tombstone hides).
func (r *storeRows) listTable(ctx context.Context, table string) ([]coord.Doc, error) {
	tree, err := r.refresh(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]coord.Doc{}
	dir := strings.TrimSuffix(storeDataPath(r.sp.folder, table, "x"), "x.json")
	files, err := r.sp.st.Repo.ReadTree(ctx, stores.TrackingBranch(r.sp.branch), strings.TrimSuffix(dir, "/"))
	switch {
	case errors.Is(err, gitref.ErrNotFound):
		// The store branch was never fetched: only live rows exist.
	case err != nil:
		return nil, fmt.Errorf("read %s from the store branch: %w", table, err)
	}
	for p, b := range files {
		if path.Ext(p) != storeRowFileExt {
			continue // not a row file (a placeholder such as .gitkeep)
		}
		d, err := decodeDoc(b)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", p, err)
		}
		byID[strings.TrimSuffix(path.Base(p), ".json")] = d
	}
	rows, bad := tree.Rows(r.sp.folder)
	if err := badRowsError(bad); err != nil {
		return nil, err
	}
	for k, f := range rows {
		if k.Table != table {
			continue
		}
		if f.Deleted() {
			delete(byID, k.ID)
			continue
		}
		byID[k.ID] = coord.Doc(f.Body)
	}
	out := make([]coord.Doc, 0, len(byID))
	for _, d := range byID {
		out = append(out, d)
	}
	return out, nil
}

// RepoName maps the group's repo ids to their names (repos rows of the
// store project, written by `task add --repo` / plans).
func (r *storeRows) RepoName(repoID string) string {
	ctx := context.Background()
	b, ok, err := r.sp.st.Repo.ReadFile(ctx, stores.TrackingBranch(r.sp.branch), storeDataPath(r.sp.folder, "repos", repoID))
	if err != nil || !ok {
		if _, tree, terr := r.sp.ref.LocalTree(ctx); terr == nil {
			if rows, _ := tree.Rows(r.sp.folder); rows != nil {
				if f, ok := rows[live.RowKey{Table: "repos", ID: repoID}]; ok {
					return coord.Doc(f.Body).Str("mount_name")
				}
			}
		}
		return repoID
	}
	d, err := decodeDoc(b)
	if err != nil {
		return repoID
	}
	return d.Str("mount_name")
}

// Stage implements coord.Rows: one commit on the store's live ref, pushed
// atomically with the store's signals ref (same remote).
func (r *storeRows) Stage(ctx context.Context, rows ...coord.RowWrite) ([]signals.Companion, error) {
	if newFormatStore(ctx, r.sp.st, r.sp.branch) {
		// A 0.1.18 store: its rows are the core's, written on the store
		// branch through the core's attachment and pushed at once
		// (STORE_SERVER_SPEC 6.1, LORE_TEAM_SPEC §3.2), after the signals
		// push confirmed the lease (coreCompanion, as for repo rows).
		return []signals.Companion{&coreCompanion{rows: rows}}, nil
	}
	return []signals.Companion{&stagedLiveRows{spot: r.spot(), rows: rows}}, nil
}

// newFormatStore reports a store whose marker on the fetched branch is
// v2 (`lore migrate run` or a 0.1.18 `lore store init`): its task rows
// belong to the core (`_v: 2` files), so a v1 document is never staged on
// its v0.1.17 live ref. The marker is read from the store branch as last
// fetched (the clone is bare: there is no lore-store.yaml file on disk).
func newFormatStore(ctx context.Context, st *stores.Store, branch string) bool {
	return st.FormatAt(ctx, stores.TrackingBranch(branch)) >= stores.NewFormatMarkerVersion
}

func (r *storeRows) spot() liveSpot {
	return liveSpot{ref: r.sp.ref, folder: r.sp.folder, clk: r.w.Clock, actor: r.w.ActorID, wrote: r.sp.wrote}
}

// MergedCheck is the lazy done → merged check (F9) for the group's tasks
// whose repo is this checkout: found → merged_at on the store row, once.
func (r *storeRows) MergedCheck(ctx context.Context) {
	docs, err := r.List(ctx)
	if err != nil {
		warnMergeUnchecked(os.Stderr, fmt.Errorf("read the store's task rows: %w", err))
		return
	}
	ck := mergewatch.Checker{Repo: r.w.Repo, Target: mergedTarget(ctx, r.w.Repo, defaultRemoteName)}
	if adapter, ok := mergeAdapter(ctx, r.w.Dir); ok {
		ck.Adapter = adapter
	}
	st := r.w.storeSignals(r.sp)
	for _, d := range docs {
		if d.Status() != string(statusDoneQ) || d.Str(keyMergedAtDoc) != "" {
			continue
		}
		if repo := d.Str("repo_id"); repo != "" && r.RepoName(repo) != r.here && ck.Adapter == nil {
			continue
		}
		c := mergewatch.Candidate{TaskID: d.Str("id"), DoneCommit: d.Str("done_commit"), DonePR: d.Int("done_pr")}
		f, ok, _, err := ck.Check(ctx, c)
		if err != nil || !ok {
			continue
		}
		if ok, err := stampMerged(ctx, r, st, c.TaskID, f.At); ok || err != nil {
			reportMerged(os.Stderr, c.TaskID, f, err)
		}
	}
}

// mergedRows is the half of coord.Rows that recording a merge needs: a
// fresh read and the staging (storeRows and liveRows in production; a seam
// for tests).
type mergedRows interface {
	Get(ctx context.Context, id string) (coord.Doc, bool, error)
	Stage(ctx context.Context, rows ...coord.RowWrite) ([]signals.Companion, error)
}

// signalsWriter is the one signals.Store call recording a merge needs.
type signalsWriter interface {
	Write(ctx context.Context, decide signals.Decide) (signals.Result, error)
}

// stampMerged records merged_at = at on task id, once (F9). The row is read
// and staged INSIDE the signals write's decision, so a lost race (another
// clone stamped the same merge, or wrote any live row, between our read and
// our push) re-reads the fresh row and decides again instead of re-pushing
// a stale commit until the retries run out. Only merged_at changes:
// updated_at is left as it was, so two clones that find the same merge
// stage the same row and the second has nothing to land. recorded is false
// (with a nil error) when the row is no longer a done task without
// merged_at, i.e. someone else recorded it first. Any failure is returned,
// so the caller reports "merged" only for a row that was actually written.
func stampMerged(ctx context.Context, rows mergedRows, st signalsWriter, id string, at time.Time) (recorded bool, err error) {
	_, err = st.Write(ctx, func(signals.View) (signals.Change, error) {
		recorded = false
		d, ok, gerr := rows.Get(ctx, id)
		if gerr != nil {
			return signals.Change{}, fmt.Errorf("read %s: %w", id, gerr)
		}
		if !ok || d.Status() != string(statusDoneQ) || d.Str(keyMergedAtDoc) != "" {
			return signals.Change{}, nil // gone, reopened, or already stamped
		}
		next := d.Clone()
		next.SetTime(keyMergedAtDoc, at)
		comps, serr := rows.Stage(ctx, coord.RowWrite{ID: id, Doc: next})
		if serr != nil {
			return signals.Change{}, fmt.Errorf("stage %s: %w", id, serr)
		}
		recorded = true
		return signals.Change{Companions: comps}, nil
	})
	if err != nil {
		return false, fmt.Errorf("write %s: %w", id, err)
	}
	return recorded, nil
}

// reportMerged prints the outcome of one found merge: "✓ merged" only when
// recording it succeeded (recErr nil); otherwise that it was found but not
// recorded and the next command retries. The host command is not failed
// (F9: the lazy check never fails the command that ran it).
func reportMerged(out io.Writer, id string, f mergewatch.Finding, recErr error) {
	if recErr != nil {
		_, _ = fmt.Fprintf(out, "lore: %s merged %s (found by: %s, %s) but it could not be recorded: %v; the next lore command retries\n",
			id, f.At.UTC().Format("15:04"), f.Method, shortSHA(f.Commit), recErr)
		return
	}
	_, _ = fmt.Fprintf(out, "✓ %s merged %s (found by: %s, %s)\n", id, f.At.UTC().Format("15:04"), f.Method, shortSHA(f.Commit))
}

// groupMergedCheck runs the merged check for the linked store project's
// tasks of this repo (F9: "in any globex-api checkout … the next lore
// command sees the trailer"). Silent when the repo has no team
// coordination or no links; when the store cannot be reached it prints one
// warning line on stderr. It never fails the command.
func groupMergedCheck(ctx context.Context) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return
	}
	groupMergedCheckIn(ctx, os.Stderr, w, openStoreTarget)
}

// storeTargetOpener opens the linked store project's coordination target
// (openStoreTarget in production; a seam so tests can make the store
// unreachable).
type storeTargetOpener func(ctx context.Context, w *coordWorld) (coordTarget, error)

// groupMergedCheckIn is groupMergedCheck for an opened world: a repo with
// no links has nothing to check; a store that cannot be opened is reported
// on warn (F9: the check never fails the command, but it must not look as
// if it ran).
func groupMergedCheckIn(ctx context.Context, warn io.Writer, w *coordWorld, open storeTargetOpener) {
	if len(w.Links) == 0 {
		return
	}
	t, err := open(ctx, w)
	if err != nil {
		warnMergeUnchecked(warn, fmt.Errorf("open the linked store: %w", err))
		return
	}
	defer t.Close()
	if r, ok := t.Rows.(*storeRows); ok {
		r.MergedCheck(ctx)
	}
}

// mergeUncheckedPrefix starts the one warning line the lazy merged check
// prints when it could not run; scripts and tests match on it.
const mergeUncheckedPrefix = "lore: warning: merge state not checked: "

// warnMergeUnchecked prints that the lazy done → merged check (F9) did not
// run, and why, as one line: done tasks of the linked store keep showing
// "done, not merged yet" until a later command reaches the store.
func warnMergeUnchecked(out io.Writer, cause error) {
	_, _ = fmt.Fprintf(out, "%s%s; done tasks may show as not merged yet (the next lore command retries)\n",
		mergeUncheckedPrefix, oneLine(cause.Error()))
}

// statusDoneQ is the done status as task documents spell it.
const statusDoneQ = "done"

// ---------------------------------------------------------------- client split

// openClientTarget coordinates a linked client repo's own task: the claim
// on the store's signals ref (folder of the linked project), the row in the
// client repo with agent details kept internal (D8).
func openClientTarget(ctx context.Context, w *coordWorld, repoT coordTarget) (coordTarget, error) {
	sp, err := openStoreSpot(ctx, w)
	if err != nil {
		repoT.Close()
		return coordTarget{}, err
	}
	inner, ok := repoT.Rows.(*liveRows)
	if !ok {
		repoT.Close()
		return coordTarget{}, errRowsNotWired()
	}
	details := lorecfg.DefaultAgentDetailsSetting
	if w.Org != nil {
		details = w.Org.EffectiveAgentDetails()
	}
	rows := &clientRows{inner: inner, store: sp, w: w, details: details}
	return coordTarget{Rows: rows, Signals: w.storeSignals(sp), Folder: sp.folder, Repo: repoT.Repo, close: func() {
		sp.land()
		if repoT.close != nil {
			repoT.close()
		}
	}}, nil
}

// clientRows is a client repo's task rows coordinated from a store.
type clientRows struct {
	inner   *liveRows
	store   storeSpot
	w       *coordWorld
	details lorecfg.AgentDetails
}

// Get implements coord.Rows: the client row merged with its store-side
// linked row, which holds what never goes on the client copy (attempts,
// claim log, time box, work branch; coordinator ruling 15:05).
func (r *clientRows) Get(ctx context.Context, id string) (coord.Doc, bool, error) {
	d, ok, err := r.inner.Get(ctx, id)
	if err != nil || !ok || !r.internalNote() {
		return d, ok, err
	}
	link, found, err := r.linkedRow(ctx, id)
	if err != nil {
		return nil, false, err
	}
	out := d.Clone()
	if found {
		for _, k := range linkedKeys {
			out[k] = link[k]
		}
	}
	return out, true, nil
}

func (r *clientRows) List(ctx context.Context) ([]coord.Doc, error) { return r.inner.List(ctx) }
func (r *clientRows) RepoName(id string) string                     { return r.inner.RepoName(id) }

// Client row fields that name how the agency works (D8): never in the
// client copy unless agent_details: client. claim_epoch stays (a bare
// number the client-side fencing of status writes needs; recorded as a
// spec side effect).
const (
	keyClaimLogDoc   = "claim_log"
	keyWorkBranchDoc = "work_branch"
	keyDoneByDoc     = "done_by"
	keyAttemptsDoc   = "attempts"
	keyTimeboxDoc    = "timebox_seconds"
)

// linkedKeys move from the client row to the store-side linked row.
var linkedKeys = []string{keyAttemptsDoc, keyClaimLogDoc, keyWorkBranchDoc, keyTimeboxDoc}

// clientLinkRole marks a store-side linked row: `task next` offers a role
// task only to `--role <role>`, so a linked row is never claimed as work.
const clientLinkRole = "client-link"

// clientLinkKeyPrefix + <client project>/<task> is a linked row's
// idempotency_key: the pointer from the internal row to the client task
// (internal → client, allowed by F15).
const clientLinkKeyPrefix = "client-task:"

// linkedID is the store-side linked row's id: derived, so every machine
// writes the same row for one client task.
func (r *clientRows) linkedID(taskID string) (string, error) {
	return ids.Deterministic(ids.PrefixTask, clientLinkRole, r.inner.projectID, taskID)
}

// linkedRow reads the linked row from the store (live wins over the
// store branch).
func (r *clientRows) linkedRow(ctx context.Context, taskID string) (coord.Doc, bool, error) {
	lid, err := r.linkedID(taskID)
	if err != nil {
		return nil, false, err
	}
	sr := &storeRows{sp: r.store, w: r.w}
	return sr.Get(ctx, lid)
}

// storeProjectID is the store project's id (its _meta.json on the store
// branch), needed by a linked row it creates.
func (r *clientRows) storeProjectID(ctx context.Context) string {
	b, ok, err := r.store.st.Repo.ReadFile(ctx, stores.TrackingBranch(r.store.branch), strings.TrimSuffix(storeDataPath(r.store.folder, "x", "x"), "x/x.json")+"_meta.json")
	if err != nil || !ok {
		return ""
	}
	d, err := decodeDoc(b)
	if err != nil {
		return ""
	}
	return d.Str("project_id")
}

// linkedDoc builds the new linked row for one client task version.
func (r *clientRows) linkedDoc(ctx context.Context, taskID string, full coord.Doc) (string, coord.Doc, error) {
	lid, err := r.linkedID(taskID)
	if err != nil {
		return "", nil, err
	}
	prev, found, err := r.linkedRow(ctx, taskID)
	if err != nil {
		return "", nil, err
	}
	now := r.w.Clock.Now()
	var d coord.Doc
	if found {
		d = prev.Clone()
	} else {
		d = coord.Doc{"_table": tableTasks, "id": lid, "project_id": r.storeProjectID(ctx), "priority": "medium",
			"commitment": "accepted", "role": clientLinkRole, "idempotency_key": clientLinkKeyPrefix + r.inner.projectID + "/" + taskID}
		d["_v"] = json.Number(strconv.Itoa(lsync.FormatVersion))
		d.SetTime("created_at", now)
	}
	d["title"] = full.Str("title")
	d["status"] = full.Status()
	for _, k := range linkedKeys {
		d[k] = full[k]
	}
	d.SetTime("updated_at", now)
	return lid, d, nil
}

// Stage implements coord.Rows: the task row goes to the client repo (pushed
// after the store's signals lease) without agent details, which go to the
// store-side linked row; notes go to the store project — unless the org
// sets agent_details: client, which keeps everything on the client side.
func (r *clientRows) Stage(ctx context.Context, rows ...coord.RowWrite) ([]signals.Companion, error) {
	var client, internal []coord.RowWrite
	for _, w := range rows {
		table := w.Table
		if table == "" {
			table = tableOf(w.ID)
		}
		switch {
		case !r.internalNote():
			client = append(client, coord.RowWrite{Table: table, ID: w.ID, Doc: w.Doc})
			if table == tableTasks {
				if a, ok := r.agentProjection(); ok {
					client = append(client, a)
				}
			}
		case table == tableComments:
			d := w.Doc.Clone()
			d["entity_project_id"] = r.inner.projectID
			internal = append(internal, coord.RowWrite{Table: table, ID: w.ID, Doc: d})
		case table == tableTasks:
			lid, ld, err := r.linkedDoc(ctx, w.ID, w.Doc)
			if err != nil {
				return nil, err
			}
			internal = append(internal, coord.RowWrite{Table: tableTasks, ID: lid, Doc: ld})
			client = append(client, coord.RowWrite{Table: table, ID: w.ID, Doc: r.redact(ctx, w.Doc)})
			if r.w.IsAgent() {
				if p, ok := r.ownerProjection(); ok {
					client = append(client, p)
				}
			}
		default:
			client = append(client, w)
		}
	}
	var out []signals.Companion
	if len(internal) > 0 {
		out = append(out, &stagedLiveRows{spot: liveSpot{ref: r.store.ref, folder: r.store.folder, clk: r.w.Clock, actor: r.w.ActorID, wrote: r.store.wrote}, rows: internal})
	}
	// Client-side rows of a core-owned table (tasks, comments, actors…)
	// go through the core after the signals push, as liveRows.Stage does:
	// a live-ref-only copy is never exported to a row file.
	var clientCore []coord.RowWrite
	clientLive := client[:0:0]
	for _, w := range client {
		if coreRowTable(coreTableFor(w)) {
			clientCore = append(clientCore, w)
		} else {
			clientLive = append(clientLive, w)
		}
	}
	client = clientLive
	if len(clientCore) > 0 {
		out = append(out, &coreCompanion{rows: clientCore})
	}
	if len(client) > 0 {
		sp := r.inner.spot()
		if r.internalNote() && r.w.IsAgent() {
			// The client copy's hlc names the writer: the agent's owning
			// person (the org identity of this machine), never the agent.
			sp.actor = r.clientActor()
		}
		out = append(out, &remoteStagedRows{stagedLiveRows{spot: sp, rows: client}})
	}
	return out, nil
}

// projectedKeyPrefix + <actor id> is the stable_key of an actor projection
// in a client repo: the column is required and unique, and the projection
// must not reveal the person's real key (F18: {id, display name, kind}).
const projectedKeyPrefix = "projected:"

// ownerProjection is the projection of the person an agent works for (the
// org identity of this machine) into the client repo, so the client copy
// names the owner, never the agent (Q11).
func (r *clientRows) ownerProjection() (coord.RowWrite, bool) {
	if r.w.Org == nil || r.w.Org.Identity.Key == "" {
		return coord.RowWrite{}, false
	}
	id, err := ids.Deterministic(ids.PrefixActor, r.w.Org.Identity.Key)
	if err != nil {
		return coord.RowWrite{}, false
	}
	name := r.w.Org.Identity.Name
	if name == "" {
		name = r.w.Org.Name
	}
	now := r.w.Clock.Now()
	d := coord.Doc{"_table": tableActors, "id": id, "kind": "human", "display_name": name, "stable_key": projectedKeyPrefix + id, "active": true}
	d["_v"] = json.Number(strconv.Itoa(lsync.FormatVersion))
	d.SetTime("created_at", now)
	d.SetTime("updated_at", now)
	d.SetTime("last_seen_at", now)
	return coord.RowWrite{Table: tableActors, ID: id, Doc: d}, true
}

// agentProjection is the agent's actor row for the client repo when the org
// shares agent details (agent_details: client): the projection {id, display
// name, kind} plus its label, so the client sees who did the work (§7.6).
func (r *clientRows) agentProjection() (coord.RowWrite, bool) {
	if !r.w.IsAgent() {
		return coord.RowWrite{}, false
	}
	label := strings.TrimPrefix(r.w.Ident.StableKey, "agent:")
	now := r.w.Clock.Now()
	d := coord.Doc{"_table": tableActors, "id": r.w.ActorID, "kind": "agent", "display_name": label, "label": label,
		"stable_key": r.w.Ident.StableKey, "active": true}
	d["_v"] = json.Number(strconv.Itoa(lsync.FormatVersion))
	d.SetTime("created_at", now)
	d.SetTime("updated_at", now)
	d.SetTime("last_seen_at", now)
	return coord.RowWrite{Table: tableActors, ID: r.w.ActorID, Doc: d}, true
}

// clientActor is who the client copy names as writer for an agent's write:
// the org identity of this machine (the agent's owner, who shares its
// HOME), or the agent's roster owner.
func (r *clientRows) clientActor() string {
	if r.w.Org != nil && r.w.Org.Identity.Key != "" {
		if id, err := ids.Deterministic(ids.PrefixActor, r.w.Org.Identity.Key); err == nil {
			return id
		}
	}
	return ownerOf(context.Background(), r.w.ActorID)
}

// internalNote reports whether notes on this client task stay internal.
func (r *clientRows) internalNote() bool { return r.details != lorecfg.AgentDetailsClient }

// redact removes the agent details from the client copy (they live on the
// linked row) and names the agent's owning human (or nobody) as done_by
// (coordinator answer Q11: fail closed).
func (r *clientRows) redact(ctx context.Context, d coord.Doc) coord.Doc {
	owner := ""
	if by := d.Str(keyDoneByDoc); by != "" && r.w.IsAgent() {
		owner = ownerOf(ctx, by)
	}
	return redactClientCopy(d, r.w.IsAgent(), owner)
}

// redactClientCopy is the pure D8 rule for a client task row written for an
// internal org: no attempts (absent; the NOT NULL column takes its default
// on import), no
// claim log, time box or work branch (they live on the linked row), and an
// agent's done_by replaced by its owner ("" = nobody).
func redactClientCopy(d coord.Doc, byAgent bool, owner string) coord.Doc {
	out := d.Clone()
	for _, k := range linkedKeys {
		out[k] = nil
	}
	// attempts is NOT NULL: absent (not null) lets the import use the
	// column default, and the client copy says nothing about it.
	delete(out, keyAttemptsDoc)
	if byAgent && out.Str(keyDoneByDoc) != "" {
		out.SetStr(keyDoneByDoc, owner)
	}
	return out
}

// ownerOf is an agent actor's owning human on the roster ("" = none known).
//
// A roster kept by the core (a 0.1.18 org store, roster_core.go) is read
// through the core; a v0.1.17 org store's roster stays on its lore.db
// mirror, where the old code is still the actors' one writer (lsync
// repoOwned: store mirrors keep actors on the old path).
func ownerOf(ctx context.Context, agentID string) string {
	if cr, err := openCoreRoster(ctx); err != nil {
		return ""
	} else if cr != nil {
		actors, err := cr.app.RosterActors(ctx, cr.target)
		if err != nil {
			return ""
		}
		for _, a := range actors {
			if a.ID == agentID {
				return a.OwnerID
			}
		}
		return ""
	}
	r, err := openRoster(ctx, &commonFlags{})
	if err != nil {
		return ""
	}
	defer func() { _ = r.client.Close() }() // a read: a close error changes nothing
	a, err := r.client.Actor.Get(ctx, agentID)
	if err != nil || a.OwnerActorID == nil {
		return ""
	}
	return *a.OwnerActorID
}
