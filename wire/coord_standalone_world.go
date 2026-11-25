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
	"io"
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
	"github.com/thesatellite-ai/lore/model/fspath"
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
	heldFile              = "held.json" // <git dir>/lore/held.json
	defaultLeasePushRetry = 5
	defaultMaxClaims      = 1
	defaultMaxAttempts    = 3
	envLiveAutopushSwitch = "LORE_LIVE_AUTOPUSH" // "0" = coordination writes fail closed (rule 11)
	envValueOff           = "0"
	humanSessionSeparator = "@"
	fallbackDefaultBranch = "main"
)

// coordWorld is one command's view of the machine for coordination.
type coordWorld struct {
	a          *App
	repo       *gitref.Repo
	stateDir   string
	remoteName string
	remote     gitremote.Remote
	autopush   bool
	// autopushChosen: live.autopush is set to true in config.yaml (the top
	// level or the org entry), the person's standing choice to push at
	// once, which is also the go-ahead for claims (requirePushAsked).
	autopushChosen bool
	lease          signals.Lease
	agents         coord.Agents
	// maxClaimsConfig is agents.max_claims_per_agent as config.yaml (top
	// level, then the org entry) sets it; 0 = unset. It may only lower the
	// team policy's cap (devco:S-20, env).
	maxClaimsConfig int
	// commonDir is the repository's shared git directory when it has no
	// remote: its signals ref is written there (devco:D-362).
	commonDir string
	folder    string
	who       identity.Resolved
	actorID   string
	// sit is the org resolution of this checkout (links, role, org).
	sit orgresolve.Result
	// orgStore is the attached name of the org store at this repo's linked
	// folder: the bare org name, or "<org>/<folder>" when the repo's
	// stores.yaml already attaches the bare name at the store root
	// (renameOrgClashes). "" with no org. Every write and claim of a
	// client task on the linked store goes here (D8), never to the root.
	orgStore store.Source
	// spot is where claims go: this repo's own signals ref, or the linked
	// store's (useStore, D8).
	spot sigSpot
	// client: the task is a client repo's own task coordinated on the
	// linked store (forTask); its rows are clientRows.
	client bool
	// mergeCheck is merge_check.adapter (F9 check 3): the top level of
	// config.yaml, then the org entry; "" = none.
	mergeCheck orgconf.MergeCheckAdapter
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
	// label is the store folder as every line names it, <org>/<folder>
	// (storeOwnName); "" for a code repo's own ref (devco:D-560).
	label string
	// url is the store clone's remote url ("" for a code repo's own ref):
	// it picks the identity that signs the store's signals commits
	// (devco:D-581, storeSigner.identityFor).
	url string
}

// openWorld gathers the world, refusing what this host cannot coordinate:
// no git repo, a caller that is neither agent nor person, a linked repo.
func (h standaloneCoord) openWorld(ctx context.Context) (*coordWorld, error) {
	a := h.a
	if sc, ok := storeHere(a.opts.RepoDir); ok {
		return nil, errCoordInStore(sc)
	}
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return nil, errCoordNoRepo(a.opts.RepoDir)
	}
	repo, err := gitref.Open(ctx, top, nil)
	if err != nil {
		return nil, errs.Wrap(errs.Unsupported, "coordination needs a git repository", errCoordNoRepo(a.opts.RepoDir))
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
	res := StandaloneOrgResolution(a.opts.Home, a.projectDir())
	res.Now = a.opts.Wall // the clone's fetch stamp is on the app clock (LORE_NOW in a test build), as every other writer of it
	sit, err := res.Situation(ctx)
	if err != nil {
		return nil, err
	}
	if sit.Result.LinkUnknown {
		msg := "cannot tell whether this repo is a client repo; refusing to write coordination data"
		if d := sit.Result.LinkDetail; d != "" {
			msg += " (" + d + ")" // names the store that was not read (B1-22)
		}
		return nil, errs.New(errs.LinkUnknown, msg).
			WithHint(linkUnknownHint).WithNext("lore", "sync")
	}
	if who.Kind == identity.KindAgent && who.Owner == nil && sit.Result.Org != nil {
		// An agent actor (LORE_ACTOR) must be on the roster; a detected
		// coding agent acts for its person (owner rule 2026-11-11), who
		// is not checked here either.
		if err = a.requireRosterAgent(ctx, who.StableKey); err != nil {
			return nil, err
		}
	}
	w := &coordWorld{
		a: a, repo: repo, stateDir: filepath.Join(strings.TrimSpace(string(gitDir)), signalsStateDir),
		folder: signalsRootFolder, who: who, actorID: actor.String(), sit: sit.Result,
	}
	if rel, inside := fspath.Rel(top, a.projectDir()); inside && rel != "." {
		w.folder = filepath.ToSlash(rel)
	}
	if sit.Result.Org != nil {
		w.orgStore = store.Source(sit.Result.Org.Name)
		if linked, ok := linkedFolderName(ctx, a.opts, top, sit.Result.Org.Name); ok {
			w.orgStore = linked
		}
	}
	w.settings(sit.Config, sit.Result.Org)
	if _, rerr := repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "remote." + w.remoteName + ".url"}}); rerr == nil {
		w.remote = &gitremote.Git{Repo: repo, Name: w.remoteName}
	} else if out, cerr := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}}); cerr == nil {
		w.commonDir = strings.TrimSpace(string(out)) // devco:D-362: the signals ref of a repo with no remote
	}
	w.spot = w.repoSpot()
	return w, nil
}

