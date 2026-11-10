package setup

// setupops_test.go — setup.fix picks the step where the check fails (not
// the first step owning it), step store rows carry their store url, and
// the team step's create-only checks never fail on the setup's own org
// (re-check of a saved step, retry of a save that stopped midway).

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

const (
	personalURL = "git@github.com:octo-personal/lore-me.git"
	teamURL     = "git@github.com:acme/team.git"
)

// storeCatalog is fakeCatalog with a personal_store and a team step that
// both run store.private and org.name_free-style team checks.
type storeCatalog struct{ fakeCatalog }

var storeChecks = map[onboard.CheckID]onboard.Check{
	onboard.CheckStorePrivate: {ID: onboard.CheckStorePrivate, Level: onboard.LevelError,
		Fix: onboard.Fix{Text: "make {url} private", Command: []string{"gh", "repo", "edit", "{url}"}, Runnable: true, Outward: true}},
	onboard.CheckOrgNameFree: {ID: onboard.CheckOrgNameFree, Level: onboard.LevelError, Fix: onboard.Fix{Text: "pick another name"}},
	onboard.CheckOrgFree:     {ID: onboard.CheckOrgFree, Level: onboard.LevelError, Fix: onboard.Fix{Text: "pick another store"}},
}

func (storeCatalog) Plan(onboard.Kind) []onboard.Step {
	return []onboard.Step{
		{ID: onboard.StepKind, Apply: onboard.ApplySetupEntry},
		{ID: onboard.StepPersonalStore, Needs: []onboard.StepID{onboard.StepKind},
			Questions: []onboard.Question{{ID: onboard.QIDStoreURL, Type: onboard.QText}},
			Checks:    []onboard.CheckID{onboard.CheckStorePrivate}},
		{ID: onboard.StepTeam, Needs: []onboard.StepID{onboard.StepKind},
			Questions: []onboard.Question{{ID: onboard.QIDOrg, Type: onboard.QText}, {ID: onboard.QIDStoreURL, Type: onboard.QText}},
			Checks:    []onboard.CheckID{onboard.CheckStorePrivate, onboard.CheckOrgFree, onboard.CheckOrgNameFree}, Apply: onboard.ApplyTeam},
	}
}

func (c storeCatalog) Check(id onboard.CheckID, step onboard.StepID, k onboard.Kind, v map[string]string) (onboard.Check, bool) {
	if sc, ok := storeChecks[id]; ok {
		return sc, true
	}
	return c.fakeCatalog.Check(id, step, k, v)
}

func (c storeCatalog) HomeStep(id onboard.CheckID, k onboard.Kind) (onboard.StepID, bool) {
	for _, st := range c.Plan(k) {
		if slices.Contains(st.Checks, id) {
			return st.ID, true
		}
	}
	return "", false
}

// urlProber answers store.private per store url (a url Target first, then
// the answered url): fail for the urls in public, skip with no url, ok
// otherwise; the org checks fail always (an orgs[] entry exists), so a
// result other than fail proves the engine never asked.
type urlProber struct {
	public []string
	asked  []onboard.CheckID
}

func (p *urlProber) Probe(_ context.Context, reqs []ProbeRequest) []ProbeResult {
	out := make([]ProbeResult, 0, len(reqs))
	for _, r := range reqs {
		p.asked = append(p.asked, r.Check.ID)
		u := r.Target
		if u == "" {
			u = r.Answers[onboard.QIDStoreURL].Text
		}
		res := ProbeResult{Check: r.Check.ID, Target: r.Target, Status: onboard.StatusOK, Vars: map[string]string{onboard.VarURL: u}}
		switch {
		case r.Check.ID == onboard.CheckOrgNameFree || r.Check.ID == onboard.CheckOrgFree:
			res.Status, res.Detail = onboard.StatusFail, "an orgs[] entry exists"
		case u == "":
			res.Status, res.Vars = onboard.StatusSkip, nil
		case slices.Contains(p.public, u):
			res.Status, res.Detail = onboard.StatusFail, "public"
		}
		out = append(out, res)
	}
	return out
}

