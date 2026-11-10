package doctor

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/ops/setup"
)

// storesEnv adds RepoStoresEnv (stores by repo root, warnings by root)
// and StampEnv (a stamp per setup name) to a setup env with a cache.
type storesEnv struct {
	*setupEnv
	byRepo   map[string][]RepoStore
	badRepos map[string]string
	stamps   map[string]string
}

func (e storesEnv) DoctorCache(context.Context) Cache { return e.cache }

func (e storesEnv) RepoStores(_ context.Context, roots []string) ([]RepoStore, []string) {
	var out []RepoStore
	var ws []string
	for _, r := range roots {
		if w, bad := e.badRepos[r]; bad {
			ws = append(ws, r+": "+w)
			continue
		}
		out = append(out, e.byRepo[r]...)
	}
	return out, ws
}

func (e storesEnv) DoctorStamp(_ context.Context, s onboard.SetupEntry) string {
	return e.stamps[s.Name]
}

// Store urls of the table (placeholders; the fake prober never dials).
const (
	urlShared  = "ssh://git@github.com/acme/shared-notes.git"
	urlUnique  = "ssh://git@github.com/acme/hub-notes.git"
	urlOffline = "ssh://git@git.acme.example/acme/vpn-notes.git"
	urlPlain   = "file:///srv/plain-folder"
	urlRoster  = "ssh://git@github.com/agency/roster.git"
)

// newStoresEnv is personal() with three known repos under its folders.
func newStoresEnv(t *testing.T) (storesEnv, []string) {
	t.Helper()
	env := newEnv(t)
	p := personal(env.home)
	env.store.cfg.Setups = []onboard.SetupEntry{p}
	base := strings.TrimSuffix(p.Folders[0], "**")
	roots := []string{base + "alpha", base + "beta", base + "gamma"}
	for _, r := range roots {
		env.store.known = append(env.store.known, onboard.KnownRepo{Root: r})
	}
	env.cache = NewMemCache(0, time.Now)
	return storesEnv{setupEnv: env, byRepo: map[string][]RepoStore{}, badRepos: map[string]string{}, stamps: map[string]string{}}, roots
}

// checksOf are the results of target in the setup report.
func checksOf(s SetupReport, target string) map[onboard.CheckID]onboard.CheckResult {
	out := map[onboard.CheckID]onboard.CheckResult{}
	for _, r := range s.Checks {
		if r.Target == target {
			out[r.Check] = r
		}
	}
	return out
}

// readsOf counts the store.read probes the prober got for url.
func readsOf(p *fakeProber, url string) int {
	n := 0
	for _, r := range p.reqs {
		if r.Check.ID == onboard.CheckStoreRead && r.Target == url {
			n++
		}
	}
	return n
}

