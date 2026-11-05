package wire

// coord_standalone_world.go — the machine side of team coordination for a
// host without its own coordination machinery (cmd/lore, P3-COORD-3,
// P3-SIGCOORD-2): the git repo and its remote, the lease/agents/live
// settings of ~/.lore/config.yaml, the caller and its session, and the
// row store a fenced write goes through.
//
// Scope: a repo without store links (or an own repo of an org whose task
// rows live in the repo, §7.6). A linked client repo coordinates on the
// store's refs, which needs the store machinery (wire/host); it stays
// E_UNSUPPORTED here.
//
// Flow of one fenced write (claim, release, revoke, next):
//
//	coordWorld ─► coord.Env{Signals: repo's refs/lore/signals, Rows: engineRows}
//	coord.Claim/Release/Override decide on the board + the row
//	   └► engineRows.Stage ─► engineCompanion (a signals.RemoteCompanion)
//	signals push confirmed ─► PushSelf: each row through the engine (scan,
//	                          policy, audit, live routing), the v0.1.17
//	                          ordered fallback window (TEAM-036)

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Machine-state file and coordination defaults (LORE_TEAM_SPEC §1, the
// v0.1.17 lorecfg defaults).
const (
	heldFile               = "held.json" // <git dir>/lore/held.json
	defaultLeasePushRetry  = 5
	defaultMaxClaims       = 1
	defaultMaxAttempts     = 3
	envLiveAutopushSwitch  = "LORE_LIVE_AUTOPUSH" // "0" = coordination writes fail closed (rule 11)
	envValueOff            = "0"
	humanSessionSeparator  = "@"
	signatureInvalidDomain = "@lore.invalid"
	fallbackDefaultBranch  = "main"
)

// coordWorld is one command's view of the machine for coordination.
type coordWorld struct {
	a          *App
	repo       *gitref.Repo
	stateDir   string
	remoteName string
	remote     gitremote.Remote
	autopush   bool
	lease      signals.Lease
	agents     coord.Agents
	folder     string
	who        identity.Resolved
	actorID    string
	// sit is the org resolution of this checkout (links, role, org).
	sit orgresolve.Result
	// spot is where claims go: this repo's own signals ref, or the linked
	// store's (useStore, D8).
	spot sigSpot
}

// sigSpot is one signals ref: the git repo holding it, its remote, the
// ref name, the observation file and the project folder on it.
type sigSpot struct {
	repo       *gitref.Repo
	remote     gitremote.Remote
	remoteName string
	ref        string
	seen       string
	folder     string
	// store: the linked store's ref (the task row is not in this repo).
	store bool
}

// openWorld gathers the world, refusing what this host cannot coordinate:
// no git repo, a caller that is neither agent nor person, a linked repo.
func (h standaloneCoord) openWorld(ctx context.Context) (*coordWorld, error) {
	a := h.a
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return nil, errs.New(errs.Unsupported, "coordination needs a git repository").
			WithHint("claims and holds live on refs/lore/signals of the repo's remote")
	}
	repo, err := gitref.Open(ctx, top, nil)
	if err != nil {
		return nil, errs.Wrap(errs.Unsupported, "coordination needs a git repository", err)
	}
	gitDir, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "locate the git dir", err)
	}
	who, err := a.Who(ctx)
	if err != nil {
		return nil, err
	}
	if who.Kind != identity.KindAgent && who.Kind != identity.KindHuman {
		return nil, errs.New(errs.IdentityFailed, "coordination needs an agent actor or a person on the roster").
			WithHint("start an agent session with `lore agent start --label <name>`, or set your org identity")
	}
	actor, err := a.Actor(ctx)
	if err != nil {
		return nil, err
	}
	sit, err := StandaloneOrgResolution(a.opts.Home, a.projectDir()).Situation(ctx)
	if err != nil {
		return nil, err
	}
	if sit.Result.LinkUnknown {
		return nil, errs.New(errs.LinkUnknown, "cannot tell whether this repo is a client repo; refusing to write coordination data").
			WithHint("`lore whoami` shows the link status; fetch the store and retry")
	}
	w := &coordWorld{
		a: a, repo: repo, stateDir: filepath.Join(strings.TrimSpace(string(gitDir)), signalsStateDir),
		folder: signalsRootFolder, who: who, actorID: actor.String(), sit: sit.Result,
	}
	if rel, rerr := filepath.Rel(top, a.projectDir()); rerr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		w.folder = filepath.ToSlash(rel)
	}
	w.settings(sit.Config, sit.Result.Org)
	if _, rerr := repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "remote." + w.remoteName + ".url"}}); rerr == nil {
		w.remote = &gitremote.Git{Repo: repo, Name: w.remoteName}
	}
	w.spot = w.repoSpot()
	return w, nil
}

