package wire

// setupteam_credit_test.go — devco D-424: a person who runs setup through
// a coding agent creates the org as themself. The team step used to
// register "<person> via <tool>" (agent:auto/<tool>/…), stamp the org's
// first rows with it and add Lore-Agent trailers to the store's commits
// before any agent label was chosen.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitcommit"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/store"
)

func TestSetupTeamStepUnderAnAgentCreditsThePerson(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	w.bareAt(t, "agency/team")
	home := w.home(t, "ana")
	agentEnv(t) // after newOrgWorld, which declares a person (LORE_HUMAN=1)
	app := teamStepApp(t, w, home)
	var said []string
	app.opts.Notice = func(s string) { said = append(said, s) }
	ap, err := app.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ans := onboard.Answers{onboard.QIDOrg: {Text: "northwind"}, onboard.QIDStoreURL: {Text: "https://github.com/agency/team"},
		onboard.QIDEmail: {Text: "ana@agency.example"}, onboard.QIDName: {Text: "Ana"},
		onboard.QIDOwn: {List: []string{"github.com/acme/*"}, IsList: true}}
	req := opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "northwind", Kind: onboard.KindAdmin}, Step: onboard.StepTeam, Apply: onboard.ApplyTeam, Answers: ans}
	if _, err = ap.Apply(ctx, req); err != nil {
		t.Fatalf("the team step under a coding agent: %v", err)
	}
	bare := filepath.Join(home, "stores", "agency", "team") // the clone: the commit waits there until lore sync
	if logs := orgGit(t, bare, "log", "--all", "--format=%B"); strings.Contains(logs, gitcommit.TrailerAgent+":") {
		t.Fatalf("the org store's commits carry a %s trailer:\n%s", gitcommit.TrailerAgent, logs)
	}
	if rows := orgGit(t, bare, "log", "--all", "-p"); strings.Contains(rows, "agent:auto/") || strings.Contains(rows, viaTool) {
		t.Fatalf("the team step wrote an agent actor into the org store:\n%s", rows)
	}
	if !strings.Contains(strings.Join(said, "\n"), "credited to Ana") {
		t.Fatalf("setup must say whom it credits when it runs under an agent; said:\n%s", strings.Join(said, "\n"))
	}
}

// TestActorMergeFromAnywhere (devco D-424): the agent actor a coding
// tool's write registered in an org store is merged into its person with
// `lore actor merge`, from inside the store's clone or from a folder that
// is no repo (naming the store with --to), appending the merge rows (no
// history rewrite). It answered E_NOT_FOUND ("no actor … in the stores
// this command reads") inside the clone and E_NOT_A_STORE outside a repo.
func TestActorMergeFromAnywhere(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	w.bareAt(t, "agency/team")
	home := w.home(t, "ana")
	app := teamStepApp(t, w, home)
	ap, err := app.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ans := onboard.Answers{onboard.QIDOrg: {Text: "northwind"}, onboard.QIDStoreURL: {Text: "https://github.com/agency/team"},
		onboard.QIDEmail: {Text: "ana@agency.example"}, onboard.QIDName: {Text: "Ana"},
		onboard.QIDOwn: {List: []string{"github.com/acme/*"}, IsList: true}}
	if _, err = ap.Apply(ctx, opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "northwind", Kind: onboard.KindAdmin}, Step: onboard.StepTeam, Apply: onboard.ApplyTeam, Answers: ans}); err != nil {
		t.Fatal(err)
	}
	// the person's coding agent writes into the org store: its actor
	agentEnv(t)
	agentApp := teamStepApp(t, w, home)
	if err = agentApp.EnsureActor(ctx, store.Source("northwind/org")); err != nil {
		t.Fatalf("the agent's actor row in the org store: %v", err)
	}
	who, err := resolveIdentity(ctx, agentApp.opts)
	if err != nil || who.Agent == "" {
		t.Fatalf("detection did not see the agent (the case under test): %+v %v", who, err)
	}
	agentID, err := who.ActorID()
	if err != nil {
		t.Fatal(err)
	}
	personID, err := who.Person().ActorID()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LORE_HUMAN", "1")
	in := `{"from":"` + agentID.String() + `","into":"` + personID.String() + `"`

	// inside the org store's clone: the dry run finds both
	clone := filepath.Join(home, "stores", "agency", "team")
	cloneOpts, _ := StandaloneOptionsOrg(clone, home, clone, "test")
	cloneApp, err := New(cloneOpts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cloneApp.Close() })
	runOp(t, ctx, cloneApp, "actor.merge", in+`,"dry_run":true}`)

	// from a folder that is no repo, naming the store
	away := t.TempDir()
	awayOpts, _ := StandaloneOptionsOrg(away, home, away, "test")
	awayApp, err := New(awayOpts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = awayApp.Close() })
	runOp(t, ctx, awayApp, "actor.merge", in+`,"dry_run":true}`) // no --store: this machine's org stores
	out := runOp(t, ctx, awayApp, "actor.merge", in+`,"store":"northwind/org"}`)
	if !strings.Contains(out, `"marked":1`) {
		t.Fatalf("the merge must mark the agent actor merged_into the person: %s", out)
	}
	if rows := orgGit(t, clone, "log", "-p", "-1"); !strings.Contains(rows, personID.String()) || !strings.Contains(rows, "merged_into") {
		t.Fatalf("the merge must append the agent actor's merged_into row:\n%s", rows)
	}
}