// Repos × stores (#16): a store shared by two repos (two spellings of its
// url) is one target probed once and names both repos; a store of one repo
// names that repo; an offline store is a warning (never broken) naming its
// repo; a folder that is not a lore store is a marker warning; a pinned
// branch is its own store; a read-only store skips the write test; a repo
// whose stores.yaml does not read is a warning and checks nothing.
func TestAllAttachedStores(t *testing.T) {
	type want struct {
		target string
		url    string
		repos  []int // indexes into roots
		status map[onboard.CheckID]onboard.Status
	}
	ok := onboard.StatusOK
	cases := []struct {
		name     string
		stores   map[int][]RepoStore
		bad      []int
		script   map[probeKey]setup.ProbeResult
		want     []want
		status   Health
		warnings int
	}{
		{
			name: "shared by two repos, spelled two ways",
			stores: map[int][]RepoStore{
				0: {{Name: "team_notes", URL: urlShared, Key: "github.com/acme/shared-notes"}},
				1: {{Name: "notes", URL: "ssh://git@github.com/acme/shared-notes", Key: "github.com/acme/shared-notes"}},
			},
			want: []want{{target: "store team_notes", url: urlShared, repos: []int{0, 1},
				status: map[onboard.CheckID]onboard.Status{onboard.CheckStoreRead: ok, onboard.CheckStoreWrite: ok}}},
			status: StatusHealthy,
		},
		{
			name: "unique to one repo",
			stores: map[int][]RepoStore{
				0: {{Name: "team_notes", URL: urlShared, Key: "github.com/acme/shared-notes"}},
				2: {{Name: "hub_notes", URL: urlUnique, Key: "github.com/acme/hub-notes"}},
			},
			want: []want{
				{target: "store team_notes", url: urlShared, repos: []int{0}},
				{target: "store hub_notes", url: urlUnique, repos: []int{2}},
			},
			status: StatusHealthy,
		},
		{
			name:   "offline store is a warning naming its repos",
			stores: map[int][]RepoStore{1: {{Name: "vpn_notes", URL: urlOffline, Key: "git.acme.example/acme/vpn-notes"}}},
			script: map[probeKey]setup.ProbeResult{
				{onboard.CheckStoreRead, urlOffline}: {Status: onboard.StatusFail, Detail: "ssh: connect to host git.acme.example: Network is unreachable",
					Vars: map[string]string{onboard.VarFixVariant: string(onboard.FixUnreachable)}},
				{onboard.CheckStoreWrite, urlOffline}: {Status: onboard.StatusFail, Detail: "ssh: connect to host git.acme.example: Network is unreachable",
					Vars: map[string]string{onboard.VarFixVariant: string(onboard.FixUnreachable)}},
			},
			want: []want{{target: "store vpn_notes", url: urlOffline, repos: []int{1},
				status: map[onboard.CheckID]onboard.Status{onboard.CheckStoreRead: onboard.StatusWarn, onboard.CheckStoreWrite: onboard.StatusWarn}}},
			status: StatusDegraded,
		},
		{
			name:   "not a lore store",
			stores: map[int][]RepoStore{0: {{Name: "plain", URL: urlPlain, Key: "file:///srv/plain-folder"}}},
			script: map[probeKey]setup.ProbeResult{
				{onboard.CheckStoreMarker, urlPlain}: {Status: onboard.StatusFail, Detail: "no lore-store.yaml in the store yet"},
			},
			want: []want{{target: "store plain", url: urlPlain, repos: []int{0},
				status: map[onboard.CheckID]onboard.Status{onboard.CheckStoreMarker: onboard.StatusFail}}},
			status: StatusDegraded,
		},
		{
			name: "same url, another branch, is another store; same name, other url, gets a suffix",
			stores: map[int][]RepoStore{
				0: {{Name: "team_notes", URL: urlShared, Key: "github.com/acme/shared-notes"}},
				1: {{Name: "team_notes", URL: urlShared, Key: "github.com/acme/shared-notes", Branch: "staging"}},
				2: {{Name: "team_notes", URL: urlUnique, Key: "github.com/acme/hub-notes"}},
			},
			want: []want{
				{target: "store team_notes", url: urlShared, repos: []int{0}},
				{target: "store team_notes@staging", url: urlShared, repos: []int{1}},
				{target: "store team_notes~2", url: urlUnique, repos: []int{2}},
			},
			status: StatusHealthy,
		},
		{
			name: "read-only everywhere skips the write test; writable in one repo tests it",
			stores: map[int][]RepoStore{
				0: {{Name: "ro", URL: urlUnique, Key: "github.com/acme/hub-notes", ReadOnly: true}},
				1: {{Name: "ro", URL: urlUnique, Key: "github.com/acme/hub-notes", ReadOnly: true}, {Name: "rw", URL: urlShared, Key: "github.com/acme/shared-notes", ReadOnly: true}},
				2: {{Name: "rw", URL: urlShared, Key: "github.com/acme/shared-notes"}},
			},
			want: []want{
				{target: "store ro", url: urlUnique, repos: []int{0, 1}, status: map[onboard.CheckID]onboard.Status{onboard.CheckStoreWrite: onboard.StatusSkip}},
				{target: "store rw", url: urlShared, repos: []int{1, 2}, status: map[onboard.CheckID]onboard.Status{onboard.CheckStoreWrite: ok}},
			},
			status: StatusHealthy,
		},
		{
			name:     "a stores.yaml that does not read is a warning",
			stores:   map[int][]RepoStore{0: {{Name: "team_notes", URL: urlShared, Key: "github.com/acme/shared-notes"}}},
			bad:      []int{1},
			want:     []want{{target: "store team_notes", url: urlShared, repos: []int{0}}},
			status:   StatusDegraded,
			warnings: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, roots := newStoresEnv(t)
			for i, list := range c.stores {
				for _, s := range list {
					s.Repo = roots[i]
					env.byRepo[roots[i]] = append(env.byRepo[roots[i]], s)
				}
			}
			for _, i := range c.bad {
				env.badRepos[roots[i]] = ".lore/stores.yaml could not be read"
			}
			for k, v := range c.script {
				env.prober.script[k] = v
			}
			r := runAllT(t, env, RunIn{})
			s := r.Setups[0]
			for _, w := range c.want {
				got := checksOf(s, w.target)
				if len(got) != len(storeChecks) {
					t.Fatalf("%s: %d checks, want %d\n%s", w.target, len(got), len(storeChecks), r.Text())
				}
				for id, st := range w.status {
					if got[id].Status != st {
						t.Errorf("%s %s: status %s, want %s", w.target, id, got[id].Status, st)
					}
				}
				for _, res := range got {
					for _, i := range w.repos {
						if !strings.Contains(res.Detail, roots[i]) {
							t.Errorf("%s %s: detail %q does not name %s", w.target, res.Check, res.Detail, roots[i])
						}
					}
				}
				stores := 0 // a url on two branches is two stores
				for _, w2 := range c.want {
					if w2.url == w.url {
						stores++
					}
				}
				if n := readsOf(env.prober, w.url); n != stores {
					t.Errorf("%s: %d store.read probes of %s, want one per store", w.target, n, w.url)
				}
				line := areaLineOf(r.Text(), w.target)
				if want := repoCount(len(w.repos)); !strings.Contains(line, want) {
					t.Errorf("text line %q lacks %q", line, want)
				}
			}
			if r.Status != c.status {
				t.Errorf("status %s, want %s\n%s", r.Status, c.status, r.Text())
			}
			if n := countPrefix(r.Warnings, warnSetupPrefix); n != c.warnings {
				t.Errorf("%d setup warnings, want %d: %v", n, c.warnings, r.Warnings)
			}
		})
	}
}

