//go:build teame2e

package teame2e

// later.md 569 (BUG-T6) on cmd/lore: the agent roster check (F18) of
// coordination syncs a cached store copy once on a miss. Needs the
// wire/coord_standalone_world.go requireRosterAgent refresh
// (fixall/te-d/requireRosterAgent-refresh.diff); teDStoreFetches is in
// teame2e_te_d_roster_test.go.

import (
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// BUG-T6 itself (stores.RosterCheck in v0.1.17): an agent started a moment
// ago by `lore agent start` on its owner's other machine is on the remote
// roster but not in this machine's copy fetched within fetch_ttl; its first
// claim here syncs the roster once and succeeds instead of being refused as
// "not on the roster". An agent nobody registered is still refused.
func TestTeD_AgentRosterMissOnCachedFetchSyncsOnce(t *testing.T) {
	a := newAgency(t, th.Cmd("agent", "start"), th.Cmd("task", "claim"), th.Flag("assign", "task", "add"), th.Flag("to", "task", "add"))
	// alice's second machine: same identity, its own home and store copy,
	// fetch_ttl one hour.
	box := a.w.Human("alicebox", "alice@northwind.example")
	cfg := aliceConfig()
	cfg.Live = &th.Live{FetchTTL: "1h"}
	box.WriteConfig(cfg)
	boxApp := box.Clone(a.app, "app")
	id := mustID(t, boxApp.MustLore("task", "add", "wire the parser", "--to", "repo", "--tasklist", a.tl, "--assign", "@dev1"), "tsk")
	boxApp.CommitAll("task")
	// The harness logs git calls per actor name: the box's own and its
	// agent session's (frontend-2) are both this machine's fetches.
	boxFetches := func() int { return len(teDStoreFetches(a.w, "alicebox")) + len(teDStoreFetches(a.w, "frontend-2")) }
	n0 := boxFetches()
	boxApp.MustLore("task", "add", "cached", "--to", "repo", "--tasklist", a.tl, "--assign", "@dev1")
	if d := boxFetches() - n0; d != 0 {
		t.Fatalf("the box's roster copy is cached: %d fetches", d)
	}

	// On her laptop alice starts the agent; the roster row is pushed.
	ag := startAgent(t, a.alice, a.aliceApp, "frontend-2")
	onBox := box.Agent("frontend-2")
	onBox.SetEnv(th.EnvLoreActor, ag.Env(th.EnvLoreActor))
	onBox.SetEnv(th.EnvLoreSession, ag.Env(th.EnvLoreSession))

	n1 := boxFetches()
	boxApp.As(onBox).MustLore("task", "claim", id)
	// At least the roster sync; the claim's own signals write fetches too.
	if d := boxFetches() - n1; d < 1 {
		t.Fatalf("the agent's miss on a cached roster must sync the store: %d fetches", d)
	}

	ghost := box.Agent("ghost")
	ghost.SetEnv(th.EnvLoreActor, "agent:ghost-unregistered")
	boxApp.As(ghost).Lore("task", "claim", id).Refused(t, "roster")
	// Refused before any signals write: the one roster sync is its only fetch.
	if d := len(teDStoreFetches(a.w, "ghost")); d != 1 {
		t.Fatalf("an unregistered agent's miss costs exactly one fetch: %d", d)
	}
}

// The same class for an agent's owner (rosterOwner, b-errs2 sweep): dev2
// registers on the laptop after the second machine's roster copy was
// fetched (within fetch_ttl there); `lore agent start` on that machine,
// owned by the caller, finds dev2 after one sync instead of "you are not
// on the roster yet".
func TestTeD_AgentOwnerMissOnCachedFetchSyncsOnce(t *testing.T) {
	a := newAgency(t, th.Cmd("agent", "start"), th.Flag("assign", "task", "add"), th.Flag("to", "task", "add"))
	const dev2Key = "human:dev2@northwind.example"
	boxCfg := th.Config{
		Personal: &th.Personal{Store: th.PersonalLocal},
		Orgs:     []th.Org{northwindOrg(dev2Key, "Dev Two", "dev2")},
		Live:     &th.Live{FetchTTL: "1h"},
	}
	box := a.w.Human("dev2box", "dev2@northwind.example")
	box.WriteConfig(boxCfg)
	boxApp := box.Clone(a.app, "app")
	// The box reads the org roster once (an assignment resolves @dev1).
	boxApp.MustLore("task", "add", "warm the roster copy", "--to", "repo", "--tasklist", a.tl, "--assign", "@dev1")

	laptop := a.w.Human("dev2", "dev2@northwind.example")
	laptop.WriteConfig(th.Config{Personal: boxCfg.Personal, Orgs: boxCfg.Orgs, Live: th.FreshFetch()})
	laptopApp := laptop.Clone(a.app, "app")
	laptopApp.MustLore("actor", "register")

	boxApp.MustLore("agent", "start", "--label", "box-agent")
}
