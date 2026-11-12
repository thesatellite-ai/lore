//go:build teame2e

package teame2e

// Shared fixtures for the LORE_TEAM_SPEC.md end-to-end suite. Every test in
// team_e2e_*_test.go drives the real lore binary with real git through
// tests/teame2e/teamharness: people and agents with their own HOMEs,
// a fake GitHub of bare repos reached through real URLs, protected main,
// offline machines, fixed clocks and crash injection.
//
// The suite runs ahead of the code: a test whose commands are not in the
// binary yet skips with "PENDING:" (LORE_TEAM_STRICT=1 makes that a failure).
// Run it with `task lore:test:team`, or by hand:
//
//	go build -tags loretest -o /tmp/lore-team ./cmd/lore
//	LORE_TEAM_BIN=/tmp/lore-team go test -tags teame2e -run 'Test(F|E|Recipe|Race|Leak|SideEffect|Next|Worked)' ./tests/teame2e/

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// envTeamBin names a lore binary built for the team suite. It exists because
// LORE_NOW is honoured only in test builds (§1 "Environment variables"); the
// package's TestMain builds the plain binary, which may ignore LORE_NOW.
const envTeamBin = "LORE_TEAM_BIN"

// Refs, folders and files the spec fixes (§1 "Refs", "Files and folders", §2).
const (
	refMain        = "refs/heads/main"
	refLive        = "refs/lore/live"
	refSignals     = "refs/lore/signals"
	refBackupLive  = "refs/lore-backup/live/"
	trackingPrefix = "refs/lore-remote/origin/"
	loreRefspec    = "+refs/lore/*:refs/lore-remote/origin/*"
	rootFolder     = "_root"
	liveEpochPath  = "_live/epoch"
	liveMetaPath   = "_live/meta.json"
	livePurgedPath = "_live/purged.json"
	storeMarker    = "lore-store.yaml"
	dataDir        = ".lore/data"
	purgedFile     = ".lore/data/_purged.json"
	metaFile       = "_meta.json"
	claimsDir      = "claims"
	resourcesDir   = "resources"
	storeBranch    = "main"
	stagingBranch  = "staging"
)

// Data tables (folder names under .lore/data, saas/pkg/aicoder/lsync/registry.go).
const (
	tblTasks     = "tasks"
	tblMemories  = "memories"
	tblRules     = "rules"
	tblDecisions = "decisions"
	tblMissions  = "missions"
	tblComments  = "comments"
	tblActors    = "actors"
	tblHandoffs  = "handoffs"
)

// The single-repo team's remote and the agency world's remotes.
const (
	slugApp       = "github.com/acme/app"
	slugAppFork   = "github.com/someone/app-fork"
	slugStore     = "github.com/northwind/lore"
	slugOwnTools  = "github.com/northwind/tools"
	slugBetaStore = "github.com/betalabs/lore"
	slugBetaAPI   = "github.com/betalabs/api"
	slugPersonal  = "github.com/mira/lore-me"
	slugLoose     = "github.com/someone/tool"
	// slugForkApp is a fork of acme/app: same committed project id, a remote
	// no org entry matches.
	slugForkApp = "github.com/forker/app"
)

// Org names, folders and identities of the agency world (§5.2).
const (
	orgNorthwind = "northwind"
	orgStaging   = "northwind-stag"
	orgBetalabs  = "betalabs"
	folderOrg    = "org"
	folderAcme   = "acme"
	folderGlobex = "globex"
	aliceKey     = "human:alice@northwind.example"
	aliceBetaKey = "human:alice@betalabs.example"
	dev1Key      = "human:dev1@northwind.example"
	aliceName    = "Alice Doe"
	// alicePersonalKey is alice's top-level (no-org) identity in Q7 tests;
	// distinct from every org key so a cross-org bleed is visible.
	alicePersonalKey = "human:alice@personal.example"
	// agentLabelBackend is the label agents start with in the client-repo
	// recipes; Q11 treats it appearing in a client repo as a leak.
	agentLabelBackend = "backend-1"
	// agentDetailsClient is the orgs[].agent_details value (§2 table, D8)
	// that deliberately shares agent details with client repos.
	agentDetailsClient = "client"
)

