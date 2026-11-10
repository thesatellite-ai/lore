//go:build machines && !windows

package machines

// sot_rightrows_test.go — lifecycle proofs for the rows of
// the source-of-truth inventory that are already right: for each copied datum
// the source changes and the copy follows (or is flagged), through the
// built binary and `lore api`, with two machines or two clones where the
// datum has them.
//
//	row  4  team profile fields   the org roster row only; a member reads the edit after a fetch
//	row  5  machine profile       the live OS read; the personal store's row follows on the next write
//	row  6  caches vs row files   a hand-edited or deleted row file wins over every cache
//	row  8  local store clones    the remote wins; a clone past live.fetch_ttl fetches
//	row 14  discover + health     a kept answer expires by TTL and by fingerprint; health by setup entry
//	row 15  repo URL history      a changed remote is flagged until confirmed; a second clone follows
//	row 16  live/branch/main      explain names the winning copy, and it moves as the copies change

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

func TestSotRightRows(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(t *testing.T)
	}{
		{"row04_team_profile", sotTeamProfile},
		{"row05_machine_profile", sotMachineProfile},
		{"row06_caches", sotCaches},
		{"row08_store_clones", sotStoreClones},
		{"row14_discover_cache", sotDiscoverCache},
		{"row14_setup_health", sotSetupHealth},
		{"row15_repo_urls", sotRepoURLs},
		{"row16_live_copies", sotLiveCopies},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}

// ── row 6: SQLite caches vs row files ─────────────────────────────────

// sotCaches: the row files are the truth. A hand edit of a row file shows
// in the CLI and in an already running `lore api` (show and search); a
// deleted row file leaves both; a deleted cache file is rebuilt with
// nothing lost; a folder store's cache follows a hand edit on a second
// machine; the cache of a store no repo attaches is listed by cache prune
// and removed by it.
func sotCaches(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	repo := m.Repos[RepoLore]
	id := sotID(t, lsRun(t, m, repo, "memory", "add", "--body", "sot6 original body"))
	api := csStartAPI(t, m, repo)
	if got := sotAPICall(t, api, "GET", "/v1/memories/"+id, "", sotStatusOK); !strings.Contains(got, "sot6 original body") {
		t.Fatalf("REST show before the edit: %s", got)
	}

	path := sotRowPath(repo, "memories", id)
	sotEditRow(t, path, "body", "sot6 handedited body")
	if got := lsRun(t, m, repo, "memory", "show", id); !strings.Contains(got, "sot6 handedited body") {
		t.Errorf("CLI show after a hand edit of the row file: %s", got)
	}
	if got := lsRun(t, m, repo, "search", "handedited"); !strings.Contains(got, id) {
		t.Errorf("CLI search after a hand edit finds no %s: %s", id, got)
	}
	if got := sotAPICall(t, api, "GET", "/v1/memories/"+id, "", sotStatusOK); !strings.Contains(got, "sot6 handedited body") {
		t.Errorf("REST show (same lore api process) after a hand edit: %s", got)
	}
	if got := sotAPICall(t, api, "POST", "/v1/search/run", `{"query":"handedited"}`, sotStatusOK); !strings.Contains(got, id) {
		t.Errorf("REST search after a hand edit finds no %s: %s", id, got)
	}

	// A deleted cache file loses nothing: the next read rebuilds it.
	kept := sotID(t, lsRun(t, m, repo, "memory", "add", "--body", "sot6 kept row"))
	cacheDB := filepath.Join(repo, ".lore", "lore-cache.db")
	if err := os.Remove(cacheDB); err != nil {
		t.Fatalf("remove the repo cache: %v", err)
	}
	if got := lsRun(t, m, repo, "memory", "list"); !strings.Contains(got, kept) || !strings.Contains(got, id) {
		t.Errorf("memory list after the cache file was deleted lacks a row: %s", got)
	}
	if _, err := os.Stat(cacheDB); err != nil {
		t.Errorf("the cache file was not rebuilt: %v", err)
	}

	// A row file deleted by hand leaves the CLI and the REST reads.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := lsRun(t, m, repo, "memory", "list"); strings.Contains(got, id) {
		t.Errorf("memory list still shows a row whose file is gone: %s", got)
	}
	sotAPICall(t, api, "GET", "/v1/memories/"+id, "", sotStatusNotFound)

	// A folder store shared by two machines: each has its own cache under
	// ~/.lore/cache/stores/<sto>, both follow the row files.
	kbDir := filepath.Join(m.Dir, "kb-store")
	kbURL := "file://" + kbDir
	sto := sotID(t, lsRun(t, m, repo, "store", "init", "sotkb", kbURL, "--yes"))
	mid := sotID(t, lsRun(t, m, repo, "memory", "add", "--body", "sot6 store body", "--to", "sotkb"))
	if _, err := os.Stat(filepath.Join(m.Home, ".lore", "cache", "stores", sto)); err != nil {
		t.Errorf("no cache folder for store %s: %v", sto, err)
	}
	m2 := Build(t, ShapeFresh, loreBin)
	repo2 := m2.Repos[RepoLore]
	lsRun(t, m2, repo2, "store", "add", "sotkb", kbURL, "--yes")
	if got := lsRun(t, m2, repo2, "memory", "show", mid); !strings.Contains(got, "sot6 store body") {
		t.Fatalf("second machine reads the folder store row: %s", got)
	}
	sotEditRow(t, sotRowPath(kbDir, "memories", mid), "body", "sot6 store handedited")
	for _, w := range []struct {
		m    *Machine
		repo string
	}{{m, repo}, {m2, repo2}} {
		if got := lsRun(t, w.m, w.repo, "memory", "show", mid); !strings.Contains(got, "sot6 store handedited") {
			t.Errorf("%s: folder store row after a hand edit: %s", w.m.Home, got)
		}
	}
	lsRun(t, m2, repo2, "memory", "edit", mid, "--body", "sot6 store edited on m2")
	if got := lsRun(t, m, repo, "memory", "show", mid); !strings.Contains(got, "sot6 store edited on m2") {
		t.Errorf("first machine after the second machine's edit: %s", got)
	}

	// Detached everywhere: cache prune lists the store's cache, then removes it.
	lsRun(t, m, repo, "store", "remove", "sotkb", "--yes")
	if got := lsRun(t, m, "", "cache", "prune", "--dry-run"); !strings.Contains(got, sto) {
		t.Errorf("cache prune --dry-run does not list the detached store's cache %s: %s", sto, got)
	}
	lsRun(t, m, "", "cache", "prune", "--yes")
	if _, err := os.Stat(filepath.Join(m.Home, ".lore", "cache", "stores", sto)); !os.IsNotExist(err) {
		t.Errorf("cache prune --yes left the detached store's cache (stat: %v)", err)
	}
}