// areaLineOf is the text's area line of a store target.
func areaLineOf(text, target string) string {
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, " "+target+"   ") {
			return l
		}
	}
	return ""
}

func countPrefix(ss []string, p string) int {
	n := 0
	for _, s := range ss {
		if strings.HasPrefix(s, p) {
			n++
		}
	}
	return n
}

// Only an unreachable store is softened: an attached store that answered
// and refused the read is still an error-level fail (broken), and the
// org's own roster store is not checked twice.
func TestAllAttachedStoreRefusalAndRoster(t *testing.T) {
	env, roots := newStoresEnv(t)
	env.byRepo[roots[0]] = []RepoStore{{Repo: roots[0], Name: "hub_notes", URL: urlUnique, Key: "github.com/acme/hub-notes"}}
	env.prober.script[probeKey{onboard.CheckStoreRead, urlUnique}] = setup.ProbeResult{Status: onboard.StatusFail,
		Detail: "ERROR: Repository not found."}
	r := runAllT(t, env, RunIn{})
	if got := checksOf(r.Setups[0], "store hub_notes")[onboard.CheckStoreRead]; got.Status != onboard.StatusFail || r.Status != StatusBroken {
		t.Fatalf("refused read: %s, run %s", got.Status, r.Status)
	}

	// A member setup of org agency: the roster store attached in a repo is
	// the org's "store agency", not a second target.
	env2, _ := newStoresEnv(t)
	a := acme(env2.home)
	env2.store.cfg.Setups = []onboard.SetupEntry{a}
	root := strings.TrimSuffix(a.Folders[0], "**") + "hub"
	env2.store.known = []onboard.KnownRepo{{Root: root}}
	env2.byRepo[root] = []RepoStore{{Repo: root, Name: "agency", URL: urlRoster, Key: "github.com/agency/roster", Org: "agency"},
		{Repo: root, Name: "hub_notes", URL: urlUnique, Key: "github.com/acme/hub-notes"}}
	r = runAllT(t, env2, RunIn{})
	var targets []string
	for _, c := range r.Setups[0].Checks {
		if c.Check == onboard.CheckStoreRead {
			targets = append(targets, c.Target)
		}
	}
	slices.Sort(targets)
	if want := []string{"store agency", "store hub_notes", "store me"}; !slices.Equal(targets, want) {
		t.Fatalf("store targets %v, want %v", targets, want)
	}
	if readsOf(env2.prober, urlRoster) != 0 {
		t.Fatal("the roster store was probed again as an attached store")
	}
}