func (p *urlProber) Prefill(context.Context, onboard.PrefillID, onboard.SetupEntry) (PrefillOut, error) {
	return PrefillOut{}, nil
}

// orgStore is fakeStore that can list orgs[] entries (OrgStoreLister).
type orgStore struct {
	*fakeStore
	orgs map[string][]string
}

func (s orgStore) OrgStoreURLs(_ context.Context, org string) ([]string, bool, error) {
	u, ok := s.orgs[org]
	return u, ok, nil
}

type storeEnv struct {
	fakeEnv
	st     Store
	prober *urlProber
}

func (e storeEnv) SetupStore(context.Context) (Store, error)   { return e.st, nil }
func (e storeEnv) SetupProber(context.Context) (Prober, error) { return e.prober, nil }

func newStoreEngine(st Store, base fakeEnv, pr *urlProber) engine {
	e := newEngine(storeEnv{fakeEnv: base, st: st, prober: pr}, storeCatalog{})
	e.now = newTestEngine(base).now
	return e
}

// An admin setup "acme" being made: kind done, personal store "none"
// (no url), team step saved with a public org store.
func teamInProgress(t *testing.T, entry onboard.SetupEntry, orgs map[string][]string) (engine, *urlProber, *fakeEnv) {
	t.Helper()
	base := newFakeEnv(entry)
	pr := &urlProber{public: []string{teamURL}}
	for _, p := range []onboard.Progress{
		{Setup: "acme", Step: onboard.StepKind, Status: onboard.StepDone},
		{Setup: "acme", Step: onboard.StepPersonalStore, Status: onboard.StepDone, Answers: onboard.Answers{}},
		{Setup: "acme", Step: onboard.StepTeam, Status: onboard.StepFailed,
			Answers: onboard.Answers{onboard.QIDOrg: text("acme"), onboard.QIDStoreURL: text(teamURL)}},
	} {
		if err := base.store.PutProgress(bg, p); err != nil {
			t.Fatal(err)
		}
	}
	return newStoreEngine(orgStore{fakeStore: base.store, orgs: orgs}, base, pr), pr, &base
}

// Bug: setup.fix with no target took the first step owning store.private
// (personal_store, whose store is "none" → skip) and refused the team
// step's failing store.private with "check is skip now".
func TestFixPicksTheStepWhereTheCheckFails(t *testing.T) {
	e, _, base := teamInProgress(t, onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin}, nil)
	in := FixIn{Setup: "acme", Check: string(onboard.CheckStorePrivate)}
	_, err := e.Fix(bg, in)
	if !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("Fix = %v, want E_NEEDS_CONFIRM for the team step's public store", err)
	}
	out, err := e.Fix(yesCtx, in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gh", "repo", "edit", teamURL}; !slices.Equal(out.Argv, want) {
		t.Fatalf("ran %v, want %v", out.Argv, want)
	}
	if len(base.runner.ran) != 1 {
		t.Fatalf("runner ran %d fixes, want 1", len(base.runner.ran))
	}
	// Nothing fails anywhere: the refusal names the first step's status.
	e2, pr2, _ := teamInProgress(t, onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin}, nil)
	pr2.public = nil
	if _, err := e2.Fix(yesCtx, in); !errs.Has(err, errs.FixRefused) {
		t.Fatalf("Fix with nothing failing = %v, want E_FIX_REFUSED", err)
	}
}

