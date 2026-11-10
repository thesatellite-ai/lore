package wire_test

// ONBOARDING_DESIGN 2.1 (L7 wire row), 2.3, 3.4, 4.2/4.4 Apply: the
// Onboard ports on *App — the store round trip incl. setup_progress, the
// prober over a file:// store, the applier's compare-before-write, and the
// fix runner's program allowlist.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/wire"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// bridgeHarness is an App with HOME pointed at a temp folder, so no probe
// or apply reads the real ~/.ssh, ~/.gitconfig or ~/.lore.
func bridgeHarness(t *testing.T) *opstest.H {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return opstest.New(t)
}

func TestSetupStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := bridgeHarness(t)
	st, err := h.App.SetupStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if created, perr := st.PutSetup(ctx, onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true}); perr != nil || !created {
		t.Fatalf("PutSetup = %v, %v", created, perr)
	}
	cfg, err := st.Config(ctx)
	if err != nil || len(cfg.Setups) != 1 || cfg.Setups[0].Name != "personal" {
		t.Fatalf("Config = %+v, %v", cfg, err)
	}

	p := onboard.Progress{Setup: "personal", Step: onboard.StepIdentity, Status: onboard.StepReady,
		Answers: onboard.Answers{"email": {Text: "mira@agency.example"}, "emails": {IsList: true, List: []string{"m@agency.example"}}},
		Checks:  []onboard.CheckResult{{Check: onboard.CheckIdentityEmailFormat, Status: onboard.StatusOK, Level: onboard.LevelError}}}
	if err = st.PutProgress(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = st.PutProgress(ctx, onboard.Progress{Setup: "personal", Step: onboard.StepKind, Status: onboard.StepDone}); err != nil {
		t.Fatal(err)
	}
	p.Status = onboard.StepDone // update in place, not a second row
	if err = st.PutProgress(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := st.Progress(ctx, "personal")
	if err != nil || len(got) != 2 {
		t.Fatalf("Progress = %+v, %v", got, err)
	}
	if got[0].Step != onboard.StepKind || got[1].Step != onboard.StepIdentity {
		t.Fatalf("not in catalog order: %s, %s", got[0].Step, got[1].Step)
	}
	if g := got[1]; g.Status != onboard.StepDone || g.Answers["email"].Text != "mira@agency.example" ||
		len(g.Answers["emails"].List) != 1 || len(g.Checks) != 1 || g.Checks[0].Check != onboard.CheckIdentityEmailFormat {
		t.Fatalf("identity progress = %+v", g)
	}
	if m, _ := filepath.Glob(filepath.Join(h.Repo, ".lore", "data", "setup_progress", "*")); len(m) != 0 {
		t.Fatalf("setup_progress reached the repo store: %v", m)
	}

	root := t.TempDir()
	if err = st.TouchRepo(ctx, onboard.KnownRepo{RepoID: "rep_01", Root: root}); err != nil {
		t.Fatal(err)
	}
	if kr, kerr := st.KnownRepos(ctx); kerr != nil || len(kr) != 1 || kr[0].RepoID != "rep_01" {
		t.Fatalf("KnownRepos = %+v, %v", kr, kerr)
	}
	// A clone whose folder was renamed since is not listed (doctor --all
	// checked its gone folder under the old name); back in place, it is.
	moved := root + "-renamed"
	if err = os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if kr, kerr := st.KnownRepos(ctx); kerr != nil || len(kr) != 0 {
		t.Fatalf("KnownRepos after the folder moved = %+v, %v", kr, kerr)
	}
	if err = os.Rename(moved, root); err != nil {
		t.Fatal(err)
	}
	if kr, kerr := st.KnownRepos(ctx); kerr != nil || len(kr) != 1 {
		t.Fatalf("KnownRepos with the folder back = %+v, %v", kr, kerr)
	}

	if found, rerr := st.RemoveSetup(ctx, "personal"); rerr != nil || !found {
		t.Fatalf("RemoveSetup = %v, %v", found, rerr)
	}
	if got, err = st.Progress(ctx, "personal"); err != nil || len(got) != 0 {
		t.Fatalf("progress after remove = %+v, %v", got, err)
	}
}

// bareStore makes an empty bare git repo and returns its file:// url.
func bareStore(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := filepath.Join(t.TempDir(), "me.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return "file://" + dir
}

func TestSetupProberFileStore(t *testing.T) {
	ctx := context.Background()
	h := bridgeHarness(t)
	url := bareStore(t)
	pr, err := h.App.SetupProber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ans := onboard.Answers{"store_url": {Text: url}}
	reqs := []opssetup.ProbeRequest{
		{Check: onboard.Check{ID: onboard.CheckStoreRead, Level: onboard.LevelError, Probe: onboard.ProbeGitRead}, Answers: ans},
		{Check: onboard.Check{ID: onboard.CheckStoreMarker, Level: onboard.LevelWarn, Probe: onboard.ProbeStoreMarker}, Answers: ans},
		{Check: onboard.Check{ID: onboard.CheckStoreRead, Level: onboard.LevelError, Probe: onboard.ProbeGitRead}},
		{Check: onboard.Check{ID: onboard.CheckIdentityEmailFormat, Level: onboard.LevelError, Probe: onboard.ProbePure},
			Answers: onboard.Answers{"email": {Text: "not an email"}}},
	}
	got := pr.Probe(ctx, reqs)
	if len(got) != len(reqs) {
		t.Fatalf("%d results for %d requests", len(got), len(reqs))
	}
	want := []onboard.Status{onboard.StatusOK, onboard.StatusWarn, onboard.StatusSkip, onboard.StatusFail}
	for i, w := range want {
		if got[i].Status != w || got[i].Check != reqs[i].Check.ID {
			t.Errorf("result %d = %+v, want %s", i, got[i], w)
		}
	}
	if got[0].Vars["url"] != url {
		t.Errorf("store.read vars = %v", got[0].Vars)
	}
}

func TestSetupApplierIdempotent(t *testing.T) {
	ctx := context.Background()
	h := bridgeHarness(t)
	ap, err := h.App.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setup := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true}
	apply := func(id onboard.ApplyID, step onboard.StepID, ans onboard.Answers) opssetup.ApplyResult {
		t.Helper()
		res, aerr := ap.Apply(ctx, opssetup.ApplyRequest{Setup: setup, Step: step, Apply: id, Answers: ans})
		if aerr != nil {
			t.Fatalf("apply %s: %v", id, aerr)
		}
		setup = res.Setup
		return res
	}
	if res := apply(onboard.ApplySetupEntry, onboard.StepKind, onboard.Answers{"kind": {Text: "solo"}, "name": {Text: "personal"}}); !res.Changed {
		t.Fatal("setup entry: first apply unchanged")
	}

	idAns := onboard.Answers{"email": {Text: "mira@agency.example"}, "name": {Text: "Mira"}, "handle": {Text: "mira"}}
	if res := apply(onboard.ApplyIdentity, onboard.StepIdentity, idAns); !res.Changed || len(res.Wrote) == 0 {
		t.Fatalf("identity first apply = %+v", res)
	}
	cfg, err := wire.SetupByName(h.Home, "personal")
	if err != nil || cfg.Identity.Key != "human:mira@agency.example" {
		t.Fatalf("setups[].identity = %+v, %v", cfg.Identity, err)
	}
	raw, _ := os.ReadFile(filepath.Join(h.Home, wire.ConfigFile))
	// SOURCE_OF_TRUTH row 1: the setup's identity is the one source; the
	// Identity step never fills the top-level identity: any more.
	if containsLine(string(raw), "identity:") {
		t.Fatalf("top-level identity written:\n%s", raw)
	}
	if res := apply(onboard.ApplyIdentity, onboard.StepIdentity, idAns); res.Changed {
		t.Fatalf("identity second apply changed: %+v", res)
	}

	url := "file://" + filepath.Join(t.TempDir(), "me")
	psAns := onboard.Answers{"personal_store": {Text: "existing"}, "store_url": {Text: url}}
	res := apply(onboard.ApplyPersonalStore, onboard.StepPersonalStore, psAns)
	if !res.Changed || setup.PersonalStore != url {
		t.Fatalf("personal store first apply = %+v", res)
	}
	if _, err = os.Stat(filepath.Join(filepath.Join(filepath.Dir(url[len("file://"):]), "me"), rowfs.MarkerFile)); err != nil {
		t.Fatalf("store.init me wrote no marker: %v", err)
	}
	if res = apply(onboard.ApplyPersonalStore, onboard.StepPersonalStore, psAns); res.Changed {
		t.Fatalf("personal store second apply changed: %+v", res)
	}
	if res = apply(onboard.ApplyNone, onboard.StepThisFolder, nil); res.Changed {
		t.Fatalf("this_folder changed: %+v", res)
	}
}

// containsLine reports whether a line of s starts with prefix.
func containsLine(s, prefix string) bool {
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && s[j] != '\n' {
			j++
		}
		if len(s[i:j]) >= len(prefix) && s[i:i+len(prefix)] == prefix {
			return true
		}
		i = j + 1
	}
	return false
}

