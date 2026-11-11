package doctor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/skill"
)

// testBinV is the fake binary version (the skill stamp in tool tests).
const testBinV = "0.1.18"

// fakeStore is setup.Store over fixed values.
type fakeStore struct {
	cfg    onboard.Config
	cfgErr error
	active setup.ActiveSetup
	known  []onboard.KnownRepo
}

func (s *fakeStore) Config(context.Context) (onboard.Config, error) { return s.cfg, s.cfgErr }
func (*fakeStore) PutSetup(context.Context, onboard.SetupEntry) (bool, error) {
	return false, nil
}
func (*fakeStore) RemoveSetup(context.Context, string) (bool, error) { return false, nil }
func (*fakeStore) PinRepo(context.Context, string) (string, error)   { return "", nil }
func (s *fakeStore) Active(_ context.Context, flag string) (setup.ActiveSetup, error) {
	if flag != "" {
		return setup.ActiveSetup{Name: flag, Why: setup.WhyFlag}, nil
	}
	if s.active.Why == "" {
		return setup.ActiveSetup{Why: setup.WhyNone}, nil
	}
	return s.active, nil
}
func (*fakeStore) Progress(context.Context, string) ([]onboard.Progress, error) { return nil, nil }
func (*fakeStore) PutProgress(context.Context, onboard.Progress) error          { return nil }
func (*fakeStore) DeleteProgress(context.Context, string) error                 { return nil }
func (s *fakeStore) KnownRepos(context.Context) ([]onboard.KnownRepo, error)    { return s.known, nil }
func (*fakeStore) TouchRepo(context.Context, onboard.KnownRepo) error           { return nil }

// probeKey picks a scripted probe outcome.
type probeKey struct {
	check  onboard.CheckID
	target string
}

// fakeProber answers ok unless scripted; it records every request.
type fakeProber struct {
	mu     sync.Mutex
	script map[probeKey]setup.ProbeResult
	reqs   []setup.ProbeRequest
	// during, when set, runs inside Probe (a client that goes away mid-run).
	during func()
}

func (p *fakeProber) Probe(_ context.Context, reqs []setup.ProbeRequest) []setup.ProbeResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.during != nil {
		p.during()
	}
	out := make([]setup.ProbeResult, len(reqs))
	for i, r := range reqs {
		p.reqs = append(p.reqs, r)
		res, ok := p.script[probeKey{r.Check.ID, r.Target}]
		if !ok {
			res = setup.ProbeResult{Status: onboard.StatusOK}
		}
		res.Check, res.Target = r.Check.ID, r.Target
		out[i] = res
	}
	return out
}

func (*fakeProber) Prefill(context.Context, onboard.PrefillID, onboard.SetupEntry) (setup.PrefillOut, error) {
	return setup.PrefillOut{}, nil
}

// setupEnv is a doctor Env with the setup engine.
type setupEnv struct {
	fakeEnv
	store  *fakeStore
	prober *fakeProber
	home   string
	top    string
	cache  Cache
}

func (e *setupEnv) SetupStore(context.Context) (setup.Store, error)   { return e.store, nil }
func (e *setupEnv) SetupProber(context.Context) (setup.Prober, error) { return e.prober, nil }
func (e *setupEnv) RepoTop(context.Context) (string, error)           { return e.top, nil }
func (*setupEnv) BinaryVersion() string                               { return testBinV }
func (*setupEnv) ToolProfiles(context.Context) ([]toolprofile.Profile, error) {
	return nil, nil
}
func (e *setupEnv) SkillHome(context.Context) (string, error) { return e.home, nil }
func (*setupEnv) SkillTools(context.Context) (skill.ToolChooser, error) {
	return nil, nil
}

// cachedEnv adds the cache upgrade.
type cachedEnv struct{ *setupEnv }

func (e cachedEnv) DoctorCache(context.Context) Cache { return e.cache }