// runOp runs registry op name with the JSON body in on app as a person
// who said yes, failing the test on an error; it returns the JSON output.
func runOp(t *testing.T, ctx context.Context, app *App, name, in string) string {
	t.Helper()
	e, ok := app.Registry().Lookup(name)
	if !ok {
		t.Fatalf("no op %s", name)
	}
	res, err := app.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{Yes: true})
	if err != nil {
		t.Fatalf("%s %s: %v", name, in, err)
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSetupTeamStepCreditedStillPolicyChecked (devco D-424): crediting the
// person does not hide the agent from this machine's
// agents.may_manage_people false: the team step run through a detected
// coding agent is still refused before anything is written.
func TestSetupTeamStepCreditedStillPolicyChecked(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	w.bareAt(t, "agency/team")
	home := w.home(t, "ana")
	agentEnv(t)
	cfg := configPath(home)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("policies:\n  agents.may_manage_people: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ap, err := teamStepApp(t, w, home).SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ans := onboard.Answers{onboard.QIDOrg: {Text: "northwind"}, onboard.QIDStoreURL: {Text: "https://github.com/agency/team"},
		onboard.QIDEmail: {Text: "ana@agency.example"}, onboard.QIDName: {Text: "Ana"},
		onboard.QIDOwn: {List: []string{"github.com/acme/*"}, IsList: true}}
	_, err = ap.Apply(ctx, opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "northwind", Kind: onboard.KindAdmin}, Step: onboard.StepTeam, Apply: onboard.ApplyTeam, Answers: ans})
	if !errs.Has(err, errs.PolicyDenied) {
		t.Fatalf("a detected agent's team step under agents.may_manage_people false = %v, want E_POLICY_DENIED", err)
	}
}

// TestOrgCreateUnderAnAgentCreditsThePerson (devco D-424): `lore org
// create` run through a coding agent registers the person on the new
// roster, never "<person> via <tool>", with no Lore-Agent trailer, and
// says whom it credited.
func TestOrgCreateUnderAnAgentCreditsThePerson(t *testing.T) {
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	agentEnv(t)
	h := orgHostAt(t, w.repo, mira)
	rep, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true)
	if err != nil {
		t.Fatalf("org create under a coding agent: %v", err)
	}
	if text := strings.Join(rep.Lines, "\n"); !strings.Contains(text, "credited to you, the person") {
		t.Fatalf("org create under an agent must say whom it credits:\n%s", text)
	}
	clone := filepath.Join(mira, "stores", "agency", "notes")
	if logs := orgGit(t, clone, "log", "--all", "--format=%B"); strings.Contains(logs, gitcommit.TrailerAgent+":") {
		t.Fatalf("org create's commits carry a %s trailer:\n%s", gitcommit.TrailerAgent, logs)
	}
	if rows := orgGit(t, clone, "log", "--all", "-p"); strings.Contains(rows, "agent:auto/") || strings.Contains(rows, viaTool) {
		t.Fatalf("org create wrote an agent actor into the org store:\n%s", rows)
	}
}