// Task statuses (dbent/schema/enums.go) the tests read back.
const (
	statusTodo       = "todo"
	statusInProgress = "in_progress"
	statusDone       = "done"
	statusBlocked    = "blocked"
)

// idPrefixed matches one lore id of a given prefix in CLI output.
func idPrefixed(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + prefix + `_[0-9a-f]{32}\b`)
}

// mustID returns the first id with prefix printed by r.
func mustID(t *testing.T, r th.Result, prefix string) string {
	t.Helper()
	id := idPrefixed(prefix).FindString(r.Out())
	if id == "" {
		t.Fatalf("no %s_ id in output: %s", prefix, r)
	}
	return id
}

// storeLiveRef is the store's live ref: the store's work happens on its
// one branch, storeBranch.
const storeLiveRef = refLive + "/" + storeBranch

// storeSignals is the store branch's signals ref.
func storeSignals() string { return refSignals + "/" + storeBranch }

// liveRowPath is a row's path inside a live ref's tree (§2: one folder per project).
func liveRowPath(folder, table, id string) string { return folder + "/" + table + "/" + id + ".json" }

// claimPath is a claim's path inside a signals ref's tree (§1).
func claimPath(folder, taskID string) string {
	return folder + "/" + claimsDir + "/" + taskID + ".json"
}

// mainRowPath is a row's path on a branch: folder "" is a project at the repo
// root, otherwise the project folder (a store project, a monorepo subfolder).
func mainRowPath(folder, table, id string) string {
	p := dataDir + "/" + table + "/" + id + ".json"
	if folder == "" {
		return p
	}
	return folder + "/" + p
}

// teamBin is the binary under test: LORE_TEAM_BIN when set, else the e2e build.
func teamBin() string {
	if b := os.Getenv(envTeamBin); b != "" {
		return b
	}
	return e2eBin
}

// newTeamWorld starts a parallel test in a fresh World and marks it pending
// unless the binary offers every capability listed.
func newTeamWorld(t *testing.T, caps ...th.Cap) *th.World {
	t.Helper()
	t.Parallel()
	w := th.New(t, teamBin())
	w.RequireTestClock() // LORE_NOW is honoured only by -tags loretest builds
	w.Require(caps...)
	if os.Getenv(envLockRanks) == "1" { // FORMAT_EVOLUTION 7.8 TestLockOrderRanks over the whole suite
		w.CheckLockRanks(knownLockOrderDefect)
	}
	return w
}

// envLockRanks runs every team test under the ranked lock watch.
const envLockRanks = "LORE_TEAM_LOCK_RANKS"

// bugLiveLockWholeCommand: the old binary's live session took L (live.lock)
// at the start of every command and held it to the end, so `lore migrate
// run` took M while holding L, the reverse of 7.5's M < L, and L stayed held
// through the whole branch phase. Fixed: a migrate command's session is
// phased (live_wire.go liveSession.hold), L only for each live step.
var bugLiveLockWholeCommand = th.Bug{ID: "BUG-LIVE-LOCK-WHOLE-COMMAND", Owner: "p3-port-live (live_wire.go)", Open: false}

// knownLockOrderDefect matches the violation bugLiveLockWholeCommand causes
// (held L, then took M) while the bug is open.
func knownLockOrderDefect(o th.LockOrder) bool {
	return bugLiveLockWholeCommand.Open && strings.HasSuffix(o.First, "/live.lock") && strings.HasSuffix(o.Then, "/migrate.lock")
}

// rowDoc is the subset of a row file every test reads; extra fields are kept in
// Raw so a test can look at one the spec adds (assignee, claim_epoch, …).
type rowDoc struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Status string `json:"status"`
	Live   *struct {
		LiveSince  string `json:"live_since"`
		HLC        string `json:"hlc"`
		Deleted    bool   `json:"deleted"`
		LastCopyAt string `json:"last_copy_at"`
	} `json:"_live"`
	Raw map[string]json.RawMessage `json:"-"`
}

