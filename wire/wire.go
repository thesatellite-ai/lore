// Package wire is the composition root of the new core (layer L7, beside
// cmd): the ONE place that picks concrete adapters and hands them to the
// engine. Everything below it takes interfaces; nothing below it may import
// it.
//
// What Open builds, in order:
//
//  1. the built-in store `repo`: the git backend in repo mode on the code
//     repository that holds Options.RepoDir;
//  2. every store listed in <repo>/.lore/stores.yaml (engine/attach),
//     opened through a store.Openers set holding the folder backend (file)
//     and the git backend (ssh, https, http) with logic/merge as its row
//     merger;
//  3. one SQLite cache file per attached store, all under the lore home:
//     <home>/cache/repo/<repo folder>-<hash>.db for `repo` (RepoCachePath;
//     never in the work tree, so a read creates no file in the repo) and
//     <home>/cache/stores/<store id>/<branch>/<folder>.db for the others;
//  4. the gitleaks scanner built from its defaults plus the secret_rules
//     rows the stores hold; logic/refs as the cache's reference expander;
//     the audit sink (Options.Audit, else an append-only file); the HLC clock and the caller's actor;
//  5. the engine, then a Refresh of every attached store's cache, so rows
//     written outside lore (a git pull, a hand edit) are seen at once.
//
// Laziness: New builds the operation registry without touching the disk,
// so a binary can mount the commands at start-up; the first operation (or
// Engine) opens everything. Open does both at once.
//
// No package-level state: every App is independent (tests open several).
package wire

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/probe"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitcommit"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsagent "github.com/thesatellite-ai/lore/ops/agent"
	opsarchitecturenote "github.com/thesatellite-ai/lore/ops/architecturenote"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
	opsbatch "github.com/thesatellite-ai/lore/ops/batch"
	opsbehaviour "github.com/thesatellite-ai/lore/ops/behaviour"
	opscache "github.com/thesatellite-ai/lore/ops/cache"
	opscheck "github.com/thesatellite-ai/lore/ops/check"
	opscomment "github.com/thesatellite-ai/lore/ops/comment"
	opsconfig "github.com/thesatellite-ai/lore/ops/config"
	opscontent "github.com/thesatellite-ai/lore/ops/content"
	contextop "github.com/thesatellite-ai/lore/ops/context"
	opscookbookrecipe "github.com/thesatellite-ai/lore/ops/cookbookrecipe"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsdecision "github.com/thesatellite-ai/lore/ops/decision"
	opsdirective "github.com/thesatellite-ai/lore/ops/directive"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsexternalsource "github.com/thesatellite-ai/lore/ops/externalsource"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	opshandoff "github.com/thesatellite-ai/lore/ops/handoff"
	opshotfix "github.com/thesatellite-ai/lore/ops/hotfix"
	opsidentity "github.com/thesatellite-ai/lore/ops/identity"
	opsincident "github.com/thesatellite-ai/lore/ops/incident"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opslink "github.com/thesatellite-ai/lore/ops/link"
	opslint "github.com/thesatellite-ai/lore/ops/lint"
	opslive "github.com/thesatellite-ai/lore/ops/live"
	opsmember "github.com/thesatellite-ai/lore/ops/member"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsmission "github.com/thesatellite-ai/lore/ops/mission"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opspattern "github.com/thesatellite-ai/lore/ops/pattern"
	opsperiod "github.com/thesatellite-ai/lore/ops/period"
	opspii "github.com/thesatellite-ai/lore/ops/piipattern"
	opspin "github.com/thesatellite-ai/lore/ops/pin"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	opsplaybook "github.com/thesatellite-ai/lore/ops/playbook"
	opsprofile "github.com/thesatellite-ai/lore/ops/profile"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	opsprojectshared "github.com/thesatellite-ai/lore/ops/projectshared"
	opsprompt "github.com/thesatellite-ai/lore/ops/prompt"
	opsreminder "github.com/thesatellite-ai/lore/ops/reminder"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opsrepoinit "github.com/thesatellite-ai/lore/ops/repoinit"
	opsreveal "github.com/thesatellite-ai/lore/ops/reveal"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	opsrule "github.com/thesatellite-ai/lore/ops/rule"
	opsrun "github.com/thesatellite-ai/lore/ops/run"
	opsscope "github.com/thesatellite-ai/lore/ops/scope"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opsselfupgrade "github.com/thesatellite-ai/lore/ops/selfupgrade"
	opssession "github.com/thesatellite-ai/lore/ops/session"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opsskill "github.com/thesatellite-ai/lore/ops/skill"
	opssnapshot "github.com/thesatellite-ai/lore/ops/snapshot"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssuggestion "github.com/thesatellite-ai/lore/ops/suggestion"
	opssupportbundle "github.com/thesatellite-ai/lore/ops/supportbundle"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	opstag "github.com/thesatellite-ai/lore/ops/tag"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	opstasklist "github.com/thesatellite-ai/lore/ops/tasklist"
	opstastepref "github.com/thesatellite-ai/lore/ops/tastepref"
	opsteam "github.com/thesatellite-ai/lore/ops/team"
	opstechdoc "github.com/thesatellite-ai/lore/ops/techdoc"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	opswhere "github.com/thesatellite-ai/lore/ops/where"
	opswhoami "github.com/thesatellite-ai/lore/ops/whoami"
	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
	opsworkflow "github.com/thesatellite-ai/lore/ops/workflow"
	opsworkspace "github.com/thesatellite-ai/lore/ops/workspace"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Options configures an App.