// Example setups (the design's example names).
func personal(home string) onboard.SetupEntry {
	return onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true,
		Folders:  []string{home + "/www/mira/**"},
		Identity: onboard.SetupIdentity{Name: "Mira", Emails: []string{"mira@personal.example"}},
		GitHub:   onboard.SetupGitHub{User: "mira-personal"}, Secrets: onboard.SetupSecrets{Backend: "gopass"}}
}

func acme(home string) onboard.SetupEntry {
	return onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindMember, Org: "agency",
		Folders: []string{home + "/www/agency/**"},
		GitHub:  onboard.SetupGitHub{User: "mira-agency", HostAlias: "github-agency"}}
}

func newEnv(t *testing.T, setups ...onboard.SetupEntry) *setupEnv {
	t.Helper()
	return &setupEnv{store: &fakeStore{cfg: onboard.Config{Setups: setups}}, prober: &fakeProber{script: map[probeKey]setup.ProbeResult{}},
		home: t.TempDir()}
}

func runAllT(t *testing.T, env Env, in RunIn) Report {
	t.Helper()
	in.All = true
	r, err := Run(context.Background(), env, in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The 6.4 exit matrix: only ok/skip → 0; a warn or a probe that could not
// run → 1 (never 2); an error-level fail → 2; a warn-level fail → 1.
func TestAllExitMatrix(t *testing.T) {
	sshAuth := probeKey{onboard.CheckGitHubSSHAuth, TargetSetup}
	alias := probeKey{onboard.CheckGitHubAliasConfig, TargetSetup}
	cases := []struct {
		name   string
		script map[probeKey]setup.ProbeResult
		status Health
		exit   int
	}{
		{"all ok", nil, StatusHealthy, 0},
		{"skip only", map[probeKey]setup.ProbeResult{alias: {Status: onboard.StatusSkip, Detail: "no alias"}}, StatusHealthy, 0},
		{"warn", map[probeKey]setup.ProbeResult{alias: {Status: onboard.StatusWarn}}, StatusDegraded, 1},
		{"warn-level fail", map[probeKey]setup.ProbeResult{alias: {Status: onboard.StatusFail}}, StatusDegraded, 1},
		{"probe error is degraded, never broken", map[probeKey]setup.ProbeResult{sshAuth: {Status: onboard.StatusError, Detail: "ssh timed out"}}, StatusDegraded, 1},
		{"error-level fail", map[probeKey]setup.ProbeResult{sshAuth: {Status: onboard.StatusFail}}, StatusBroken, 2},
		{"mix", map[probeKey]setup.ProbeResult{sshAuth: {Status: onboard.StatusFail}, alias: {Status: onboard.StatusWarn},
			{onboard.CheckGitHubGHAccount, TargetSetup}: {Status: onboard.StatusError}}, StatusBroken, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newEnv(t)
			env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home)}
			for k, v := range c.script {
				env.prober.script[k] = v
			}
			r := runAllT(t, env, RunIn{})
			if r.Status != c.status || r.Exit != c.exit || r.ExitStatus() != c.exit || r.OK != (c.exit == 0) {
				t.Fatalf("status %s exit %d, want %s %d\n%s", r.Status, r.Exit, c.status, c.exit, r.Text())
			}
			if len(r.Setups) != 1 || r.Setups[0].Status != c.status {
				t.Fatalf("setup status %+v", r.Setups)
			}
		})
	}
}

