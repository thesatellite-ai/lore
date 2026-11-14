package setup

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// prefillProber answers prefills from a table; an id in fail errors.
type prefillProber struct {
	*fakeProber
	out   map[onboard.PrefillID]PrefillOut
	fail  map[onboard.PrefillID]bool
	mu    sync.Mutex
	asked []onboard.PrefillID
	got   onboard.SetupEntry
}

func (p *prefillProber) Prefill(_ context.Context, id onboard.PrefillID, s onboard.SetupEntry) (PrefillOut, error) {
	p.mu.Lock() // the engine resolves prefills in parallel
	p.asked = append(p.asked, id)
	p.got = s
	p.mu.Unlock()
	if p.fail[id] {
		return PrefillOut{}, errs.New(errs.Internal, "probe broke")
	}
	return p.out[id], nil
}

// prefillEnv is fakeEnv with prefillProber.
type prefillEnv struct {
	fakeEnv
	p *prefillProber
}

func (e prefillEnv) SetupProber(context.Context) (Prober, error) { return e.p, nil }

func newPrefillEnv(entries ...onboard.SetupEntry) prefillEnv {
	f := newFakeEnv(entries...)
	p := &prefillProber{fakeProber: f.prober, fail: map[onboard.PrefillID]bool{}, out: map[onboard.PrefillID]PrefillOut{
		onboard.PrefillGitUserName:  {Value: onboard.Answer{Text: "Mira Patel"}},
		onboard.PrefillGitUserEmail: {Value: onboard.Answer{Text: "mira@agency.example"}},
		onboard.PrefillSSHGitHubKeys: {Value: onboard.Answer{Text: "github.com"}, Choices: []onboard.Choice{
			{Value: "github.com", Label: "github.com", Detail: "logs in as mira"}, {Value: "github-agency", Label: "github-agency"}}},
		PrefillGitHubUsers:        {Value: onboard.Answer{Text: "mira"}, Choices: []onboard.Choice{{Value: "mira", Label: "mira"}, {Value: "mira-agency", Label: "mira-agency"}}},
		onboard.PrefillSSHPubKeys: {Choices: []onboard.Choice{{Value: "~/.ssh/id_ed25519.pub", Label: "~/.ssh/id_ed25519.pub"}}},
		onboard.PrefillToolsDetected: {Value: onboard.Answer{IsList: true, List: []string{"claude_code"}},
			Choices: []onboard.Choice{{Value: "claude_code", Label: "claude_code"}, {Value: "codex", Label: "codex"}}},
		onboard.PrefillSecretsFound: {Value: onboard.Answer{Text: "gopass"},
			Choices: []onboard.Choice{{Value: "gopass", Label: "gopass"}, {Value: onboard.SecretsNone, Label: onboard.SecretsNone}}},
		onboard.PrefillFoldersHere: {Value: onboard.Answer{IsList: true, List: []string{"~/code/**"}}},
	}}
	return prefillEnv{fakeEnv: f, p: p}
}

// planStepOf finds id in out.
func planStepOf(t *testing.T, out PlanOut, id onboard.StepID) PlanStep {
	t.Helper()
	for _, s := range out.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no step %s in the plan", id)
	return PlanStep{}
}

// choiceValues lists q's static choice values.
func choiceValues(t *testing.T, s PlanStep, qid string) []string {
	t.Helper()
	for _, q := range s.Questions {
		if q.ID == qid {
			var out []string
			for _, c := range q.Choices.Static {
				out = append(out, c.Value)
			}
			return out
		}
	}
	t.Fatalf("no question %s in step %s", qid, s.ID)
	return nil
}