type Options struct {
	// RepoDir is any folder inside the code repository (usually the
	// working directory). Required.
	RepoDir string
	// Home is the lore home (~/.lore): store clones, per-store caches.
	// Required.
	Home string
	// Actor stamps every write. When zero, ResolveActor is called once at
	// open, else the identity chain of adapters/identity runs (config.yaml
	// identity, git email, …; CONFIG_GUIDE 5.1).
	Actor ids.ID[ids.Actor]
	// ResolveActor, when set, overrides the identity chain (tests, an
	// embedder with its own login). No actor row is written for it.
	ResolveActor func(ctx context.Context) (ids.ID[ids.Actor], error)
	// FlagActor is the --actor value, when the surface offers one.
	FlagActor string
	// PersonOnly resolves the identity chain without agent detection: the
	// process is the machine's person whatever coding tool started it.
	// `lore api` sets it (ServeAsPerson): its environment is not any HTTP
	// client's, so a server started from a Claude Code session must not
	// stamp every request as "<person> via claude_code" (REST spec 9.7:
	// every request runs as the machine's person; the agent is the
	// client's X-Lore-Agent).
	PersonOnly bool
	// Org resolves the org entry of this repo (CONFIG_GUIDE 5.2) for the
	// identity chain; nil means no org. Org resolution is still v0.1.17's
	// (LORE_ORG, repo_orgs, links, own:/repos: patterns), so the old binary
	// supplies it until the org noun migrates.
	Org func(ctx context.Context) (*identity.Org, error)
	// Wall is the wall clock (default time.Now).
	Wall func() time.Time
	// GuardRepoWrite, when set, is asked before every create that lands
	// in the repo store, with the row's table, the text it writes and
	// whether the caller named the repo store itself (--to repo); an error
	// refuses the write. The old binary uses it to keep
	// v0.1.17's routing refusals (a client repo linked to an org store
	// must not receive internal knowledge: F10 E_LINK_UNKNOWN, the F16
	// leak check) until the store noun migrates. Nil allows every write.
	GuardRepoWrite func(ctx context.Context, q WriteQuery, explicit bool) error
	// DefaultTarget, when set, is asked before every create that names no
	// target (CONFIG_GUIDE default-target rules): it returns the store the
	// row goes to (a name the engine attached, such as the org store the
	// repo is linked to), or "" / repo to keep the engine's default target,
	// after which GuardRepoWrite still runs. An error refuses the write.
	// v0.1.17 routed a client repo's internal kinds (memories, rules,
	// decisions, …) to the org store this way (routeWrite, F14); the old
	// binary keeps that rule here until the org noun migrates.
	DefaultTarget func(ctx context.Context, q WriteQuery) (store.Source, error)
	// ModeB reads a retired v0.1.17 Mode B project (shared lore.db) for
	// `lore migrate run` (wire/migrate_modeb.go); nil = such a repo is
	// refused but cannot be converted.
	ModeB ModeBExporter
	// Audit, when set, receives every core write's audit event in place of
	// AuditFile (the old binary passes a sink that appends the v0.1.17
	// audit_logs hash chain, so `lore audit log|verify` see core writes).
	// Nil keeps the append-only AuditFile.
	Audit engine.AuditSink
	// Roster, when set, does `lore actor register|deactivate` (ops/infra
	// Roster): the old binary passes its roster, which resolves the org,
	// opens the org store's org folder (store-only outside a repo) and
	// keeps the v0.1.17 org-store path. Nil: the repo's own roster with no
	// org (wire/roster_ops.go), an org's roster is E_NOT_IMPLEMENTED.
	Roster opsinfra.Roster
	// RepoIsClient reports whether this code repository is a client repo
	// (a customer org's repository: CONFIG_GUIDE "a client repo is tied to
	// the store by a link kept only in the store"). Wire sets the engine's
	// Attached.Client for the repo store from it, which decides the actor
	// projection (F18), the F15 direction rule and the secret-ref move
	// refusal. It is injected because the link logic (org resolution, the
	// links kept in the org stores) still lives in the old binary until the
	// org-store noun migrates. Nil, or an error, makes the repo a client
	// repo (fail closed); the reason is kept for RepoClassNote.
	RepoIsClient func(ctx context.Context) (bool, error)
	// RepoLinkedByRemote reports the remote a store link names this repo
	// by when the repo holds no lore data of its own (no .lore/: a client
	// repo linked with `lore org link` and never `lore init`), "" when it
	// is not such a repo. Such a repo is never written to: its cache lives
	// under ~/.lore, its repo store is read-only, and its git is never
	// wired (no hooks, .gitignore or .gitattributes lines).
	RepoLinkedByRemote func(ctx context.Context) string
	// OrgStores lists the store folders the repo's org brings (CONFIG_GUIDE
	// section 4 step 5b: the org's stores plus the folder this repo is
	// linked to), attached for this process beside stores.yaml and never
	// written to any file of the repo: in a client repo the org link, kept
	// only in the store, is how internal stores are reached without their
	// url in a file the client reads (CFG7). Typical entries: the org name
	// at the linked folder (`--to agency`) and "<org>/<org folder>" at the
	// company folder (`--to agency/org`). An entry whose name stores.yaml
	// already uses is skipped (the committed file wins). Nil, or an error,
	// attaches nothing (fail closed: an internal store is never guessed).
	OrgStores func(ctx context.Context) ([]attach.Store, error)
	// OrgEntryStore finds the store of a configured org entry that is not
	// necessarily the one in use (`--from staging/acme` after `org use
	// northwind`, v0.1.17 fixed rule 5): its url and branch, at folder, or
	// at this repo's link folder for that entry when folder is "". found
	// is false for an unknown entry. Nil reaches no other entry.
	OrgEntryStore func(ctx context.Context, entry, folder string) (s attach.Store, found bool, err error)
	// OrgSigners lists, for every configured org that has an identity, the
	// org's store urls and the signature its commits carry (Q-R2-1). Nil
	// means no store is an org store and FLAG4 signs everything.
	OrgSigners func(ctx context.Context) ([]OrgSigner, error)
	// OrgStoreURLs lists the store urls of every configured org, with or
	// without an identity, by entry name: store init|add|set-url asks
	// before committing one of them into .lore/stores.yaml (INTEG-CFG7-1).
	// OrgSigners only covers orgs with an identity, so without this hook an
	// org whose identity is not set up yet would go unnoticed. Nil, or an
	// error, adds nothing (OrgStores and OrgSigners still apply).
	OrgStoreURLs func(ctx context.Context) ([]OrgStoreURL, error)
	// WrapBackend, when set, wraps each store's backend before the
	// engine, the scanner and the format readers' map see it (the CLI's
	// live-ref overlay, v0.1.17 live mode: open rows of live tables go to
	// and come from refs/lore/live, merged with the branch copy by HLC).
	// name is the store's source (store.SourceRepo for the repo store),
	// folder the attached store's project folder ("" for the repo store).
	// Returning b unchanged is a no-op; nil changes nothing. The wrapper's
	// Close must close b. The close list and the migration rewrite keep
	// the unwrapped backends (RewriteV1 rewrites branch files).
	WrapBackend func(ctx context.Context, name store.Source, folder string, b store.Backend) store.Backend
	// Coord is the host's team coordination machinery (the signals ref,
	// roster and store links) behind task claim|next|…, hold, gate, inbox
	// and mission status (ops/coord, P3-COORD-1). nil: those operations
	// answer E_UNSUPPORTED.
	Coord opscoord.Host
	// AllowNetworkFS lets stores (the repo itself included) live inside a
	// cloud-sync folder or on a network file system, refused otherwise
	// with E_NETWORK_FS. The CLI sets it from LORE_ALLOW_NETWORK_FS=1, the
	// v0.1.17 override; false, the default, keeps the refusal.
	AllowNetworkFS bool
	// LockTimeout is `store.lock_timeout` (0 = the backends' default); open
	// fills it from ~/.lore/config.yaml when unset (F63, FA-AGENT-8).
	LockTimeout time.Duration
	// Notice receives one-line notices for the person, once per open (a
	// CLI prints them on stderr; nil drops them). Today: LiveNote, when the
	// v0.1.17 live ref holds rows the core cannot see yet.
	Notice func(msg string)
	// ProjectHint names the project the running command works in when the
	// repo store holds several live projects (App.Project): the CLI passes
	// v0.1.17's resolution (the project of the checkout's lore.db). Why: a
	// clone that ran `lore init` before it ever synced has its own project
	// row beside the one teammates committed, until the sync pass adopts
	// them into one; v0.1.17 wrote to lore.db's project meanwhile. Only a
	// hint that names one of the live projects is used; ok=false, or a
	// hint naming none of them, keeps E_AMBIGUOUS_PROJECT. nil = no hint.
	ProjectHint func(ctx context.Context) (id string, ok bool)
	// LiveRef is the live ref of the project the command runs in, behind
	// `lore live show|diff|log|restore|compact|purge` (ops/live, P3-LIVE-1):
	// the host that owns the live session implements it. nil answers
	// E_UNSUPPORTED (no live ref wired in this binary).
	LiveRef func(ctx context.Context) (opslive.Ref, error)
	// WorkDir is the folder the command runs in (the process's working
	// directory), for `_o.dir`: PROV6 records it relative to the repo
	// root for monorepos and omits it at the root. Empty = RepoDir.
	WorkDir string
	// Version is the lore version written into every `_w` stamp (OQ7,
	// PROVENANCE_DESIGN 6.2). Empty leaves `v` out.
	Version string
	// Sync is the binary's repo-sync host: `lore sync …`, `lore land`,
	// `lore explain` (ops/sync). nil registers the same operations, each
	// answering E_UNSUPPORTED (P3-SYNC-1).
	Sync opssync.Env
	// Machine returns this machine's id for `_w.m` (PROV4); nil reads the
	// OS machine id through adapters/machine. An error leaves `m` out
	// (never a made-up id). Tests pass a fixed id.
	Machine func() (ids.MachineID, error)
	// OrgCommands is the machinery behind `lore org create|import|link|
	// rename|set|use` (ops/orgcmd.Host: store clones, org resolution, the
	// roster registration); nil answers E_UNSUPPORTED (P3-ORG-G-2).
	OrgCommands opsorgcmd.Host
	// Whoami is the machinery behind `lore whoami` (ops/whoami.Host); nil
	// answers E_UNSUPPORTED (P3-ORG-G-3).
	Whoami opswhoami.Host
	// Local supplies the rules of the Local operations (config schema,
	// leak checks, v0.1.17 store clones; wire/localops.go, P3-CFG-1). A
	// nil part answers E_UNSUPPORTED.
	Local LocalPorts
	// Context supplies the machinery behind the context commands that
	// still lives in the binary (render history, …; wire/context_hosts.go,
	// P3-MISC-3). A nil part answers E_UNSUPPORTED.
	Context ContextHosts
	// Sessions are the binary's hosts behind `lore session list` and
	// `lore agent start` (wire/runs.go); zero = E_UNSUPPORTED.
	Sessions SessionHosts
}