func TestSetupFixRunnerAllowlist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := opstest.New(t)
	fr, err := h.App.SetupFixRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Refused programs that are harmless even if a broken allowlist ran
	// them (a break-verify run executes them for real).
	for _, argv := range [][]string{{"env"}, {"/usr/bin/git", "--version"}, {}, {"true"}} {
		if _, ferr := fr.RunFix(ctx, argv); !errs.Has(ferr, errs.FixRefused) {
			t.Errorf("RunFix(%q) = %v, want E_FIX_REFUSED", argv, ferr)
		}
	}
	if _, err = exec.LookPath("git"); err == nil {
		run, rerr := fr.RunFix(ctx, []string{"git", "--version"})
		if rerr != nil || run.Exit != 0 || run.OutputTail == "" || run.Argv[0] != "git" {
			t.Fatalf("git --version = %+v, %v", run, rerr)
		}
	}
}

// A member entry waits for its org (A6 note 1); create/existing without a
// url is refused (note 2); "store me" resolves the setup's personal store (A9 targets).
func TestSetupApplierMemberDeferAndStoreTargets(t *testing.T) {
	ctx := context.Background()
	h := bridgeHarness(t)
	ap, err := h.App.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	member := onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindMember, Org: "acme_hub"}
	res, err := ap.Apply(ctx, opssetup.ApplyRequest{Setup: member, Step: onboard.StepGitHub, Apply: onboard.ApplyGitHub,
		Answers: onboard.Answers{"github_account": {Text: "tomas"}}})
	if err != nil || res.Changed || res.Setup.GitHub.User != "tomas" {
		t.Fatalf("member before join = %+v, %v", res, err)
	}
	if _, serr := wire.SetupByName(h.Home, "acme-hub"); !errs.Has(serr, errs.SetupNotFound) {
		t.Fatalf("member entry written before its org: %v", serr)
	}
	for _, choice := range []string{onboard.PersonalStoreCreate, onboard.PersonalStoreExisting} {
		_, perr := ap.Apply(ctx, opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo},
			Step: onboard.StepPersonalStore, Apply: onboard.ApplyPersonalStore, Answers: onboard.Answers{"personal_store": {Text: choice}}})
		if !errs.Has(perr, errs.InvalidInput) {
			t.Errorf("%s without url = %v, want E_INVALID_INPUT", choice, perr)
		}
	}

	// A bare git repository named by file:// is refused (store init's rule,
	// wire/storeinit_bare.go); a plain folder is the store.
	if _, berr := ap.Apply(ctx, opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true}, Step: onboard.StepPersonalStore, Apply: onboard.ApplyPersonalStore,
		Answers: onboard.Answers{"personal_store": {Text: onboard.PersonalStoreExisting}, "store_url": {Text: bareStore(t)}}}); !errs.Has(berr, errs.InvalidInput) {
		t.Fatalf("a file:// bare repo as the personal store = %v, want E_INVALID_INPUT", berr)
	}
	url := "file://" + filepath.Join(t.TempDir(), "me")
	solo := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true}
	// The repo is the setup's own personal_store (personal.store is never
	// written from a setup, realmac N2); "store me" resolves it.
	saved, err := ap.Apply(ctx, opssetup.ApplyRequest{Setup: solo, Step: onboard.StepPersonalStore, Apply: onboard.ApplyPersonalStore,
		Answers: onboard.Answers{"personal_store": {Text: onboard.PersonalStoreExisting}, "store_url": {Text: url}}})
	if err != nil {
		t.Fatal(err)
	}
	solo = saved.Setup
	pr, err := h.App.SetupProber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := pr.Probe(ctx, []opssetup.ProbeRequest{
		{Check: onboard.Check{ID: onboard.CheckStoreRead, Level: onboard.LevelError}, Setup: solo, Target: "store me"},
		{Check: onboard.Check{ID: onboard.CheckStoreRead, Level: onboard.LevelError}, Setup: onboard.SetupEntry{Name: "x", Kind: onboard.KindSolo, PersonalStore: onboard.PersonalStoreNone}, Target: "store me"},
	})
	if got[0].Status != onboard.StatusOK || got[1].Status != onboard.StatusSkip {
		t.Fatalf("store me targets = %+v", got)
	}
}