// readRow decodes a row file found in ref's tree on a remote; ok is false when absent.
func readRow(t *testing.T, r *th.Remote, ref, path string) (rowDoc, bool) {
	t.Helper()
	body, ok := r.Read(ref, path)
	if !ok {
		return rowDoc{}, false
	}
	var v rowDoc
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode %s:%s: %v\n%s", ref, path, err, body)
	}
	if err := json.Unmarshal([]byte(body), &v.Raw); err != nil {
		t.Fatal(err)
	}
	return v, true
}

// mustRow is readRow that fails when the file is absent.
func mustRow(t *testing.T, r *th.Remote, ref, path string) rowDoc {
	t.Helper()
	v, ok := readRow(t, r, ref, path)
	if !ok {
		t.Fatalf("%s has no %s (files: %v)", ref, path, r.Files(ref))
	}
	return v
}

// rawString reads one string field of a decoded row ("" when absent).
func rawString(t *testing.T, v rowDoc, field string) string {
	t.Helper()
	raw, ok := v.Raw[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return strings.Trim(string(raw), `"`)
	}
	return s
}

// signal is a claim or resource file on a signals ref (§3.4).
type signal struct {
	Kind       string `json:"kind"`
	Task       string `json:"task"`
	Name       string `json:"name"`
	Holder     string `json:"holder"`
	Session    string `json:"session"`
	ClaimToken string `json:"claim_token"`
	ClaimEpoch int    `json:"claim_epoch"`
	Expires    string `json:"expires"`
	RenewedSeq int    `json:"renewed_seq"`
	WorkBranch string `json:"work_branch"`
}

// readSignal decodes a signal file; ok is false when absent.
func readSignal(t *testing.T, r *th.Remote, ref, path string) (signal, bool) {
	t.Helper()
	body, ok := r.Read(ref, path)
	if !ok {
		return signal{}, false
	}
	var s signal
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("decode signal %s: %v\n%s", path, err, body)
	}
	return s, true
}

// taskListed is `lore task list --json` decoded to the fields views promise.
type taskListed struct {
	Data []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
	} `json:"data"`
}

// listedTitles returns the titles a user sees in `lore task list --json`.
func listedTitles(t *testing.T, c *th.Clone, extra ...string) []string {
	t.Helper()
	l := th.DecodeJSON[taskListed](t, c.Lore(append([]string{"task", "list", "--json"}, extra...)...))
	var out []string
	for _, d := range l.Data {
		out = append(out, d.Title)
	}
	return out
}