// openOptions is what every store open of this App passes to a backend:
// the lore home and the user's network-FS override.
func (o Options) openOptions() store.OpenOptions {
	return store.OpenOptions{Home: o.Home, AllowNetworkFS: o.AllowNetworkFS, LockTimeout: o.LockTimeout}
}

// rowfsOptions are the rowfs options of a store this App makes on disk
// (folder.Init), matching openOptions.
func (o Options) rowfsOptions() []rowfs.Option {
	return []rowfs.Option{rowfs.WithAllowNetworkFS(o.AllowNetworkFS), rowfs.WithLockTimeout(o.LockTimeout)}
}

// repoRowfsOptions is rowfsOptions for a code repo's checkout: rows in its
// .lore/data (rowfs.LayoutRepo), lock and state in its .lore.
func (o Options) repoRowfsOptions() []rowfs.Option {
	return append(o.rowfsOptions(), rowfs.WithLayout(rowfs.LayoutRepo))
}

// storeRowfsOptions is rowfsOptions for the local folder of a store at dir
// (storeDir, withStoreDir): in a git store's clone the lock is the one the
// git backend takes, <clone>/.git/lore/lock (git.LockPathOf), never a
// second lock file in the clone's files; a folder store keeps rowfs's own.
func (o Options) storeRowfsOptions(dir string) []rowfs.Option {
	opts := o.rowfsOptions()
	if st, err := os.Stat(git.StateDirOf(dir)); err == nil && st.IsDir() {
		opts = append(opts, rowfs.WithLockPath(git.LockPathOf(dir)))
	}
	return opts
}

// WriteQuery is what the write hooks (Options.GuardRepoWrite,
// Options.DefaultTarget) learn about one create.
type WriteQuery struct {
	// Table is the row's table.
	Table row.Table
	// Text is every text value the row writes, sorted by field and joined
	// by newlines (for a leak check).
	Text string
	// Routes is the effective policy.KeyClientRoutes (kind → target) of
	// every attached store's policies rows (CFG8: the team's client-repo
	// defaults); empty when none is set.
	Routes map[string]string
}

// App is one opened (or openable) core: the registry every surface mounts,
// and the engine behind it.
type App struct {
	opts Options
	reg  *op.Registry

	// watchPlaces (ServeAsPerson): each request checks whether a store
	// moved since the state opened (servestale.go); placeFiles/placesAt
	// are what it compares, retired the states it let go (closed by Close).
	watchPlaces bool
	placeFiles  []string
	placesAt    string
	retired     []*state

	mu     sync.Mutex
	opened bool
	err    error
	st     *state
	// extra are the `--to <store>/<folder>` targets this App attaches
	// beside stores.yaml (STORE TARGETS); adding one reopens the state.
	extra []attach.Store

	// cfg is the repo-only state the store operations run on, so a store
	// that cannot be opened never blocks `lore store remove` (cfgOpened,
	// cfgErr as for st).
	cfgOpened bool
	cfgErr    error
	cfg       *state
	// top is repoTop's answer found without opening the repo (git.RepoRoot),
	// kept once found: the repo of an App never changes.
	top string

	// probeLimiter bounds the onboarding probes of this process to
	// probe.DefaultConcurrency at once across every op call (ONB 6.1: a
	// doctor run never floods ssh). doctorCache is the in-process fallback
	// of the doctor check cache, used only when machine.db does not open;
	// machDoctor is the shared machine.db cache (doctor_cache.go), opened
	// once per App under doctorMu and closed by Close, so repeated doctor
	// runs (the page server) reuse one handle instead of leaking one each.
	probeLimiter *probe.Limiter
	doctorCache  *opsdoctor.MemCache
	doctorMu     sync.Mutex
	machDoctor   *machineDoctorCache
	// prefillCache keeps setup.plan's detections for opsdoctor.CacheTTL
	// (setupprefillcache.go), so reopening a step does not probe again.
	prefillCache *prefillCache
	// aliasCache keeps setup.aliases' ssh -T table (setupaliases.go).
	aliasCache *aliasCache
	// healthMu serialises this process's read-modify-write of the doctor
	// health file (setuphealth.go).
	healthMu sync.Mutex

	// mach is the machine-only App of a guided-setup op run outside a repo
	// (machineApp, wire/machineonly.go); machOpened, machErr as for st.
	machOpened bool
	machErr    error
	mach       *App
}

