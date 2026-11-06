package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// TestOrgCreateThenAgentRegisterPushes (later 128, the e2e smoke (c) of
// the org layer on cmd/lore; its team half is dropped, dropped.md team
// add/list): after `lore org create` in a repo, `lore actor register
// --kind agent` writes the agent's actor row into
// <org folder>/.lore/data/actors as a _v: 2 row with its owner, and
// pushes it at once (autopush on: the row is on the store's main without
// a sync); a second HOME's `lore org import` outside any repo adds its
// own roster row next to both, so the remote roster holds all three.
func TestOrgCreateThenAgentRegisterPushes(t *testing.T) {
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.Use(ctx, opsorgcmd.UseIn{Entry: "agency"}); err != nil {
		t.Fatalf("org use: %v", err)
	}
	app, err := New(StandaloneOptions(w.repo, mira, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := app.ActorRegister(ctx, opsinfra.ActorRegisterIn{Kind: opsinfra.RegisterKindAgent, Label: "agent-frontend"})
	_ = app.Close()
	if err != nil {
		t.Fatalf("register --kind agent: %v", err)
	}
	if !out.Created || out.Label != "agent-frontend" || !strings.HasPrefix(out.ID, "act_") {
		t.Fatalf("register out %+v", out)
	}
	notes := filepath.Join(w.hosts, "agency/notes.git")
	agent := orgGit(t, notes, "show", "main:org/.lore/data/actors/"+out.ID+".json")
	for _, want := range []string{`"_v": 2`, `"agent"`, `"agent:agent-frontend"`, `"owner_actor_id": "act_`} {
		if !strings.Contains(agent, want) {
			t.Fatalf("the pushed agent row lacks %s:\n%s", want, agent)
		}
	}
	if n := strings.Count(w.storeFiles(t), "org/.lore/data/actors/act_"); n != 2 {
		t.Fatalf("roster rows on the remote after create + agent = %d, want 2 (mira, her agent)", n)
	}
	if strings.Contains(orgGit(t, w.repo, "ls-files"), "actors/"+out.ID) {
		t.Fatal("the org roster's agent row reached the client repo's tracked files")
	}

	bob := w.home(t, "bob")
	outside := filepath.Join(w.root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := orgHostAt(t, outside, bob).Import(ctx, opsorgcmd.ImportIn{URL: orgTestImport, Key: "human:bob@agency.example", Name: "Bob Roe", Handle: "bob"}, true); err != nil {
		t.Fatalf("import: %v", err)
	}
	if n := strings.Count(w.storeFiles(t), "org/.lore/data/actors/act_"); n != 3 {
		t.Fatalf("roster rows on the remote after import = %d, want 3 (mira, agent, bob)", n)
	}
}