// Invalidation (#17): a cached result is reused only under the stamp it
// was made with. A setup edit or key change (a new StampEnv stamp)
// re-probes that setup only; an attached store whose url changed under the
// same name re-probes that store only; the repos a cached store names are
// this run's, not the cached run's.
func TestAllCacheInvalidation(t *testing.T) {
	type step struct {
		name    string
		change  func(env storesEnv, roots []string)
		reprobe []string // targets probed again (by setup, store)
		kept    []string // targets served from the cache
	}
	steps := []step{
		{name: "nothing changed", kept: []string{"personal|", "personal|store team_notes", "acme-hub|"}},
		{name: "personal edited", change: func(env storesEnv, _ []string) { env.stamps["personal"] = "edited" },
			reprobe: []string{"personal|", "personal|store team_notes"}, kept: []string{"acme-hub|"}},
		{name: "acme-hub key changed", change: func(env storesEnv, _ []string) { env.stamps["acme-hub"] = "new key" },
			reprobe: []string{"acme-hub|"}, kept: []string{"personal|", "personal|store team_notes"}},
		{name: "store url changed", change: func(env storesEnv, roots []string) {
			env.byRepo[roots[0]] = []RepoStore{{Repo: roots[0], Name: "team_notes", URL: urlUnique, Key: "github.com/acme/hub-notes"}}
		}, reprobe: []string{"personal|store team_notes"}, kept: []string{"personal|", "acme-hub|"}},
	}
	env, roots := newStoresEnv(t)
	env.store.cfg.Setups = append(env.store.cfg.Setups, acme(env.home))
	env.byRepo[roots[0]] = []RepoStore{{Repo: roots[0], Name: "team_notes", URL: urlShared, Key: "github.com/acme/shared-notes"}}
	runAllT(t, env, RunIn{})
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			if s.change != nil {
				s.change(env, roots)
			}
			before := len(env.prober.reqs)
			runAllT(t, env, RunIn{})
			probed := map[string]bool{}
			for _, r := range env.prober.reqs[before:] {
				if !Cacheable(r.Check) {
					continue // local file reads are probed on every run
				}
				target := ""
				switch {
				case r.Target == urlShared || r.Target == urlUnique:
					target = "store team_notes"
				case strings.HasPrefix(r.Target, targetStore):
					target = r.Target
				case r.Target != TargetSetup:
					continue // repo checks follow the setup's stamp too; keyed below by setup-wide ones
				}
				probed[r.Setup.Name+"|"+target] = true
			}
			for _, k := range s.reprobe {
				if !probed[k] {
					t.Errorf("%s was served from the cache", k)
				}
			}
			for _, k := range s.kept {
				if probed[k] {
					t.Errorf("%s was probed again", k)
				}
			}
		})
	}
	// A cached store result names the repos attaching it now.
	env.byRepo[roots[1]] = []RepoStore{{Repo: roots[1], Name: "notes", URL: urlUnique, Key: "github.com/acme/hub-notes"}}
	before := len(env.prober.reqs)
	r := runAllT(t, env, RunIn{})
	if readsOf(&fakeProber{reqs: env.prober.reqs[before:]}, urlUnique) != 0 {
		t.Fatal("the store was probed again; the repos case needs a cached result")
	}
	got := checksOf(r.Setups[0], "store team_notes")[onboard.CheckStoreRead]
	if !strings.Contains(got.Detail, roots[0]) || !strings.Contains(got.Detail, roots[1]) || strings.Count(got.Detail, "attached by") != 1 {
		t.Fatalf("cached detail %q: want both repos named once", got.Detail)
	}
}