// teamStepAnswers are the admin team step's answers for the org northwind
// on the store agency/team.
func teamStepAnswers() onboard.Answers {
	return onboard.Answers{onboard.QIDOrg: {Text: "northwind"}, onboard.QIDStoreURL: {Text: "https://github.com/agency/team"},
		onboard.QIDEmail: {Text: "ana@agency.example"}, onboard.QIDName: {Text: "Ana"},
		onboard.QIDOwn: {List: []string{"github.com/acme/*"}, IsList: true}}
}

// TestSetupTeamStepUnderLoreActorAgentCreditsThePerson (devco D-440): an
// admin's wrapper sets LORE_ACTOR=agent:<label> and runs the admin setup.
// The team step of the new org failed ("the agent … has no person behind
// it … cannot change the roster") after it had created and committed the
// org's store clone: a half-made org. Like a detected coding agent, the
// step is the person's own: it completes, credited to the person, with no
// agent actor written.
func TestSetupTeamStepUnderLoreActorAgentCreditsThePerson(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	w.bareAt(t, "agency/team")
	home := w.home(t, "ana")
	t.Setenv("LORE_NO_DETECT", "1")
	t.Setenv("LORE_HUMAN", "")
	t.Setenv("LORE_ACTOR", "agent:store-builder")
	app := teamStepApp(t, w, home)
	var said []string
	app.opts.Notice = func(s string) { said = append(said, s) }
	ap, err := app.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "northwind", Kind: onboard.KindAdmin}, Step: onboard.StepTeam, Apply: onboard.ApplyTeam, Answers: teamStepAnswers()}
	if _, err = ap.Apply(ctx, req); err != nil {
		t.Fatalf("the team step under LORE_ACTOR=agent:store-builder: %v", err)
	}
	clone := filepath.Join(home, "stores", "agency", "team")
	if rows := orgGit(t, clone, "log", "--all", "-p"); strings.Contains(rows, "agent:store-builder") {
		t.Fatalf("the team step wrote the agent actor into the org store:\n%s", rows)
	}
	if !strings.Contains(strings.Join(said, "\n"), "credited to Ana") {
		t.Fatalf("setup must say whom it credits when LORE_ACTOR names an agent; said:\n%s", strings.Join(said, "\n"))
	}
}

// TestSetupTeamStepRefusesAnOwnerlessAgentFirst (devco D-440): the team
// step of a new org named as an agent with no person behind it (--actor
// agent:<label>, where a surface offers --actor) is refused before
// anything is written: no store clone, no org entry, nothing on the
// remote.
func TestSetupTeamStepRefusesAnOwnerlessAgentFirst(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	w.bareAt(t, "agency/team")
	bare := filepath.Join(w.hosts, "agency/team.git")
	home := w.home(t, "ana")
	opts, _ := StandaloneOptionsOrg(w.repo, home, w.repo, "test")
	opts.FlagActor = "agent:store-builder"
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	ap, err := app.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "northwind", Kind: onboard.KindAdmin}, Step: onboard.StepTeam, Apply: onboard.ApplyTeam, Answers: teamStepAnswers()}
	_, err = ap.Apply(ctx, req)
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("an ownerless agent's team step = %v, want E_INVALID_INPUT before any write", err)
	}
	if _, serr := os.Stat(filepath.Join(home, "stores", "agency", "team")); !os.IsNotExist(serr) {
		t.Fatalf("the refused team step made the org's store clone: %v", serr)
	}
	if b, rerr := os.ReadFile(configPath(home)); rerr == nil && strings.Contains(string(b), "northwind") {
		t.Fatalf("the refused team step wrote the org entry:\n%s", b)
	}
	if refs := orgGit(t, bare, "for-each-ref"); strings.TrimSpace(refs) != "" {
		t.Fatalf("the refused team step pushed to the remote:\n%s", refs)
	}
}