// openWorldFor is openWorld pointed at where task id is coordinated
// (forTask; "" = the work queue).
func (h standaloneCoord) openWorldFor(ctx context.Context, id string) (*coordWorld, error) {
	if sw, ok, serr := h.openStoreWorld(ctx, id); ok {
		return sw, serr // a store task outside every code repo (devco:D-635)
	}
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

// requireRosterAgent refuses an agent the org roster does not list (F18,
// §7.4: v0.1.17's identity chain failed with ErrAgentNotOnRoster); a
// roster that cannot be read refuses too (fail closed). A miss on an org
// roster re-reads it once after syncing its store (rosterFind)
// (BUG-T6, v0.1.17 stores.RosterCheck): an agent its owner started a
// moment ago on another machine is on the remote roster but not yet in a
// copy fetched within live.fetch_ttl. A hit costs no extra fetch; a sync
// that fails leaves the local copy's answer (the refusal) standing. The
// refusal names the roster as every roster sentence does, "the roster of
// acme (acme/org)" (devco D-668: "is not on acme's roster" named the org
// only).
func (a *App) requireRosterAgent(ctx context.Context, stableKey string) error {
	r, err := a.coordRoster(ctx)
	if err != nil {
		return err
	}
	_, ok, err := a.rosterFind(ctx, r, func(x RosterActor) bool { return x.StableKey == stableKey })
	if err != nil || ok {
		return err
	}
	return errs.New(errs.IdentityFailed, identity.EnvActor+"="+stableKey+" is not on "+r.of()+" (register it with `lore agent start --label <label>`)").
		WithHint("its owner runs `lore agent start --label <l>` first")
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
//     the store, its rows split by clientRows (agent details internal);
//   - any other id, or "" (task next): the linked store's signals ref.
func (w *coordWorld) forTask(ctx context.Context, id string) error {
	if !w.linked() {
		return nil
	}
	if id != "" {
		if src, _, ok, err := w.a.locateRow(ctx, tableFor(id), id); err != nil {
			return err
		} else if ok && src == store.SourceRepo {
			if w.sit.Role == orgresolve.RoleClient && len(w.sit.Links) > 0 {
				// The claim on the linked store, the rows split by
				// clientRows (D8). A client repo with no store link (client
				// by repos: pattern only) has no store to keep the claim
				// on: its own signals ref, as v0.1.17 (p3-cmdlore NOTE).
				if err = w.useStore(ctx); err != nil {
					return err
				}
				w.client = true
			}
			return nil
		}
	}
	return w.useStore(ctx)
}

// forHolds points the world at where this checkout's holds live: the
// repo's own signals ref, or, in a client repo linked to an org store, the
// linked store's signals ref under the link folder (D8: coordination data
// is never written into the client repo; the hold path is
// <folder>/resources/<name>.json there, like a client task's claim). A
// client repo with no store link (client by a repos: pattern only) has
// no store to keep the hold on and is refused, writing nothing.
func (w *coordWorld) forHolds(ctx context.Context) error {
	if w.sit.Role != orgresolve.RoleClient {
		return nil
	}
	if w.unlinkedClient() {
		return errs.New(errs.Unsupported, "this is a client repo with no store link: its holds belong on a linked store's signals ref, and none is linked").
			WithHint("never written into the client repo; link it with `" + w.linkCommand(ctx) + "`")
	}
	return w.useStore(ctx)
}

// unlinkedClient reports a client repo no store link names: its holds
// have no signals ref to go on (forHolds).
func (w *coordWorld) unlinkedClient() bool {
	return w.sit.Role == orgresolve.RoleClient && len(w.sit.Links) == 0
}

// linkFolderPlaceholder stands for the store folder in linkCommand when
// the repo has no name to offer.
const linkFolderPlaceholder = "<folder>"

// linkCommand is the command that links this client repo to its org's
// store: the org resolved for it (the only org entry is the default, so
// it is left out when none resolved) and the repo's folder name as the
// store folder (any internal project folder will do; the person may pick
// another).
func (w *coordWorld) linkCommand(ctx context.Context) string {
	cmd := "lore org link"
	if w.sit.Org != nil && w.sit.Org.Name != "" {
		cmd += " --org " + w.sit.Org.Name
	}
	folder := linkFolderPlaceholder
	if top, err := w.a.repoTop(ctx); err == nil && top != "" {
		folder = filepath.Base(top)
	}
	return cmd + " --project " + folder
}

// useStore points the world at the linked store's signals ref: the store
// clone the core attached for the resolved org entry, its branch's
// refs/lore/signals/<branch>, the link folder.
func (w *coordWorld) useStore(ctx context.Context) error {
	if w.sit.Org == nil {
		return errs.New(errs.Unsupported, "this repo is linked but no org entry resolved; its claims live on the store's signals ref").
			WithHint("`lore whoami` shows the org resolution")
	}
	dir, branch, folder, ok := w.a.storeClone(ctx, w.orgStore)
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
	remote := &gitremote.Git{Repo: repo, Name: gitbackend.RemoteName}
	// The store clone's signals ref is reached as the setup that owns the
	// store (storeRoute); a store no setup owns gets no route.
	label, url := string(w.orgStore), ""
	if u, uerr := repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "remote." + gitbackend.RemoteName + ".url"}}); uerr == nil {
		url = strings.TrimSpace(string(u))
		remote.RouteArgs, remote.RouteEnv, _ = storeRoute(ctx, w.a.opts.Home, w.a.projectDir(), url)
		if own := storeOwnName(w.a.opts.Home, url, folder); own != "" {
			label = own
		}
	}
	w.spot = sigSpot{repo: repo, remote: remote, remoteName: gitbackend.RemoteName,
		ref: signals.StoreRef(branch), seen: filepath.Join(gitbackend.StateDirOf(dir), signalsSeenFile), folder: folder, store: true, label: label, url: url}
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