// state is everything Open built; nil until the first use.
type state struct {
	eng *engine.Engine
	// linkedByRemote is the remote a store link names this repo by when it
	// holds no lore data (Options.RepoLinkedByRemote): the repo store is
	// read-only and the repo cache lives under ~/.lore; "" otherwise.
	linkedByRemote string
	actor          ids.ID[ids.Actor]
	caches         *caches
	backends       []store.Backend
	// machine closes the machine store's backend (P3-LOCAL-2); nil when
	// none was opened. Kept apart from backends, which follow Stores().
	machine io.Closer
	// byName maps every attached store (the repo included) to its backend.
	byName map[store.Source]store.Backend
	// folders is each attached store's folder ("" or "." = the store root);
	// the repo store has none.
	folders map[store.Source]string
	repoTop string
	// format is the DOCS O6 answer computed at open: whether the first
	// 0.1.18 write must ask (see format.go). Guarded by App.mu.
	format formatState
	// storeFormat is the O6 answer per attached v0.1.17 org store that
	// asks (format.go readStoreFormats); absent = never asks. Guarded by
	// App.mu.
	storeFormat map[store.Source]formatState
	audit       engine.AuditSink
	// who is the resolved identity (zero when Options set the actor).
	who identity.Resolved
	// defaultTarget is the engine's default write target.
	defaultTarget store.Source
	// here is the store folder a command run inside a store's checkout
	// works on (storehere.go); "" in a code repo.
	here store.Source
	// actorMu / actorDone: the actor row is written once per store.
	actorMu   sync.Mutex
	actorDone map[store.Source]bool
	// agentOwner is the owning person of the App's agent identity in a
	// client repo (agentOwnerOf); zero otherwise. Read by clientActorAs.
	agentOwner ids.ID[ids.Actor]
	// agentOwnerName is that person's roster display_name ("" unknown).
	agentOwnerName string
	// unreachable holds the org stores (Options.OrgStores) left out at open
	// because they have no local copy and cannot be reached, with that
	// error; nil when every store opened (openAttached, unreachableGuard).
	unreachable map[store.Source]error
	// repoClassNote says why the repo store is client-class without a
	// positive answer (no RepoIsClient hook, or its error); empty when the
	// hook answered.
	repoClassNote string
}

// New validates opts and builds the registry of every migrated operation,
// without opening anything.
func New(opts Options) (*App, error) {
	if opts.RepoDir == "" || opts.Home == "" {
		return nil, errs.New(errs.ConfigInvalid, "wire: RepoDir and Home are required")
	}
	if opts.Wall == nil {
		opts.Wall = time.Now
	}
	closeLoreHome(opts.Home)                 // homemode.go: an older lore left it open
	movePersonalFolder(opts.Home, os.Stderr) // personalfolder.go: stores/me → stores/_me, once
	a := &App{opts: opts, probeLimiter: probe.NewLimiter(probe.DefaultConcurrency), doctorCache: opsdoctor.NewMemCache(0, opts.Wall), prefillCache: newPrefillCache(opts.Wall), aliasCache: newAliasCache(opts.Wall)}
	if b, ok := opts.Audit.(appBinder); ok {
		b.bindApp(a) // a sink that links sessions and stamps with the App's clock (wire/auditchain.go)
	}
	reg, err := Registry(a)
	if err != nil {
		return nil, err
	}
	a.reg = reg
	return a, nil
}

// Open is New followed by opening every store, cache and the engine.
func Open(ctx context.Context, opts Options) (*App, error) {
	a, err := New(opts)
	if err != nil {
		return nil, err
	}
	if _, err := a.Engine(ctx); err != nil {
		return nil, errors.Join(err, a.Close())
	}
	return a, nil
}

