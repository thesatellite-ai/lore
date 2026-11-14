package doctor

// all.go — `lore doctor --all` and the active-setup section of plain
// `lore doctor` (ONBOARDING_DESIGN 6.1-6.4, 10.10).
//
//	doctor.run ─▶ the single-repo report (Run, unchanged keys)
//	          ─▶ SetupEnv? (optional upgrade; absent: no setup section,
//	          │   --all is E_UNSUPPORTED)
//	          ─▶ Store.Config ─▶ setups (--all: every one, or the --setup one;
//	          │   plain: the active one, Store.Active)
//	          ─▶ per setup the 6.1 check list by scope: setup, member/admin,
//	          │   admin, each store, each repo, repo × tool, each tool
//	          │   (repos: known_repos under the setup's folders + this repo)
//	          ─▶ prober checks: cache (60 s, skipped by --fresh) ─▶ one
//	          │   Prober.Probe batch (the 4-at-once cap lives in the prober)
//	          ─▶ tool checks: skill.Installer.Status (4.7.1)
//	          ─▶ machine: folders.overlap (logic/onboard.Overlaps)
//	          ─▶ counts, per-setup status, overall status ─▶ exit 0/1/2 (6.4)
//
// Invariant (6.4): a probe that could not run (status error) degrades,
// never breaks — the thing it tests may be fine. Only an error-level fail
// breaks.

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/skill"
)

// SetupEnv is the optional upgrade of Env that --all and the [setup]
// section need; *wire.App implements it (setup.Onboard's store and prober,
// RepoTop, and skill.InstallEnv for the tools checks). An Env without it
// still runs the single-repo report: optional upgrade, not required
// config, so a binary without the setup engine never lies about it.
type SetupEnv interface {
	SetupStore(ctx context.Context) (setup.Store, error)
	SetupProber(ctx context.Context) (setup.Prober, error)
	// RepoTop is the current repo's top folder ("" or an error outside
	// one); its repo checks always run (6.1 "the current repo").
	RepoTop(ctx context.Context) (string, error)
	skill.InstallEnv
}

// HomeEnv is the optional check that the lore home can be written. Why:
// a read works on a home lore cannot write (its caches go to a temp folder
// removed at exit, so a CI runner's read-only HOME is fine), which also
// means nothing then fails on the person's own machine either: setups,
// store clones and machine state are silently lost at exit. doctor --all
// (the machine check) names it as its finding; plain doctor stays quiet
// so a CI clone reads HEALTHY.
type HomeEnv interface {
	// HomeWritable is nil when the lore home can be written, else an
	// E_READ_ONLY_FS error naming the home and the fix.
	HomeWritable(ctx context.Context) error
}

// CacheEnv is the optional source of the 60-second result cache (6.1).
// Without it every run probes; with it, --fresh skips the reads. The cache
// may be shared by every lore process on the machine (wire keeps it in
// machine.db); a cache that also implements io.Closer is closed when the
// run ends.
type CacheEnv interface {
	DoctorCache(ctx context.Context) Cache
}

// StampEnv is an optional upgrade of CacheEnv: the stamp of what a setup's
// checks look at (its entry, the key file it names, its org entry). A
// cached result made under another stamp is not reused, so a setup edit,
// a key change or a store change re-probes the checks it affects at once
// instead of serving a result about inputs that no longer exist. Without
// it the stamp is empty and only the TTL ages results (the in-process
// cache of a binary without the setup engine).
type StampEnv interface {
	DoctorStamp(ctx context.Context, e onboard.SetupEntry) string
}

// HealthSink is an optional upgrade of SetupEnv: it keeps each setup's
// latest doctor results so setup.list's Health cell (ONB 5.4) shows the
// last run, from this process or another (a CLI doctor while the page is
// open), and after the 60 s probe cache has expired. A failed record is
// bookkeeping only: the run's own report is unaffected.
type HealthSink interface {
	RecordHealth(ctx context.Context, setup string, results []onboard.CheckResult) error
}

// CacheTTL is how long a probe result is reused (6.1: "so the page and the
// CLI do not probe twice in a row").
const CacheTTL = 60 * time.Second

// Targets of per-item checks (setup.ProbeRequest.Target): the prober and
// the page parse them, so they are built here only. A setup-wide check has
// TargetSetup (""), a store "store <name>", a tool its profile name, a repo
// its absolute root, repo × tool "<tool> <root>" (tool names hold no
// space; roots are absolute, so the forms never collide).
const (
	TargetSetup   = ""
	TargetMachine = "machine"
	targetStore   = "store "
	// storeMe is the personal store's name in a target (`--to me`).
	storeMe = "me"
)

// TargetStore is the target of a store check.
func TargetStore(name string) string { return targetStore + name }

// TargetRepoTool is the target of a repo × tool check.
func TargetRepoTool(tool, root string) string { return tool + " " + root }

// CacheKey identifies one cached result (setup, check, target) and the
// inputs it was checked with (Stamp: StampEnv's stamp, plus the probed url
// for a store attached by a repo). A Get whose stamp differs from the
// stored one is a miss.
type CacheKey struct {
	Setup  string
	Check  onboard.CheckID
	Target string
	Stamp  string
}

// Cache holds recent check results. Get returns a result only while it is
// younger than the cache's TTL.
type Cache interface {
	Get(k CacheKey) (onboard.CheckResult, bool)
	Put(k CacheKey, r onboard.CheckResult)
}