// openWorldFor is openWorld pointed at where task id is coordinated
// (forTask; "" = the work queue).
func (h standaloneCoord) openWorldFor(ctx context.Context, id string) (*coordWorld, error) {
	w, err := h.openWorld(ctx)
	if err != nil {
		return nil, err
	}
	if err = w.forTask(ctx, id); err != nil {
		return nil, err
	}
	return w, nil
}

// forHeld points the world at the ref a claim of this session was taken
// on (its held record), else at the task's own place.
func (w *coordWorld) forHeld(ctx context.Context, id string) error {
	if rec, ok := w.held().Get(w.session(), id); ok && rec.Ref == signals.CodeRepoRef {
		w.spot = w.repoSpot()
		w.folder = rec.Folder
		w.spot.folder = rec.Folder
		return nil
	}
	return w.forTask(ctx, id)
}

// repoSpot is this repo's own signals ref.
func (w *coordWorld) repoSpot() sigSpot {
	return sigSpot{repo: w.repo, remote: w.remote, remoteName: w.remoteName, ref: signals.CodeRepoRef,
		seen: filepath.Join(w.stateDir, signalsSeenFile), folder: w.folder}
}

// remoteRepoName is this checkout's repo name in a group: the last path
// segment of its remote (github.com/globex/backend → backend), v0.1.17
// hereRepoName.
func (w *coordWorld) remoteRepoName(ctx context.Context) string {
	out, err := w.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "remote." + w.remoteName + ".url"}})
	if err != nil {
		return ""
	}
	u := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(string(out)), "/"), ".git")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	return u
}

// linked reports a repo with store links (a group or client repo).
func (w *coordWorld) linked() bool {
	return len(w.sit.Links) > 0 || w.sit.Role == orgresolve.RoleClient
}

// forTask points the world at where task id is coordinated (D8, §7.5,
// v0.1.17 openCoordTarget):
//   - a repo without store links: its own signals ref;
//   - a linked repo's own row (in the repo store): an own repo keeps it
//     on its own ref (§7.6); a client repo's own task keeps its claim on
//     the store with the agent details internal, not built here;
//   - any other id, or "" (task next): the linked store's signals ref.
func (w *coordWorld) forTask(ctx context.Context, id string) error {
	if !w.linked() {
		return nil
	}
	if id != "" {
		if src, _, ok, err := w.a.locateRow(ctx, tableFor(id), id); err != nil {
			return err
		} else if ok && src == store.SourceRepo {
			if w.sit.Role == orgresolve.RoleClient {
				return errs.New(errs.Unsupported, "a client repo's own task keeps its claim on the store with agent details internal, which this binary does not do yet").
					WithHint("run it with the release binary")
			}
			return nil
		}
	}
	return w.useStore(ctx)
}

// forRepoOnly refuses a client repo for a verb that only has the repo's
// own ref (hold claim/release): its coordination belongs on the store (D8).
func (w *coordWorld) forRepoOnly() error {
	if w.sit.Role == orgresolve.RoleClient {
		return errs.New(errs.Unsupported, "this is a client repo: its holds belong on the linked store's signals ref, which this binary does not reach for holds").
			WithHint("never written into the client repo (D8); run it with the release binary")
	}
	return nil
}