// Registry builds the operation registry with every operation bound to env
// and the migration operations bound to mig. Exported so docs tests and
// `lore spec` render the real registry without opening a repository.
func Registry(a *App) (*op.Registry, error) {
	entries := opsmemory.Ops(a)
	entries = append(entries, opsmigrate.Ops(a)...)
	entries = append(entries, opsstores.Ops(a)...)
	entries = append(entries, opspattern.Ops(a)...)
	entries = append(entries, opsplaybook.Ops(a)...)
	entries = append(entries, opsprompt.Ops(a)...)
	entries = append(entries, opsrule.Ops(a)...)
	entries = append(entries, opsdecision.Ops(a)...)
	entries = append(entries, opshotfix.Ops(a)...)
	// CONTEXT_BUDGET 6.2 (D12): lore scope show|list|move.
	entries = append(entries, opsscope.Ops(a)...)
	// CONTEXT_BUDGET 6.4 (FA-CTX-6): lore pin set|unset.
	entries = append(entries, opspin.Ops(a)...)
	entries = append(entries, opstag.Ops(a)...)
	entries = append(entries, opscomment.Ops(a)...)
	entries = append(entries, opslink.Ops(a, gitcommit.Resolver{})...)
	entries = append(entries, opssettings.Ops(a)...)
	entries = append(entries, opstask.Ops(a)...)
	// XQ9: importing is a verb under the noun it creates (lore task import).
	entries = append(entries, opstask.ImportOps(a)...)
	// XQ1: lore batch list|show over the machine-local batch log (Q-R3-8).
	entries = append(entries, opsbatch.Ops(a)...)
	// CLI_REFERENCE 3.17 "existing" (P3-SKILL-1): lore skill compile as the Local operation skill.compile.
	entries = append(entries, opsskill.Ops(a)...)
	entries = append(entries, opsskill.InstallOps(a)...) // skill.install, skill.status (ONB 4.7.1)
	// DELETION 2.2-2.6: lore trash list|show|restore|purge (DQ1-DQ4).
	entries = append(entries, opstrash.Ops(a)...)
	// CLI_REFERENCE 3.5 (PASS3 ONE-PATH, P3-UPG-1): lore upgrade as the Local operation upgrade.run.
	entries = append(entries, opsselfupgrade.Ops(a)...)
	// LORE_TEAM_SPEC §1, §6.6, F8: lore live show|diff|log|restore|compact|purge.
	entries = append(entries, opslive.Ops(a)...)
	entries = append(entries, opstasklist.Ops(a)...)
	entries = append(entries, opsmission.Ops(a)...)
	entries = append(entries, opsplan.Ops(a)...)
	entries = append(entries, opsinfra.Ops(a)...)
	// MACHINE PROFILE / MACHINE LABEL: lore machine list|show|label.
	entries = append(entries, opsinfra.MachineOps(a)...)
	entries = append(entries, opsarchitecturenote.Ops(a)...)
	entries = append(entries, opsbehaviour.Ops(a)...)
	entries = append(entries, opscookbookrecipe.Ops(a)...)
	entries = append(entries, opsincident.Ops(a)...)
	entries = append(entries, opssuggestion.Ops(a)...)
	entries = append(entries, opstastepref.Ops(a)...)
	entries = append(entries, opsworkflow.Ops(a)...)
	entries = append(entries, opsworkspace.Ops(a)...)
	entries = append(entries, opshandoff.Ops(a)...)
	entries = append(entries, opssnapshot.Ops(a)...)
	entries = append(entries, opstechdoc.Ops(a)...)
	entries = append(entries, opsexternalsource.Ops(a)...)
	entries = append(entries, opsreminder.Ops(a)...)
	entries = append(entries, opssql.Ops(a)...)
	// CLI 2.7 / 2.13, PROV 9.1: lore show|move <any id> (lore row show|move).
	entries = append(entries, opsrow.Ops(a)...)
	// D2/D3 plain files: lore row path|markdown|edit (lore path, lore open, lore edit).
	entries = append(entries, opsrow.EditOps(a)...)
	// lore lint: one host for the findings the design sends to lint (PROV9, SQ20/22, TQ2/TQ4, DOCS O2).
	entries = append(entries, opslint.Ops(a)...)
	// P3-CFG-1: lore config get|set|list|check and lore check leaks|pr-body (Local).
	entries = append(entries, opsconfig.Ops(a)...)
	// machine reveal | path list: show lore's own files in the file manager.
	entries = append(entries, opsreveal.Ops(a, os.Getenv)...)
	entries = append(entries, opscheck.Ops(a)...)
	// TIME_TRACKING 2.2/4: lore run start|end|cancel on the machine store (P3-LOCAL-3).
	entries = append(entries, opsrun.Ops(a)...)
	// PASS3 ONE-PATH: lore directive install|show (Local, p3-g-org).
	entries = append(entries, opsdirective.Ops(a)...)
	// PASS3 ONE-PATH: lore identity set|unset|show (Local, p3-g-org).
	entries = append(entries, opsidentity.Ops(a)...)
	// CLI_REFERENCE 2.25 (PASS3 ONE-PATH): lore pii-pattern list (leftover v0.1.17 rows), add|enable|disable refused.
	entries = append(entries, opspii.Ops(a)...)
	// CLI_REFERENCE 2.5, SEARCH_INDEX_DESIGN: lore search run|status|rebuild (P3-MISC-1).
	entries = append(entries, opssearch.Ops(a)...)
	// lore audit log|verify over the repo sink's hash chain (P3-AUDIT-1..4).
	entries = append(entries, opsaudit.Ops(a)...)
	// lore actor register|deactivate|delete (F18, BATCH2 #8), run direct in Run.
	entries = append(entries, opsinfra.ActorOps(a)...)
	// TEAM_PROFILES 3 (TP-1..TP-23): profile, periods and members, run direct
	// on the org roster (profilestore.IsDirect, wire/profile_roster.go).
	entries = append(entries, opsprofile.Ops(a)...)
	entries = append(entries, opsperiod.Ops(a)...)
	entries = append(entries, opsmember.Ops(a)...)
	// P3-MISC-2: lore cache prune (Local; asks through Confirm.MayAsk + op.Confirmed).
	entries = append(entries, opscache.Ops(a)...)
	// P3-MISC-3: the context commands; their machinery is Options.Context (wire/context_hosts.go).
	entries = append(entries, opswhycontext.Ops(a)...)
	entries = append(entries, opssetup.Ops(a)...)
	entries = append(entries, opssetup.GuidedOps(a)...)   // ONB 7.1: setup.plan/check/apply/fix/list/show/use/edit/remove/join/repair
	entries = append(entries, opssetup.DiscoverOps(a)...) // setup.discover: account discovery for the setup page
	entries = append(entries, opssetup.FolderOps(a)...)   // setup.folders / setup.browse: the Folders step's suggestions and folder browser
	entries = append(entries, opssetup.ImportOps(a)...)   // setup.import_scan / setup.import: import an existing lore install
	entries = append(entries, opsteam.Ops(a)...)          // ONB 4.6: team.invite, .list, .revoke, .resend
	entries = append(entries, opsteam.TeamOps(a)...)      // F18: team.add, .list, .show, .remove, .member.add|remove
	entries = append(entries, opsrepoinit.Ops(a)...)      // ONB 4.10: lore init as a registry op (repo.init) for the setup page
	entries = append(entries, opscoord.Ops(a)...)
	entries = append(entries, opssupportbundle.Ops(a)...)
	entries = append(entries, contextop.Ops(a)...)
	// PASS3 ONE-PATH: lore sync …, land, explain (the host is Options.Sync).
	entries = append(entries, opssync.Ops(a.syncEnv())...)
	entries = append(entries, opsrender.Ops(a)...)
	entries = append(entries, opsdoctor.Ops(a)...)
	entries = append(entries, opsfix.Ops(a)...) // lore fix (D-ak, D-al; fixproject.go)
	// CLI 3.8 / 2.11: lore session list, lore agent start (P3-RUNS-4, P3-RUNS-6).
	entries = append(entries, opssession.Ops(a)...)
	entries = append(entries, opsagent.Ops(a)...)
	// PASS3 ONE-PATH (p3-g-org): lore org, lore whoami, the retired lore project shared-init|shared-list.
	entries = append(entries, opsorgcmd.Ops(a)...)
	entries = append(entries, opswhoami.Ops(a)...)
	// plain files sweep 3.2: lore where (alias lore files), every path lore keeps.
	entries = append(entries, opswhere.Ops(a)...)
	entries = append(entries, opsprojectshared.Ops()...)
	return op.NewRegistry(entries...)
}

// Registry returns the operations every surface mounts.
func (a *App) Registry() *op.Registry { return a.reg }

// Engine opens the App on first use and returns its engine.
func (a *App) Engine(ctx context.Context) (*engine.Engine, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	return st.eng, nil
}