// statusOf directly, including the level of a fail.
func TestStatusOf(t *testing.T) {
	res := func(st onboard.Status, l onboard.Level) onboard.CheckResult {
		return onboard.CheckResult{Status: st, Level: l}
	}
	cases := []struct {
		rs   []onboard.CheckResult
		want Health
	}{
		{nil, StatusHealthy},
		{[]onboard.CheckResult{res(onboard.StatusOK, onboard.LevelError), res(onboard.StatusSkip, onboard.LevelError)}, StatusHealthy},
		{[]onboard.CheckResult{res(onboard.StatusError, onboard.LevelError)}, StatusDegraded},
		{[]onboard.CheckResult{res(onboard.StatusFail, onboard.LevelWarn)}, StatusDegraded},
		{[]onboard.CheckResult{res(onboard.StatusWarn, onboard.LevelError)}, StatusDegraded},
		{[]onboard.CheckResult{res(onboard.StatusError, onboard.LevelError), res(onboard.StatusFail, onboard.LevelError)}, StatusBroken},
	}
	for i, c := range cases {
		if got := statusOf(c.rs); got != c.want {
			t.Errorf("case %d: %s want %s", i, got, c.want)
		}
	}
}

// A degraded single-repo report stays degraded when the setups are
// healthy, and a broken setup raises it.
func TestAllFoldsBaseStatus(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home)}
	env.runs = []CheckRun{{Check: "task_lead", Findings: []Finding{{Check: "task_lead", Level: LevelWarning, Line: "task_lead: tsk_1 has no lead"}}}}
	if r := runAllT(t, env, RunIn{}); r.Exit != 1 {
		t.Fatalf("exit %d", r.Exit)
	}
	env.prober.script[probeKey{onboard.CheckGitHubSSHKey, TargetSetup}] = setup.ProbeResult{Status: onboard.StatusFail}
	if r := runAllT(t, env, RunIn{}); r.Exit != 2 {
		t.Fatalf("exit %d", r.Exit)
	}
}