// ── row 5: machine profile ────────────────────────────────────────────

// sotMachineProfile: the live OS read is the source and the personal
// store's machines row its copy. The row is written on the first write
// to the personal store, left alone (same HLC) while nothing changed,
// rewritten on the next ordinary write after the OS read changed (WSL
// distro set, then gone), seen by `machine list` and REST at once; the
// label is set through REST; no repo store ever gets a machines row.
func sotMachineProfile(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	repo := m.Repos[RepoLore]
	meDir := filepath.Join(m.Dir, "me-store")
	lsRun(t, m, repo, "store", "init", "me", "file://"+meDir, "--yes")
	lsRun(t, m, repo, "memory", "add", "--body", "sot5 one", "--to", "me")

	rows, _ := filepath.Glob(filepath.Join(meDir, ".lore", "data", "machines", "*.json"))
	if len(rows) != 1 {
		t.Fatalf("personal store holds %d machine rows, want 1: %v", len(rows), rows)
	}
	row := rows[0]
	hlc1 := sotRowText(t, row, "_hlc")
	if rt := sotRowText(t, row, "runtime"); !strings.Contains(rt, `"bare"`) {
		t.Fatalf("first profile runtime = %s, want bare", rt)
	}

	lsRun(t, m, repo, "memory", "add", "--body", "sot5 two", "--to", "me")
	if h := sotRowText(t, row, "_hlc"); h != hlc1 {
		t.Errorf("an unchanged profile was rewritten: _hlc %s → %s", hlc1, h)
	}

	// The OS read changes (a WSL distro): the next write refreshes the copy.
	wsl := sotWithEnv(m, "WSL_DISTRO_NAME=SotDistroA")
	lsRun(t, wsl, repo, "memory", "add", "--body", "sot5 three", "--to", "me")
	if rt := sotRowText(t, row, "runtime"); !strings.Contains(rt, "SotDistroA") || !strings.Contains(rt, "wsl2") {
		t.Errorf("after the OS read changed, runtime = %s, want wsl2 SotDistroA", rt)
	}
	hlc2 := sotRowText(t, row, "_hlc")
	if hlc2 == hlc1 {
		t.Errorf("the changed profile kept its HLC %s", hlc1)
	}
	if got := lsRun(t, m, repo, "machine", "list"); !strings.Contains(got, "SotDistroA") && !strings.Contains(got, "wsl2") {
		t.Errorf("machine list does not show the refreshed profile: %s", got)
	}
	api := csStartAPI(t, m, repo)
	if got := sotAPICall(t, api, "GET", "/v1/machines", "", sotStatusOK); !strings.Contains(got, "wsl2") {
		t.Errorf("REST machine list does not show the refreshed profile: %s", got)
	}

	// The distro is gone again: the copy follows on the next write.
	lsRun(t, m, repo, "memory", "add", "--body", "sot5 four", "--to", "me")
	if rt := sotRowText(t, row, "runtime"); strings.Contains(rt, "SotDistroA") || !strings.Contains(rt, `"bare"`) {
		t.Errorf("after the OS read changed back, runtime = %s, want bare", rt)
	}
	if got := sotAPICall(t, api, "GET", "/v1/machines", "", sotStatusOK); strings.Contains(got, "wsl2") {
		t.Errorf("REST machine list (same lore api process) still shows the old profile: %s", got)
	}

	// The label through REST, read back by the CLI and the row.
	sotAPICall(t, api, "POST", "/v1/machines/label", `{"label":"sot-box"}`, sotStatusOK)
	if l := sotRowText(t, row, "label"); l != "sot-box" {
		t.Errorf("machine row label = %q after REST machine.label, want sot-box", l)
	}
	if got := lsRun(t, m, repo, "machine", "list"); !strings.Contains(got, "sot-box") {
		t.Errorf("machine list lacks the label set through REST: %s", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".lore", "data", "machines")); !os.IsNotExist(err) {
		t.Errorf("the repo store got machine rows (stat: %v); a profile belongs in the personal store only", err)
	}
}