// Actor opens the App on first use and returns the actor its writes are
// stamped with (Options.Actor, or what ResolveActor found).
func (a *App) Actor(ctx context.Context) (ids.ID[ids.Actor], error) {
	st, err := a.state(ctx)
	if err != nil {
		return ids.ID[ids.Actor]{}, err
	}
	return st.actor, nil
}

// ServeAsPerson makes this App resolve the machine's person, never a
// detected agent (Options.PersonOnly), for a server whose callers are not
// its own process. It must run before the App opens; called later it
// reopens the state so the identity is resolved again.
func (a *App) ServeAsPerson() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.opts.PersonOnly = true
	a.watchPlaces = true
	if a.opened && a.st != nil {
		_ = a.st.close()
	}
	a.opened, a.st, a.err = false, nil, nil
}

// state opens the App once. A failed open is remembered: every later call
// returns the same error rather than half-opening again.
func (a *App) state(ctx context.Context) (*state, error) {
	st, opened, err := a.openState(ctx)
	if err != nil {
		return st, a.refineNoProject(ctx, err) // G-3: the specific "no project" answer (untouched_reason.go)
	}
	if opened && err == nil {
		// outside a.mu: the pick-up writes through the engine, whose
		// scanner and hooks ask the App again (handedit.go)
		if err = st.pickUpThenSync(ctx, a.opts); err != nil {
			a.mu.Lock()
			a.err = err
			a.mu.Unlock()
			return nil, err
		}
	}
	return st, err
}

// openState is state's open under a.mu; opened reports that this call
// opened it (the pick-up of hand edits then runs once, in state).
func (a *App) openState(ctx context.Context) (st *state, opened bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	run := noCreateOf(ctx)
	if !a.opened && run != nil && run.stateErr != nil {
		return nil, false, run.stateErr // refused earlier in this read run (doctor_nocreate.go)
	}
	if !a.opened {
		a.opened, opened = true, true
		a.st, a.err = open(ctx, a.opts, a.extra, false)
		if a.err == nil {
			a.st.eng.SetRedactHook(a.redactTask) // D21: write-time store-then-rotate task (wire/secretmgrs.go)
			a.notePlaces(a.st)                   // servestale.go
		}
		if a.err == nil && a.opts.Notice != nil {
			a.healedNotices(a.st)
			a.rowFileNotices(a.st) // fail closed: name every row file the refresh left out (rowfile_notice.go)
			if n := liveNote(ctx, a.st); n != "" {
				a.opts.Notice(n)
			}
			for _, n := range strayProjectNotes(ctx, a.st) {
				a.opts.Notice(n)
			}
		}
		if a.err != nil && run != nil {
			a.opened = false // doctor_nocreate.go: a later command on this App opens for real
			run.stateErr = a.err
		}
	}
	return a.st, opened, a.err
}

// configState opens the repo-only state once (see App.cfg).
func (a *App) configState(ctx context.Context) (*state, error) {
	st, err := a.configStateLocked(ctx)
	return st, a.refineNoProject(ctx, err) // G-3 (untouched_reason.go), outside a.mu
}

// configStateLocked is configState's open under a.mu.
func (a *App) configStateLocked(ctx context.Context) (*state, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	run := noCreateOf(ctx)
	if !a.cfgOpened && run != nil && run.configErr != nil {
		return nil, run.configErr // refused earlier in this read run (doctor_nocreate.go)
	}
	if !a.cfgOpened {
		a.cfgOpened = true
		a.cfg, a.cfgErr = open(ctx, a.opts, nil, true)
		if a.cfgErr == nil {
			a.healedNotices(a.cfg)
		}
		if a.cfgErr != nil && run != nil {
			a.cfgOpened = false // doctor_nocreate.go
			run.configErr = a.cfgErr
		}
	}
	return a.cfg, a.cfgErr
}

// healedNotices hands Options.Notice one line per damaged cache file the
// open of st moved aside and rebuilt (caches.openHealing). Either open
// (full or repo-only) may be the one that healed, so both call it.
func (a *App) healedNotices(st *state) {
	if a.opts.Notice == nil {
		return
	}
	for _, n := range st.caches.healed {
		a.opts.Notice(n)
	}
}

// repoTop is the code repository's top folder (the lore project root in
// it). Only the path is needed, so nothing is opened for it: the repo-only
// state when it is already open, else git.RepoRoot, which creates nothing.
// A store command run in a repo it does not change (store folder add,
// store list) must leave that repo as it was: opening the repo-only state
// would create .lore/ and its cache there.
func (a *App) repoTop(ctx context.Context) (string, error) {
	a.mu.Lock()
	opened, st, err, known := a.cfgOpened, a.cfg, a.cfgErr, a.top
	a.mu.Unlock()
	if opened {
		if err != nil {
			return "", err
		}
		return st.repoTop, nil
	}
	if known != "" {
		return known, nil
	}
	// doctor_nocreate.go: within one read run a refusal is answered from
	// the run's memory (doctor's checks each ask), as configState does
	run := noCreateOf(ctx)
	a.mu.Lock()
	if run != nil && run.configErr != nil {
		err = run.configErr
	}
	a.mu.Unlock()
	if err != nil {
		return "", err
	}
	top, err := git.RepoRoot(ctx, a.opts.RepoDir, a.opts.Home)
	if sc, in := storeHere(a.opts.RepoDir); in && err == nil {
		top, err = "", errInStore(sc) // a store's checkout is never a code repo (storehere.go)
	}
	a.mu.Lock()
	switch {
	case err == nil:
		a.top = top
	case run != nil:
		run.configErr = err
	}
	a.mu.Unlock()
	return top, err
}

// storeRepoTop is repoTop for the store commands that need only the setup
// (opsstores.RunsWithoutRepo): "" with no error when the command runs
// outside any git work tree, so they resolve team, me, the org's stores and
// an org store's url from the setup alone; a repo that cannot be read is
// still its error.
func (a *App) storeRepoTop(ctx context.Context) (string, error) {
	top, err := a.repoTop(ctx)
	if err != nil && errs.Has(err, errs.NotAStore) && (!insideWorkTree(ctx, a.opts.RepoDir) || inStore(a.opts.RepoDir)) {
		return "", nil
	}
	return top, err
}

// Run implements op.Runner: open on first use, then the engine's Run path.
// The store operations run on the repo-only engine (configState); a
// `--to <store>/<folder>` target is attached first (prepareTarget).
func (a *App) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	// B1-17: a refused write leaves the tree as it was. The open makes
	// .lore/ for a write's implicit setup; when the write is refused in a
	// repo that had none, the folders it left empty go again.
	top := ""
	if e.Spec().Access == op.AccessWrite && !e.Spec().Local {
		if t := gitTopLevel(ctx, a.projectDir()); t != "" && !loreFolderAt(t) {
			top = t
		}
	}
	a.refreshMovedStores() // servestale.go: a serving App follows a store set-url made since it opened
	res, err := a.run(ctx, e, d, c)
	if err != nil && top != "" {
		removeEmptyDirs(filepath.Join(top, loreFolderName))
	}
	return res, err
}