// A step's store rows carry the store url they probed, so the page's
// "Run it" (setup.fix --target <url>) re-probes that same store; a fix
// with that target works even for a finished setup, which has no saved
// team answers to find the store from.
func TestStepStoreRowsCarryTheirURL(t *testing.T) {
	e, _, _ := teamInProgress(t, onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin}, nil)
	out, err := e.Check(bg, CheckIn{Setup: "acme", Step: string(onboard.StepTeam)})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(out.Checks, func(r onboard.CheckResult) bool { return r.Check == onboard.CheckStorePrivate })
	if i < 0 || out.Checks[i].Target != teamURL {
		t.Fatalf("store.private row = %+v, want target %s", out.Checks, teamURL)
	}
	// Finished setup: no progress rows, the target alone names the store.
	base := newFakeEnv(onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin, Org: "acme", PersonalStore: personalURL})
	f := newStoreEngine(orgStore{fakeStore: base.store}, base, &urlProber{public: []string{teamURL}})
	if _, err := f.Fix(bg, FixIn{Setup: "acme", Check: string(onboard.CheckStorePrivate)}); !errs.Has(err, errs.FixRefused) {
		t.Fatalf("finished, no target: %v, want E_FIX_REFUSED (the personal store is private)", err)
	}
	if _, err := f.Fix(bg, FixIn{Setup: "acme", Check: string(onboard.CheckStorePrivate), Target: teamURL}); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("finished, target %s: %v, want E_NEEDS_CONFIRM", teamURL, err)
	}
	// A url the answers or the entry do not hold exactly (the sanitized
	// form of one) is not carried.
	if got := stepTarget(ProbeResult{Vars: map[string]string{onboard.VarURL: "https://example.org/x"}}, stepState{}, onboard.Answers{}); got != "" {
		t.Fatalf("stepTarget of an unknown url = %q, want empty", got)
	}
}

// Bug: re-checking the saved team step of an admin setup failed
// org.name_free on the setup's own org (setup list ✗ 1 while doctor was
// healthy), and a retry after a save that stopped midway failed it on
// the orgs[] entry that same save left. Neither may fail; a real clash
// still does.
func TestTeamCreateOnlyChecksOnOwnOrg(t *testing.T) {
	answers := onboard.Answers{onboard.QIDOrg: text("acme"), onboard.QIDStoreURL: text(teamURL)}
	status := func(t *testing.T, e engine, pr *urlProber) map[onboard.CheckID]onboard.Status {
		t.Helper()
		pr.public = nil
		out, err := e.Check(bg, CheckIn{Setup: "acme", Step: string(onboard.StepTeam), Answers: answers})
		if err != nil {
			t.Fatal(err)
		}
		m := map[onboard.CheckID]onboard.Status{}
		for _, r := range out.Checks {
			m[r.Check] = r.Status
		}
		if got := len(out.Checks); got != 3 {
			t.Fatalf("got %d results, want the step's 3 in order", got)
		}
		return m
	}
	cases := []struct {
		name  string
		entry onboard.SetupEntry
		other *onboard.SetupEntry
		orgs  map[string][]string
		want  onboard.Status
	}{
		{"saved team step (own org)", onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin, Org: "acme"}, nil, map[string][]string{"acme": {teamURL}}, onboard.StatusSkip},
		{"half-written, same store", onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin}, nil, map[string][]string{"acme": {teamURL + "/"}}, onboard.StatusSkip},
		{"orgs entry for another store", onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin}, nil, map[string][]string{"acme": {"git@github.com:other/store.git"}}, onboard.StatusFail},
		{"org used by another setup", onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin}, &onboard.SetupEntry{Name: "work", Kind: onboard.KindMember, Org: "acme"}, map[string][]string{"acme": {teamURL}}, onboard.StatusFail},
		{"no orgs entry: the probe decides", onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin}, nil, nil, onboard.StatusFail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, pr, base := teamInProgress(t, c.entry, c.orgs)
			if c.other != nil {
				if _, err := base.store.PutSetup(bg, *c.other); err != nil {
					t.Fatal(err)
				}
			}
			got := status(t, e, pr)
			for _, id := range []onboard.CheckID{onboard.CheckOrgNameFree, onboard.CheckOrgFree} {
				if got[id] != c.want {
					t.Errorf("%s = %s, want %s", id, got[id], c.want)
				}
			}
			asked := slices.Contains(pr.asked, onboard.CheckOrgNameFree)
			if asked != (c.want == onboard.StatusFail) {
				t.Errorf("org.name_free probed = %v, want %v", asked, c.want == onboard.StatusFail)
			}
		})
	}
	// The retry's save goes through: the gate no longer sees a failure.
	e, _, base := teamInProgress(t, onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin}, map[string][]string{"acme": {teamURL}})
	e.env.(storeEnv).prober.public = nil
	if _, err := e.Apply(bg, ApplyIn{Setup: "acme", Step: string(onboard.StepTeam), Answers: answers}); err != nil {
		t.Fatalf("retry apply after a half-write: %v", err)
	}
	if !slices.Contains(base.applier.calls, onboard.StepTeam) {
		t.Fatalf("applier calls %v, want the team step", base.applier.calls)
	}
}