// MemCache is an in-memory Cache keyed by CacheKey, aged by CheckedAt.
// It is a value the composition root owns (no package state). One entry
// per (setup, check, target): a Put under a new stamp replaces the old one.
type MemCache struct {
	mu  sync.Mutex
	ttl time.Duration
	now func() time.Time
	m   map[CacheKey]memEntry // keys without their stamp (slot)
}

// memEntry is one cached result and the stamp it was made under.
type memEntry struct {
	r     onboard.CheckResult
	stamp string
}

// slot is k without its stamp: the one place a check's result is kept.
func (k CacheKey) slot() CacheKey { k.Stamp = ""; return k }

// NewMemCache returns a cache with ttl (CacheTTL when <= 0) and the clock
// now (time.Now when nil).
func NewMemCache(ttl time.Duration, now func() time.Time) *MemCache {
	if ttl <= 0 {
		ttl = CacheTTL
	}
	if now == nil {
		now = time.Now
	}
	return &MemCache{ttl: ttl, now: now, m: map[CacheKey]memEntry{}}
}

// Get implements Cache.
func (c *MemCache) Get(k CacheKey) (onboard.CheckResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k.slot()]
	if !ok || e.stamp != k.Stamp || !Fresh(e.r.CheckedAt, c.now(), c.ttl) {
		return onboard.CheckResult{}, false
	}
	return e.r, true
}

// Fresh reports whether a result checked at may still be served at now
// under ttl: younger than ttl, and not from the future (a clock set back
// since, or a hand-edited cache: re-probe rather than trust it).
func Fresh(at, now time.Time, ttl time.Duration) bool {
	age := now.Sub(at)
	return age >= 0 && age < ttl
}

// Put implements Cache.
func (c *MemCache) Put(k CacheKey, r onboard.CheckResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k.slot()] = memEntry{r: r, stamp: k.Stamp}
}

// ForgetTarget drops every setup's results about target (TargetStore of a
// store that just synced): they are older than what lore now knows.
func (c *MemCache) ForgetTarget(target string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if k.Target == target {
			delete(c.m, k)
		}
	}
}

// Results are the unexpired results cached for setup, ordered by check id
// then target (map order is random; the Health cell and tests want a
// stable list). setup.list's Health cell reads them through
// App.SetupHealth (ONB 5.4, 6.1): a finished setup has no progress rows,
// so a recent doctor run is the only health it has without probing.
func (c *MemCache) Results(setup string) []onboard.CheckResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	var keys []CacheKey
	for k, e := range c.m {
		if k.Setup == setup && Fresh(e.r.CheckedAt, now, c.ttl) {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(x, y CacheKey) int {
		if c := strings.Compare(string(x.Check), string(y.Check)); c != 0 {
			return c
		}
		return strings.Compare(x.Target, y.Target)
	})
	out := make([]onboard.CheckResult, 0, len(keys))
	for _, k := range keys {
		out = append(out, c.m[k].r)
	}
	return out
}

// SetupReport is one setup's block (6.3 setups[]).
type SetupReport struct {
	Name    string                `json:"name"`
	Kind    string                `json:"kind"`
	Org     string                `json:"org,omitempty"`
	Default bool                  `json:"default"`
	Folders []string              `json:"folders"`
	Status  Health                `json:"status"`
	Checks  []onboard.CheckResult `json:"checks"`
	// Why is how the active setup was chosen (plain doctor only; 3.3).
	Why string `json:"why,omitempty"`
	// summary feeds the text's group lines (identity email, github user,
	// tool skill versions); not part of the JSON.
	summary setupSummary
}

// setupSummary is what the 6.2 group lines print.
type setupSummary struct {
	email, user, alias, backend string
	// https: the setup's route is https with the user's gh login.
	https       bool
	toolVersion map[string]string
	// storeRepos: the repos attaching each attached store, by target.
	storeRepos map[string][]string
	// localPersonal: the personal store is a folder on this machine (the
	// join lines' "local" choice): no copy of it exists anywhere else
	// (H-17: a dead laptop lost every personal note, and doctor --all
	// showed only "read ✓ write ✓").
	localPersonal bool
}

// MachineReport is the machine-wide block (6.3 machine).
type MachineReport struct {
	Checks []onboard.CheckResult `json:"checks"`
}

// Counts are the results by status (6.3 counts).
type Counts struct {
	OK    int `json:"ok"`
	Warn  int `json:"warn"`
	Fail  int `json:"fail"`
	Skip  int `json:"skip"`
	Error int `json:"error"`
}

// add counts one result; a note (info-level warning) counts as ok.
func (c *Counts) add(r onboard.CheckResult) {
	if r.Note() {
		c.OK++
		return
	}
	switch r.Status {
	case onboard.StatusOK:
		c.OK++
	case onboard.StatusWarn:
		c.Warn++
	case onboard.StatusFail:
		c.Fail++
	case onboard.StatusSkip:
		c.Skip++
	case onboard.StatusError:
		c.Error++
	}
}

// statusOf is the 6.4 decision over check results: an error-level fail
// breaks; a warn, a warn-level fail or a probe that could not run
// (StatusError) degrades; only ok/skip is healthy.
func statusOf(rs []onboard.CheckResult) Health {
	st := StatusHealthy
	for _, r := range rs {
		if r.Note() {
			continue // info: worth reading, nothing to fix
		}
		switch r.Status {
		case onboard.StatusFail:
			if r.Level == onboard.LevelError {
				return StatusBroken
			}
			st = StatusDegraded
		case onboard.StatusWarn, onboard.StatusError:
			st = StatusDegraded
		}
	}
	return st
}