// removeEmptyDirs removes dir and every folder under it that holds no
// file, deepest first, and returns how many it removed; a folder holding
// anything stays.
func removeEmptyDirs(dir string) int {
	var dirs []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	n := 0
	for i := len(dirs) - 1; i >= 0; i-- {
		if os.Remove(dirs[i]) == nil { // only an empty folder goes
			n++
		}
	}
	return n
}

// run is Run without the refusal clean-up.
func (a *App) run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	ctx = a.TeamContext(ctx) // P3-COORD-3: --assign @h, --mine, STATE without a host Team (wire/coord_standalone.go)
	// P3-CFG-1: a Local operation opens nothing, not even the
	// agent detection's repo state (wire/localops.go).
	if e.Spec().Local {
		return a.runLocal(ctx, e, d, c)
	}
	// P3-AUDIT-4: audit reads and roster verbs run without the repo engine
	// (wire/roster_ops.go), before anything opens the App: opening it can
	// run the host's sync pass, which would record as accepted the very
	// changes audit verify looks for.
	if hostRunOp(e.Spec().Name) {
		if profilestore.IsDirect(e.Spec().Name) {
			// TEAM_PROFILES 3.4: the profile ops read --yes (op.Confirmed)
			// and the caller (TS-4) without the engine around them.
			ctx = profileContext(ctx, c)
		}
		return runDirect(ctx, e, d)
	}
	// ONB 3.3 / BUGFIX-18: a --setup or LORE_SETUP naming no enabled setup
	// is refused on reads too, before anything opens.
	if err := a.setupExplicitGate(ctx); err != nil {
		return op.Result{}, err
	}
	// A store command that needs only the setup (store folder add|list,
	// store list, store set, …) runs on it alone outside a lore repo, before
	// anything opens the repo (the agent detection below would): from
	// the plain parent folder of an org's client folders, or in a repo it
	// does not change, which it must leave as it was (wire/storefolders.go).
	if a.setupOnlyHere(ctx, e.Spec().Name) {
		return a.runSetupOnly(ctx, e, d, c)
	}
	// `lore sync` with no code repo here syncs every store of the setup,
	// or the store checkout it runs in, each on its own App (syncsetup.go)
	if e.Spec().Name == opssync.OpRun && a.opts.Sync != nil && a.setupSyncHere(ctx) {
		return runDirect(ctx, e, d)
	}
	// AGENT_IDENTITY 8.2 (#7): the CLI caller carries the detected agent, so
	// agents.person_only fires on the CLI too, not only for REST callers
	// that send X-Lore-Agent. A surface that already set an agent or an
	// explicit actor keeps it.
	if !c.AgentFixed && c.Agent == "" && c.Actor.IsZero() {
		c.Agent = a.CallerAgent(ctx)
	}
	// v0.1.17 order: a live operation outside a project (or where live
	// rows do not apply) is refused before any store opens (P3-LIVE-1).
	if err := a.liveReady(ctx, e.Spec().Name); err != nil {
		return op.Result{}, err
	}
	// migrate.run converts a Mode B repo and migrate.status reports its
	// pointer (a note naming the next step), so neither is refused for it.
	if name := e.Spec().Name; name != opsmigrate.OpRun && name != opsmigrate.OpStatus && !opsstores.IsStoreOp(name) {
		if err := a.RefuseRetiredModeB(ctx); err != nil {
			return op.Result{}, err
		}
	}
	// owner item 11: a read in a project with v0.1.17 rows says "run lore
	// fix"; a write there is refused where it reaches the project's rows
	// (D13, formatold.go)
	if err := a.oldRowsGate(ctx, e); err != nil {
		return op.Result{}, err
	}
	if opsstores.IsStoreOp(e.Spec().Name) {
		if a.personalInitOutsideRepo(ctx, e, d) { // G-CORE-7: no repo needed
			return runDirect(ctx, e, d)
		}
		if err := a.refuseStoresFileOutsideRepo(ctx, e.Spec().Name); err != nil {
			return op.Result{}, err
		}
		if err := a.confirmClientStoresFile(ctx, e, d, c); err != nil {
			return op.Result{}, err
		}
		st, err := a.configState(ctx)
		if err != nil {
			return op.Result{}, err
		}
		return st.eng.Run(ctx, e, d, c)
	}
	d, err := a.resolveAlias(ctx, e, d)
	if err != nil {
		return op.Result{}, err
	}
	if ctx, d, err = a.prepareTarget(ctx, e, d, c); err != nil {
		return op.Result{}, err
	}
	// F17 under STORE TARGETS (PL-PORT-1): a note for a personal place
	// every repo reads that names a known org asks first.
	if err = a.confirmPersonalMentions(ctx, e, d, c); err != nil {
		return op.Result{}, err
	}
	if err = a.prepareSource(ctx, e, d); err != nil {
		return op.Result{}, err
	}
	// B2-21: a row named by id in a folder of the org's stores this repo
	// does not attach (idfolder.go)
	search, err := a.attachFolderOfID(ctx, e, d)
	if err != nil {
		return op.Result{}, err
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return op.Result{}, err
	}
	if uerr := a.unreachableTarget(e, d); uerr != nil {
		return op.Result{}, uerr
	}
	res, err := eng.Run(ctx, e, d, c)
	if err == nil {
		a.ensureFolderProject(ctx) // the folder written to has its project row
		a.afterWrite(ctx, e)       // OV3 MACH-AUTO: the profile goes with the next ordinary write
	}
	return res, search.withSearchHint(err)
}

// unreachableTarget answers an operation whose `to` names an org store
// left out at open (state.unreachable) with that store's own
// E_STORE_UNREACHABLE, instead of the engine's "no store named …".
func (a *App) unreachableTarget(e op.Entry, d op.Decoder) error {
	a.mu.Lock()
	var gone map[store.Source]error
	if a.st != nil {
		gone = a.st.unreachable
	}
	a.mu.Unlock()
	if len(gone) == 0 {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			if uerr, ok := gone[store.Source(tv.Text)]; ok {
				return uerr
			}
		}
	}
	return nil
}