// onboard.OwnOrg decides only the org create checks.
func TestOwnOrgOnlyOrgChecks(t *testing.T) {
	c := onboard.OrgClaim{Org: "acme", SavedOrg: "acme"}
	if _, ok := onboard.OwnOrg(onboard.CheckSetupNameFree, c); ok {
		t.Fatal("setup.name_free must not be decided by OwnOrg")
	}
	if _, ok := onboard.OwnOrg(onboard.CheckOrgNameFree, onboard.OrgClaim{}); ok {
		t.Fatal("no org answered: not own")
	}
}

// offlineStoreHost answers like the schema step in a repo whose attached
// store is offline with no local copy yet.
type offlineStoreHost struct{ fakeHost }

func (*offlineStoreHost) UpdateSchema(context.Context, bool) ([]string, error) {
	return nil, errs.New(errs.StoreUnreachable, "attach: store team_notes could not be opened: git: the store has no local copy yet and cannot be reached\ngit fetch exited 128")
}

// Bug: bare `lore setup` in a repo with an offline attached store failed
// with E_STORE_UNREACHABLE before the page opened. The page run reports
// the skipped schema step as one warning line and opens the page;
// --schema-only (lore upgrade) still fails.
func TestPageRunOpensWithAnOfflineStore(t *testing.T) {
	out, err := Run(t.Context(), &offlineStoreHost{}, RunIn{}, false)
	if err != nil || !out.Web.Open || len(out.Lines) != 1 || !strings.HasPrefix(out.Lines[0], schemaSkippedLine) || strings.Contains(out.Lines[0], "\n") {
		t.Fatalf("page run: %+v %v", out, err)
	}
	if _, err := Run(t.Context(), &offlineStoreHost{}, RunIn{SchemaOnly: true}, false); !errs.Has(err, errs.StoreUnreachable) {
		t.Fatalf("--schema-only: %v, want E_STORE_UNREACHABLE", err)
	}
}

// A store.write fail on a store that is not on GitHub (a local folder
// store, another git host) shows the not_github write fix, filled; and a
// fix whose values cannot all be filled shows a readable text, never the
// raw E_FIX_REFUSED refusal nor a {placeholder}.
func TestStoreWriteFixTextForNonGitHubStore(t *testing.T) {
	const folder = "file:///srv/notes"
	base := newFakeEnv(onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, PersonalStore: folder})
	base.prober.results[onboard.CheckStoreWrite] = ProbeResult{Status: onboard.StatusFail, Detail: "permission denied",
		Vars: map[string]string{onboard.VarURL: folder, onboard.VarFixVariant: string(onboard.FixNotGitHub)}}
	e := newEngine(base, onboardCatalog{})
	step := onboard.Step{ID: onboard.StepPersonalStore, Checks: []onboard.CheckID{onboard.CheckStoreWrite}}
	s := stepState{name: "personal", entry: base.store.cfg.Setups[0], exists: true, kind: onboard.KindSolo}
	got, err := e.probe(bg, s, step, onboard.Answers{onboard.QIDStoreURL: text(folder)})
	if err != nil || len(got) != 1 {
		t.Fatalf("probe: %v %v", got, err)
	}
	if f := got[0].Fix.Text; !strings.Contains(f, folder) || !strings.Contains(f, "folder store") || strings.Contains(f, "{") {
		t.Fatalf("fix text %q: want the not_github write fix naming %s", f, folder)
	}
	// The default (GitHub) text needs {repo}, which this url has none of.
	tf := onboard.TextFix(onboard.FixFor(onboard.CheckStoreWrite, "", onboard.FixDefault), map[string]string{onboard.VarURL: folder})
	if strings.Contains(tf.Text, "{") || strings.Contains(tf.Text, string(errs.FixRefused)) || len(tf.Command) != 0 || tf.Runnable {
		t.Fatalf("TextFix = %+v", tf)
	}
}