// ── row 8: local store clones ─────────────────────────────────────────

// sotKBURL is the git store two machines share (rewritten to a bare repo).
const sotKBURL = "git@github.com:sotorg/sot-kb.git"

// sotStoreClones: a git store attached on two machines. Machine b's
// clone follows machine a's writes once its last fetch is older than
// live.fetch_ttl (set to 1h on b, so "within the TTL" is not a race), on
// `lore sync`, and through `lore api`; the clone's work tree ends at the
// remote's commit.
func sotStoreClones(t *testing.T) {
	a := Build(t, ShapeFresh, loreBin)
	b := Build(t, ShapeFresh, loreBin)
	bare := filepath.Join(a.Dir, "sot-kb.git")
	sotBare(t, bare)
	sotRewrite(t, a, bare, sotKBURL)
	sotRewrite(t, b, bare, sotKBURL)
	ra, rb := a.Repos[RepoLore], b.Repos[RepoLore]
	lsRun(t, a, ra, "store", "init", "sotkb", sotKBURL, "--yes")
	lsRun(t, a, ra, "memory", "add", "--body", "sot8 one", "--to", "sotkb")
	lsRun(t, b, rb, "store", "add", "sotkb", sotKBURL, "--yes")
	lsRun(t, b, "", "config", "set", "live.fetch_ttl", "1h")
	if got := lsRun(t, b, rb, "memory", "list", "--store", "sotkb"); !strings.Contains(got, "sot8 one") {
		t.Fatalf("b's first read of the store lacks a's row: %s", got)
	}
	stamp := filepath.Join(b.Home, ".lore", "clones", "github.com", "sotorg", "sot-kb", "fetch-stamp")
	fetched := sotReadStamp(t, stamp)

	// Within the TTL b reads its clone as it is (a's new row is not there
	// yet); the stamp is untouched.
	lsRun(t, a, ra, "memory", "add", "--body", "sot8 two", "--to", "sotkb")
	if got := lsRun(t, b, rb, "memory", "list", "--store", "sotkb"); strings.Contains(got, "sot8 two") {
		t.Errorf("b fetched within live.fetch_ttl (1h): %s", got)
	}
	if !sotReadStamp(t, stamp).Equal(fetched) {
		t.Errorf("b's fetch stamp moved within live.fetch_ttl")
	}

	// Past the TTL the next read fetches: the copy follows the remote.
	sotBackdateStamp(t, stamp, 2*time.Hour)
	if got := lsRun(t, b, rb, "memory", "list", "--store", "sotkb"); !strings.Contains(got, "sot8 two") {
		t.Errorf("b past live.fetch_ttl still lacks a's row: %s", got)
	}
	if !sotReadStamp(t, stamp).After(fetched) {
		t.Errorf("b's fetch stamp was not renewed by the fetch")
	}

	// lore sync fetches whatever the TTL says.
	lsRun(t, a, ra, "memory", "add", "--body", "sot8 three", "--to", "sotkb")
	sotOK(t, b, rb, "sync")
	if got := lsRun(t, b, rb, "memory", "list", "--store", "sotkb"); !strings.Contains(got, "sot8 three") {
		t.Errorf("b after lore sync lacks a's row: %s", got)
	}

	// Through lore api: the same rule in a long-running process.
	api := csStartAPI(t, b, rb)
	lsRun(t, a, ra, "memory", "add", "--body", "sot8 four", "--to", "sotkb")
	sotBackdateStamp(t, stamp, 2*time.Hour)
	if got := sotAPICall(t, api, "GET", "/v1/memories?store=sotkb", "", sotStatusOK); !strings.Contains(got, "sot8 four") {
		t.Errorf("REST memory list past live.fetch_ttl lacks a's row: %s", got)
	}

	// The clone ends at the remote's commit.
	work := filepath.Join(b.Home, ".lore", "clones", "github.com", "sotorg", "sot-kb", "work")
	if head, main := sotGit(t, b, work, "rev-parse", "HEAD"), sotGit(t, b, bare, "rev-parse", "main"); head != main {
		t.Errorf("b's clone is at %s, the remote's main at %s", head, main)
	}
}