// confirmClientStoresFile is CFG7: `lore store init|add|set-url <name>`
// writes the store url into the committed stores.yaml, so it answers
// E_NEEDS_CONFIRM until the caller says yes (--yes, a tty answer, or the
// REST `yes` field) when either
//   - this is a client repo: the client reads the file (the org link is
//     the recommended way); a failed client check asks too (fail closed);
//     with no RepoIsClient hook the embedder has no org concept and this
//     part never asks; or
//   - the url is an org store's (INTEG-CFG7-1, owner 2026-11-07): the org
//     link keeps that url out of the repo, so committing it, visible to
//     everyone who can read the repo, is a choice the person makes.
//
// `me` is never written there. It runs before the operation, so nothing is
// written before the confirm.
func (a *App) confirmClientStoresFile(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) error {
	if !opsstores.WritesStoresFile(e.Spec().Name) || c.Yes {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	var name, rawURL string
	for _, tv := range in.Texts() {
		switch tv.Field {
		case storeNameField:
			name = tv.Text
		case storeURLField:
			rawURL = tv.Text
		}
	}
	if name == opsstores.PersonalName {
		return nil
	}
	if a.opts.RepoIsClient != nil {
		if client, err := a.opts.RepoIsClient(ctx); err != nil || client {
			return errs.New(errs.NeedsConfirm, "this is a client repo: the client can read .lore/stores.yaml and will see the store url").
				WithQuestion("write the store url into this client repo's .lore/stores.yaml?").
				WithHint("link the repo instead (`lore org link --org <org> --project <folder>`: the link is kept only in the store), or rerun with --yes")
		}
	}
	if org, ok := a.orgStoreOf(ctx, rawURL); ok {
		whose, link := "an org's store", "<org>"
		if org != "" {
			whose, link = "org "+org+"'s store", org
		}
		return errs.New(errs.NeedsConfirm, "store url "+rawURL+" is "+whose+": writing it into .lore/stores.yaml commits the url, visible to everyone who can read this repo").
			WithQuestion("write " + whose + " url into .lore/stores.yaml? it is committed and visible to everyone who can read this repo").
			WithHint("rerun with --yes to write it, or link the repo instead (`lore org link --org " + link + " --project <folder>`: the link is kept only in the store)")
	}
	return nil
}

// orgStoreOf reports whether raw names the same store (storeKey: any
// spelling of the remote, trailing slash and .git ignored; branch and
// folder ignored) as a store an org brings: this repo's org
// (Options.OrgStores), any configured org (Options.OrgStoreURLs, with or
// without an identity) or any org with an identity (Options.OrgSigners).
// org is the org's name, or "" when only the signer list knew the url (it
// carries the identity's name, not the org's). An unparsable url, or no
// hooks, is never an org store (the url check of the operation refuses a
// bad url itself).
func (a *App) orgStoreOf(ctx context.Context, raw string) (org string, ok bool) {
	if raw == "" {
		return "", false
	}
	want, ok := storeKey(raw)
	if !ok {
		return "", false
	}
	same := func(u string) bool {
		got, ok := storeKey(u)
		return ok && got == want
	}
	for _, s := range rawOrgStores(ctx, a.opts) {
		if same(s.URL) {
			base, _, _ := strings.Cut(string(s.Name), targetSep)
			return base, true
		}
	}
	if a.opts.OrgStoreURLs != nil {
		if all, err := a.opts.OrgStoreURLs(ctx); err == nil {
			for _, s := range all {
				if same(s.URL) {
					return s.Org, true
				}
			}
		}
	}
	if a.opts.OrgSigners != nil {
		signers, err := a.opts.OrgSigners(ctx)
		if err != nil {
			return "", false
		}
		for _, sg := range signers {
			for _, u := range sg.StoreURLs {
				if same(u) {
					return "", true
				}
			}
		}
	}
	return "", false
}

// storeNameField is the input field of store.init/store.add naming the
// store (ops/stores InitIn.Name, AddIn.Name json tag).
const storeNameField = "name"

// storeURLField is the input field of store.init/add/set_url carrying the
// url (ops/stores InitIn.URL, AddIn.URL, SetURLIn.URL json tag).
const storeURLField = "url"

// Close ends the setup probes still running (each ssh with its process
// group) and releases every backend and cache file. Safe to call on an App
// that never opened, and more than once.
func (a *App) Close() error {
	// The alias probes a check left running end first (they need no lock
	// and must not outlive the App: orphan ssh, setupaliases.go).
	a.aliasCache.close()
	var all []error
	a.doctorMu.Lock()
	if a.machDoctor != nil {
		all = append(all, a.machDoctor.Close())
		a.machDoctor = nil
	}
	a.doctorMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg != nil {
		all = append(all, a.cfg.close())
		a.cfg = nil
		a.cfgErr = errs.New(errs.Internal, "wire: the app is closed")
	}
	if a.st != nil {
		all = append(all, a.st.close())
		a.st = nil
		a.err = errs.New(errs.Internal, "wire: the app is closed")
	}
	for _, r := range a.retired { // servestale.go: states a moved store retired
		all = append(all, r.close())
	}
	a.retired = nil
	if a.mach != nil {
		all = append(all, a.mach.Close())
		a.mach = nil
		a.machErr = errs.New(errs.Internal, "wire: the app is closed")
	}
	return errors.Join(all...)
}

// close releases what open built.
func (s *state) close() error {
	var all []error
	for _, b := range s.backends {
		all = append(all, b.Close())
	}
	if s.machine != nil {
		all = append(all, s.machine.Close())
	}
	if s.caches != nil {
		all = append(all, s.caches.Close())
	}
	return errors.Join(all...)
}

var (
	_ op.Runner           = (*App)(nil)
	_ opsmemory.Env       = (*App)(nil)
	_ opsmemory.Mover     = (*App)(nil)
	_ opsstores.Syncer    = (*App)(nil)
	_ opscontent.Env      = (*App)(nil)
	_ opstag.Env          = (*App)(nil)
	_ opscomment.Env      = (*App)(nil)
	_ opslink.Env         = (*App)(nil)
	_ opssettings.Env     = (*App)(nil)
	_ opsinfra.Env        = (*App)(nil)
	_ opsreminder.Env     = (*App)(nil)
	_ opsreminder.Actorer = (*App)(nil)
)

// SyncStores is the 0.1.18 half of `lore sync` (O12b: sync is one
// command, not a store verb): every attached store except the repo store,
// or only the one named.
func (a *App) SyncStores(ctx context.Context, only string) (opsstores.SyncOut, error) {
	if _, err := a.Engine(ctx); err != nil {
		return opsstores.SyncOut{}, err
	}
	return opsstores.SyncAll(ctx, a, only)
}

// builtinRegistry is the schema every App validates against: the built-in
// tables (kinds join from the caches at run time).
func builtinRegistry() (*schema.Registry, error) {
	return schema.NewRegistry(schema.Builtin()...)
}
