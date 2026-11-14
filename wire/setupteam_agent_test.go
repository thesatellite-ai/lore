package wire

// setupteam_agent_test.go — an agent saves the admin setup's team step
// (owner decision 2026-11-11). agents.may_manage_people defaults to true:
// the agent acts for its person, and the org store the step creates has no
// policy rows to read, so creating the team is never refused by the flag.
// A team that sets the flag false gets today's refusal on the agent's next
// profile write, the team step saved again included.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// agentCtx is ctx as an agent caller (the REST X-Lore-Agent shape: the
// surface fixed the agent, so detection does not decide).
func agentCtx(ctx context.Context) context.Context {
	return profileContext(ctx, op.Caller{Agent: "fe-agent", AgentFixed: true, Yes: true})
}

// teamStepApp opens ana's App over w.repo with the org entries on disk now.
func teamStepApp(t *testing.T, w *orgWorld, home string) *App {
	t.Helper()
	opts, _ := StandaloneOptionsOrg(w.repo, home, w.repo, "test")
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestSetupTeamStepByAnAgent(t *testing.T) {
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
		// the repo the step runs in is the org's own (its own: pattern), so
		// the org store attaches there without a .lore/stores.yaml entry
		onboard.QIDOwn: {List: []string{"github.com/acme/*"}, IsList: true}}
	req := opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "northwind", Kind: onboard.KindAdmin}, Step: onboard.StepTeam, Apply: onboard.ApplyTeam, Answers: ans}

	// No policy anywhere and no team store yet: the agent creates the team.
	res, err := ap.Apply(agentCtx(ctx), req)
	if err != nil {
		t.Fatalf("an agent saving the team step of a new org: %v", err)
	}
	if !res.Changed || !isOrgAdmin(home, "northwind") {
		t.Fatalf("team step by an agent = %+v, want the org created with its admin setup", res)
	}
	if files := orgGit(t, filepath.Join(w.hosts, "agency/team.git"), "for-each-ref"); files == "" {
		t.Fatal("the team store has no commits after the agent's team step")
	}
	// D-6: the step makes the store for this machine (the org entry points
	// at it); it writes nothing into the repo it runs in, which may be a
	// client's
	if b, rerr := os.ReadFile(filepath.Join(w.repo, ".lore", "stores.yaml")); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		t.Fatal(rerr)
	} else if strings.Contains(string(b), "agency/team") || strings.Contains(string(b), "northwind") {
		t.Fatalf("the team step attached its store in the repo it ran in:\n%s", b)
	}

	// A person sets the flag false on the team's org folder: the agent's
	// next people write (the team step saved again) is refused.
	app2 := teamStepApp(t, w, home)
	e, ok := app2.Registry().Lookup("policy.set")
	if !ok {
		t.Fatal("no op policy.set")
	}
	in, _ := json.Marshal(map[string]string{"key": "agents.may_manage_people", "value": "false", "to": "northwind/org"})
	if _, err := app2.Run(ctx, e, op.JSONDecoder{Body: in}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("a person sets agents.may_manage_people false on the org: %v", err)
	}
	ap2, err := app2.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ap2.Apply(agentCtx(ctx), req)
	if !errs.Has(err, errs.PolicyDenied) || !strings.Contains(err.Error(), "agents.may_manage_people is false") {
		t.Fatalf("the team step saved by an agent with the flag false = %v, want E_POLICY_DENIED", err)
	}
	if _, err = ap2.Apply(ctx, req); err != nil {
		t.Fatalf("the person saves the same step: %v", err)
	}
}

// TestSetupTeamStepMachinePolicyRefusesFirst (agentpol 4): this machine's
// own agents.may_manage_people false (config.yaml policies:) used to refuse
// an agent's team step only at the new org's roster write, after the step
// had written the org's store and its config.yaml entry (a half-write the
// next save tripped over). It now refuses before anything is written: no
// commit in the team store, no org entry, config.yaml as it was. The
// person saving the same step is not refused (the flag is about agents).
func TestSetupTeamStepMachinePolicyRefusesFirst(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	w.bareAt(t, "agency/team")
	home := w.home(t, "ana")
	cfg := configPath(home)
	before, err := os.ReadFile(cfg)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	before = append(before, []byte("policies:\n  agents.may_manage_people: false\n")...)
	if err = os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cfg, before, 0o600); err != nil {
		t.Fatal(err)
	}
	app := teamStepApp(t, w, home)
	ap, err := app.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ans := onboard.Answers{onboard.QIDOrg: {Text: "northwind"}, onboard.QIDStoreURL: {Text: "https://github.com/agency/team"},
		onboard.QIDEmail: {Text: "ana@agency.example"}, onboard.QIDName: {Text: "Ana"},
		onboard.QIDOwn: {List: []string{"github.com/acme/*"}, IsList: true}}
	req := opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "northwind", Kind: onboard.KindAdmin}, Step: onboard.StepTeam, Apply: onboard.ApplyTeam, Answers: ans}

	_, err = ap.Apply(agentCtx(ctx), req)
	if !errs.Has(err, errs.PolicyDenied) || !strings.Contains(err.Error(), "agents.may_manage_people is false") {
		t.Fatalf("an agent's team step under this machine's false = %v, want E_POLICY_DENIED", err)
	}
	if refs := orgGit(t, filepath.Join(w.hosts, "agency/team.git"), "for-each-ref"); refs != "" {
		t.Fatalf("the refused team step wrote the team store:\n%s", refs)
	}
	if after, _ := os.ReadFile(cfg); string(after) != string(before) || isOrgAdmin(home, "northwind") {
		t.Fatalf("the refused team step changed config.yaml:\n%s", after)
	}

	if _, err = ap.Apply(ctx, req); err != nil {
		t.Fatalf("the person saves the same step: %v", err)
	}
	if !isOrgAdmin(home, "northwind") {
		t.Fatal("the person's team step made no org entry")
	}
}