// 6.3: the --all JSON keeps every existing key and adds setups, machine
// and counts; a warn result carries a fix whose command prints [].
func TestAllJSONShape(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home), acme(env.home)}
	env.prober.script[probeKey{onboard.CheckStoreWrite, TargetStore("agency")}] = setup.ProbeResult{Status: onboard.StatusFail,
		Detail: "Permission to agency-org/notes.git denied to mira-agency"}
	r := runAllT(t, env, RunIn{})
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"schema_version", "ok", "status", "db_ok", "wal_size_bytes", "identity", "binary_version", "exit_status", "setups", "machine", "counts"} {
		if _, ok := m[k]; !ok {
			t.Errorf("key %s missing: %s", k, b)
		}
	}
	var shape struct {
		SchemaVersion int `json:"schema_version"`
		Setups        []struct {
			Name    string                `json:"name"`
			Kind    string                `json:"kind"`
			Org     string                `json:"org"`
			Default *bool                 `json:"default"`
			Folders []string              `json:"folders"`
			Status  string                `json:"status"`
			Checks  []onboard.CheckResult `json:"checks"`
		} `json:"setups"`
		Machine struct {
			Checks []onboard.CheckResult `json:"checks"`
		} `json:"machine"`
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(b, &shape); err != nil {
		t.Fatal(err)
	}
	if shape.SchemaVersion != 1 || len(shape.Setups) != 2 || shape.Setups[0].Default == nil || !*shape.Setups[0].Default {
		t.Fatalf("shape %+v", shape)
	}
	a := shape.Setups[1]
	if a.Name != "acme-hub" || a.Kind != "member" || a.Org != "agency" || a.Status != string(StatusBroken) || len(a.Folders) != 1 {
		t.Fatalf("acme %+v", a)
	}
	for _, k := range []string{"ok", "warn", "fail", "skip", "error"} {
		if _, ok := shape.Counts[k]; !ok {
			t.Errorf("counts.%s missing", k)
		}
	}
	if shape.Counts["fail"] != 1 || shape.Counts["ok"] == 0 {
		t.Errorf("counts %v", shape.Counts)
	}
	if len(shape.Machine.Checks) != 1 || shape.Machine.Checks[0].Check != onboard.CheckFoldersOverlap || shape.Machine.Checks[0].Target != TargetMachine {
		t.Errorf("machine %+v", shape.Machine)
	}
	if !strings.Contains(string(b), `"check":"store.write","status":"fail","level":"error","target":"store agency"`) {
		t.Errorf("store.write result: %s", b)
	}
	if !strings.Contains(string(b), `"command":[]`) && !strings.Contains(string(b), `"command":["`) {
		t.Errorf("fix command never printed: %s", b)
	}
	// Plain doctor keeps the old shape: no machine, no counts.
	plain, err := Run(context.Background(), newEnv(t), RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := json.Marshal(plain)
	for _, k := range []string{`"machine"`, `"counts"`, `"setups"`} {
		if strings.Contains(string(pb), k) {
			t.Errorf("plain report has %s: %s", k, pb)
		}
	}
}

// 6.2 text for two setups: area lines, a repo warning with its per-repo
// fix, an error with its repair line, and the count line.
func TestAllTextTwoSetups(t *testing.T) {
	env := newEnv(t)
	p, a := personal(env.home), acme(env.home)
	env.store.cfg.Setups = []onboard.SetupEntry{p, a}
	repo := env.home + "/www/mira/hub"
	env.store.known = []onboard.KnownRepo{{Root: repo}, {Root: "/elsewhere/x"}}
	env.prober.script[probeKey{onboard.CheckIdentityGitEmail, repo}] = setup.ProbeResult{Status: onboard.StatusWarn,
		Detail: "hub commits as mira@old.example", Vars: map[string]string{onboard.VarGitEmail: "mira@old.example"}}
	env.prober.script[probeKey{onboard.CheckStoreWrite, TargetStore("agency")}] = setup.ProbeResult{Status: onboard.StatusFail,
		Detail: "Permission to agency-org/notes.git denied to mira-agency"}
	env.prober.script[probeKey{onboard.CheckGitHubSSHKey, TargetSetup}] = setup.ProbeResult{Status: onboard.StatusOK, Detail: "SHA256:9fQ"}
	r := runAllT(t, env, RunIn{})
	txt := r.Text()
	want := []string{
		"setup personal (solo, default) ─ " + env.home + "/www/mira/**\n",
		"  ✓ identity    mira@personal.example\n",
		"  ✓ github      mira-personal via github.com (SHA256:9fQ)\n",
		"  ✓ store me    read ✓ write ✓ private ✓\n",
		"  ✓ secrets     gopass\n",
		"  ⚠ identity.git_email  hub commits as mira@old.example\n",
		"      fix: git -C " + filepath.ToSlash(repo) + " config user.email mira@personal.example\n",
		"\nsetup acme-hub (member of agency) ─ " + env.home + "/www/agency/**\n",
		"  ✗ store agency   read ✓ write ✗ private ✓\n",
		"  ✗ store.write  agency: Permission to agency-org/notes.git denied to mira-agency\n",
		"      repair: lore setup repair acme-hub\n",
		" · 1 warning · 1 error → broken (exit 2)\n",
		"\n2 setups · ",
	}
	for _, w := range want {
		if !strings.Contains(txt, w) {
			t.Errorf("text lacks %q:\n%s", w, txt)
		}
	}
	// The repo outside every setup's folders is not checked.
	for _, rq := range env.prober.reqs {
		if strings.Contains(rq.Target, "/elsewhere") {
			t.Errorf("checked a repo outside the setups: %s", rq.Target)
		}
		if rq.Target == repo && rq.Setup.Name != "personal" {
			t.Errorf("repo checked under %s", rq.Setup.Name)
		}
	}
	if t.Failed() {
		t.Log(txt)
	}
}

// --setup limits --all to one setup; an unknown one is E_SETUP_NOT_FOUND.
func TestAllOnlySetup(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home), acme(env.home)}
	r := runAllT(t, env, RunIn{Setup: "acme-hub"})
	if len(r.Setups) != 1 || r.Setups[0].Name != "acme-hub" {
		t.Fatalf("%+v", r.Setups)
	}
	ctx := op.WithSetup(context.Background(), "personal")
	r2, err := Run(ctx, env, RunIn{All: true})
	if err != nil || len(r2.Setups) != 1 || r2.Setups[0].Name != "personal" {
		t.Fatalf("%v %+v", err, r2.Setups)
	}
	if _, err := Run(context.Background(), env, RunIn{All: true, Setup: "nope"}); !errs.Has(err, errs.SetupNotFound) {
		t.Fatalf("err %v", err)
	}
}