// useStore points the world at the linked store's signals ref: the store
// clone the core attached for the resolved org entry, its branch's
// refs/lore/signals/<branch>, the link folder.
func (w *coordWorld) useStore(ctx context.Context) error {
	if w.sit.Org == nil {
		return errs.New(errs.Unsupported, "this repo is linked but no org entry resolved; its claims live on the store's signals ref").
			WithHint("`lore whoami` shows the org resolution")
	}
	dir, branch, folder, ok := w.a.storeClone(ctx, store.Source(w.sit.Org.Name))
	if !ok {
		return errs.New(errs.Unsupported, "the linked store of "+w.sit.Org.Name+" is not a git clone here: its signals ref cannot be reached").
			WithHint("`lore stores show` lists the attached stores")
	}
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		return errs.Wrap(errs.Internal, "open the store clone", err)
	}
	if folder == "" {
		folder = signalsRootFolder
	}
	w.spot = sigSpot{repo: repo, remote: &gitremote.Git{Repo: repo, Name: gitbackend.RemoteName}, remoteName: gitbackend.RemoteName,
		ref: signals.StoreRef(branch), seen: filepath.Join(filepath.Dir(dir), signalsStateDir, signalsSeenFile), folder: folder, store: true}
	w.folder = folder
	return nil
}

// storeClone is the local clone of an attached git store: its worktree,
// branch and the project folder attached ("" for the root); ok=false for a
// store that is not a git clone (a folder store) or not attached.
func (a *App) storeClone(ctx context.Context, name store.Source) (dir, branch, folder string, ok bool) {
	if _, err := a.state(ctx); err != nil {
		return "", "", "", false
	}
	a.mu.Lock()
	var b store.Backend
	if a.st != nil {
		b = a.st.byName[name]
		folder = strings.Trim(a.st.folders[name], "/")
	}
	a.mu.Unlock()
	if b == nil {
		return "", "", "", false
	}
	g, isGit := concrete(b).(*gitbackend.Backend)
	if !isGit {
		return "", "", "", false
	}
	branch = g.Branch()
	if branch == "" {
		branch = fallbackDefaultBranch
	}
	if folder == "." {
		folder = ""
	}
	return g.Dir(), branch, folder, true
}