// TestPlanResolvesPrefills (A13 bug 1): setup.plan for a setup being made
// resolves every detection through the Prober — values per step, choices
// into choices.static (detected after nothing, static None last), the
// github_account choices are users (not host aliases), the kind defaults
// name the first solo setup personal and make it the default — and a
// detection that fails leaves its question empty without failing the plan.
func TestPlanResolvesPrefills(t *testing.T) {
	env := newPrefillEnv()
	env.p.fail[onboard.PrefillSSHPubKeys] = true
	e := newEngine(env, onboardCatalog{})
	out, err := e.Plan(bg, PlanIn{Kind: string(onboard.KindSolo), Setup: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	kind := planStepOf(t, out, onboard.StepKind)
	if kind.Values[onboard.QIDName].Text != "personal" || kind.Values[onboard.QIDKind].Text != "solo" || !kind.Values[onboard.QIDDefault].Bool {
		t.Fatalf("kind values = %+v", kind.Values)
	}
	id := planStepOf(t, out, onboard.StepIdentity)
	if id.Values[onboard.QIDName].Text != "Mira Patel" || id.Values[onboard.QIDEmail].Text != "mira@agency.example" {
		t.Fatalf("identity values = %+v", id.Values)
	}
	gh := planStepOf(t, out, onboard.StepGitHub)
	if got := choiceValues(t, gh, onboard.QIDGitHubAccount); !slices.Equal(got, []string{"mira", "mira-agency"}) {
		t.Fatalf("github_account choices = %v, want the users", got)
	}
	if got := choiceValues(t, gh, onboard.QIDHostAlias); !slices.Equal(got, []string{"github.com", "github-agency"}) {
		t.Fatalf("host_alias choices = %v", got)
	}
	if gh.Values[onboard.QIDGitHubAccount].Text != "mira" || gh.Values[onboard.QIDHostAlias].Text != "github.com" {
		t.Fatalf("github values = %+v", gh.Values)
	}
	if _, has := gh.Values[onboard.QIDSSHKey]; has || len(choiceValues(t, gh, onboard.QIDSSHKey)) != 0 {
		t.Fatalf("a failed detection gave ssh_key %+v / %v", gh.Values[onboard.QIDSSHKey], choiceValues(t, gh, onboard.QIDSSHKey))
	}
	sec := planStepOf(t, out, onboard.StepSecrets)
	if got := choiceValues(t, sec, onboard.QIDBackend); !slices.Equal(got, []string{"gopass", onboard.SecretsNone}) {
		t.Fatalf("backend choices = %v, want gopass then none once", got)
	}
	tools := planStepOf(t, out, onboard.StepTools)
	if got := tools.Values[onboard.QIDTools].List; !slices.Equal(got, []string{"claude_code"}) {
		t.Fatalf("tools value = %v", got)
	}
	if f := planStepOf(t, out, onboard.StepFolders); !slices.Equal(f.Values[onboard.QIDFolders].List, []string{"~/code/**"}) {
		t.Fatalf("folders value = %+v", f.Values)
	}
	// each detection once, even when two questions name it
	seen := map[onboard.PrefillID]int{}
	for _, a := range env.p.asked {
		seen[a]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("prefill %s asked %d times", id, n)
		}
	}
	// the JSON the page reads: steps[].values and steps[].questions[].choices.static
	b, err := json.Marshal(out)
	if err != nil || !strings.Contains(string(b), `"values":{"email":"mira@agency.example","name":"Mira Patel"}`) ||
		!strings.Contains(string(b), `"rules":["email"],"kinds":["solo","admin"],"value":"mira@agency.example"`) ||
		!strings.Contains(string(b), `"value":["claude_code"]`) || !strings.Contains(string(b), `"value":true`) {
		t.Fatalf("json: %v %s", err, b)
	}
}

// TestPlanValuesFromTheEntry: a saved setup starts from its own values, not
// from fresh detections (A13 bug 9: the default box of a default setup).
func TestPlanValuesFromTheEntry(t *testing.T) {
	ent := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true, Folders: []string{"~/www/**"},
		Tools: []string{"codex"}, GitHub: onboard.SetupGitHub{User: "mira-agency", HostAlias: "github-agency"},
		PersonalStore: "git@github.com:mira/lore-me.git"}
	env := newPrefillEnv(ent)
	out, err := newEngine(env, onboardCatalog{}).Plan(bg, PlanIn{Setup: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	f := planStepOf(t, out, onboard.StepFolders)
	if !f.Values[onboard.QIDDefault].Bool || !slices.Equal(f.Values[onboard.QIDFolders].List, []string{"~/www/**"}) {
		t.Fatalf("folders values = %+v", f.Values)
	}
	if v := planStepOf(t, out, onboard.StepTools).Values[onboard.QIDTools].List; !slices.Equal(v, []string{"codex"}) {
		t.Fatalf("tools value = %v", v)
	}
	gh := planStepOf(t, out, onboard.StepGitHub)
	if gh.Values[onboard.QIDGitHubAccount].Text != "mira-agency" || gh.Values[onboard.QIDHostAlias].Text != "github-agency" {
		t.Fatalf("github values = %+v", gh.Values)
	}
	ps := planStepOf(t, out, onboard.StepPersonalStore)
	if ps.Values[onboard.QIDPersonalStore].Text != onboard.PersonalStoreExisting || ps.Values[onboard.QIDStoreURL].Text != ent.PersonalStore {
		t.Fatalf("personal store values = %+v", ps.Values)
	}
	if len(out.Progress) != len(out.Steps) || out.Progress[len(out.Progress)-1].Status != onboard.StepDone {
		t.Fatalf("a finished setup's progress = %+v, want every step done", out.Progress)
	}
	if env.p.got.Name != "personal" || env.p.got.GitHub.User != "mira-agency" {
		t.Fatalf("prefills ran for %+v, want the saved entry", env.p.got)
	}
	// a second solo setup is not offered the taken name nor the default
	env2 := newPrefillEnv(ent)
	out2, err := newEngine(env2, onboardCatalog{}).Plan(bg, PlanIn{Kind: string(onboard.KindSolo)})
	if err != nil {
		t.Fatal(err)
	}
	k := planStepOf(t, out2, onboard.StepKind)
	if _, has := k.Values[onboard.QIDName]; has || k.Values[onboard.QIDDefault].Bool {
		t.Fatalf("second setup kind values = %+v", k.Values)
	}
}

// TestHealthCell: the 5.4 Home cell is the worst outcome and its count.
func TestHealthCell(t *testing.T) {
	r := func(s onboard.Status, l onboard.Level) onboard.CheckResult {
		return onboard.CheckResult{Status: s, Level: l}
	}
	ok, warn := r(onboard.StatusOK, onboard.LevelError), r(onboard.StatusWarn, onboard.LevelWarn)
	for _, c := range []struct {
		in   []onboard.CheckResult
		want string
	}{
		{nil, ""},
		{[]onboard.CheckResult{r(onboard.StatusSkip, onboard.LevelWarn)}, ""},
		{[]onboard.CheckResult{ok, ok, r(onboard.StatusSkip, onboard.LevelWarn)}, "✓ 2"},
		{[]onboard.CheckResult{ok, warn, r(onboard.StatusFail, onboard.LevelWarn)}, "⚠ 2"},
		{[]onboard.CheckResult{ok, warn, r(onboard.StatusFail, onboard.LevelError)}, "✗ 1"},
		{[]onboard.CheckResult{ok, r(onboard.StatusError, onboard.LevelError)}, "? 1"},
		// An info-level warning is a note (upgrade lore first): no "to fix" on Home.
		{[]onboard.CheckResult{ok, r(onboard.StatusWarn, onboard.LevelInfo)}, "✓ 2"},
	} {
		if got := HealthCell(c.in); got != c.want {
			t.Fatalf("HealthCell(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// healthEnv is fakeEnv with cached doctor results.
type healthEnv struct {
	fakeEnv
	cached map[string][]onboard.CheckResult
}

func (h healthEnv) SetupHealth(_ context.Context, name string) ([]onboard.CheckResult, bool) {
	rs, ok := h.cached[name]
	return rs, ok
}

// TestListHealth (A13 bug 9): setup.list fills Health from the cached
// doctor results when the env has them, else from the unfinished steps'
// last checks, else leaves it empty; nothing is probed.
func TestListHealth(t *testing.T) {
	a := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo}
	b := onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindSolo}
	c := onboard.SetupEntry{Name: "client", Kind: onboard.KindSolo}
	f := newFakeEnv(a, b, c)
	_ = f.store.PutProgress(bg, onboard.Progress{Setup: "acme-hub", Step: onboard.StepGitHub, Status: onboard.StepFailed,
		Checks: []onboard.CheckResult{{Status: onboard.StatusFail, Level: onboard.LevelError}}})
	env := healthEnv{fakeEnv: f, cached: map[string][]onboard.CheckResult{"personal": {{Status: onboard.StatusOK}, {Status: onboard.StatusOK}}}}
	out, err := newEngine(env, onboardCatalog{}).List(bg, ListIn{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range out.Setups {
		got[s.Name] = s.Health
	}
	if got["personal"] != "✓ 2" || got["acme-hub"] != "✗ 1" || got["client"] != "" || f.prober.calls != 0 {
		t.Fatalf("health = %v, probes %d", got, f.prober.calls)
	}
}

// TestListHealthNewestWins: Home's Health cell shows the newer of the
// last doctor run and the last step check, so a fresh doctor run replaces
// older failing step results and a later step re-check replaces an older
// doctor run (the smoke saw "✗ 2" from old step checks beside a fresh
// Doctor "warn 1, fail 0").
func TestListHealthNewestWins(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
	stepFail := []onboard.CheckResult{{Status: onboard.StatusFail, Level: onboard.LevelError, CheckedAt: t0},
		{Status: onboard.StatusFail, Level: onboard.LevelError, CheckedAt: t0}}
	docWarn := func(at time.Time) []onboard.CheckResult {
		return []onboard.CheckResult{{Status: onboard.StatusOK, CheckedAt: at}, {Status: onboard.StatusWarn, Level: onboard.LevelWarn, CheckedAt: at}}
	}
	for _, c := range []struct {
		name string
		doc  []onboard.CheckResult
		want string
	}{
		{"doctor after the step check", docWarn(t0.Add(time.Minute)), "⚠ 1"},
		{"step check after the doctor", docWarn(t0.Add(-time.Minute)), "✗ 2"},
		{"no doctor run", nil, "✗ 2"},
	} {
		f := newFakeEnv(onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo})
		_ = f.store.PutProgress(bg, onboard.Progress{Setup: "personal", Step: onboard.StepPersonalStore, Status: onboard.StepFailed, Checks: stepFail})
		env := healthEnv{fakeEnv: f, cached: map[string][]onboard.CheckResult{}}
		if c.doc != nil {
			env.cached["personal"] = c.doc
		}
		out, err := newEngine(env, onboardCatalog{}).List(bg, ListIn{})
		if err != nil {
			t.Fatal(err)
		}
		if got := out.Setups[0].Health; got != c.want {
			t.Errorf("%s: health %q, want %q", c.name, got, c.want)
		}
	}
}

// An unset personal_store reads as "same as my other setups" only when
// another setup exists; on the first setup it gives no value (ONB 4.4).
func TestEntryValuePersonalStoreInherit(t *testing.T) {
	q := onboard.Question{ID: onboard.QIDPersonalStore}
	e := onboard.SetupEntry{Name: "personal"}
	if v, ok := entryValue(onboard.StepPersonalStore, q, e, true, false); ok {
		t.Fatalf("first setup got %+v", v)
	}
	if v, ok := entryValue(onboard.StepPersonalStore, q, e, true, true); !ok || v.Text != onboard.PersonalStoreInherit {
		t.Fatalf("with another setup: %+v %v", v, ok)
	}
	e.PersonalStore = onboard.PersonalStoreInherit
	if v, ok := entryValue(onboard.StepPersonalStore, q, e, true, false); !ok || v.Text != onboard.PersonalStoreInherit {
		t.Fatalf("explicit inherit: %+v %v", v, ok)
	}
}

// TestPlanFinishedCarriesCachedChecks (realmac #11): a finished setup's
// synthesized progress rows carry the cached doctor results of each
// step's own checks, so a page's step marks read the same before and
// after a reload; nothing cached → empty lists; nothing is probed.
func TestPlanFinishedCarriesCachedChecks(t *testing.T) {
	a := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo}
	fail := onboard.CheckResult{Check: onboard.CheckGitHubKeyMatches, Status: onboard.StatusFail, Level: onboard.LevelError}
	email := onboard.CheckResult{Check: onboard.CheckIdentityEmailFormat, Status: onboard.StatusOK}
	for _, c := range []struct {
		name   string
		cached map[string][]onboard.CheckResult
		want   map[onboard.StepID][]onboard.CheckID
	}{
		{"cached", map[string][]onboard.CheckResult{"personal": {email, fail}},
			map[onboard.StepID][]onboard.CheckID{onboard.StepIdentity: {email.Check}, onboard.StepGitHub: {fail.Check}, onboard.StepTools: nil}},
		{"other setup cached", map[string][]onboard.CheckResult{"work": {fail}},
			map[onboard.StepID][]onboard.CheckID{onboard.StepIdentity: nil, onboard.StepGitHub: nil}},
		{"nothing cached", nil, map[onboard.StepID][]onboard.CheckID{onboard.StepGitHub: nil}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeEnv(a)
			env := healthEnv{fakeEnv: f, cached: c.cached}
			out, err := newEngine(env, onboardCatalog{}).Plan(bg, PlanIn{Setup: "personal"})
			if err != nil {
				t.Fatal(err)
			}
			rows := map[onboard.StepID]onboard.Progress{}
			for _, p := range out.Progress {
				rows[p.Step] = p
			}
			for step, want := range c.want {
				p, ok := rows[step]
				if !ok || p.Status != onboard.StepDone || p.Checks == nil {
					t.Fatalf("%s row = %+v (ok %v); want done with a non-nil checks list", step, p, ok)
				}
				var got []onboard.CheckID
				for _, r := range p.Checks {
					got = append(got, r.Check)
				}
				if !slices.Equal(got, want) {
					t.Fatalf("%s checks = %v, want %v", step, got, want)
				}
			}
			if f.prober.calls != 0 {
				t.Fatalf("plan probed %d times", f.prober.calls)
			}
		})
	}
}

// TestPlanFoldersPrefillOnlyUnsaved: the folders prefill (the start
// folder) fills the Folders step only while it was never saved; a saved
// or skipped step, or a finished setup, with no folders stays empty, and
// saved folders always win.
func TestPlanFoldersPrefillOnlyUnsaved(t *testing.T) {
	here := []string{"~/code/**"}
	for _, c := range []struct {
		name    string
		folders []string
		row     onboard.StepStatus // "" = no folders progress row
		started bool               // an unfinished setup (a kind row exists); else finished
		want    []string
	}{
		{"unfinished, never saved", nil, "", true, here},
		{"unfinished, draft row (checked, not saved)", nil, onboard.StepReady, true, here},
		{"unfinished, saved empty", nil, onboard.StepDone, true, nil},
		{"unfinished, skipped", nil, onboard.StepSkipped, true, nil},
		{"finished, no folders", nil, "", false, nil},
		{"finished, saved folders", []string{"~/w/**"}, "", false, []string{"~/w/**"}},
		{"unfinished, saved folders", []string{"~/w/**"}, onboard.StepDone, true, []string{"~/w/**"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := newPrefillEnv(onboard.SetupEntry{Name: "work", Kind: onboard.KindSolo, Folders: c.folders})
			if c.started {
				_ = env.store.PutProgress(bg, onboard.Progress{Setup: "work", Step: onboard.StepKind, Status: onboard.StepDone})
			}
			if c.row != "" {
				_ = env.store.PutProgress(bg, onboard.Progress{Setup: "work", Step: onboard.StepFolders, Status: c.row})
			}
			out, err := newEngine(env, onboardCatalog{}).Plan(bg, PlanIn{Setup: "work"})
			if err != nil {
				t.Fatal(err)
			}
			got := planStepOf(t, out, onboard.StepFolders).Values[onboard.QIDFolders].List
			if !slices.Equal(got, c.want) {
				t.Fatalf("folders value = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPlanSavedPickersNeverDetected (owner report 2026-11-09): a saved
// step of an existing setup starts from what the setup holds. An empty
// tools list and no secret manager are saved choices, never replaced by
// the tools and the manager this machine detects; a saved manager this
// machine does not have is still offered, so the picker shows it picked.
// A step never saved (a new setup, or a draft row) keeps the detections.
func TestPlanSavedPickersNeverDetected(t *testing.T) {
	for _, c := range []struct {
		name        string
		tools       []string
		backend     string
		row         onboard.StepStatus // "" = no progress row for tools and secrets
		started     bool               // an unfinished setup (a kind row exists); else finished
		wantTools   []string
		wantBackend string
		wantChoices []string // backend choices, sorted
	}{
		{"finished, none saved", nil, "", "", false, []string{}, onboard.SecretsNone, []string{"gopass", onboard.SecretsNone}},
		{"finished, saved differ from detected", []string{"codex"}, "op", "", false, []string{"codex"}, "op", []string{"gopass", onboard.SecretsNone, "op"}},
		{"unfinished, saved empty", nil, "", onboard.StepDone, true, []string{}, onboard.SecretsNone, []string{"gopass", onboard.SecretsNone}},
		{"unfinished, skipped", nil, "", onboard.StepSkipped, true, []string{}, onboard.SecretsNone, []string{"gopass", onboard.SecretsNone}},
		{"unfinished, never saved", nil, "", "", true, []string{"claude_code"}, "gopass", []string{"gopass", onboard.SecretsNone}},
		{"unfinished, draft row", nil, "", onboard.StepReady, true, []string{"claude_code"}, "gopass", []string{"gopass", onboard.SecretsNone}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := newPrefillEnv(onboard.SetupEntry{Name: "work", Kind: onboard.KindSolo, Tools: c.tools, Secrets: onboard.SetupSecrets{Backend: c.backend}})
			if c.started {
				_ = env.store.PutProgress(bg, onboard.Progress{Setup: "work", Step: onboard.StepKind, Status: onboard.StepDone})
			}
			if c.row != "" {
				_ = env.store.PutProgress(bg, onboard.Progress{Setup: "work", Step: onboard.StepTools, Status: c.row})
				_ = env.store.PutProgress(bg, onboard.Progress{Setup: "work", Step: onboard.StepSecrets, Status: c.row})
			}
			out, err := newEngine(env, onboardCatalog{}).Plan(bg, PlanIn{Setup: "work"})
			if err != nil {
				t.Fatal(err)
			}
			tools := planStepOf(t, out, onboard.StepTools).Values[onboard.QIDTools]
			if !tools.IsList || !slices.Equal(tools.List, c.wantTools) {
				t.Fatalf("tools value = %+v, want %v", tools, c.wantTools)
			}
			sec := planStepOf(t, out, onboard.StepSecrets)
			if got := sec.Values[onboard.QIDBackend].Text; got != c.wantBackend {
				t.Fatalf("backend value = %q, want %q", got, c.wantBackend)
			}
			got := choiceValues(t, sec, onboard.QIDBackend)
			slices.Sort(got)
			if !slices.Equal(got, c.wantChoices) {
				t.Fatalf("backend choices = %v, want %v", got, c.wantChoices)
			}
		})
	}
}

// recordingEnv keeps what the engine hands its HealthRecorder and serves it
// back as HealthSource (the wire health file in one map).
type recordingEnv struct {
	fakeEnv
	kept map[string][]onboard.CheckResult
}

func (r recordingEnv) RecordHealth(_ context.Context, name string, rs []onboard.CheckResult) error {
	r.kept[name] = append([]onboard.CheckResult{}, rs...)
	return nil
}

func (r recordingEnv) SetupHealth(_ context.Context, name string) ([]onboard.CheckResult, bool) {
	rs, ok := r.kept[name]
	return rs, ok
}

// TestFinishKeepsStepResults (owner report 2026-11-08): the Save that
// finishes a setup deletes its progress rows, and the results of every
// step's checks went with them, so the setup opened right after showed
// "not checked" on every Summary row. Finishing hands them to the
// HealthRecorder; the finished setup's plan then carries each step's own.
func TestFinishKeepsStepResults(t *testing.T) {
	f := newFakeEnv()
	env := recordingEnv{fakeEnv: f, kept: map[string][]onboard.CheckResult{}}
	e := newEngine(env, fakeCatalog{})
	e.now = func() time.Time { return time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC) }
	for _, in := range []ApplyIn{
		{Setup: "personal", Step: "kind", Answers: kindAnswers},
		{Setup: "personal", Step: "identity", Answers: emailAnswers},
		{Setup: "personal", Step: "tools"},
		{Setup: "personal", Step: "github", Answers: onboard.Answers{"x": text("y")}},
		{Setup: "personal", Step: string(onboard.StepThisFolder)},
		{Setup: "personal", Step: "summary"},
	} {
		if _, err := e.Apply(bg, in); err != nil {
			t.Fatalf("apply %s: %v", in.Step, err)
		}
	}
	if rows := f.store.progress["personal"]; len(rows) != 0 {
		t.Fatalf("progress after a complete summary = %v, want none", rows)
	}
	if len(env.kept["personal"]) == 0 {
		t.Fatal("finishing the setup kept none of its check results")
	}
	plan, err := e.Plan(bg, PlanIn{Setup: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Progress {
		if p.Step == onboard.StepIdentity && len(p.Checks) != 2 {
			t.Fatalf("identity row after finishing = %+v, want its 2 checks", p.Checks)
		}
	}
	// An edit of the finished setup (a step saved again, its checks run on
	// the saved answers) replaces that step's results and keeps the rest.
	all := len(env.kept["personal"])
	var others []onboard.CheckResult
	for _, r := range env.kept["personal"] {
		if r.Check != onboard.CheckIdentityEmailFormat && r.Check != onboard.CheckIdentityGitEmail {
			others = append(others, r)
		}
	}
	env.kept["personal"] = others // as if identity had never been checked
	if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: emailAnswers}); err != nil {
		t.Fatal(err)
	}
	if got := len(env.kept["personal"]); got != all {
		t.Fatalf("after saving identity again the setup keeps %d results, want %d (identity's 2 back, the rest kept)", got, all)
	}
}

// stalingEnv is recordingEnv whose kept results answer only while no
// Applier write happened since they were recorded (the wire health file's
// entry fingerprint: any write that rewrites the entry hides them).
type stalingEnv struct {
	recordingEnv
	at map[string]int
}

func (r stalingEnv) RecordHealth(ctx context.Context, name string, rs []onboard.CheckResult) error {
	r.at[name] = len(r.applier.calls)
	return r.recordingEnv.RecordHealth(ctx, name, rs)
}

func (r stalingEnv) SetupHealth(ctx context.Context, name string) ([]onboard.CheckResult, bool) {
	if at, ok := r.at[name]; !ok || at != len(r.applier.calls) {
		return nil, false
	}
	return r.recordingEnv.SetupHealth(ctx, name)
}

// TestEditKeepsOtherStepsWhenTheEntryChanges (page agent round 2): saving
// one step of a finished setup again can rewrite its entry (the GitHub
// step fills route and key_fingerprint), and the health then no longer
// matched the entry, so every other step's kept results were dropped and
// the Summary after a reload said "not checked" for them. They are read
// before the write and kept.
func TestEditKeepsOtherStepsWhenTheEntryChanges(t *testing.T) {
	f := newFakeEnv()
	env := stalingEnv{recordingEnv: recordingEnv{fakeEnv: f, kept: map[string][]onboard.CheckResult{}}, at: map[string]int{}}
	e := newEngine(env, fakeCatalog{})
	e.now = func() time.Time { return time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC) }
	for _, in := range []ApplyIn{
		{Setup: "personal", Step: "kind", Answers: kindAnswers},
		{Setup: "personal", Step: "identity", Answers: emailAnswers},
		{Setup: "personal", Step: "tools"},
		{Setup: "personal", Step: "github", Answers: onboard.Answers{"x": text("y")}},
		{Setup: "personal", Step: string(onboard.StepThisFolder)},
		{Setup: "personal", Step: "summary"},
	} {
		if _, err := e.Apply(bg, in); err != nil {
			t.Fatalf("apply %s: %v", in.Step, err)
		}
	}
	all := len(env.kept["personal"])
	if all == 0 {
		t.Fatal("finishing kept nothing")
	}
	// another identity: the Applier writes, so the entry changes
	if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: onboard.Answers{"email": text("other@agency.example")}}); err != nil {
		t.Fatal(err)
	}
	if got := len(env.kept["personal"]); got != all {
		t.Fatalf("after an entry-changing edit of identity the setup keeps %d results, want %d (the other steps' kept too)", got, all)
	}
}

// TestPlanTeamStepShowsItsOwnStore (round-3 finding): a saved admin
// setup's team step showed a fresh "create" store suggestion instead of
// its team's own store, so saving it unchanged was refused with
// E_SETUP_ORG_MISMATCH. It shows orgs[<org>]'s store.
func TestPlanTeamStepShowsItsOwnStore(t *testing.T) {
	const own = "git@github.com:acme/lore-team.git"
	f := newFakeEnv(onboard.SetupEntry{Name: "hubadmin", Kind: onboard.KindAdmin, Org: "hub"})
	f.store.cfg.OrgStores = map[string]string{"hub": own}
	out, err := newEngine(f, onboardCatalog{}).Plan(bg, PlanIn{Setup: "hubadmin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range out.Steps {
		if s.ID != onboard.StepTeam {
			continue
		}
		if got := s.Values[onboard.QIDStoreURL].Text; got != own {
			t.Fatalf("team step store_url = %q, want the team's own store %q", got, own)
		}
		return
	}
	t.Fatal("the admin plan has no team step")
}

// TestJoinedMemberJoinStepResave (round-3 finding): saving a joined member
// setup's join step again (an edit, from the page) was refused with
// "Invite is required": the invite line is never kept. With no invite it
// is now a no-op: done, nothing applied.
func TestJoinedMemberJoinStepResave(t *testing.T) {
	f := newFakeEnv(onboard.SetupEntry{Name: "hub", Kind: onboard.KindMember, Org: "hub"})
	e := newEngine(f, onboardCatalog{})
	c, err := e.Check(bg, CheckIn{Setup: "hub", Step: string(onboard.StepJoin), Answers: onboard.Answers{}})
	if err != nil || len(c.FieldErrors) > 0 {
		t.Fatalf("check of a joined setup's join step: %+v %v", c, err)
	}
	out, err := e.Apply(bg, ApplyIn{Setup: "hub", Step: string(onboard.StepJoin), Answers: onboard.Answers{}})
	if err != nil || out.Status != onboard.StepDone || out.Changed {
		t.Fatalf("re-save of a joined setup's join step: %+v %v, want done and unchanged", out, err)
	}
	if len(f.applier.calls) != 0 {
		t.Fatalf("the join was applied again: %v", f.applier.calls)
	}
	// run:life:rest-kinds-open: each check the step lists shows as skipped
	// with the reason, never as "not checked" on the step and Summary.
	var step onboard.Step
	for _, st := range (onboardCatalog{}).Plan(onboard.KindMember) {
		if st.ID == onboard.StepJoin {
			step = st
		}
	}
	if len(step.Checks) == 0 {
		t.Fatal("the join step lists no checks: the test proves nothing")
	}
	for name, got := range map[string][]onboard.CheckResult{"check": c.Checks, "save": out.Checks} {
		for _, id := range step.Checks {
			if !slices.ContainsFunc(got, func(r onboard.CheckResult) bool {
				return r.Check == id && r.Status == onboard.StatusSkip && r.Detail == onboard.SkipJoinedAlready
			}) {
				t.Errorf("%s of a joined setup's join step: no skipped result for %s (Summary: not checked): %+v", name, id, got)
			}
		}
	}
}