// settings applies lease, agents, merge_check and live.autopush/remote: the defaults,
// then the top level of config.yaml, then the org entry (v0.1.17
// lorecfg LeaseFor/AgentsFor/LiveFor), then LORE_LIVE_REMOTE and
// LORE_LIVE_AUTOPUSH.
func (w *coordWorld) settings(cfg *orgconf.Config, org *orgconf.Org) {
	w.lease = signals.Lease{TTL: defaultLeaseTTL, RenewEvery: defaultLeaseRenewEvery, Grace: defaultLeaseGrace, PushRetries: defaultLeasePushRetry}
	w.agents = coord.Agents{MaxClaims: defaultMaxClaims, MaxAttempts: defaultMaxAttempts}
	w.autopush, w.remoteName = true, defaultRemote
	if cfg != nil {
		w.mergeCheck = cfg.MergeCheck.Adapter
		if org != nil && org.MergeCheck != nil && org.MergeCheck.Adapter != "" {
			w.mergeCheck = org.MergeCheck.Adapter
		}
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
				w.maxClaimsConfig = *ag.MaxClaimsPerAgent
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
				w.autopushChosen = *l.Autopush
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
		w.autopush, w.autopushChosen = false, false
	}
}

// envPushClaims = envPushClaimsOn is the standing go-ahead of a shell or
// an agent's environment to push its claims and holds to a code repo's
// remote, as --push gives it for one command (D-15).
const (
	envPushClaims   = "LORE_PUSH_CLAIMS"
	envPushClaimsOn = "1"
)

// pushAsk is what a claim or hold refused for want of --push prints as
// its next step (devco:D-669): the noun of the refusal ("a claim", "a
// hold"), the exact command that pushes it now (the person's own command
// with every flag they gave, plus --push) and, where there is one, the
// command that does the work without a claim. Why: the refusal printed
// "run the command again with --push" and "`lore task start --id <id>`
// works on a task without a claim" for every refusal, a hold's included:
// a placeholder where the id was known, and a task step in a hold refusal.
type pushAsk struct {
	what    string   // the noun the refusal names: "a claim", "a hold", …
	rerun   []string // the command after "lore", without --push (pushRerun appends it)
	without []string // a command after "lore" that needs no claim; nil when none
}

// claimAsk is the pushAsk of `task claim` (and of anything that names the
// claim of one task): the claim again with --push, or `task start`,
// which marks the task in progress with no claim and no push.
func claimAsk(what string, rerun []string, id string) pushAsk {
	return pushAsk{what: what, rerun: rerun, without: []string{"task", "start", "--id", id}}
}

// pushNeeded reports whether a new claim or hold here must be asked for
// with --push (requirePushAsked): the place has a remote, and neither
// LORE_PUSH_CLAIMS=1 nor live.autopush: true gives a standing go-ahead.
func (w *coordWorld) pushNeeded() bool {
	return !w.autopushChosen && w.spot.remote != nil && os.Getenv(envPushClaims) != envPushClaimsOn
}

// requirePushAsked refuses a new claim or hold (ask names it and its next
// step) that would push refs/lore/signals to this code repo's own remote
// when the person did not ask for that push (asked: the command's --push;
// or LORE_PUSH_CLAIMS=1; or live.autopush: true in config.yaml, the choice
// to push every write at once). Owner rule D-15: nothing reaches a remote without
// an explicit push, sync or git push, and a claim is pushed in the same
// command because it is a lease two people race for: it cannot wait for
// lore sync. A claim on a linked store's signals ref (a client task, a
// linked repo) is not a code remote and is not asked here; a repo with no
// remote fails as before (offline). Renewing, releasing or finishing a
// claim already pushed needs no new go-ahead: it changes what the person
// published.
func (w *coordWorld) requirePushAsked(asked bool, ask pushAsk) error {
	if asked || !w.pushNeeded() {
		return nil
	}
	hint := pushAskHint(ask)
	if w.spot.store {
		// devco:D-580: a linked store's signals ref is a remote too; a claim
		// there was pushed unasked although --push says it is refused.
		return errs.New(errs.Refused, fmt.Sprintf("%s here is shared through the signals ref %s of store %s on the store's remote, and lore pushes nothing to a remote unless you ask; nothing was written", ask.what, w.spot.ref, w.spot.label)).
			WithHint(hint).WithNext(pushRerun(ask.rerun)[0], pushRerun(ask.rerun)[1:]...)
	}
	return errs.New(errs.Refused, fmt.Sprintf("%s here is shared through %s on this repo's remote %s, and lore pushes nothing to a code remote unless you ask; nothing was written", ask.what, signals.CodeRepoRef, w.remoteName)).
		WithHint(hint).WithNext(pushRerun(ask.rerun)[0], pushRerun(ask.rerun)[1:]...)
}