// Without the setup engine plain doctor is unchanged and --all refuses.
func TestAllUnsupported(t *testing.T) {
	_, err := Run(context.Background(), &fakeEnv{}, RunIn{All: true})
	if !errs.Has(err, errs.Unsupported) {
		t.Fatalf("err %v", err)
	}
}

// An invalid setups list is a machine setup.config error (broken).
func TestAllConfigInvalid(t *testing.T) {
	env := newEnv(t)
	env.store.cfgErr = errs.New(errs.ConfigInvalid, "setups[x]: bad")
	r := runAllT(t, env, RunIn{})
	if r.Exit != 2 || len(r.Machine.Checks) != 1 || r.Machine.Checks[0].Check != onboard.CheckSetupConfig {
		t.Fatalf("%+v", r)
	}
}

// folders.overlap: two setups with the same glob break the run.
func TestAllFoldersOverlap(t *testing.T) {
	env := newEnv(t)
	p, a := personal(env.home), acme(env.home)
	a.Folders = p.Folders
	env.store.cfg.Setups = []onboard.SetupEntry{p, a}
	r := runAllT(t, env, RunIn{})
	if r.Exit != 2 || r.Machine.Checks[0].Status != onboard.StatusFail {
		t.Fatalf("%+v", r.Machine)
	}
	if !strings.Contains(r.Text(), "\nmachine\n  ✗ folders.overlap  ") {
		t.Fatalf("text:\n%s", r.Text())
	}
}

// Plain doctor adds the [setup] section for the active setup only, with
// the current repo's checks.
func TestPlainActiveSetup(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home), acme(env.home)}
	env.store.active = setup.ActiveSetup{Name: "acme-hub", Why: setup.WhyFolder}
	env.top = env.home + "/www/agency/app"
	env.host = &fakeHost{base: healthy()}
	r, err := Run(context.Background(), env, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Setups) != 1 || r.Setups[0].Name != "acme-hub" || r.Setups[0].Why != setup.WhyFolder {
		t.Fatalf("%+v", r.Setups)
	}
	sawRepo := false
	for _, c := range r.Setups[0].Checks {
		if c.Target == env.top {
			sawRepo = true
		}
	}
	txt := r.Text()
	if !sawRepo || !strings.Contains(txt, "[setup]\n  setup acme-hub (member of agency) ─ ") || !strings.Contains(txt, "chosen by folder") {
		t.Fatalf("repo %v text:\n%s", sawRepo, txt)
	}
	// No active setup: no section, the old report.
	env.store.active = setup.ActiveSetup{}
	r, _ = Run(context.Background(), env, RunIn{})
	if len(r.Setups) != 0 || strings.Contains(r.Text(), "[setup]") {
		t.Fatalf("%+v", r.Setups)
	}
}