// settings applies lease, agents and live.autopush/remote: the defaults,
// then the top level of config.yaml, then the org entry (v0.1.17
// lorecfg LeaseFor/AgentsFor/LiveFor), then LORE_LIVE_REMOTE and
// LORE_LIVE_AUTOPUSH.
func (w *coordWorld) settings(cfg *orgconf.Config, org *orgconf.Org) {
	w.lease = signals.Lease{TTL: defaultLeaseTTL, RenewEvery: defaultLeaseRenewEvery, Grace: defaultLeaseGrace, PushRetries: defaultLeasePushRetry}
	w.agents = coord.Agents{MaxClaims: defaultMaxClaims, MaxAttempts: defaultMaxAttempts}
	w.autopush, w.remoteName = true, defaultRemote
	if cfg != nil {
		leases := []*orgconf.Lease{&cfg.Lease}
		agents := []*orgconf.Agents{&cfg.Agents}
		lives := []*orgconf.Live{&cfg.Live}
		if org != nil {
			leases = append(leases, org.Lease)
			agents = append(agents, org.Agents)
			lives = append(lives, org.Live)
		}
		for _, l := range leases {
			w.applyLease(l)
		}
		for _, ag := range agents {
			if ag == nil {
				continue
			}
			if ag.MaxClaimsPerAgent != nil {
				w.agents.MaxClaims = *ag.MaxClaimsPerAgent
			}
			if ag.MaxAttempts != nil {
				w.agents.MaxAttempts = *ag.MaxAttempts
			}
		}
		for _, l := range lives {
			if l == nil {
				continue
			}
			if l.Autopush != nil {
				w.autopush = *l.Autopush
			}
			if l.Remote != "" {
				w.remoteName = l.Remote
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv(envLiveRemote)); v != "" {
		w.remoteName = v
	}
	if os.Getenv(envLiveAutopushSwitch) == envValueOff {
		w.autopush = false
	}
}

// applyLease overlays one `lease:` block (nil = none).
func (w *coordWorld) applyLease(l *orgconf.Lease) {
	if l == nil {
		return
	}
	if l.TTL != nil {
		w.lease.TTL = l.TTL.D()
	}
	if l.RenewEvery != nil {
		w.lease.RenewEvery = l.RenewEvery.D()
	}
	if l.Grace != nil {
		w.lease.Grace = l.Grace.D()
	}
	if l.PushRetries != nil {
		w.lease.PushRetries = *l.PushRetries
	}
}

// isAgent reports an agent caller.
func (w *coordWorld) isAgent() bool { return w.who.Kind == identity.KindAgent }

// session is LORE_SESSION for an agent; for a person `<stable_key>@<host>`
// (one person at one machine is one holder; v0.1.17 humanSession).
func (w *coordWorld) session() string {
	if w.isAgent() {
		if v := strings.TrimSpace(os.Getenv(identity.EnvSession)); v != "" {
			return v
		}
	}
	host, _ := os.Hostname() // no host name: the key alone still names the person
	return w.who.StableKey + humanSessionSeparator + host
}

// caller is the signals caller (v0.1.17 coordWorld.Caller).
func (w *coordWorld) caller() signals.Caller {
	host, _ := os.Hostname() // display only
	label := w.who.StableKey
	if !w.isAgent() {
		label = w.who.DisplayName
		if w.who.Handle != "" {
			label = rosterHandleMark + w.who.Handle
		}
	}
	return signals.Caller{ActorID: w.actorID, Label: label, Session: w.session(), Host: host}
}

// held is this checkout's held-claims record.
func (w *coordWorld) held() coord.HeldStore {
	return coord.HeldStore{Path: filepath.Join(w.stateDir, heldFile)}
}

// signalsStore is this repo's own signals ref (§1 "Refs").
func (w *coordWorld) signalsStore() *signals.Store {
	name := w.who.DisplayName
	email := signatureEmail(w.who.StableKey)
	sp := w.spot
	return &signals.Store{
		Repo: sp.repo, Remote: sp.remote, RemoteName: sp.remoteName, Ref: sp.ref,
		Seen:  signals.SeenStore{Path: sp.seen},
		Clock: w.a, Lease: w.lease,
		Signature: func(at time.Time) gitref.Signature {
			return gitref.Signature{Name: name, Email: email, When: at}
		},
		Autopush: w.autopush && sp.remote != nil, Tokens: signals.CryptoTokens{}, Jitter: signals.CryptoJitter{},
		// Gate (FORMAT_EVOLUTION rule 2, E_OLD_LORE) is left nil: the
		// engine's own format floor refuses the row half of every fenced
		// write in a project this binary may not write.
	}
}

// signatureEmail turns a stable key into a commit e-mail; agents get a
// non-routable address (v0.1.17 signatureEmail).
func signatureEmail(stableKey string) string {
	if v, ok := strings.CutPrefix(stableKey, identity.KindHuman+":"); ok && strings.Contains(v, "@") {
		return v
	}
	return strings.ReplaceAll(stableKey, ":", ".") + signatureInvalidDomain
}

// env is the coord.Env of this repo.
func (w *coordWorld) env(ctx context.Context) coord.Env {
	e := coord.Env{Signals: w.signalsStore(), Folder: w.folder, Rows: engineRows{a: w.a}, Caller: w.caller(), Agents: w.agents, Held: w.held()}
	if set, err := w.a.Policy(ctx, store.SourceRepo); err == nil {
		// OV15B-URG, OV7-6: the repo's urgency weights and claim-span keys;
		// a policy that cannot be read leaves the built-in defaults.
		if uw, uerr := opstask.UrgencyWeights(set); uerr == nil {
			e.Urgency = uw
		}
		e.Time = claimSpans(set)
	}
	return e
}

// Built-in time.long_span / time.coalesce_gap (TQ2 10h, TQ4 2m).
const (
	claimLongSpan    = 10 * time.Hour
	claimCoalesceGap = 2 * time.Minute
)

// claimSpans are time.long_span / time.coalesce_gap for the claim spans
// the coordination writer closes (OV7-6, FA-LTASK-3).
func claimSpans(set policy.Set) coord.TimeSpans {
	out := coord.TimeSpans{LongSpan: claimLongSpan, CoalesceGap: claimCoalesceGap, Read: true}
	if n, ok := set.Limit(policy.KeyTimeLongSpan); ok {
		out.LongSpan = time.Duration(n) * time.Minute
	}
	if n, ok := set.Limit(policy.KeyTimeCoalesceGap); ok {
		out.CoalesceGap = time.Duration(n) * time.Minute
	}
	return out
}

// workBranch is the checked-out branch when it is a work branch (not
// detached, not the default branch), else "".
func (w *coordWorld) workBranch(ctx context.Context) string {
	out, err := w.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(string(out))
	if b == "" || b == w.defaultBranch(ctx) {
		return ""
	}
	return b
}

// defaultBranch reads <remote>/HEAD (fixed rule 1), falling back to main.
func (w *coordWorld) defaultBranch(ctx context.Context) string {
	if o, err := w.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "refs/remotes/" + w.remoteName + "/HEAD"}}); err == nil {
		if d := strings.TrimPrefix(strings.TrimSpace(string(o)), w.remoteName+"/"); d != "" {
			return d
		}
	}
	return fallbackDefaultBranch
}