// pushRerun is the argv ("lore", …) of rerun with --push appended.
func pushRerun(rerun []string) []string {
	return append(append([]string{defaultCommandName}, rerun...), "--push")
}

// pushAskHint is the hint of a push refusal: the exact command that pushes
// now, the standing go-ahead, and the claim-free command when there is one.
func pushAskHint(ask pushAsk) string {
	h := "run `" + printedCommand(pushRerun(ask.rerun)) + "` to push it now (LORE_PUSH_CLAIMS=1 in the environment, or live.autopush: true in config.yaml, asks for every claim and hold)"
	if len(ask.without) > 0 {
		h += "; `" + printedCommand(append([]string{defaultCommandName}, ask.without...)) + "` starts the task without a claim and pushes nothing"
	}
	return h
}

// printedCommand is argv as a person pastes it into a POSIX shell: each
// word that a shell would split or expand is single-quoted (shellWord).
func printedCommand(argv []string) string {
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = shellWord(a)
	}
	return strings.Join(words, " ")
}

// taskInRepo reports whether the claimed task row lives in this checkout's
// repo (HeldRecord.InRepo, which lets the commit-msg hook add the
// Lore-Task trailer): a claim on the repo's own signals ref, and a client
// repo's own task whose claim sits on the linked store (D-14: its row is
// in the client repo's history already, so the trailer reveals nothing new
// and a squash merge keeps the link; it was left out because the claim's
// ref is the store's).
func (w *coordWorld) taskInRepo() bool { return !w.spot.store || w.client }

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
// (one person at one machine is one holder; v0.1.17 humanSession). It is
// coordSession, the one rule the commit-msg hook also uses, so a claim and
// the Lore-Task trailer always name the same session.
func (w *coordWorld) session() string { return coordSession(w.who) }

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

// localSignalsRemoteName names the tracking namespace of the signals ref
// of a repo with no remote (refs/lore-remote/local/…), so it never mixes
// with a remote's copy once the repo gets one.
const localSignalsRemoteName = "local"

// localSignalsRemote is the repo's own git directory as the signals
// "remote" when this repo's own ref has no remote (devco:D-362: claims in a
// solo repo failed E_OFFLINE "autopush is off"). The signals ref then
// lives in this repository only; a linked store's ref never takes this.
func localSignalsRemote(sp sigSpot, commonDir string) (gitremote.Remote, bool) {
	if sp.remote != nil || sp.store || sp.repo == nil || commonDir == "" {
		return nil, false
	}
	return &gitremote.Git{Repo: sp.repo, Name: commonDir}, true
}

// signalsStore is this repo's own signals ref (§1 "Refs").
func (w *coordWorld) signalsStore(ctx context.Context) *signals.Store {
	sig := w.signalsSignature(ctx)
	sp := w.spot
	remote, remoteName, autopush := sp.remote, sp.remoteName, w.autopush && sp.remote != nil
	if local, ok := localSignalsRemote(sp, w.commonDir); ok {
		// devco:D-362: a repo with no remote coordinates on its own git
		// directory: the claim is local by nature (nothing to push to),
		// with the same lease, fencing and companions as a pushed one.
		remote, remoteName, autopush = local, localSignalsRemoteName, true
	}
	return &signals.Store{
		Repo: sp.repo, Remote: remote, RemoteName: remoteName, Ref: sp.ref,
		Seen:  signals.SeenStore{Path: sp.seen},
		Clock: w.a, Lease: w.lease,
		Signature: func(at time.Time) gitref.Signature {
			return gitref.Signature{Name: sig.Name, Email: sig.Email, When: at}
		},
		Autopush: autopush, Tokens: signals.CryptoTokens{}, Jitter: signals.CryptoJitter{},
		Trace: signalsTrace(os.Stderr),
		// FORMAT_EVOLUTION rule 2 (OV48): no claim, renew or release on a
		// project this lore may not write. The engine's floor alone
		// refused only the row half, after the signal was already pushed.
		Gate: w.formatGate(sp),
	}
}