func hasItem(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------ one repo team

// repoTeam is one code repo (github.com/acme/app) with lore at its root and
// two developers, no orgs: the P2 world of cookbook recipe 2.
type repoTeam struct {
	w        *th.World
	app      *th.Remote
	dev1     *th.Actor
	dev2     *th.Actor
	c1       *th.Clone
	c2       *th.Clone
	tasklist string
}

// repoTeamCaps is what newRepoTeam itself needs beyond today's CLI: every
// repo-team test is about live rows, so --live on task add is the floor.
var repoTeamCaps = []th.Cap{th.Flag("live", "task", "add")}

// capLand gates every test that needs landing (F5) or ride-along (F6):
// `lore land` ships with landing, so before it exists those tests are
// pending rather than red; finishing a task alone (an older command) would
// otherwise make them run against a binary that cannot land yet.
var capLand = th.Cmd("land")

// newRepoTeam bootstraps acme/app: dev1 runs lore init, creates the task
// list, commits and pushes; dev2 clones and adopts. Both configs only set
// live.fetch_ttl 0s (see th.FreshFetch).
func newRepoTeam(t *testing.T, caps ...th.Cap) *repoTeam {
	t.Helper()
	w := newTeamWorld(t, append(append([]th.Cap{}, repoTeamCaps...), caps...)...)
	rt := &repoTeam{w: w, app: w.Remote(slugApp)}
	rt.dev1 = w.Human("dev1", "dev1@acme.example")
	rt.dev2 = w.Human("dev2", "dev2@acme.example")
	for _, a := range []*th.Actor{rt.dev1, rt.dev2} {
		a.WriteConfig(th.Config{Live: th.FreshFetch()})
	}
	rt.c1 = rt.dev1.Clone(rt.app, "app")
	rt.c1.MustLore("init", "--non-interactive", "--name=app")
	rt.tasklist = createTasklist(t, rt.c1)
	rt.c1.CommitAll("lore")
	rt.c1.AdminPush("origin", "main")
	rt.c2 = rt.dev2.Clone(rt.app, "app")
	rt.c2.MustLore("task", "list")
	return rt
}

// createTasklist adds the "Inbox" task list every task add names (the
// existing CLI requires --tasklist; see the status file's QUESTION Q1).
func createTasklist(t *testing.T, c *th.Clone) string {
	t.Helper()
	r := c.MustLore("tasklist", "add", "--title", "Inbox", "--body", "default list", "--json")
	var v struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil || v.Data.ID == "" {
		t.Fatalf("tasklist id: %v %s", err, r)
	}
	return v.Data.ID
}

// addTask adds a task in the repo project and returns its id.
func (rt *repoTeam) addTask(t *testing.T, c *th.Clone, title string, extra ...string) string {
	t.Helper()
	r := c.MustLore(append([]string{"task", "add", title, "--tasklist", rt.tasklist}, extra...)...)
	return mustID(t, r, "tsk")
}

// liveTask reads a task from origin's live ref (root project folder).
func (rt *repoTeam) liveTask(t *testing.T, id string) (rowDoc, bool) {
	t.Helper()
	return readRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
}

// mainTask reads a task from origin's main.
func (rt *repoTeam) mainTask(t *testing.T, id string) (rowDoc, bool) {
	t.Helper()
	return readRow(t, rt.app, refMain, mainRowPath("", tblTasks, id))
}

// ------------------------------------------------------------ agency world

// agency is the §5.2 / §7.5 world: Northwind's store with org/, acme/ and
// globex/ projects, the client repo acme/app linked to acme/, Northwind's own
// repo northwind/tools, and Betalabs as Alice's second company.
//
//	alice    — Northwind owner, also in Betalabs (three org entries, incl. staging)
//	dev1    — Northwind developer
//	acmedev — Acme's own developer: no Northwind config, can clone acme/app only
type agency struct {
	w        *th.World
	store    *th.Remote
	app      *th.Remote
	tools    *th.Remote
	alice    *th.Actor
	dev1     *th.Actor
	acmedev  *th.Actor
	aliceApp *th.Clone // alice's acme/app checkout (linked to acme/)
	dev1App  *th.Clone
	acmeApp  *th.Clone // the client's own checkout
	tl       string    // the client repo's task list
}

// agencyCaps is what newAgency itself runs.
var agencyCaps = []th.Cap{
	th.Cmd("store", "init"),
	th.Cmd("org", "link"),
	th.Cmd("actor", "register"),
	th.Cmd("whoami"),
}

// northwindOrg is the northwind org entry for one person.
func northwindOrg(key, name, handle string) th.Org {
	return th.Org{
		Name:     orgNorthwind,
		Identity: th.Identity{Key: key, Name: name, Handle: handle},
		Stores:   []th.Store{{URL: "https://" + slugStore, OrgFolder: folderOrg}},
		Own:      []string{"github.com/northwind/*"},
		Repos:    []string{"github.com/acme/*", "github.com/globex/*"},
		Defaults: &th.OrgDefaults{ClientRepos: map[string]string{"task": orgNorthwind, "memory": orgNorthwind, "rule": "repo", "decision": "repo"}},
	}
}

// aliceConfig is §5.2's three-entry config (northwind, staging, betalabs).
func aliceConfig() th.Config {
	stag := northwindOrg(aliceKey, aliceName, "alice")
	stag.Name = orgStaging
	stag.Stores = []th.Store{{URL: "https://" + slugStore, Branch: stagingBranch, OrgFolder: folderOrg}}
	stag.Defaults = nil
	return th.Config{
		Personal: &th.Personal{Store: th.PersonalLocal},
		Orgs: []th.Org{
			northwindOrg(aliceKey, aliceName, "alice"),
			stag,
			{
				Name:     orgBetalabs,
				Identity: th.Identity{Key: aliceBetaKey, Name: aliceName, Handle: "alice"},
				Stores:   []th.Store{{URL: "https://" + slugBetaStore, OrgFolder: folderOrg}},
				Own:      []string{"github.com/betalabs/*"},
			},
		},
		Live: th.FreshFetch(),
	}
}

// newAgency builds the agency world with real commands only: the client
// bootstraps acme/app; alice inits the store, links acme/app to acme/ and
// registers; dev1 clones and registers.
func newAgency(t *testing.T, caps ...th.Cap) *agency {
	t.Helper()
	w := newTeamWorld(t, append(append([]th.Cap{}, agencyCaps...), caps...)...)
	a := &agency{w: w, store: w.Remote(slugStore), app: w.Remote(slugApp), tools: w.Remote(slugOwnTools)}
	a.alice = w.Human("alice", "alice@northwind.example")
	a.dev1 = w.Human("dev1", "dev1@northwind.example")
	a.acmedev = w.Human("acmedev", "dev@acme.example")
	a.alice.WriteConfig(aliceConfig())
	a.dev1.WriteConfig(th.Config{
		Personal: &th.Personal{Store: th.PersonalLocal},
		Orgs:     []th.Org{northwindOrg(dev1Key, "Dev One", "dev1")},
		Live:     th.FreshFetch(),
	})
	a.acmedev.WriteConfig(th.Config{Live: th.FreshFetch()})

	a.acmeApp = a.acmedev.Clone(a.app, "app")
	a.acmeApp.MustLore("init", "--non-interactive", "--name=app")
	a.tl = createTasklist(t, a.acmeApp)
	a.acmeApp.CommitAll("lore")
	a.acmeApp.AdminPush("origin", "main")

	initStore(w, a.alice, "northwind", "https://"+slugStore, "", "--yes").OK(t)
	a.aliceApp = a.alice.Clone(a.app, "app")
	a.aliceApp.MustLore("org", "link", orgNorthwind, "--project", folderAcme, "--yes")
	a.aliceApp.MustLore("actor", "register")
	a.dev1App = a.dev1.Clone(a.app, "app")
	a.dev1App.MustLore("actor", "register")
	return a
}

// initStore makes url a lore store the 0.1.18 way. v0.1.17's
// `lore store init <url>` (run from anywhere) was removed by O12b: the core
// command is `lore store init <name> <url>`, and it also attaches the store
// to the repo it runs in (.lore/stores.yaml). So the person runs it from a
// scratch repo under their HOME, never from one of the world's code repos
// (whose files the tests inspect); name is only that scratch repo's entry
// name, nothing the org config refers to ("" derives a snake_case one
// from the url's owner/repo, as store names must be). branch != "" is v0.1.17's
// `--branch <b>`, spelled by the core as the git url's ?branch= parameter.
// extra are further flags (e.g. --yes).
func initStore(w *th.World, a *th.Actor, name, url, branch string, extra ...string) th.Result {
	w.T.Helper()
	if name == "" {
		name = storeNameFor(url)
	}
	dir := filepath.Join(a.Home, "store-init", name)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			w.T.Fatalf("store-init dir: %v", err)
		}
		w.Git(dir, "init", "-q", "-b", storeBranch)
		a.Lore(dir, "init", "--non-interactive", "--name="+name).OK(w.T)
	}
	if branch != "" {
		url += "?branch=" + branch
	}
	return a.Lore(dir, append([]string{"store", "init", name, url}, extra...)...)
}

