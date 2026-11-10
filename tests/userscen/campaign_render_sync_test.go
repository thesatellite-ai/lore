//go:build userscen

package userscen

// campaign_render_sync_test.go — the render area across two clones of one
// repo and a bare remote (the fake GitHub), the way two teammates meet it:
//   - alice inits and pushes; bob's clone already carries the project: his
//     first `lore init` adopts it (the same project id, sets up his git
//     hooks), a second refuses as already initialised, and his render
//     writes the same LORE.md;
//   - a policy alice sets (and later unsets) reaches bob after git pull,
//     and bob's render follows it;
//   - with the remote unreachable, init, render, policy and context still
//     answer from the checkout.

import (
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// campTwoClones is alice's initialised, pushed clone and bob's clone of it.
func campTwoClones(t *testing.T) (alice, bob *th.Clone) {
	t.Helper()
	w := newWorld(t)
	app := w.Remote(renderSlugOwn)
	a := w.Human("alice", "alice@example.com")
	a.WriteConfig(th.Config{Identity: &th.Identity{Key: "human:alice@example.com", Name: "Alice Example", Handle: "alice"}, Live: th.FreshFetch()})
	b := w.Human("bob", "bob@example.com")
	b.WriteConfig(th.Config{Identity: &th.Identity{Key: "human:bob@example.com", Name: "Bob Example", Handle: "bob"}, Live: th.FreshFetch()})
	alice = a.Clone(app, "tool")
	alice.MustLore("init", "--name=tool")
	alice.MustLore("rule", "add", "--body", "money is stored in minor units", "--severity", "must")
	alice.MustLore("render")
	alice.CommitAll("lore init")
	alice.AdminPush("origin", "main")
	bob = b.Clone(app, "tool")
	return alice, bob
}

func TestCampaignRender_SyncTwoClones(t *testing.T) {
	alice, bob := campTwoClones(t)
	pid := renderID(t, alice.Lore("init"), "prj") // alice's refusal names her project
	bob.MustLore("init").Says(t, "✓ adopted the shared lore project", pid)
	bob.Lore("init").Fails(t, campExitRefused, "[E_ALREADY_INITIALIZED]", pid, "hint: ")
	bob.MustLore("render")
	if a, b := renderRead(t, alice, renderLoreMD), renderRead(t, bob, renderLoreMD); a != b {
		t.Errorf("both clones render the same LORE.md:\n--- alice\n%s\n--- bob\n%s", a, b)
	}
	// alice tightens the budget warning; bob gets it with the code.
	alice.MustLore("policy", "set", "context.warn_at", "1", "--yes")
	alice.CommitAll("policy")
	alice.AdminPush("origin", "main")
	bob.Git("pull", "-q", "--no-rebase", "origin", "main")
	got := campJSON[campPolicyShow](t, bob.Lore("policy", "show", "context.warn_at", "--json"))
	if len(got.Data) != 1 || got.Data[0].Value != "1" || got.Data[0].Level != "repo" {
		t.Fatalf("bob sees alice's repo policy after pull: %+v", got)
	}
	bob.Lore("render", "--check").Fails(t, campExitRefused, "[E_REFUSED]")
	// alice removes it again; after the next pull bob is back to the default.
	alice.MustLore("policy", "unset", "context.warn_at", "--yes")
	alice.CommitAll("policy unset")
	alice.AdminPush("origin", "main")
	bob.Git("pull", "-q", "--no-rebase", "origin", "main")
	back := campJSON[campPolicyShow](t, bob.Lore("policy", "show", "context.warn_at", "--json"))
	if len(back.Data) != 1 || back.Data[0].Source != "default" {
		t.Errorf("after the unset is pulled, the default applies: %+v", back)
	}
	bob.MustLore("render", "--check")
}

func TestCampaignRender_SyncOffline(t *testing.T) {
	alice, bob := campTwoClones(t)
	alice.A.Offline()
	bob.A.Offline()
	alice.MustLore("render").Says(t, "· .lore/LORE.md (unchanged)")
	alice.MustLore("policy", "set", "tasks.stale_days", "30", "--yes")
	alice.MustLore("policy", "show", "tasks.stale_days").Says(t, "tasks.stale_days\t30\trepo")
	alice.MustLore("context", "--topics")
	alice.MustLore("policy", "unset", "tasks.stale_days", "--yes")
	bob.MustLore("init").Says(t, "✓ adopted the shared lore project")
	bob.MustLore("render")
	// A fresh repo initialised with no network at all.
	fresh := bob.In("vendor/offline")
	fresh.Git("init", "-q")
	fresh.MustLore("init", "--name=offline").Says(t, "lore initialized")
}