// formatGate is the signals.Store.Gate of spot: the format declaration of
// the project the signals belong to decides (the repo's _meta.json, or the
// linked store folder's), the same check as every engine write.
func (w *coordWorld) formatGate(sp sigSpot) func(context.Context) error {
	name := store.SourceRepo
	if sp.store {
		name = w.orgStore
	}
	return func(ctx context.Context) error {
		st, err := w.a.state(ctx)
		if err != nil {
			return err
		}
		return st.eng.FormatGate(ctx, name)
	}
}

// envTrace is the trace switch of CLI_REFERENCE 1.1 (`LORE_TRACE=1`, set
// by cmd/lore for `--trace` too): the signals writes print their fetches,
// pushes and lost-race retries (LORE_TEAM_SPEC "pushes and retries").
const envTrace = "LORE_TRACE"

// envTraceOn is the value of envTrace that turns tracing on.
const envTraceOn = "1"

// signalsTrace is signals.Store.Trace: nil unless LORE_TRACE=1, else each
// line goes to w as "trace: <line>" (v0.1.17 traceFunc). The lines name
// refs and counts only, never row content.
func signalsTrace(w io.Writer) func(string) {
	if os.Getenv(envTrace) != envTraceOn {
		return nil
	}
	return func(line string) { _, _ = fmt.Fprintln(w, "trace: "+line) } // stderr gone: nothing left to report to
}

// signalsSignature is who signs a signals commit (devco:D-581,
// devco:D-983): always a person, never the agent. On a linked store's ref
// the identity every other commit of that store uses
// (storeSigner.identityFor: the org identity for an org store); on a code
// repo's own ref git's identity of the repo (FLAG4: git identity wins, as
// on refs/lore/live), else the person (the owner of a detected agent).
// It was "<person> via <tool>" <agent.auto/…@lore.invalid> when the
// person's key held no address, and that author reached public remotes.
// With no person address anywhere it is git.DefaultAuthor (lore itself),
// never a made-up one. The agent is named nowhere in the commit.
func (w *coordWorld) signalsSignature(ctx context.Context) gitref.Signature {
	if w.spot.store && w.spot.url != "" {
		if s := storeIdentityFunc(w.a.opts).identityFor(ctx, w.spot.url); s.Name != "" && s.Email != "" && !agentSignature(s.Name, s.Email) {
			return gitref.Signature{Name: s.Name, Email: s.Email}
		}
	}
	if w.spot.repo != nil {
		if s, ok := w.spot.repo.GitIdentity(ctx); ok {
			return s
		}
	}
	if name, email := w.who.Person().CommitSignature(); name != "" && email != "" && !agentSignature(name, email) {
		return gitref.Signature{Name: name, Email: email}
	}
	return gitref.Signature{Name: gitbackend.DefaultAuthor.Name, Email: gitbackend.DefaultAuthor.Email}
}

// env is the coord.Env of this repo.
func (w *coordWorld) env(ctx context.Context) coord.Env {
	var rows coord.Rows = engineRows{a: w.a, at: w.atomicLive(ctx)}
	if w.client {
		rows = clientRowsFor(w)
	}
	agents := w.agents
	agents.PerCheckout = !w.isAgent() // devco:Q-1: a person's limit is per worktree, an agent's per session
	e := coord.Env{Signals: w.signalsStore(ctx), Folder: w.folder, Rows: rows, Caller: w.caller(), Agents: agents, Held: w.held()}
	set, perr := w.a.Policy(ctx, store.SourceRepo)
	e.Agents.TeamMaxClaims, e.Agents.MaxClaims = maxClaims(set, perr == nil, w.maxClaimsConfig)
	if perr == nil {
		// OV15B-URG, OV7-6: the repo's urgency weights and claim-span keys;
		// a policy that cannot be read leaves the built-in defaults.
		if uw, uerr := opstask.UrgencyWeights(set); uerr == nil {
			e.Urgency = uw
		}
		e.Time = claimSpans(set)
	}
	return e
}