// Plain doctor's summary covers the active setup's checks (decision 6):
// a setup check that warns is counted in counts and degrades the status,
// so the top level never reads "all ok" while setups[].checks warns.
func TestPlainCountsSetupChecks(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home)}
	env.store.active = setup.ActiveSetup{Name: "personal", Why: setup.WhyFolder}
	env.host = &fakeHost{base: healthy()}
	env.prober.script[probeKey{onboard.CheckGitHubAliasConfig, TargetSetup}] = setup.ProbeResult{Status: onboard.StatusWarn, Detail: "no alias"}
	r, err := Run(context.Background(), env, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Setups) != 1 {
		t.Fatalf("setups %+v", r.Setups)
	}
	var want Counts
	for _, c := range r.Setups[0].Checks {
		want.add(c)
	}
	if want.Warn != 1 || r.Counts != want {
		t.Fatalf("counts %+v, want %+v (the setup's own checks)", r.Counts, want)
	}
	if r.OK || r.Status != StatusDegraded || r.Exit != 1 {
		t.Fatalf("ok %v status %s exit %d with a setup warning", r.OK, r.Status, r.Exit)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Counts *Counts `json:"counts"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.Counts == nil || m.Counts.Warn != 1 {
		t.Fatalf("json counts %+v: %s", m.Counts, b)
	}
}

// TestRowChecksNotCached: the source-of-truth checks that read the open
// repo's row files and stores.yaml are probed every run, so the doctor
// right after their fix (`lore actor refresh`, `lore store set-url`) no
// longer reports what the fix just repaired; the pure config checks
// beside them stay cached.
func TestRowChecksNotCached(t *testing.T) {
	t.Parallel()
	for _, id := range []onboard.CheckID{onboard.CheckIdentityActorCopies, onboard.CheckStoreAddress} {
		c, ok := checkFor(id, onboard.KindSolo, "")
		if !ok {
			t.Fatalf("%s is not in the catalog for a solo setup", id)
		}
		if Cacheable(c) {
			t.Errorf("%s is cacheable: a cached result outlives its fix", id)
		}
	}
	if c, ok := checkFor(onboard.CheckIdentityTopLevel, onboard.KindSolo, ""); !ok || !Cacheable(c) {
		t.Errorf("identity.top_level (in the setup stamp) should stay cacheable: %v", ok)
	}
}

// The 60 s cache: a second run reuses results; --fresh probes again; an
// expired entry probes again.
func TestAllCache(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home)}
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	env.cache = NewMemCache(0, clock)
	ce := cachedEnv{env}
	runAllT(t, ce, RunIn{})
	first := len(env.prober.reqs)
	if first == 0 {
		t.Fatal("nothing probed")
	}
	local := 0 // local file checks (Cacheable false) are probed every run
	for _, r := range env.prober.reqs {
		if !Cacheable(r.Check) {
			local++
		}
	}
	if local == 0 || local == first {
		t.Fatalf("want both cached and local checks, got %d local of %d", local, first)
	}
	// Results carry time.Now; age the clock relative to them.
	now = time.Now()
	runAllT(t, ce, RunIn{})
	if len(env.prober.reqs) != first+local {
		t.Fatalf("cache missed: %d → %d (%d local)", first, len(env.prober.reqs), local)
	}
	for _, r := range env.prober.reqs[first:] {
		if Cacheable(r.Check) {
			t.Errorf("%s was probed again, not served from the cache", r.Check.ID)
		}
	}
	first += local
	runAllT(t, ce, RunIn{Fresh: true})
	if len(env.prober.reqs) != first+(first-local) {
		t.Fatalf("--fresh used the cache: %d", len(env.prober.reqs))
	}
	now = time.Now().Add(CacheTTL + time.Second)
	runAllT(t, ce, RunIn{})
	if len(env.prober.reqs) != first+2*(first-local) {
		t.Fatalf("expired entry reused: %d", len(env.prober.reqs))
	}
}

// sinkEnv records the results each run hands its HealthSink.
type sinkEnv struct {
	*setupEnv
	got map[string][]onboard.CheckResult
}

func (e sinkEnv) RecordHealth(_ context.Context, setup string, rs []onboard.CheckResult) error {
	e.got[setup] = rs
	return nil
}

// Every doctor run (--all, one setup, plain doctor's active setup) hands
// each setup's results to the HealthSink, so Home's Health cell shows the
// run just made, from any process.
func TestAllRecordsHealth(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home), acme(env.home)}
	se := sinkEnv{setupEnv: env, got: map[string][]onboard.CheckResult{}}
	r := runAllT(t, se, RunIn{})
	for _, s := range r.Setups {
		if got := se.got[s.Name]; len(got) != len(s.Checks) || len(got) == 0 {
			t.Errorf("%s: recorded %d results, report has %d", s.Name, len(got), len(s.Checks))
		}
	}
	delete(se.got, "acme-hub")
	runAllT(t, se, RunIn{Setup: "acme-hub"})
	if _, ok := se.got["acme-hub"]; !ok {
		t.Error("one-setup run not recorded")
	}
}

// abortEnv is a cached doctor Env with a HealthSink.
type abortEnv struct {
	cachedEnv
	got map[string][]onboard.CheckResult
}

func (e abortEnv) RecordHealth(_ context.Context, setup string, rs []onboard.CheckResult) error {
	e.got[setup] = rs
	return nil
}

// A run whose caller went away mid-probe (the page left Doctor while it
// ran) answers with "context canceled" and slot timeouts: those describe
// the abort, not the machine, so neither the 60 s cache nor Home's Health
// (and the Summary that reads it) keeps them. Before the fix, opening the
// setup right after showed ten "?" rows ("waited too long for a free probe
// slot") under "To look at", and every lore process reused them for 60 s.
func TestAbortedRunKeepsNothing(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home)}
	cache := NewMemCache(0, time.Now)
	env.cache = cache
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.prober.during = cancel
	for _, id := range []onboard.CheckID{"github.ssh_auth", "github.gh_account", "store.read"} {
		env.prober.script[probeKey{id, ""}] = setup.ProbeResult{Status: onboard.StatusError, Detail: "context canceled"}
	}
	ae := abortEnv{cachedEnv: cachedEnv{env}, got: map[string][]onboard.CheckResult{}}
	_, _ = Run(ctx, ae, RunIn{All: true}) // a canceled run may report the cancel; what matters is what it kept
	if got := cache.Results("personal"); len(got) != 0 {
		t.Errorf("an aborted run cached %d results, e.g. %s: %s", len(got), got[0].Check, got[0].Detail)
	}
	if got, ok := ae.got["personal"]; ok {
		t.Errorf("an aborted run recorded %d results as the setup's health", len(got))
	}
	// The same run, not aborted, keeps both.
	env.prober.during = nil
	runAllT(t, ae, RunIn{})
	if len(cache.Results("personal")) == 0 || len(ae.got["personal"]) == 0 {
		t.Error("a finished run kept nothing")
	}
}

// Tools checks come from skill.status: installed at this version → ok;
// a tool without the skill → warn with the install fix; a tool with no
// skill folder → skip.
func TestAllTools(t *testing.T) {
	env := newEnv(t)
	p := personal(env.home)
	p.Tools = []string{"claude_code", "codex", "cursor"}
	env.store.cfg.Setups = []onboard.SetupEntry{p}
	reg, err := toolprofile.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ins := skill.Installer{Home: env.home, Version: testBinV, Registry: reg}
	if _, err := ins.Install(context.Background(), skill.InstallIn{Tools: []string{"claude_code"}}); err != nil {
		t.Fatal(err)
	}
	r := runAllT(t, env, RunIn{})
	got := map[string]onboard.CheckResult{}
	for _, c := range r.Setups[0].Checks {
		got[string(c.Check)+" "+c.Target] = c
	}
	want := map[string]onboard.Status{
		"tools.skill_installed claude_code": onboard.StatusOK,
		"tools.skill_version claude_code":   onboard.StatusOK,
		"tools.skill_modified claude_code":  onboard.StatusOK,
		"tools.skill_installed codex":       onboard.StatusWarn,
		"tools.skill_version codex":         onboard.StatusSkip,
		"tools.skill_installed cursor":      onboard.StatusSkip,
	}
	for k, st := range want {
		if got[k].Status != st {
			t.Errorf("%s: %s want %s", k, got[k].Status, st)
		}
	}
	fix := got["tools.skill_installed codex"].Fix
	if strings.Join(fix.Command, " ") != "lore skill install --tool codex" || !fix.Runnable {
		t.Errorf("fix %+v", fix)
	}
	if r.Exit != 1 {
		t.Errorf("exit %d", r.Exit)
	}
	if !strings.Contains(r.Text(), "  ⚠ tools       claude_code (skill "+testBinV+"), codex, cursor\n") {
		t.Errorf("text:\n%s", r.Text())
	}
}

// MemCache.Results (setup.list's Health source, ONB 5.4): only the named
// setup's unexpired results, in check-then-target order.
func TestMemCacheResults(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	c := NewMemCache(0, func() time.Time { return now })
	fresh := now.Add(-time.Second)
	c.Put(CacheKey{Setup: "personal", Check: "tools.skill_installed", Target: "codex"}, onboard.CheckResult{Check: "tools.skill_installed", Target: "codex", CheckedAt: fresh})
	c.Put(CacheKey{Setup: "personal", Check: "identity.git_email"}, onboard.CheckResult{Check: "identity.git_email", CheckedAt: fresh})
	c.Put(CacheKey{Setup: "personal", Check: "tools.skill_installed", Target: "claude_code"}, onboard.CheckResult{Check: "tools.skill_installed", Target: "claude_code", CheckedAt: fresh})
	c.Put(CacheKey{Setup: "acme-hub", Check: "identity.git_email"}, onboard.CheckResult{Check: "identity.git_email", CheckedAt: fresh})
	c.Put(CacheKey{Setup: "personal", Check: "github.ssh_auth"}, onboard.CheckResult{Check: "github.ssh_auth", CheckedAt: now.Add(-CacheTTL)})
	got := c.Results("personal")
	var ids []string
	for _, r := range got {
		ids = append(ids, string(r.Check)+"/"+r.Target)
	}
	want := "identity.git_email/ tools.skill_installed/claude_code tools.skill_installed/codex"
	if strings.Join(ids, " ") != want {
		t.Fatalf("got %q want %q", strings.Join(ids, " "), want)
	}
	if len(c.Results("nobody")) != 0 {
		t.Fatal("results for an unknown setup")
	}
}

// Outside a repo (RepoTop names no folder) the core checks that failed only
// for want of a store are one skipped line, not a "This repo, warnings"
// block, and they do not degrade; inside a repo the same failures stay
// warnings, and a different error outside a repo stays a warning too (B4).
func TestAllOutsideRepoSkipsRepoChecks(t *testing.T) {
	notStore := errs.New(errs.NotAStore, "git: the folder is not inside a git work tree")
	runs := []CheckRun{{Check: "task_lead", Err: notStore}, {Check: "store_lock", Err: notStore}, {Check: "trash", Err: errs.New(errs.Internal, "disk full")}}
	cases := []struct {
		name     string
		top      string
		warnings int
		skipped  bool
		status   Health
	}{
		{"outside a repo", "", 1, true, StatusDegraded},
		{"inside a repo", "/work/site", 3, false, StatusDegraded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			env.top = tc.top
			env.runs = runs
			r := runAllT(t, env, RunIn{})
			if len(r.Warnings) != tc.warnings {
				t.Fatalf("warnings = %q, want %d", r.Warnings, tc.warnings)
			}
			text := r.Text()
			if got := strings.Contains(text, "repo checks were skipped (2)"); got != tc.skipped {
				t.Fatalf("skip line shown = %v, want %v:\n%s", got, tc.skipped, text)
			}
			if tc.skipped && strings.Contains(text, "store_lock") {
				t.Fatalf("a skipped check is still listed:\n%s", text)
			}
			if r.Status != tc.status {
				t.Fatalf("status = %s, want %s", r.Status, tc.status)
			}
		})
	}
	// Only skips: healthy, exit 0.
	env := newEnv(t)
	env.runs = runs[:2]
	if r := runAllT(t, env, RunIn{}); r.Status != StatusHealthy || r.Exit != 0 {
		t.Fatalf("only skipped checks: status %s exit %d, want healthy 0", r.Status, r.Exit)
	}
}