// ── row 14: discover cache and setup health ──────────────────────────

// sotDiscoverCachePath is the kept discovery answer of m.
func sotDiscoverCachePath(m *Machine) string {
	return filepath.Join(m.Home, ".lore", "state", "discover-cache.json")
}

// sotKeptDiscover is the kept answer's when and fingerprint.
type sotKeptDiscover struct {
	At          time.Time `json:"at"`
	Fingerprint string    `json:"fingerprint"`
}

func sotReadKept(t *testing.T, m *Machine) sotKeptDiscover {
	t.Helper()
	b, err := os.ReadFile(sotDiscoverCachePath(m))
	if err != nil {
		t.Fatalf("discover cache: %v", err)
	}
	var k sotKeptDiscover
	if err := json.Unmarshal(b, &k); err != nil {
		t.Fatalf("discover cache: %v: %s", err, b)
	}
	return k
}

// sotAgeKept moves the kept answer's "at" age into the past (the clock
// seam: the file is the copy, its at is the copy's age), leaving every
// other field as it is.
func sotAgeKept(t *testing.T, m *Machine, age time.Duration) time.Time {
	t.Helper()
	p := sotDiscoverCachePath(m)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age).UTC()
	raw, err := json.Marshal(at)
	if err != nil {
		t.Fatal(err)
	}
	doc["at"] = raw
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return at
}

// sotDiscoverCLI is `lore setup discover --async` (one process: a kept,
// valid answer comes back done and from the cache).
func sotDiscoverCLI(t *testing.T, m *Machine) opssetup.DiscoverStatus {
	t.Helper()
	var out opssetup.DiscoverOut
	got := lsRun(t, m, "", "setup", "discover", "--async")
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("setup discover --async: %v: %s", err, got)
	}
	return out.Status
}

// sotDiscoverPage asks the page's REST API (async, as the page does) and
// polls until the answer is done; it returns the first answer's status
// and the done one's.
func sotDiscoverPage(t *testing.T, p *Page, fresh bool) (opssetup.DiscoverStatus, opssetup.DiscoverStatus) {
	t.Helper()
	var first opssetup.DiscoverOut
	if err := p.Call(opssetup.OpDiscover, opssetup.DiscoverIn{Async: true, Fresh: fresh}, &first); err != nil {
		t.Fatalf("setup.discover: %v", err)
	}
	last := first
	deadline := time.Now().Add(pageCallTimeout)
	for last.Status.State != opssetup.DiscoverDone {
		if time.Now().After(deadline) {
			t.Fatalf("setup.discover still %s after %s", last.Status.State, pageCallTimeout)
		}
		time.Sleep(200 * time.Millisecond) // the page polls every 400 ms
		last = opssetup.DiscoverOut{}
		if err := p.Call(opssetup.OpDiscover, opssetup.DiscoverIn{Async: true}, &last); err != nil {
			t.Fatalf("setup.discover poll: %v", err)
		}
	}
	return first.Status, last.Status
}