// worse is the worse of two statuses.
func worse(a, b Health) Health {
	rank := func(s Health) int {
		switch s {
		case StatusBroken:
			return exitBroken
		case StatusDegraded:
			return exitDegraded
		}
		return exitHealthy
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// item is one planned check: run by the prober, or by the skill status.
type item struct {
	setup  onboard.SetupEntry
	check  onboard.Check
	target string
	// tool / repo fill the fix template ({tool}, {repo_root}).
	tool, repo string
	// stamp is the setup's StampEnv stamp ("" without one).
	stamp string
	// attached is set for a store a known repo attaches (#16): the prober
	// gets its url as the target, the result keeps the "store <name>" one.
	attached *attachedStore
}

// key is the item's cache key: the setup stamp, and for an attached store
// its url and branch too, so the same name pointed at another store is a
// miss.
func (it item) key() CacheKey {
	st := it.stamp
	if it.attached != nil {
		st += "|" + it.attached.url + "|" + it.attached.branch
	}
	return CacheKey{Setup: it.setup.Name, Check: it.check.ID, Target: it.target, Stamp: st}
}

// present is a result (fresh or cached) as this run reports it: an
// attached store's detail names the repos attaching it now.
func (it item) present(r onboard.CheckResult) onboard.CheckResult {
	if it.attached != nil {
		return it.attached.withRepos(r)
	}
	return r
}

// probeTarget is the target the prober is asked about.
func (it item) probeTarget() string {
	if it.attached != nil {
		return it.attached.url
	}
	return it.target
}

// Scope lists of 6.1 (the kind filter comes from onboard.CheckKinds).
var (
	setupChecks = []onboard.CheckID{
		onboard.CheckSetupConfig, onboard.CheckSetupOrgEntry,
		onboard.CheckIdentityEmailFormat, onboard.CheckIdentityRoster, onboard.CheckIdentityPeriod,
		onboard.CheckIdentityTopLevel, onboard.CheckIdentityTopLevelKeep, onboard.CheckIdentityActorCopies,
		onboard.CheckGitHubSSHKey, onboard.CheckGitHubSSHAuth, onboard.CheckGitHubSSHUser, onboard.CheckGitHubKeyMatches,
		onboard.CheckGitHubAliasConfig, onboard.CheckGitHubGHAccount, onboard.CheckGitHubIdentityEmail,
		onboard.CheckSecretsBackendPresent, onboard.CheckFoldersExist, onboard.CheckFoldersReachable,
		onboard.CheckInvitePending, onboard.CheckInviteGitHubAccess,
		onboard.CheckStoreAddress, onboard.CheckOrgSource, // SOURCE_OF_TRUTH rows 7, 9
	}
	storeChecks    = []onboard.CheckID{onboard.CheckStoreRead, onboard.CheckStoreWrite, onboard.CheckStoreMarker, onboard.CheckStorePrivate}
	repoChecks     = []onboard.CheckID{onboard.CheckIdentityGitEmail, onboard.CheckLiveMode}
	repoToolChecks = []onboard.CheckID{onboard.CheckToolsPointerFile, onboard.CheckToolsHooks}
	toolChecks     = []onboard.CheckID{onboard.CheckToolsSkillInstalled, onboard.CheckToolsSkillVersion, onboard.CheckToolsSkillModified}
)

// appliesTo reports whether the check runs for kind (an empty kind list
// means every kind).
func appliesTo(id onboard.CheckID, k onboard.Kind) bool {
	ks := onboard.CheckKinds(id)
	return len(ks) == 0 || slices.Contains(ks, k)
}

// checkFor is the catalog check with its context level (6.1 store levels).
func checkFor(id onboard.CheckID, k onboard.Kind, role onboard.StoreRole) (onboard.Check, bool) {
	step, _ := onboard.StepOfCheck(id, k)
	return onboard.CheckFor(id, step, onboard.LevelCtx{Kind: k, Store: role}, onboard.FixDefault)
}

// storeTarget is one store of a setup and its role.
type storeTarget struct {
	name string
	role onboard.StoreRole
}

// storesOf are the stores a setup's store checks run on: `me` unless the
// setup has none, and the org's roster store for member/admin (named by
// the org; the prober resolves it).
func storesOf(e onboard.SetupEntry) []storeTarget {
	var out []storeTarget
	if e.PersonalStore != onboard.PersonalStoreNone {
		out = append(out, storeTarget{storeMe, onboard.StoreRoleMe})
	}
	if e.Kind.HasOrg() && e.Org != "" {
		out = append(out, storeTarget{e.Org, onboard.StoreRoleOrg})
	}
	return out
}

// plan is the 6.1 check list of one setup over its repos and the stores
// those repos attach: prober items and tool (skill status) items.
func plan(e onboard.SetupEntry, repos []string, attached []attachedStore) (probe, tools []item) {
	add := func(dst *[]item, id onboard.CheckID, role onboard.StoreRole, target, tool, repo string) *item {
		if !appliesTo(id, e.Kind) {
			return nil
		}
		c, ok := checkFor(id, e.Kind, role)
		if !ok {
			return nil
		}
		*dst = append(*dst, item{setup: e, check: c, target: target, tool: tool, repo: repo})
		return &(*dst)[len(*dst)-1]
	}
	for _, id := range setupChecks {
		add(&probe, id, onboard.StoreRoleNone, TargetSetup, "", "")
	}
	for _, s := range storesOf(e) {
		for _, id := range storeChecks {
			add(&probe, id, s.role, TargetStore(s.name), "", "")
		}
	}
	for i := range attached {
		as := &attached[i]
		for _, id := range storeChecks {
			if it := add(&probe, id, as.role(), as.target, "", ""); it != nil {
				it.attached = as
			}
		}
	}
	for _, root := range repos {
		for _, id := range repoChecks {
			add(&probe, id, onboard.StoreRoleNone, root, "", root)
		}
		for _, t := range e.Tools {
			for _, id := range repoToolChecks {
				add(&probe, id, onboard.StoreRoleNone, TargetRepoTool(t, root), t, root)
			}
		}
	}
	for _, t := range e.Tools {
		for _, id := range toolChecks {
			add(&tools, id, onboard.StoreRoleNone, t, t, "")
		}
	}
	return probe, tools
}

// setupRun is everything one doctor run over setups needs.
type setupRun struct {
	env    SetupEnv
	store  setup.Store
	prober setup.Prober
	cache  Cache
	sink   HealthSink // nil: nothing keeps the results for Home
	// stamps / stores are StampEnv and RepoStoresEnv when the env has them.
	stamps StampEnv
	stores RepoStoresEnv
	fresh  bool
	home   string
	now    func() time.Time
}

// allOut is what the setup part adds to the Report.
type allOut struct {
	setups  []SetupReport
	machine MachineReport
	counts  Counts
	status  Health
	// warning is a setup-part problem that is not a check (degrades).
	warning string
	// disabled are the disabled setups --all skipped, one line each.
	disabled []string
	// storeWarnings: repos whose .lore/stores.yaml could not be read, so
	// their stores were not checked (degrades; #16).
	storeWarnings []string
	// unchecked: known repos under no setup's folders whose stores were not
	// checked because no setup applies to them (no default), one info line
	// each (UncheckedNote; never degrades).
	unchecked []string
}

// DisabledNote is doctor --all's one line about a disabled setup it skipped.
func DisabledNote(name string) string {
	return "setup " + name + " is disabled: skipped (lore setup enable --name " + name + ")"
}

// errNoSetupEngine is --all in a binary without the setup engine.
func errNoSetupEngine() error {
	return errs.New(errs.Unsupported, "lore doctor --all needs the setup engine, which this binary does not have").
		WithHint("run lore doctor without --all")
}

// newSetupRun opens the store and prober.
func newSetupRun(ctx context.Context, env SetupEnv, fresh bool) (*setupRun, error) {
	st, err := env.SetupStore(ctx)
	if err != nil {
		return nil, err
	}
	pr, err := env.SetupProber(ctx)
	if err != nil {
		return nil, err
	}
	home, err := env.SkillHome(ctx)
	if err != nil {
		return nil, err
	}
	sr := &setupRun{env: env, store: st, prober: pr, fresh: fresh, home: home, now: time.Now}
	if ce, ok := env.(CacheEnv); ok {
		sr.cache = ce.DoctorCache(ctx)
	}
	if hs, ok := env.(HealthSink); ok {
		sr.sink = hs
	}
	if st, ok := env.(StampEnv); ok {
		sr.stamps = st
	}
	if rs, ok := env.(RepoStoresEnv); ok {
		sr.stores = rs
	}
	return sr, nil
}

// close releases the cache when it holds a resource (a machine.db handle).
func (sr *setupRun) close() {
	if c, ok := sr.cache.(io.Closer); ok {
		_ = c.Close() // a read-mostly handle: nothing is lost on a failed close
	}
}

// runAll is `lore doctor --all`: every setup (or only `only` when named),
// plus the machine checks.
func (sr *setupRun) runAll(ctx context.Context, only string) (allOut, error) {
	var out allOut
	cfg, err := sr.store.Config(ctx)
	if err != nil {
		return configBroken(err, sr.now()), nil
	}
	entries := cfg.Enabled()
	if only == "" {
		for _, s := range cfg.Setups {
			if s.Disabled {
				out.disabled = append(out.disabled, DisabledNote(s.Name))
			}
		}
	}
	if only != "" {
		e, ok := cfg.Setup(only)
		if !ok {
			return out, errs.New(errs.SetupNotFound, "no setup named "+only).WithHint("lore setup list shows the setups")
		}
		entries = []onboard.SetupEntry{e}
	}
	repos, unassigned, warning := sr.reposBySetup(ctx, cfg.Enabled())
	out.warning = warning
	storeRepos, unchecked := sr.storeReposOf(ctx, cfg.Enabled(), entries, repos, unassigned, only)
	out.unchecked = unchecked
	attached, swarn := sr.attachedBySetup(ctx, entries, storeRepos)
	out.storeWarnings = swarn
	out.setups = sr.runSetups(ctx, entries, repos, attached)
	out.machine.Checks = sr.machineChecks(cfg.Setups)
	out.status = StatusHealthy
	for _, s := range out.setups {
		out.status = worse(out.status, s.Status)
		for _, r := range s.Checks {
			out.counts.add(r)
		}
	}
	out.status = worse(out.status, statusOf(out.machine.Checks))
	for _, r := range out.machine.Checks {
		out.counts.add(r)
	}
	if warning != "" || len(out.storeWarnings) > 0 {
		out.status = worse(out.status, StatusDegraded)
	}
	return out, nil
}

// configBroken is the --all report when ~/.lore/config.yaml cannot be read
// or used (an invalid setups list, ~/.lore a file or unreadable): that is
// itself the one finding (setup.config, error), and the error's hint (what
// to do) is the fix text when it has one.
func configBroken(err error, at time.Time) allOut {
	var out allOut
	fix := onboard.Fix{Text: "fix ~/.lore/config.yaml, then run lore config check", Command: []string{"lore", "config", "check"}}
	var e *errs.Error
	if errors.As(err, &e) && e.Hint != "" {
		fix = onboard.Fix{Text: e.Hint, Command: []string{}}
	}
	r := onboard.CheckResult{Check: onboard.CheckSetupConfig, Status: onboard.StatusFail, Level: onboard.LevelError,
		Target: TargetMachine, Detail: err.Error(), CheckedAt: at, Fix: fix}
	out.machine.Checks = []onboard.CheckResult{r}
	out.counts.add(r)
	out.status = StatusBroken
	return out
}

// runActive is the [setup] section of plain doctor: the active setup only
// (3.3), with the current repo's checks. ok is false when no setup is
// active (lore behaves as before).
func (sr *setupRun) runActive(ctx context.Context, flag string) (SetupReport, bool, error) {
	act, err := sr.store.Active(ctx, flag)
	if err != nil {
		return SetupReport{}, false, err
	}
	if act.Name == "" || act.Why == setup.WhyNone {
		return SetupReport{}, false, nil
	}
	cfg, err := sr.store.Config(ctx)
	if err != nil {
		return SetupReport{}, false, err
	}
	e, ok := cfg.Setup(act.Name)
	if !ok {
		return SetupReport{}, false, errs.New(errs.SetupNotFound, "no setup named "+act.Name).WithHint("lore setup list shows the setups")
	}
	var repos []string
	if top := sr.repoTop(ctx); top != "" {
		repos = []string{top}
	}
	rs := sr.runSetups(ctx, []onboard.SetupEntry{e}, map[string][]string{e.Name: repos}, nil)
	rs[0].Why = act.Why
	return rs[0], true, nil
}

// repoTop is the current repo's top folder, "" outside one.
func (sr *setupRun) repoTop(ctx context.Context) string {
	top, err := sr.env.RepoTop(ctx)
	if err != nil {
		return ""
	}
	return top
}

// reposBySetup assigns each known repo, plus the current one, to the setup
// whose folders hold it most specifically (3.3; a tie is folders.overlap's
// finding, so the repo is checked under neither). The current repo, when
// no folder holds it, goes to the active setup. The known repos no
// setup's folders hold (and no tie) are returned as unassigned: their
// stores are checked under the default setup (storeReposOf). A failure to
// list repos is returned as a warning: the setup checks still run.
func (sr *setupRun) reposBySetup(ctx context.Context, setups []onboard.SetupEntry) (map[string][]string, []string, string) {
	out := map[string][]string{}
	var unassigned []string
	var warning string
	seen := map[string]bool{}
	assign := func(root, fallback string) {
		if root == "" || seen[root] {
			return
		}
		seen[root] = true
		if _, _, inStore := rowfs.StoreTop(root); inStore {
			return // a lore store's checkout is no repo of any setup (STORE layout 1.5)
		}
		m := onboard.MatchFolder(setups, root, sr.home, filepath.EvalSymlinks)
		switch {
		case m.Found:
			out[m.Match.Setup] = append(out[m.Match.Setup], root)
		case !m.Ambiguous() && fallback != "":
			out[fallback] = append(out[fallback], root)
		case !m.Ambiguous():
			unassigned = append(unassigned, root)
		}
	}
	if top := sr.repoTop(ctx); top != "" {
		fallback := ""
		if act, err := sr.store.Active(ctx, op.SetupOf(ctx)); err == nil && act.Why != setup.WhyNone {
			fallback = act.Name
		}
		assign(top, fallback)
	}
	known, err := sr.store.KnownRepos(ctx)
	if err != nil {
		warning = "known repos could not be listed, so only this repo was checked: " + err.Error()
	}
	for _, k := range known {
		assign(k.Root, "")
	}
	return out, unassigned, warning
}

// runSetups runs every entry's checks: one prober batch for all of them
// (the machine-wide cap is the prober's), cache first unless fresh.
func (sr *setupRun) runSetups(ctx context.Context, entries []onboard.SetupEntry, repos map[string][]string, attached map[string][]attachedStore) []SetupReport {
	reports := make([]SetupReport, len(entries))
	var probeItems []item
	var owner []int // probeItems[i] belongs to reports[owner[i]]
	toolItems := make([][]item, len(entries))
	for i, e := range entries {
		reports[i] = SetupReport{Name: e.Name, Kind: string(e.Kind), Org: e.Org, Default: e.Default,
			Folders: append([]string{}, e.Folders...), Checks: []onboard.CheckResult{},
			summary: setupSummary{email: firstEmail(e), user: e.GitHub.User, alias: e.GitHub.HostAlias, https: e.GitHub.IsHTTPS(),
				backend: e.Secrets.Backend, toolVersion: map[string]string{}, localPersonal: strings.HasPrefix(e.PersonalStore, fileuri.Scheme+"://")}}
		p, t := plan(e, repos[e.Name], attached[e.Name])
		reports[i].summary.storeRepos = map[string][]string{}
		for _, as := range attached[e.Name] {
			reports[i].summary.storeRepos[as.target] = as.repos
		}
		stamp := ""
		if sr.stamps != nil {
			stamp = sr.stamps.DoctorStamp(ctx, e)
		}
		for k := range p {
			p[k].stamp = stamp
		}
		for k := range t {
			t[k].stamp = stamp
		}
		probeItems = append(probeItems, p...)
		for range p {
			owner = append(owner, i)
		}
		toolItems[i] = t
	}
	probed := sr.probe(ctx, probeItems)
	for j, r := range probed {
		reports[owner[j]].Checks = append(reports[owner[j]].Checks, r)
	}
	for i := range entries {
		rs, versions := sr.toolResults(ctx, toolItems[i])
		reports[i].Checks = append(reports[i].Checks, rs...)
		reports[i].summary.toolVersion = versions
		reports[i].Status = statusOf(reports[i].Checks)
		if sr.sink != nil && ctx.Err() == nil {
			// Bookkeeping for Home's Health cell; a failed write only
			// leaves Home on the older result, never fails the run. A run
			// whose caller went away (ctx done) keeps nothing: its results
			// say "context canceled", not how the setup is.
			_ = sr.sink.RecordHealth(ctx, entries[i].Name, reports[i].Checks)
		}
	}
	return reports
}

// firstEmail is the setup identity's main email.
func firstEmail(e onboard.SetupEntry) string {
	if len(e.Identity.Emails) == 0 {
		return ""
	}
	return e.Identity.Emails[0]
}

// localProbes are the probe families that only read files in the repos
// and folders on this machine (a pointer file, hook settings, a folder):
// cheap to run, and not covered by the setup stamp, so a cached result
// outlived the fix. A render that stitched CLAUDE.md, or a chmod that
// made it writable, still showed "CLAUDE.md does not import .lore/LORE.md"
// with its fix line for a minute while the file was right.
var localProbes = []onboard.ProbeID{onboard.ProbeTools, onboard.ProbeFS}

// rowChecks are the source-of-truth checks whose answer is the row files
// and stores.yaml of the repo open here (SOURCE_OF_TRUTH rows 2-3 and 7):
// neither is in the setup stamp, so a cached result outlived its own fix
// (`lore actor refresh` ran, doctor still showed the stale copy for a
// minute) and one repo's answer was served in another.
var rowChecks = []onboard.CheckID{onboard.CheckIdentityActorCopies, onboard.CheckStoreAddress}

// Cacheable reports whether a check's result may be served from the
// cache: every check except the local file reads (localProbes), the
// row-file checks (rowChecks) and the repo checks other than the attached
// stores' reads (repo.init and repo.org read the repo's own files).
func Cacheable(c onboard.Check) bool {
	if slices.Contains(localProbes, c.Probe) || slices.Contains(rowChecks, c.ID) {
		return false
	}
	return c.Probe != onboard.ProbeRepo || c.ID == onboard.CheckRepoStores
}

// probe runs the prober items, reading the cache first unless fresh, and
// writes every new result back.
func (sr *setupRun) probe(ctx context.Context, items []item) []onboard.CheckResult {
	out := make([]onboard.CheckResult, len(items))
	var reqs []setup.ProbeRequest
	var idx []int
	for i, it := range items {
		if it.attached.skipsWrite(it.check.ID) {
			out[i] = it.present(sr.result(it, onboard.StatusSkip, detailAttachedReadOnly, nil, 0))
			continue
		}
		if sr.cache != nil && !sr.fresh && Cacheable(it.check) {
			if r, ok := sr.cache.Get(it.key()); ok {
				// The page's per-check Details show "cached, from <time>"
				// rather than no calls (the probe's calls are in that run).
				probe.RecordCached(probe.WithLabel(ctx, string(it.check.ID)), cachedWhat(it), r.CheckedAt)
				out[i] = it.present(r)
				continue
			}
		}
		reqs = append(reqs, setup.ProbeRequest{Check: it.check, Setup: it.setup, Target: it.probeTarget()})
		idx = append(idx, i)
	}
	if len(reqs) == 0 {
		return out
	}
	res := sr.prober.Probe(ctx, reqs)
	for j, i := range idx {
		var pr setup.ProbeResult
		if j < len(res) {
			pr = res[j]
		} else {
			// The prober broke its one-result-per-request contract: the
			// check could not run (degrades, never breaks; 6.4).
			pr = setup.ProbeResult{Status: onboard.StatusError, Detail: "the prober returned no result"}
		}
		if as := items[i].attached; as != nil {
			pr = as.adjust(pr)
		}
		r := sr.result(items[i], pr.Status, pr.Detail, pr.Vars, pr.TookMS)
		// An aborted run (ctx done: the page left Doctor mid-run) is never
		// cached: its timeouts and "context canceled" would be every lore
		// process's answer for the next CacheTTL.
		if sr.cache != nil && Cacheable(items[i].check) && ctx.Err() == nil {
			sr.cache.Put(items[i].key(), r)
		}
		out[i] = items[i].present(r)
	}
	return out
}

// refusedFixText is the fix shown when the check's fix template cannot be
// filled for this case: the check's own title as the goal, and where to
// look (the detail line says what failed).
func refusedFixText(c onboard.Check) string {
	goal := c.Title
	if goal == "" {
		goal = string(c.ID)
	}
	// The title is kept as written: lowercasing its first letter turned
	// "GitHub accepts the key" into "gitHub accepts the key".
	return "No ready-made fix for this case. Goal: " + goal +
		". The line above says what failed; fix that by hand, then run lore doctor --all --fresh"
}

// cachedWhat names a cached item in its trace call: the check id, and the
// target when the check runs per store, repo or tool.
func cachedWhat(it item) string {
	if it.target == TargetSetup {
		return string(it.check.ID)
	}
	return string(it.check.ID) + " " + it.target
}

// result builds the 2.2 CheckResult: the catalog level, the fix filled
// only for warn/fail.
func (sr *setupRun) result(it item, st onboard.Status, detail string, vars map[string]string, took int64) onboard.CheckResult {
	r := onboard.CheckResult{Check: it.check.ID, Status: st, Level: it.check.Level, Target: it.target,
		Detail: detail, CheckedAt: sr.now(), TookMS: took}
	if st == onboard.StatusWarn && vars[onboard.VarLevel] == string(onboard.LevelInfo) {
		r.Level = onboard.LevelInfo // a note, nothing to do now (as setup's result does)
	}
	if st == onboard.StatusWarn || st == onboard.StatusFail {
		r.Fix = sr.fix(it, vars)
	}
	return r
}

// fix fills the check's fix template from the setup, the item and the
// probe's vars. A refused fill (a value that fails its rule) keeps only
// the reason: never a half-filled command.
func (sr *setupRun) fix(it item, vars map[string]string) onboard.Fix {
	stepID, _ := onboard.StepOfCheck(it.check.ID, it.setup.Kind)
	variant := onboard.FixVariant(vars[onboard.VarFixVariant])
	if variant == onboard.FixDefault && it.repo != "" && it.check.ID == onboard.CheckIdentityGitEmail {
		// Doctor checks git_email per repo: the per-repo fix (git -C).
		variant = onboard.FixRepo
	}
	if variant != onboard.FixDefault {
		it.check.Fix = onboard.FixFor(it.check.ID, stepID, variant)
	}
	all := map[string]string{
		onboard.VarSetup: it.setup.Name,
		onboard.VarOrg:   it.setup.Org,
		onboard.VarEmail: firstEmail(it.setup),
		onboard.VarUser:  it.setup.GitHub.User,
		onboard.VarAlias: it.setup.GitHub.HostAlias,
	}
	if it.tool != "" {
		all[onboard.VarTool] = it.tool
	}
	if it.repo != "" {
		all[onboard.VarRepoRoot] = it.repo
	}
	for k, v := range vars {
		all[k] = v
	}
	step, _ := onboard.StepByID(stepID)
	f, err := onboard.FillFix(it.check.ID, it.check.Fix, all, step)
	if err != nil {
		// The template needs a value this case lacks (store.write's {repo}
		// for a store that is not on GitHub): say what to do in plain words,
		// never the raw E_FIX_REFUSED (it reads like lore broke).
		return onboard.Fix{Text: refusedFixText(it.check), Command: []string{}}
	}
	if f.Command == nil {
		f.Command = []string{}
	}
	return f
}

// toolResults maps skill.status (4.7.1) to the three tool checks. A
// status that cannot be read is a probe that could not run (error).
func (sr *setupRun) toolResults(ctx context.Context, items []item) ([]onboard.CheckResult, map[string]string) {
	versions := map[string]string{}
	if len(items) == 0 {
		return nil, versions
	}
	var names []string
	for _, it := range items {
		if !slices.Contains(names, it.tool) {
			names = append(names, it.tool)
		}
	}
	byTool := map[string]skill.ToolStatus{}
	out, err := sr.skillStatus(ctx, names)
	for _, ts := range out.Tools {
		byTool[ts.Tool] = ts
		if ts.Installed {
			versions[ts.Tool] = ts.Version
		}
	}
	rs := make([]onboard.CheckResult, 0, len(items))
	older := skill.OlderPathLoreOnce(ctx, sr.env, out.BinaryVersion)
	for _, it := range items {
		ts, ok := byTool[it.tool]
		if err != nil || !ok {
			why := "skill status: no result for " + it.tool
			if err != nil {
				why = "skill status: " + err.Error()
			}
			rs = append(rs, sr.result(it, onboard.StatusError, why, nil, 0))
			continue
		}
		// One verdict with the setup page (skill.CheckRow): the same
		// status, detail, fix and info level, so Home's "N to fix" and the
		// Repair button agree with the Tools step's rows.
		row := skill.CheckRow(it.check.ID, ts, out.BinaryVersion, older)
		rs = append(rs, sr.result(it, row.Status, row.Detail, row.Vars, 0))
	}
	return rs, versions
}

// skillStatus reads skill.status for names.
func (sr *setupRun) skillStatus(ctx context.Context, names []string) (skill.StatusOut, error) {
	reg, err := skill.Registry(ctx, sr.env)
	if err != nil {
		return skill.StatusOut{}, err
	}
	ins := skill.Installer{Home: sr.home, Version: sr.env.BinaryVersion(), Registry: reg}
	return ins.Status(ctx, skill.StatusIn{Tools: names})
}

// machineChecks are the machine-wide checks: folders.overlap (6.1, error).
func (sr *setupRun) machineChecks(setups []onboard.SetupEntry) []onboard.CheckResult {
	c, ok := onboard.CheckByID(onboard.CheckFoldersOverlap)
	if !ok {
		return nil
	}
	r := onboard.CheckResult{Check: c.ID, Status: onboard.StatusOK, Level: c.Level, Target: TargetMachine, CheckedAt: sr.now()}
	ovs := onboard.Overlaps(setups, sr.home, filepath.EvalSymlinks)
	if len(ovs) == 0 {
		return []onboard.CheckResult{r}
	}
	var parts []string
	for _, o := range ovs {
		parts = append(parts, o.A.Setup+" "+o.A.Glob+" and "+o.B.Setup+" "+o.B.Glob)
	}
	r.Status = onboard.StatusFail
	r.Detail = "setups tie on a folder: " + strings.Join(parts, "; ")
	// The fix names the first tie; the detail lists them all.
	vars := map[string]string{onboard.VarFolder: ovs[0].A.Glob, onboard.VarSetup: ovs[0].A.Setup, onboard.VarOther: ovs[0].B.Setup}
	f, err := onboard.FillFix(c.ID, c.Fix, vars, onboard.Step{})
	if err != nil {
		f = onboard.Fix{Text: err.Error()}
	}
	if f.Command == nil {
		f.Command = []string{}
	}
	r.Fix = f
	return []onboard.CheckResult{r}
}

// warnSetupPrefix starts the warning of a setup section that could not run.
const warnSetupPrefix = "setup: "

// configUnusable: err says config.yaml cannot be read or used (invalid,
// ~/.lore a file, not writable), the case doctor --all reports as its
// setup.config finding instead of failing. Why: doctor --all is what a
// person runs when something is wrong, so a config.yaml they broke by hand
// (a tab for indent) or a ~/.lore they cannot write must be reported like
// every other problem, never as an error envelope with no report.
func configUnusable(err error) bool {
	return errs.Has(err, errs.ConfigInvalid) || errs.Has(err, errs.BadPath) || errs.Has(err, errs.ReadOnlyFS)
}

// reportConfigBroken puts configBroken's finding into r as the whole --all report.
func reportConfigBroken(r *Report, err error) {
	out := configBroken(err, time.Now())
	r.all, r.Setups, r.Machine, r.Counts = true, []SetupReport{}, out.machine, out.counts
	r.Status = worse(r.Status, out.status)
}

// NoteNoSetups is doctor --all's note on a machine with no setup (C-12):
// nothing names the repos, so none was checked.
const NoteNoSetups = "no setup on this machine, so --all checked no repo (a setup's folders tell it where your repos are)\n" +
	"  check one repo: run lore doctor inside it\n" +
	"  every repo under a folder: lore fix --all --dirs <folder> --dry-run (lists each repo and what it needs, changes nothing)\n" +
	"  make a setup: lore setup"

// addSetups adds the setup part to r: with --all every setup plus the
// machine checks (6.2, 6.3), else the active setup's [setup] section
// (6.2 last line). Both fold into r.Status by the 6.4 rule. A binary
// without the setup engine keeps today's report; --all there is
// E_UNSUPPORTED.
func addSetups(ctx context.Context, env Env, in RunIn, r *Report) error {
	se, ok := env.(SetupEnv)
	if !ok {
		if in.All {
			return errNoSetupEngine()
		}
		return nil
	}
	name := in.Setup
	if name == "" {
		name = op.SetupOf(ctx)
	}
	if he, ok := env.(HomeEnv); ok && in.All {
		if err := he.HomeWritable(ctx); err != nil {
			reportConfigBroken(r, err) // the machine's one finding, as an unwritable config.yaml is
			return nil
		}
	}
	sr, err := newSetupRun(ctx, se, in.Fresh)
	if err != nil {
		if in.All && configUnusable(err) {
			// config.yaml is invalid, ~/.lore is a file (E_BAD_PATH) or not
			// writable (E_READ_ONLY_FS, errs.FileIO): report it as the
			// finding, never fail the report (--all must answer).
			reportConfigBroken(r, err)
			return nil
		}
		if in.All {
			return err
		}
		if !errs.Has(err, errs.Unsupported) {
			r.Warnings = append(r.Warnings, warnSetupPrefix+"the active setup could not be checked: "+err.Error())
			degrade(r)
		}
		return nil
	}
	defer sr.close()
	if in.All {
		out, aerr := sr.runAll(ctx, name)
		if aerr != nil {
			if !configUnusable(aerr) {
				return aerr
			}
			reportConfigBroken(r, aerr)
			return nil
		}
		r.all = true
		r.Setups = out.setups
		if r.Setups == nil {
			r.Setups = []SetupReport{}
		}
		r.Machine = out.machine
		if r.Machine.Checks == nil {
			r.Machine.Checks = []onboard.CheckResult{}
		}
		r.Counts = out.counts
		r.Notes = append(r.Notes, out.disabled...)
		r.disabled = out.disabled
		r.Notes = append(r.Notes, out.unchecked...)
		r.unchecked = out.unchecked
		if len(out.setups) == 0 && len(out.disabled) == 0 {
			// C-12: --all checks repos through the setups' folders; with
			// none it checks no repo, and a bare "healthy" read as "every
			// repo is fine". Say so, with the next steps.
			r.Notes = append(r.Notes, NoteNoSetups)
			r.unchecked = append(r.unchecked, NoteNoSetups)
		}
		if out.warning != "" {
			r.Warnings = append(r.Warnings, warnSetupPrefix+out.warning)
		}
		for _, w := range out.storeWarnings {
			r.Warnings = append(r.Warnings, warnSetupPrefix+w)
		}
		r.Status = worse(r.Status, out.status)
		return nil
	}
	rep, found, aerr := sr.runActive(ctx, name)
	if aerr != nil {
		// E_SETUP_AMBIGUOUS and friends: the setup part is degraded, the
		// rest of the report still stands.
		r.Warnings = append(r.Warnings, warnSetupPrefix+aerr.Error())
		degrade(r)
		return nil
	}
	if found {
		r.Setups = []SetupReport{rep}
		// The summary covers the setup's own checks too (owner decision 6,
		// 2026-11-10): counts never read "all ok" beside a warning that
		// only setups[].checks names.
		for _, c := range rep.Checks {
			r.Counts.add(c)
		}
		r.Status = worse(r.Status, rep.Status)
	}
	return nil
}