// storeNameFor is the snake_case store name initStore gives a url:
// owner_repo of its last two path parts, every other character an '_'.
func storeNameFor(url string) string {
	parts := strings.Split(strings.TrimSuffix(url, "/"), "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return nonSnake.ReplaceAllString(strings.ToLower(strings.Join(parts, "_")), "_")
}

// nonSnake matches what a store name may not hold (store names are snake_case).
var nonSnake = regexp.MustCompile(`[^a-z0-9_]`)

// storeMainRow reads a row of a store project from the store branch.
func (a *agency) storeMainRow(t *testing.T, folder, table, id string) (rowDoc, bool) {
	t.Helper()
	return readRow(t, a.store, refMain, storeRowPath(folder, table, id))
}

// storeRowPath is a row's path in a store's tree: <folder>/<table>/<id>.json,
// <table>/<id>.json for the store root ("" or "."); a store keeps no
// .lore/data (rows sit right under each folder).
func storeRowPath(folder, table, id string) string {
	p := table + "/" + id + ".json"
	if folder == "" || folder == "." {
		return p
	}
	return folder + "/" + p
}

// clientSees reports which needles a client developer can obtain from the
// client repo: every ref fetched, every object, every file of their checkout.
func (a *agency) clientSees(t *testing.T, needles ...string) []th.Finding {
	t.Helper()
	a.acmeApp.FetchEverything()
	return a.acmeApp.LeakScan(needles...)
}

// agentActorID returns the act_… id of the agent actor with label on the
// org roster (the store's org folder on main). Client copies name actors by
// this id, not by the LORE_ACTOR key, so leak scans must look for both.
func agentActorID(t *testing.T, store *th.Remote, label string) string {
	t.Helper()
	for _, f := range store.FilesMatching(refMain, folderOrg+"/"+tblActors+"/") {
		if body, _ := store.Read(refMain, f); strings.Contains(body, label) && strings.Contains(body, `"agent"`) {
			return strings.TrimSuffix(filepath.Base(f), ".json")
		}
	}
	t.Fatalf("agent %q is not on the org roster", label)
	return ""
}

// clientTaskFieldsNeverShared are the claim fields D8 (coordinator 15:05)
// keeps off a client task row (only claim_epoch may be there), each with the
// JSON value that carries nothing: null, or the column's schema default that
// every exported row lists whether claimed or not (tasks.attempts is
// Int.Default(0), schema 9c030c4). Any other value shares claim data.
var clientTaskFieldsNeverShared = map[string]string{
	"attempts":    "0",
	"claim_log":   "null",
	"work_branch": "null",
	"claim_token": "null",
}

// clientTaskRow reads a task row from the client repo acme/app: live ref
// first, then main.
func (a *agency) clientTaskRow(t *testing.T, id string) rowDoc {
	t.Helper()
	if r, ok := readRow(t, a.app, refLive, liveRowPath(rootFolder, tblTasks, id)); ok {
		return r
	}
	return mustRow(t, a.app, refMain, mainRowPath("", tblTasks, id))
}

// agentEnv is what `lore agent start` prints for the session to export.
var agentEnvRe = regexp.MustCompile(`LORE_ACTOR=(\S+)\s+LORE_SESSION=(\S+)`)

// agentOwnerHandle is the roster handle every agent of the suite is
// started for: alice runs the agents in every world.
const agentOwnerHandle = "alice"

// startAgent runs `lore agent start --label <label> --owner @alice` on the
// owner's machine and returns the agent session with the printed
// LORE_ACTOR / LORE_SESSION (§7.5 step 2, F18).
func startAgent(t *testing.T, owner *th.Actor, dir *th.Clone, label string) *th.Actor {
	t.Helper()
	return startAgentFor(t, owner, dir, label, agentOwnerHandle)
}

// startAgentFor is startAgent for an agent whose roster owner is not alice
// (ownerHandle without the @): a story where another person runs the agent
// must register it under that person, or the owner checks are not exercised.
func startAgentFor(t *testing.T, owner *th.Actor, dir *th.Clone, label, ownerHandle string) *th.Actor {
	t.Helper()
	r := dir.As(owner).MustLore("agent", "start", "--label", label, "--owner", "@"+ownerHandle)
	m := agentEnvRe.FindStringSubmatch(r.Out())
	if m == nil {
		t.Fatalf("agent start must print LORE_ACTOR=… LORE_SESSION=…: %s", r)
	}
	ag := owner.Agent(label)
	ag.SetEnv(th.EnvLoreActor, m[1])
	ag.SetEnv(th.EnvLoreSession, m[2])
	return ag
}