// requireHuman refuses agents for human overrides (§7.4).
func (w *coordWorld) requireHuman(what string) error {
	if w.who.Kind == identity.KindHuman {
		return nil
	}
	return errs.New(errs.InvalidInput, what+" is a human override; an agent actor may not do it").
		WithHint("run it as yourself (the identity in ~/.lore/config.yaml), not from an agent session")
}

// ---- the row store: the engine ----

// engineRows is coord.Rows over the engine: reads see what the engine
// sees (the live overlay included, after the command's fetch), writes go
// through the engine after the signals push.
type engineRows struct{ a *App }

var _ coord.Rows = engineRows{}

// tableFor picks the table of a coordinated id (tasks or missions).
func tableFor(id string) row.Table {
	if coord.IsMission(id) {
		return schema.TableMissions
	}
	return schema.TableTasks
}

// Get implements coord.Rows.
func (r engineRows) Get(ctx context.Context, id string) (coord.Doc, bool, error) {
	_, rw, ok, err := r.a.locateRow(ctx, tableFor(id), id)
	if err != nil || !ok {
		return nil, false, err
	}
	d, err := docOfRow(rw)
	if err != nil {
		return nil, false, err
	}
	return d, true, nil
}

// List returns every live task row of every attached store (task next).
func (r engineRows) List(ctx context.Context) ([]coord.Doc, error) {
	var out []coord.Doc
	for _, src := range r.a.Stores() {
		rows, err := r.a.List(ctx, src, schema.TableTasks)
		if err != nil {
			return nil, err
		}
		for _, rw := range rows {
			if rw.Deleted {
				continue
			}
			d, err := docOfRow(rw)
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
	}
	return out, nil
}

// RepoName maps a repo id to its mount name (what --repo matches).
func (r engineRows) RepoName(repoID string) string {
	id, err := ids.ParseAny(repoID)
	if err != nil {
		return repoID
	}
	_, rw, gerr := r.a.Get(context.Background(), engine.Ref{Table: schema.TableRepos, ID: id})
	if gerr != nil {
		return repoID
	}
	if t, ok := rw.Fields[fieldMountName].(value.Text); ok && t.String() != "" {
		return t.String()
	}
	return repoID
}

// fieldMountName is the repos table's mount name.
const fieldMountName = "mount_name"

// Stage implements coord.Rows: one companion writing every row through the
// engine once the signals push was confirmed.
func (r engineRows) Stage(_ context.Context, rows ...coord.RowWrite) ([]signals.Companion, error) {
	return []signals.Companion{&engineCompanion{a: r.a, rows: rows}}, nil
}

// locateRow finds one row and its store; an id held by several stores is
// the repo store's copy. ok=false for an unknown id, a non-id or a
// tombstone.
func (a *App) locateRow(ctx context.Context, table row.Table, id string) (store.Source, row.Row, bool, error) {
	aid, err := ids.ParseAny(strings.TrimSpace(id))
	if err != nil {
		return "", row.Row{}, false, nil
	}
	ref := engine.Ref{Table: table, ID: aid}
	src, rw, err := a.Get(ctx, ref)
	if errs.Has(err, errs.AmbiguousID) {
		ref.Store = store.SourceRepo
		src, rw, err = a.Get(ctx, ref)
	}
	if errs.Has(err, errs.NotFound) || (err == nil && rw.Deleted) {
		return "", row.Row{}, false, nil
	}
	if err != nil {
		return "", row.Row{}, false, err
	}
	return src, rw, true, nil
}

// engineCompanion writes staged coordination rows through the engine after
// the signals push (v0.1.17 coreCompanion).
type engineCompanion struct {
	a    *App
	rows []coord.RowWrite
}

var _ signals.RemoteCompanion = (*engineCompanion)(nil)

// Prepare checks every row's table before anything is pushed.
func (c *engineCompanion) Prepare(ctx context.Context) (gitremote.RefUpdate, error) {
	for _, w := range c.rows {
		if _, ok := c.a.TableSchema(ctx, rowTable(w)); !ok {
			return gitremote.RefUpdate{}, fmt.Errorf("coordination rows: unknown table %q", rowTable(w))
		}
	}
	return gitremote.RefUpdate{}, nil
}

// PushSelf writes every row: an update of the fields that changed, or a
// create with the row's id when the engine has none yet (a note).
func (c *engineCompanion) PushSelf(ctx context.Context) error {
	for _, w := range c.rows {
		if err := c.a.writeDoc(ctx, rowTable(w), w.ID, w.Doc); err != nil {
			return err
		}
	}
	return nil
}

// Finalize has nothing left: PushSelf wrote the rows.
func (c *engineCompanion) Finalize(context.Context) error { return nil }

// rowTable is a staged row's table ("" = the id's kind).
func rowTable(w coord.RowWrite) row.Table {
	if w.Table != "" {
		return row.Table(w.Table)
	}
	return tableFor(w.ID)
}

// Envelope keys of a v0.1.17 row document (not row fields).
const (
	docKeyID        = "id"
	docKeyCreatedAt = "created_at"
	docKeyUpdatedAt = "updated_at"
	docKeyAuthor    = "created_by_actor_id"
	docTimeLayout   = "2006-01-02T15:04:05.000000000Z"
)

// docOfRow is the v0.1.17 document of a row: its fields as canonical JSON
// plus id, created_at, updated_at from the envelope, and the author column
// from the envelope when the row has none.
func docOfRow(r row.Row) (coord.Doc, error) {
	d := coord.Doc{}
	for k, v := range r.Fields {
		b, err := v.MarshalJSON()
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "encode "+r.ID.String()+"."+k, err)
		}
		cv, err := canonjson.Raw(b)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "encode "+r.ID.String()+"."+k, err)
		}
		d[k] = cv
	}
	d[docKeyID] = canonjson.String(r.ID.String())
	d[docKeyCreatedAt] = canonjson.String(r.CreatedAt.UTC().Format(docTimeLayout))
	d[docKeyUpdatedAt] = canonjson.String(r.UpdatedAt.UTC().Format(docTimeLayout))
	if d.Str(docKeyAuthor) == "" && !r.CreatedBy.IsZero() {
		d[docKeyAuthor] = canonjson.String(r.CreatedBy.String())
	}
	return d, nil
}