// TestSetupGateSparesMachineWrites: on a machine whose folders tie between
// two setups, a shared write is refused (E_SETUP_AMBIGUOUS, ONB 3.3) while
// a new setup's progress (machine store, 3.4) still saves.
func TestSetupGateSparesMachineWrites(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	ctx := context.Background()
	h := bridgeHarness(t)
	glob := h.Repo + "/**"
	cfg := "setups:\n  - {name: one, kind: solo, folders: [" + glob + "]}\n  - {name: two, kind: solo, folders: [" + glob + "]}\n"
	if err := os.WriteFile(filepath.Join(h.Home, wire.ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Run("memory.add", `{"body":"a shared row"}`); errs.CodeOf(err) != errs.SetupAmbiguous {
		t.Fatalf("shared write = %v, want E_SETUP_AMBIGUOUS", err)
	}
	st, err := h.App.SetupStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.PutProgress(ctx, onboard.Progress{Setup: "fresh", Step: onboard.StepKind, Status: onboard.StepReady}); err != nil {
		t.Fatalf("progress of a new setup refused: %v", err)
	}
	got, err := st.Progress(ctx, "fresh")
	if err != nil || len(got) != 1 {
		t.Fatalf("Progress = %+v, %v", got, err)
	}
	if err = st.DeleteProgress(ctx, "fresh"); err != nil {
		t.Fatalf("delete progress: %v", err)
	}
}

// TestSkillToolsFollowSetup: skill.install's default tools are the active
// setup's (ONB 4.7.1); with no setup they come from detection.
func TestSkillToolsFollowSetup(t *testing.T) {
	ctx := context.Background()
	h := bridgeHarness(t)
	ch, err := h.App.SkillTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ch.ActiveSetupTools(ctx); ok || err != nil {
		t.Fatalf("no setup: ok=%v err=%v", ok, err)
	}
	cfg := "setups:\n  - {name: agency, kind: solo, folders: [" + h.Repo + "/**], tools: [codex]}\n"
	if err = os.WriteFile(filepath.Join(h.Home, wire.ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	tools, ok, err := ch.ActiveSetupTools(ctx)
	if err != nil || !ok || len(tools) != 1 || tools[0] != "codex" {
		t.Fatalf("setup tools = %v %v %v", tools, ok, err)
	}
	if c := h.App.DoctorCache(ctx); c == nil || c != h.App.DoctorCache(ctx) {
		t.Fatal("DoctorCache must be the App's one cache")
	}
}

// TestSetupStoreOutsideRepo (A11 bug 2): `lore setup join` and the setup
// steps run from ~ (no git work tree); their progress rows live in the
// machine store, which opens alone there (machineApp), instead of
// failing E_NOT_A_STORE on the repo open.
func TestSetupStoreOutsideRepo(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("LORE_ACTOR", "human:mira@agency.example")
	plain := t.TempDir() // not a git work tree
	app, err := wire.New(wire.StandaloneOptions(plain, filepath.Join(home, ".lore"), plain, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	st, err := app.SetupStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := onboard.Progress{Setup: "agency", Step: onboard.StepKind, Status: onboard.StepDone,
		Answers: onboard.Answers{"kind": {Text: "member"}, "name": {Text: "agency"}}}
	if err = st.PutProgress(ctx, p); err != nil {
		t.Fatalf("PutProgress outside a repo: %v", err)
	}
	got, err := st.Progress(ctx, "agency")
	if err != nil || len(got) != 1 || got[0].Answers["kind"].Text != "member" {
		t.Fatalf("Progress outside a repo = %+v, %v", got, err)
	}
	if err = st.DeleteProgress(ctx, "agency"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.KnownRepos(ctx); err != nil {
		t.Fatalf("KnownRepos outside a repo: %v", err)
	}
	// the repo engine itself still refuses (fail closed)
	if _, err = app.Engine(ctx); !errs.Has(err, errs.NotAStore) {
		t.Errorf("Engine outside a repo = %v, want E_NOT_A_STORE", err)
	}
}

// Two setups on different accounts each keep the repo chosen for them
// (realmac N2): each entry records its own url, each store gets its
// marker, and personal.store is never written, so "Same as my other
// setups" cannot silently reach another account's store. inherit is
// refused while no personal.store exists and saved once one does.
func TestSetupApplierOwnPersonalStoreInit(t *testing.T) {
	ctx := context.Background()
	h := bridgeHarness(t)
	ap, err := h.App.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	apply := func(name string, ans onboard.Answers) (onboard.SetupEntry, error) {
		t.Helper()
		res, aerr := ap.Apply(ctx, opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: name, Kind: onboard.KindSolo},
			Step: onboard.StepPersonalStore, Apply: onboard.ApplyPersonalStore, Answers: ans})
		return res.Setup, aerr
	}
	existing := func(url string) onboard.Answers {
		return onboard.Answers{"personal_store": {Text: "existing"}, "store_url": {Text: "file://" + url}}
	}
	octo := filepath.Join(t.TempDir(), "octo-user-store")
	work := filepath.Join(t.TempDir(), "work-user-store")
	for name, url := range map[string]string{"personal": octo, "work": work} {
		e, aerr := apply(name, existing(url))
		if aerr != nil {
			t.Fatalf("apply %s: %v", name, aerr)
		}
		if e.PersonalStore != "file://"+url {
			t.Errorf("%s personal_store = %q, want its own url", name, e.PersonalStore)
		}
		if _, serr := os.Stat(filepath.Join(url, rowfs.MarkerFile)); serr != nil {
			t.Errorf("%s store has no marker: %v", name, serr)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(h.Home, wire.ConfigFile))
	if strings.Contains(string(raw), "personal:") {
		t.Errorf("personal.store written from a setup:\n%s", raw)
	}
	inherit := onboard.Answers{"personal_store": {Text: "inherit"}}
	if _, aerr := apply("third", inherit); !errs.Has(aerr, errs.InvalidInput) {
		t.Fatalf("inherit with no personal.store: %v", aerr)
	}
	if werr := os.WriteFile(filepath.Join(h.Home, wire.ConfigFile), append(raw, []byte("personal:\n  store: file://"+octo+"\n")...), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if e, aerr := apply("third", inherit); aerr != nil || e.PersonalStore != onboard.PersonalStoreInherit {
		t.Fatalf("inherit with personal.store = %+v, %v", e, aerr)
	}
}