// maxClaims is the team's cap and the effective WIP limit (devco:S-20):
// the policy agents.max_claims_per_agent (built-in 1 when no level sets
// it or the policy cannot be read; below 1 counts as 1), lowered by the
// person's config.yaml when that sets less. A config value above the
// team's cap is ignored: an agent cannot lift its own limit.
func maxClaims(set policy.Set, read bool, configured int) (team, effective int) {
	team = defaultMaxClaims
	if read {
		if n, ok := set.Limit(policy.KeyAgentsMaxClaimsPerAgent); ok {
			team = max(int(n), 1)
		}
	}
	effective = team
	if configured > 0 && configured < team {
		effective = configured
	}
	return team, effective
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

// requireHuman refuses a human override (§7.4) by an agent with no person
// behind it: a fixed lore rule, so E_REFUSED (exit 23, B-ERRS-4; v0.1.17
// said E_INVALID_INPUT), with needs_person set. A person, a detected
// coding agent and an agent actor whose roster row names an owner act for
// a person (owner rule 2026-11-11) and pass; a team that wants these
// overrides kept to people lists the operation in agents.person_only,
// which the engine checks before this (task.revoke, task.reassign,
// task.cancel, task.done).
func (w *coordWorld) requireHuman(ctx context.Context, what string) error {
	if w.who.Kind == identity.KindHuman || w.who.Owner != nil {
		return nil
	}
	if w.a == nil {
		return w.refuseOverride(what)
	}
	if r, err := w.a.coordRoster(ctx); err == nil {
		if _, perr := w.a.personBehind(ctx, r, w.who, what); perr == nil {
			return nil
		}
	}
	return w.refuseOverride(what)
}

// refuseOverride is requireHuman's refusal, naming the fix.
func (w *coordWorld) refuseOverride(what string) error {
	e := errs.New(errs.Refused, what+" is a human override; the agent "+w.who.StableKey+" has no person behind it").
		WithHint("its person runs it in their own terminal, or registers the agent with an owner: `lore agent start --label <l> --owner @<person>`")
	e.NeedsPerson = true
	return e
}

// personID is the actor id of the person the caller is or acts for: a
// detected coding agent's person (owner rule 2026-11-11), else the
// caller's own actor id.
func (w *coordWorld) personID() string {
	if w.who.Owner != nil {
		if id, err := w.who.Owner.ActorID(); err == nil {
			return id.String()
		}
	}
	return w.actorID
}

// ---- the row store: the engine ----

// engineRows is coord.Rows over the engine: reads see what the engine
// sees (the live overlay included, after the command's fetch), writes go
// through the engine after the signals push, or with it when at is set
// (coord_standalone_atomic.go).
type engineRows struct {
	a *App
	// at: the repo's live ref joins the signals push (nil: rows after it).
	at LiveAtomic
}

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
	if err := r.rollback(ctx); err != nil {
		return nil, false, err
	}
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
	if err := r.rollback(ctx); err != nil {
		return nil, err
	}
	var out []coord.Doc
	for _, src := range r.a.ReadStores() {
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

// anyRow reports whether keep holds for a live task row of any read store,
// after the same rollback List makes; it decodes no document (List does).
func (r engineRows) anyRow(ctx context.Context, keep func(row.Row) bool) (bool, error) {
	if err := r.rollback(ctx); err != nil {
		return false, err
	}
	for _, src := range r.a.ReadStores() {
		rows, err := r.a.List(ctx, src, schema.TableTasks)
		if err != nil {
			return false, err
		}
		for _, rw := range rows {
			if !rw.Deleted && keep(rw) {
				return true, nil
			}
		}
	}
	return false, nil
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

// rollback undoes a lost attempt's rows before a decision reads (a no-op
// without an atomic live ref or an open mark).
func (r engineRows) rollback(ctx context.Context) error {
	if r.at == nil {
		return nil
	}
	return r.at.AtomicRollback(ctx)
}

// Stage implements coord.Rows: one companion writing every row through the
// engine, in the signals push when the repo's live ref can join it
// (atomicCompanion), else once the signals push was confirmed.
func (r engineRows) Stage(ctx context.Context, rows ...coord.RowWrite) ([]signals.Companion, error) {
	if r.at != nil {
		c, ok, err := r.stageAtomic(ctx, rows)
		if err != nil {
			return nil, err
		}
		if ok {
			return []signals.Companion{c}, nil
		}
	}
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
		// The repo store's copy first; else the first attached store, in
		// attach order, with a live copy (an actor sits in the org store
		// and its org folder under one id: v0.1.17 coreLocateInStores).
		src, rw, err = "", row.Row{}, errs.New(errs.NotFound, "no live copy of "+aid.String())
		for _, s := range append([]store.Source{store.SourceRepo}, a.Stores()...) {
			ref.Store = s
			if gs, gr, gerr := a.Get(ctx, ref); gerr == nil && !gr.Deleted {
				src, rw, err = gs, gr, nil
				break
			}
		}
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
	// targets: the store a new row of this id is created in (clientRows:
	// the linked row and an internal note go to the org store); absent =
	// newRowTarget.
	targets map[string]store.Source
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
		if err := c.a.writeDoc(ctx, rowTable(w), w.ID, w.Doc, c.targets[w.ID]); err != nil {
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
// repo store under its id, or in target when set.
func (a *App) writeDoc(ctx context.Context, table row.Table, id string, doc coord.Doc, target store.Source) error {
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
		if target == "" {
			target = a.newRowTarget(ctx, doc)
		}
		_, err = a.CreateWithID(ctx, target, table, aid, patch)
		return err
	}
	if len(patch) == 0 {
		return nil
	}
	_, err = a.Update(ctx, engine.Ref{Table: table, ID: aid, Store: src}, patch)
	return err
}

// newRowTarget is the store a new staged row is created in: where the row
// it points at lives (a note on a store task goes to the store, never into
// the repo, N2), else the repo store.
func (a *App) newRowTarget(ctx context.Context, doc coord.Doc) store.Source {
	et, eid := doc.Str(schema.FieldEntityTable), doc.Str(schema.FieldEntityID)
	if et == "" || eid == "" {
		return store.SourceRepo
	}
	if src, _, ok, err := a.locateRow(ctx, row.Table(et), eid); err == nil && ok {
		return src
	}
	return store.SourceRepo
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

// claimPushFlag is " --push" when a new claim or hold here is refused
// without it (requirePushAsked: a code repo's remote or a linked store's,
// with no LORE_PUSH_CLAIMS=1 and no live.autopush), else "". Every hint
// that names `lore task next --claim` or `lore task claim --id …` appends
// it, so the printed next step is never the claim refusal (devco:D-607).
func (w *coordWorld) claimPushFlag() string {
	if w == nil || !w.pushNeeded() {
		return ""
	}
	return " --push"
}

// coordErr is coordErrPush with this world's claim flag: a hint that
// sends the person to another claim names --push where the claim needs it.
func (w *coordWorld) coordErr(err error) error {
	return coordErrPush(err, w.claimPushFlag())
}

// coordErr maps coordination failures to the spec's codes (§1 "Error
// codes", v0.1.17 coordError) for a place whose claims need no --push;
// anything else passes through.
func coordErr(err error) error { return coordErrPush(err, "") }

// coordErrPush is coordErr whose claim hints carry push (claimPushFlag:
// " --push" or "").
func coordErrPush(err error, push string) error {
	if err == nil {
		return nil
	}
	next := "lore task next --claim" + push // the command that takes other work
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
		return errs.New(errs.HasOpenChildren, hoc.Error()).WithHint("lore task claim --id " + hoc.Children[0] + push + "   (or: lore task claim --id " + hoc.Task + " --coordinate" + push + ")")
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
		if code == errs.LostClaim && fe.SelfHeld {
			// devco:D-896: the claim on the ref is the caller's own actor's.
			if fe.OtherSession != "" {
				return errs.New(code, fe.Error()).WithHint("you hold it in another session (" + fe.OtherSession + "): work on it there, or take other work with `" + next + "`")
			}
			return errs.New(code, fe.Error()).WithHint("`lore task claim --id " + fe.Task + push + "` takes your claim on it again")
		}
		return errs.New(code, fe.Error()).WithHint("stop work on this task; `" + next + "` takes other work")
	case errors.As(err, &off) && off.RacesLost:
		// Reachable, but another writer won every attempt: contention, not
		// a network problem (the remote answered each push).
		return errs.New(errs.Contended, off.Error()).WithHint("other writers kept winning the signals ref; retry after a short wait")
	case errors.As(err, &off) && off.AutopushOff():
		return errs.New(errs.Offline, off.Error()).
			WithHint("coordination writes (claims, releases, holds) are never queued; turn live.autopush on (or unset LORE_LIVE_AUTOPUSH=0) and run the command again")
	case errors.As(err, &off):
		return errs.New(errs.Offline, off.Error()).
			WithHint("coordination writes (claims, releases, holds) must reach the remote so two people never hold the same work, and are never queued; run the command again once the remote is reachable (`lore whoami` shows the store's status)")
	case errors.As(err, &he):
		msg := he.Error()
		if coord.IsMission(he.By.Task) && !he.SameActor {
			msg = fmt.Sprintf("%s is coordinated by %s (until %s)", he.By.Task, holderName(he.By), he.By.Expires.UTC().Format(time.Kitchen))
		}
		if he.SameActor {
			// P2-TASK-3: waiting will not help. devco:M-8: the other session
			// keeps it; this one picks other work or works under its own name.
			return errs.New(errs.Refused, msg).WithHint(next + " takes other work; the other session finishes or releases this one")
		}
		return errs.New(errs.Contended, msg).WithHint("retry after the hold ends, or take other work with `" + next + "`")
	case errors.As(err, &we):
		// B-ERRS-4: the WIP limit, a poisoned task and a task that is not
		// open are refusals by rule or by the data's state (E_REFUSED,
		// exit 23), not bad input; waiting or retrying will not help.
		return errs.New(errs.Refused, we.Error())
	case errors.As(err, &nw):
		// The hint says where to look next: with tasks waiting, the board
		// shows what each waits on; with none open, task add makes one.
		hint := "lore task add makes a task; lore task list shows the open ones"
		if len(nw.Waiting) > 0 {
			hint = "lore task board shows what each task waits on"
		}
		return errs.New(errs.NoWork, nw.Error()).WithHint(hint)
	case errors.Is(err, coord.ErrContended):
		msg := "ready work exists but every claim attempt lost a race"
		var ce *coord.ContendedError
		if errors.As(err, &ce) {
			msg = fmt.Sprintf("ready work exists but every claim attempt lost a race: %d attempts over %s, other agents kept taking the ready tasks first", ce.Attempts, ce.Elapsed.Round(time.Millisecond))
		}
		return errs.New(errs.Contended, msg).WithHint("retry after a short wait")
	case errors.As(err, &pe):
		return errs.New(errs.Refused, pe.Error())
	case errors.Is(err, coord.ErrUnknownTask):
		// A task id gets no hint here: the host's F1 answer (wire/host
		// explainNotFound, v0.1.17 explainLiveNotFound) fills it for the
		// id argument: a live tombstone or purge, else `lore sync peek
		// <remote>/<branch> tasks`. It only adds a hint to an error that
		// has none (later 218). A mission id (`lore task claim msn_…
		// --coordinate`) is not a row of the task noun, so the host says
		// nothing for it: the hint names where the mission ids are.
		e := errs.New(errs.NotFound, err.Error())
		if unknownCoordID(err).Prefix() == ids.PrefixMission {
			e = e.WithHint("`lore mission list` shows the mission ids")
		}
		return e
	case errors.Is(err, coord.ErrNotOpen):
		// devco:M-9: one plain refusal, never the package's "coord:" text;
		// the next step is the next ready task.
		hint := "lore task next shows the next ready task (--claim" + push + " takes it); lore task list shows the open ones"
		if ad, ok := coord.IsAlreadyDone(err); ok {
			return errs.New(errs.Refused, ad.Error()).WithHint(hint)
		}
		if rv, ok := coord.IsRevoked(err); ok {
			// devco:S-13: the person who revoked it decides who works on it.
			return errs.New(errs.Refused, rv.Error()).
				WithHint(next + " takes other work; a person hands it back with `lore task reassign --id " + rv.Task + " --assign @<handle>" + push + "`")
		}
		msg := strings.TrimPrefix(err.Error(), coord.ErrNotOpen.Error()+": ")
		return errs.New(errs.Refused, msg+"; only an open task can be claimed").WithHint(hint)
	case errors.Is(err, coord.ErrNotGated):
		// devco:L-3: approving a task nobody asked a gate for is a refusal
		// by the data's state, not an internal error; the next step is to
		// request the gate first. The id is the last word before the
		// parenthesised next step coord.Approve appends.
		id := notGatedID(err)
		return errs.New(errs.Refused, id+" has no gate to approve (nobody requested one)").
			WithHint("lore gate request --id " + id + " puts a gate on it; approve once it is requested")
	case errors.Is(err, signals.ErrNotHeld):
		return errs.New(errs.LostClaim, err.Error())
	case errors.Is(err, signals.ErrBadName):
		return errs.New(errs.InvalidInput, err.Error())
	case errors.Is(err, signals.ErrRejected):
		// devco:D-967: the remote's own reason is the message; unmapped it
		// printed as a bare "[E_INTERNAL] internal error"
		return errs.Wrap(errs.Refused, err.Error(), err).WithHint(signalsRejectedHint)
	case errors.Is(err, signals.ErrPartial):
		return errs.Wrap(errs.Internal, err.Error(), err).WithHint(signalsPartialHint)
	}
	return err
}

// signalsRejectedHint is the next step of a signals write the remote
// refused for a reason a retry cannot fix (signals.ErrRejected).
const signalsRejectedHint = "the remote refused the write to its coordination ref (a hook, a protected ref or a permission, named above); fix that on the remote, then run the command again"

// signalsPartialHint is the next step of a signals write that landed while
// its companion write did not (signals.ErrPartial: a host without atomic
// push); a rerun converges.
const signalsPartialHint = "the coordination write reached the remote but the write that goes with it did not; run the command again (a rerun completes it)"

// notGatedID is the task id a coord.ErrNotGated names (its text is
// "<ErrNotGated>: <id> (request one …)"); "the task" when none parses.
func notGatedID(err error) string {
	rest := strings.TrimPrefix(err.Error(), coord.ErrNotGated.Error()+": ")
	if f := strings.Fields(rest); len(f) > 0 {
		if _, perr := ids.ParseAny(f[0]); perr == nil {
			return f[0]
		}
	}
	return "the task"
}

// unknownCoordID is the id a coord.ErrUnknownTask names (its text is
// "<ErrUnknownTask>: <id>"); the zero id when it names none that parses.
func unknownCoordID(err error) ids.AnyID {
	msg := err.Error()
	id, perr := ids.ParseAny(strings.TrimSpace(msg[strings.LastIndex(msg, ":")+1:]))
	if perr != nil {
		return ids.AnyID{}
	}
	return id
}

// markOK is a coordination outcome line's success mark (v0.1.17
// style.Success without colour: the op's text is plain).
const markOK = "✓"

// errCoordInStore refuses coordination (task next, claims, inbox) inside a
// store clone with the real reason (devco D-434): the folder is a git
// repo, but a store's, and claims live on refs/lore/signals of a code
// repo's remote. Decision: coordination stays a code-repo feature; the
// hint names where it runs and what works here instead.
func errCoordInStore(sc storeCheckout) error {
	return errs.New(errs.Unsupported, "coordination (task next, claims, inbox) runs in a code repo: "+sc.shown+" is a lore store clone, not a code repo").
		WithHint("run it in a code repo linked to this folder (`lore org link --org <org> --project <folder>`); here, `lore task list` lists the tasks and `lore task edit --id <id> --assign @<handle>` assigns one")
}

// ClaimPushFlag implements task.ClaimPushFlagger: " --push" when a claim
// of task id here is refused without it (claimPushFlag), "" otherwise and
// when the place has no coordination to ask.
func (a *App) ClaimPushFlag(ctx context.Context, id string) string {
	w, err := standaloneCoord{a: a}.openWorldFor(ctx, id)
	if err != nil {
		return ""
	}
	return w.claimPushFlag()
}