// sotDiscoverCache: the live probes are the source, discover-cache.json
// the copy. A kept answer is served (from_cache, cached_at = its at)
// while it is younger than DiscoverCacheTTL and its fingerprint matches;
// one older than the TTL, one whose source file changed, and "fresh" each
// probe again and rewrite the copy. Time passes by moving the copy's at
// into the past, never by waiting.
func sotDiscoverCache(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	lsRun(t, m, "", "setup", "discover")
	k0 := sotReadKept(t, m)
	if k0.Fingerprint == "" || k0.At.IsZero() {
		t.Fatalf("the kept answer carries no at or fingerprint: %+v", k0)
	}
	st := sotDiscoverCLI(t, m)
	if !st.FromCache || st.State != opssetup.DiscoverDone || st.CachedAt != k0.At.UTC().Format(time.RFC3339) {
		t.Errorf("a fresh kept answer: status %+v, want done from_cache cached_at %s", st, k0.At.UTC().Format(time.RFC3339))
	}

	// Just inside the TTL: still served.
	inside := sotAgeKept(t, m, opssetup.DiscoverCacheTTL-time.Minute)
	if st := sotDiscoverCLI(t, m); !st.FromCache || st.CachedAt != inside.Format(time.RFC3339) {
		t.Errorf("an answer %s old (TTL %s): status %+v, want from_cache", opssetup.DiscoverCacheTTL-time.Minute, opssetup.DiscoverCacheTTL, st)
	}

	// Past the TTL: probed again, through the page's REST API.
	p := m.StartPage(t)
	old := sotAgeKept(t, m, opssetup.DiscoverCacheTTL+time.Minute)
	first, done := sotDiscoverPage(t, p, false)
	if first.FromCache || first.State != opssetup.DiscoverRunning {
		t.Errorf("an answer past the TTL was served: first status %+v", first)
	}
	if k := sotReadKept(t, m); !k.At.After(old) {
		t.Errorf("the probe past the TTL did not rewrite the kept answer (at %s)", k.At)
	}
	if done.CachedAt == old.Format(time.RFC3339) {
		t.Errorf("the done answer still carries the expired cached_at %s", done.CachedAt)
	}
	var again opssetup.DiscoverOut
	if err := p.Call(opssetup.OpDiscover, opssetup.DiscoverIn{Async: true}, &again); err != nil {
		t.Fatal(err)
	}
	if !again.Status.FromCache {
		t.Errorf("the rewritten answer is not served from the cache: %+v", again.Status)
	}

	// A source file changes (git config): the fingerprint no longer
	// matches, so the copy is probed again although it is young.
	k1 := sotReadKept(t, m)
	f, err := os.OpenFile(filepath.Join(m.Home, ".gitconfig"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# sot14: an edit to a file discovery reads\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if st := sotDiscoverCLI(t, m); st.FromCache {
		t.Errorf("a kept answer with a stale fingerprint was served: %+v", st)
	}
	first, _ = sotDiscoverPage(t, p, false)
	if first.FromCache {
		t.Errorf("page: a kept answer with a stale fingerprint was served: %+v", first)
	}
	if k := sotReadKept(t, m); k.Fingerprint == k1.Fingerprint {
		t.Errorf("the rewritten answer kept the old fingerprint")
	}

	// fresh always probes.
	if first, _ := sotDiscoverPage(t, p, true); first.FromCache {
		t.Errorf("fresh was served from the cache: %+v", first)
	}
}

// sotHealthOf is setup list's health cell of a setup ("" = not checked).
func sotHealthOf(t *testing.T, m *Machine, name string) string {
	t.Helper()
	var o struct {
		Setups []struct {
			Name   string `json:"name"`
			Health string `json:"health"`
		} `json:"setups"`
	}
	got := lsRun(t, m, "", "setup", "list")
	if err := json.Unmarshal([]byte(got), &o); err != nil {
		t.Fatalf("setup list: %v: %s", err, got)
	}
	for _, s := range o.Setups {
		if s.Name == name {
			return s.Health
		}
	}
	t.Fatalf("setup list has no %s: %s", name, got)
	return ""
}

// sotSetupHealth: the doctor's checks are the source and
// cache/doctor-health.json the copy Home shows. Each result carries
// checked_at; a doctor run (CLI or the page's REST) fills the cell; a
// note edit keeps it (a note changes nothing a check reads); any other
// edit of the setup entry clears it to "not checked" until the next run.
func sotSetupHealth(t *testing.T) {
	m := Build(t, ShapeMigrated, loreBin)
	const setup = "main"
	if h := sotHealthOf(t, m, setup); h != "" {
		t.Fatalf("health before any doctor run: %q", h)
	}
	_ = m.Lore(t, "", "doctor", "--all", "--json") // exit 1 on a warning; the file is what counts
	h1 := sotHealthOf(t, m, setup)
	if h1 == "" {
		t.Fatalf("no health after doctor --all")
	}
	var file struct {
		Setups map[string][]struct {
			Check     string `json:"check"`
			CheckedAt string `json:"checked_at"`
		} `json:"setups"`
	}
	b, err := os.ReadFile(filepath.Join(m.Home, ".lore", "cache", "doctor-health.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Setups[setup]) == 0 {
		t.Fatalf("doctor-health.json has no results for %s: %s", setup, b)
	}
	for _, r := range file.Setups[setup] {
		if _, err := time.Parse(time.RFC3339Nano, r.CheckedAt); err != nil {
			t.Errorf("health result %s has no checked_at (%q)", r.Check, r.CheckedAt)
		}
	}
	lsRun(t, m, "", "setup", "edit", setup, "--note", "sot14 note")
	if h := sotHealthOf(t, m, setup); h != h1 {
		t.Errorf("a note edit changed the health cell %q → %q", h1, h)
	}
	lsRun(t, m, "", "setup", "edit", setup, "--folder", "~/sot-elsewhere/**")
	if h := sotHealthOf(t, m, setup); h != "" {
		t.Errorf("health %q survived an edit of the setup entry; want not checked", h)
	}
	p := m.StartPage(t)
	var dout json.RawMessage
	if err := p.Call("doctor.run", map[string]bool{"all": true}, &dout); err != nil {
		t.Fatalf("page doctor.run: %v", err)
	}
	if h := sotHealthOf(t, m, setup); h == "" {
		t.Errorf("no health after the page's doctor run")
	}
}

// ── row 15: repo URL history ──────────────────────────────────────────

// Repo addresses of row 15; both rewrite to the same bare repo, as a
// GitHub rename redirects.
const (
	sotAppOne = "git@github.com:sotorg/sot-app-one.git"
	sotAppTwo = "git@github.com:sotorg/sot-app-two.git"
)

// sotRepoURLs: the repo row's confirmed urls[] are the source, a clone's
// git remote the evidence. A changed remote is flagged by sync and doctor
// with the three fixes; confirming it through REST (repo.set_url) clears
// the flag. A second clone whose remote moved too is flagged until it
// reads the confirmed row from the remote, then clears with no command.
func sotRepoURLs(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	sotLoreOnPath(t, m) // git's lore merge driver runs on pull
	bare := filepath.Join(m.Dir, "sot-app.git")
	sotBare(t, bare)
	sotRewrite(t, m, bare, sotAppOne)
	sotRewrite(t, m, bare, sotAppTwo)
	code := filepath.Join(m.Home, "code")
	sotGit(t, m, code, "clone", "-q", sotAppOne, "sot-a")
	a := filepath.Join(code, "sot-a")
	sotOK(t, m, a, "init", "--name", "sot-app")
	sotGit(t, m, a, "add", "-A")
	sotGit(t, m, a, "commit", "-q", "-m", "lore init")
	sotGit(t, m, a, "push", "-q", "origin", "main")
	sotGit(t, m, code, "clone", "-q", sotAppOne, "sot-b")
	b := filepath.Join(code, "sot-b")
	const notice = "is unconfirmed"
	if got := sotOK(t, m, b, "doctor"); strings.Contains(got, notice) {
		t.Fatalf("second clone at the confirmed address is flagged: %s", got)
	}

	sotGit(t, m, a, "remote", "set-url", "origin", sotAppTwo)
	got := sotOut(m.Lore(t, a, "sync"))
	for _, want := range []string{"remote changed", "lore repo set-url github.com/sotorg/sot-app-two", "--mirror", "lore repo fork"} {
		if !strings.Contains(got, want) {
			t.Errorf("sync after the remote moved lacks %q: %s", want, got)
		}
	}
	if got := sotOut(m.Lore(t, a, "doctor")); !strings.Contains(got, "sot-app-two "+notice) {
		t.Errorf("doctor after the remote moved does not flag it: %s", got)
	}

	// The second clone moves its remote too, before anyone confirms: its
	// copy of the repo row names the old address, so it is flagged.
	sotGit(t, m, b, "remote", "set-url", "origin", sotAppTwo)
	if got := sotOut(m.Lore(t, b, "sync")); !strings.Contains(got, "remote changed") {
		t.Errorf("second clone with the old repo row: sync does not flag the moved remote: %s", got)
	}
	if got := sotOut(m.Lore(t, b, "doctor")); !strings.Contains(got, "sot-app-two "+notice) {
		t.Errorf("second clone with the old repo row: doctor does not flag the moved remote: %s", got)
	}

	// Confirmed in the first clone through REST: its flag clears.
	api := csStartAPI(t, m, a)
	sotAPICall(t, api, "POST", "/v1/repos/set_url", `{"url":"github.com/sotorg/sot-app-two"}`, sotStatusOK)
	if got := sotOut(m.Lore(t, a, "sync")); strings.Contains(got, "remote changed") {
		t.Errorf("sync after set-url still flags the remote: %s", got)
	}
	if got := sotOut(m.Lore(t, a, "doctor")); strings.Contains(got, notice) {
		t.Errorf("doctor after set-url still flags the remote: %s", got)
	}
	if got := sotAPICall(t, api, "POST", "/v1/doctor/run", `{}`, sotStatusOK); strings.Contains(got, notice) {
		t.Errorf("REST doctor after set-url still flags the remote: %s", got)
	}
	sotGit(t, m, a, "add", "-A")
	sotGit(t, m, a, "commit", "-q", "-m", "confirm the new address")
	sotGit(t, m, a, "push", "-q", "origin", "main")

	// The second clone's sync recorded what it saw (an "unconfirmed"
	// urls[] entry, a local change of the repo row); committed and merged
	// with the confirmed row from the remote (lore's merge driver, field
	// level, newest wins), the copy follows the source and the flag clears.
	sotGit(t, m, b, "add", "-A")
	sotGit(t, m, b, "commit", "-q", "-m", "the address this clone saw")
	sotGit(t, m, b, "pull", "--no-rebase", "-q", "origin", "main")
	if got := sotOut(m.Lore(t, b, "sync")); strings.Contains(got, "remote changed") {
		t.Errorf("second clone after merging the confirmed row: sync still flags it: %s", got)
	}
	if got := sotOut(m.Lore(t, b, "doctor")); strings.Contains(got, notice) {
		t.Errorf("second clone after merging the confirmed row: doctor still flags it: %s", got)
	}
	var urls struct {
		URLs []struct {
			URL  string `json:"url"`
			Kind string `json:"kind"`
			To   string `json:"to"`
		} `json:"urls"`
	}
	rows, _ := filepath.Glob(filepath.Join(b, ".lore", "data", "repos", "*.json"))
	if len(rows) != 1 {
		t.Fatalf("second clone holds %d repo rows, want 1", len(rows))
	}
	raw, err := os.ReadFile(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &urls); err != nil {
		t.Fatal(err)
	}
	primary := ""
	for _, u := range urls.URLs {
		if u.Kind == "primary" && u.To == "" {
			primary = u.URL
		}
	}
	if primary != "github.com/sotorg/sot-app-two" {
		t.Errorf("second clone's repo row: current primary %q, want the confirmed github.com/sotorg/sot-app-two:\n%s", primary, raw)
	}
}

// ── row 16: live, branch and default-branch copies ───────────────────

// sotLiveCopies: the land rules decide; `lore explain` (CLI and REST)
// names every copy and the winner. A live write wins in its clone; a
// second clone reads it after sync; that clone's committed edit, pushed
// to main, becomes the winner in the first clone after a fetch (the
// default-branch read-through) and after a pull (this branch).
func sotLiveCopies(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	bare := filepath.Join(m.Dir, "sot-live.git")
	const url = "git@github.com:sotorg/sot-live.git"
	sotBare(t, bare)
	sotRewrite(t, m, bare, url)
	code := filepath.Join(m.Home, "code")
	sotGit(t, m, code, "clone", "-q", url, "sot-a")
	a := filepath.Join(code, "sot-a")
	sotOK(t, m, a, "init", "--name", "sot-live")
	sotGit(t, m, a, "add", "-A")
	sotGit(t, m, a, "commit", "-q", "-m", "lore init")
	sotGit(t, m, a, "push", "-q", "origin", "main")

	id := sotID(t, lsRun(t, m, a, "memory", "add", "--body", "sot16 live body", "--live"))
	type copyOut struct {
		Place   string `json:"place"`
		Present bool   `json:"present"`
		Winner  bool   `json:"winner"`
	}
	type explainOut struct {
		Copies []copyOut `json:"copies"`
		Winner string    `json:"winner"`
		Reason string    `json:"reason"`
	}
	explain := func(dir string) explainOut {
		t.Helper()
		var o explainOut
		got := lsRun(t, m, dir, "explain", id)
		if err := json.Unmarshal([]byte(got), &o); err != nil {
			t.Fatalf("explain: %v: %s", err, got)
		}
		return o
	}
	if e := explain(a); e.Winner != "live" {
		t.Errorf("after a live write the winner is %q (%s), want live", e.Winner, e.Reason)
	}

	sotGit(t, m, code, "clone", "-q", url, "sot-b")
	b := filepath.Join(code, "sot-b")
	sotOK(t, m, b, "sync")
	if got := lsRun(t, m, b, "memory", "show", id); !strings.Contains(got, "sot16 live body") {
		t.Fatalf("second clone after sync: %s", got)
	}
	lsRun(t, m, b, "memory", "edit", id, "--body", "sot16 edited in b")
	if e := explain(b); e.Winner != "branch" && e.Winner != "work_tree" {
		t.Errorf("b's own edit: winner %q (%s), want this branch", e.Winner, e.Reason)
	}
	sotGit(t, m, b, "add", "-A")
	sotGit(t, m, b, "commit", "-q", "-m", "edit in b")
	sotGit(t, m, b, "push", "-q", "origin", "main")

	// a fetches: the default branch's copy wins until a pulls.
	sotOK(t, m, a, "sync")
	if got := lsRun(t, m, a, "memory", "show", id); !strings.Contains(got, "sot16 edited in b") {
		t.Errorf("a after fetching b's pushed edit reads: %s", got)
	}
	if e := explain(a); e.Winner != "default_branch" {
		t.Errorf("a before pulling: winner %q (%s), want default_branch", e.Winner, e.Reason)
	}
	api := csStartAPI(t, m, a)
	var re explainOut
	if err := json.Unmarshal([]byte(sotAPICall(t, api, "POST", "/v1/explains/"+id+"/run", `{}`, sotStatusOK)), &re); err != nil {
		t.Fatal(err)
	}
	if re.Winner != "default_branch" {
		t.Errorf("REST explain before pulling: winner %q (%s), want default_branch", re.Winner, re.Reason)
	}
	sotGit(t, m, a, "pull", "-q", "origin", "main")
	if e := explain(a); e.Winner != "branch" {
		t.Errorf("a after pulling: winner %q (%s), want branch", e.Winner, e.Reason)
	}
	if err := json.Unmarshal([]byte(sotAPICall(t, api, "POST", "/v1/explains/"+id+"/run", `{}`, sotStatusOK)), &re); err != nil {
		t.Fatal(err)
	}
	if re.Winner != "branch" {
		t.Errorf("REST explain after pulling: winner %q (%s), want branch", re.Winner, re.Reason)
	}
}

// ── row 4: team profile fields ────────────────────────────────────────

// sotTeamProfile: the org roster row is the one place for profile
// fields. An admin's title edit (CLI, then REST) writes the roster only
// (no repo copy carries a title), a member on another machine reads each
// edit after a fetch, and a repo with no org refuses the edit.
func sotTeamProfile(t *testing.T) {
	// The admin is another person: the owner-like machine as its work
	// account (bravo over github-bravo), as lsTeamFor does.
	admin := Build(t, ShapeOwnerLike, loreBin)
	member := Build(t, ShapeFresh, loreBin)
	bare := filepath.Join(admin.Dir, "team.git")
	sotBare(t, bare)
	tm := lsTeam{storeURL: lsTeamURL, bare: bare}
	for _, m := range []*Machine{admin, member} {
		lsGHHosts(t, m)
		tm.rewrite(t, m)
	}
	lsCreate(t, admin, "boss", kindAdmin, "boss@example.test", []string{"~/code/**"}, true, tm, lsGitHub{LoginBravo, "github-bravo"})
	var inv struct {
		Text string `json:"text"`
	}
	ar := admin.Repos[RepoWorkApp]
	if err := json.Unmarshal([]byte(lsRun(t, admin, ar, "team", "invite", "sot-member@example.test", "--yes")), &inv); err != nil || !strings.HasPrefix(inv.Text, "lore-invite:") {
		t.Fatalf("team invite: %v %q", err, inv.Text)
	}
	tm.invite = inv.Text
	lsCreate(t, member, "crew", kindMember, "sot-member@example.test", []string{"~/code/**"}, true, tm, lsAlpha)
	mr := member.Repos[RepoWorkApp]

	act := sotActorID(t, admin, ar)
	lsRun(t, admin, ar, "actor", "edit", act, "--title", "Sot Lead")

	roster := filepath.Join(admin.Home, ".lore", "clones", "github.com", "lifeorg", "lore-team", "work", "org")
	if got := sotRowText(t, sotRowPath(roster, "actors", act), "title"); got != "Sot Lead" {
		t.Errorf("roster row title = %q, want Sot Lead", got)
	}
	for _, repo := range []string{ar, admin.Repos[RepoLore]} {
		p := sotRowPath(repo, "actors", act)
		if _, err := os.Stat(p); err == nil {
			if got := sotRowText(t, p, "title"); got != "" {
				t.Errorf("repo copy %s carries the title %q; profile fields live in the roster only", p, got)
			}
		}
	}

	sotOK(t, member, mr, "sync")
	if got := lsRun(t, member, mr, "actor", "show", act); !strings.Contains(got, "Sot Lead") {
		t.Errorf("member after a fetch does not read the admin's title: %s", got)
	}

	api := csStartAPI(t, admin, ar)
	sotAPICall(t, api, "PATCH", "/v1/actors/"+act, `{"title":"Sot Lead Two"}`, sotStatusOK)
	if got := sotRowText(t, sotRowPath(roster, "actors", act), "title"); got != "Sot Lead Two" {
		t.Errorf("roster row title after REST = %q", got)
	}
	sotOK(t, member, mr, "sync")
	if got := lsRun(t, member, mr, "actor", "show", act); !strings.Contains(got, "Sot Lead Two") {
		t.Errorf("member after a fetch does not read the REST title edit: %s", got)
	}
	mapi := csStartAPI(t, member, mr)
	if got := sotAPICall(t, mapi, "GET", "/v1/actors/"+act, "", sotStatusOK); !strings.Contains(got, "Sot Lead Two") {
		t.Errorf("member REST actor show lacks the title: %s", got)
	}

	// A repo with no org: refused, naming where profiles live.
	solo := Build(t, ShapeFresh, loreBin)
	sr := solo.Repos[RepoLore]
	r := solo.Lore(t, sr, "actor", "edit", sotActorID(t, solo, sr), "--title", "Nobody", "--json")
	if r.Code == 0 || !strings.Contains(r.Stdout+r.Stderr, "E_REFUSED") || !strings.Contains(r.Stdout+r.Stderr, "org store") {
		t.Errorf("actor edit --title in a repo with no org: %s", r)
	}
}