// A known repo under no setup's folders (#16 follow-up): its stores are
// checked under the default setup, the setup lore uses in such a folder,
// and never under another setup; repo checks are not added for it. With
// no default setup, nothing is probed and one info line names the repo
// (a repo that attaches no store gets none); the run stays healthy. A
// --setup run of a non-default setup checks none of them.
func TestAllAttachedStoresOutsideEverySetup(t *testing.T) {
	cases := []struct {
		name       string
		defaultSet bool
		only       string
		wantUnder  string // setup report holding "store hub_notes"; "" = nobody
		wantNote   bool
	}{
		{name: "default setup checks it", defaultSet: true, wantUnder: "personal"},
		{name: "--setup of the default checks it", defaultSet: true, only: "personal", wantUnder: "personal"},
		{name: "--setup of another setup does not", defaultSet: true, only: "acme-hub"},
		{name: "no default: an info line, no probe", wantNote: true},
		{name: "no default, --setup run: no line", only: "acme-hub"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, _ := newStoresEnv(t)
			p, a := personal(env.home), acme(env.home)
			p.Default = c.defaultSet
			env.store.cfg.Setups = []onboard.SetupEntry{p, a}
			outside := env.home + "/src/loose"  // under neither setup's folders
			bare := env.home + "/src/no-stores" // outside too, attaches nothing
			env.store.known = []onboard.KnownRepo{{Root: outside}, {Root: bare}}
			env.byRepo[outside] = []RepoStore{{Repo: outside, Name: "hub_notes", URL: urlUnique, Key: "github.com/acme/hub-notes"}}
			r := runAllT(t, env, RunIn{Setup: c.only})
			for _, s := range r.Setups {
				got := checksOf(s, "store hub_notes")
				if s.Name == c.wantUnder {
					if got[onboard.CheckStoreRead].Status != onboard.StatusOK || !strings.Contains(got[onboard.CheckStoreRead].Detail, outside) {
						t.Errorf("setup %s: store hub_notes read %+v, want ok naming %s\n%s", s.Name, got[onboard.CheckStoreRead], outside, r.Text())
					}
				} else if len(got) > 0 {
					t.Errorf("setup %s checked store hub_notes; only %q may\n%s", s.Name, c.wantUnder, r.Text())
				}
				for _, ch := range s.Checks {
					if strings.Contains(ch.Target, "loose") || strings.Contains(ch.Target, "no-stores") {
						t.Errorf("setup %s got a repo check for a repo outside its folders: %+v", s.Name, ch)
					}
				}
			}
			if c.wantUnder == "" && readsOf(env.prober, urlUnique) != 0 {
				t.Errorf("store probed %d times with no setup applying to it", readsOf(env.prober, urlUnique))
			}
			note := UncheckedNote(outside)
			if has := slices.Contains(r.Notes, note); has != c.wantNote {
				t.Errorf("note %q present %v, want %v (notes %v)", note, has, c.wantNote, r.Notes)
			}
			if c.wantNote && !strings.Contains(r.Text(), note) {
				t.Errorf("text has no %q:\n%s", note, r.Text())
			}
			if slices.Contains(r.Notes, UncheckedNote(bare)) {
				t.Errorf("a repo attaching no store got a note")
			}
			if r.Status != StatusHealthy {
				t.Errorf("status %s, want healthy\n%s", r.Status, r.Text())
			}
		})
	}
}