// writeDoc writes doc as the next version of table/id: every field of the
// table the document names or the current row has (a field the document
// leaves out is cleared: a whole staged row); a new row is created in the
// repo store under its id.
func (a *App) writeDoc(ctx context.Context, table row.Table, id string, doc coord.Doc) error {
	def, ok := a.TableSchema(ctx, table)
	if !ok {
		return errs.New(errs.Internal, "coordination rows: unknown table "+string(table))
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		return errs.Wrap(errs.InvalidID, id+" is not an id", err)
	}
	src, cur, found, err := a.locateRow(ctx, table, id)
	if err != nil {
		return err
	}
	var old coord.Doc
	if found {
		if old, err = docOfRow(cur); err != nil {
			return err
		}
	}
	patch := map[string]value.Value{}
	for _, f := range def.Fields {
		nv, inNew := doc[f.Name]
		ov, inOld := old[f.Name]
		switch {
		case !inNew && !inOld:
			continue
		case inNew && inOld && canonjson.Equal(nv, ov):
			continue
		case !inNew || nv.IsNull():
			if found && inOld && !ov.IsNull() {
				patch[f.Name] = value.Null{}
			}
			continue
		}
		v, perr := docValue(f, nv)
		if perr != nil {
			return errs.Wrap(errs.InvalidInput, "coordination rows: "+string(table)+"."+f.Name, perr)
		}
		patch[f.Name] = v
	}
	if !found {
		// A new row (a note) names its author only when that actor has a
		// row; else the envelope's created_by records the writer.
		if ref, isRef := patch[docKeyAuthor].(value.Ref); isRef {
			if _, _, known, gerr := a.locateRow(ctx, schema.TableActors, ref.ID().String()); gerr != nil || !known {
				delete(patch, docKeyAuthor)
			}
		}
		_, err = a.CreateWithID(ctx, store.SourceRepo, table, aid, patch)
		return err
	}
	if len(patch) == 0 {
		return nil
	}
	_, err = a.Update(ctx, engine.Ref{Table: table, ID: aid, Store: src}, patch)
	return err
}

// docValue types one document value by its field definition.
func docValue(f schema.FieldDef, v canonjson.Value) (value.Value, error) {
	s, err := canonjson.CompactValue(v)
	if err != nil {
		return nil, err
	}
	if f.Kind == value.KindList {
		return value.ParseList(f.Elem, []byte(s))
	}
	return value.Parse(f.Kind, []byte(s))
}

// ---- errors ----

// coordErr maps coordination failures to the spec's codes (§1 "Error
// codes", v0.1.17 coordError); anything else passes through.
func coordErr(err error) error {
	if err == nil {
		return nil
	}
	var e *errs.Error
	if errors.As(err, &e) {
		return err
	}
	var fe *coord.FencedError
	var he *signals.HeldError
	var we *signals.WIPError
	var nw *coord.NoWorkError
	var pe *coord.PoisonedError
	var off *signals.OfflineError
	var hoc *coord.HasOpenChildrenError
	switch {
	case errors.As(err, &hoc):
		return errs.New(errs.HasOpenChildren, hoc.Error()).WithHint("lore task claim " + hoc.Children[0] + "   (or: lore task claim " + hoc.Task + " --coordinate)")
	case errors.As(err, &fe):
		code := errs.LostClaim // an unknown kind fails closed as a lost claim
		switch fe.Kind {
		case coord.FenceCancelled:
			code = errs.Cancelled
		case coord.FenceReassigned:
			code = errs.Reassigned
		case coord.FenceLost:
			code = errs.LostClaim
		}
		return errs.New(code, fe.Error()).WithHint("stop work on this task; `lore task next` finds other work")
	case errors.As(err, &off):
		return errs.New(errs.Offline, off.Error()).
			WithHint("coordination writes are never queued; reconnect (or turn live.autopush on) and run the command again")
	case errors.As(err, &he):
		msg := he.Error()
		if coord.IsMission(he.By.Task) && !he.SameActor {
			msg = fmt.Sprintf("%s is coordinated by %s (until %s)", he.By.Task, holderName(he.By), he.By.Expires.UTC().Format(time.Kitchen))
		}
		if he.SameActor {
			return errs.New(errs.Refused, msg) // P2-TASK-3: waiting will not help
		}
		return errs.New(errs.Contended, msg).WithHint("retry after the hold ends, or pick other work with `lore task next`")
	case errors.As(err, &we):
		return errs.New(errs.InvalidInput, we.Error())
	case errors.As(err, &nw):
		return errs.New(errs.NoWork, nw.Error())
	case errors.Is(err, coord.ErrContended):
		return errs.New(errs.Contended, "ready work exists but every claim attempt lost a race").WithHint("retry after a short wait")
	case errors.As(err, &pe):
		return errs.New(errs.InvalidInput, pe.Error())
	case errors.Is(err, coord.ErrUnknownTask):
		return errs.New(errs.NotFound, err.Error()).WithHint("`lore sync peek` shows rows not fetched yet")
	case errors.Is(err, coord.ErrNotOpen):
		return errs.New(errs.InvalidInput, err.Error())
	case errors.Is(err, signals.ErrNotHeld):
		return errs.New(errs.LostClaim, err.Error())
	case errors.Is(err, signals.ErrBadName):
		return errs.New(errs.InvalidInput, err.Error())
	}
	return err
}

// markOK is a coordination outcome line's success mark (v0.1.17
// style.Success without colour: the op's text is plain).
const markOK = "✓"
