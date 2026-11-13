//go:build userscen

package userscen

// campaign_render_policy_test.go — `lore policy set|show|unset` the way a
// person tunes their repo and a team lead sets an org rule (CLI_REFERENCE
// policy, logic/policy: lower levels may only tighten a higher one):
//   - the reverse path: show (default) → set (asks; --yes) → show (the
//     repo row) → set again (the same row) → unset → show (default again) →
//     unset again (E_NOT_FOUND with the show hint); --json at every step;
//   - bad input for every value shape (flag, whole number, range, choice,
//     profile list, operation list), an unknown key, a missing value, an
//     unknown --to place;
//   - the same answers over REST (`lore api`);
//   - an org level and a repo level: a looser repo value never wins.

import (
	"encoding/json"
	"strings"
	"testing"
)

// campPolicyWrite is `lore policy set|unset --json`.
type campPolicyWrite struct {
	ID    string `json:"id"`
	Store string `json:"store"`
}

func TestCampaignRender_PolicyReversePath(t *testing.T) {
	c := renderSolo(t)
	const key = "trash.retention_days"
	show := func() campPolicyRow {
		t.Helper()
		got := campJSON[campPolicyShow](t, c.Lore("policy", "show", key, "--json"))
		if got.Count != 1 || len(got.Data) != 1 || got.Data[0].Key != key || got.Data[0].Doc == "" {
			t.Fatalf("policy show %s --json: one documented entry, got %+v", key, got)
		}
		return got.Data[0]
	}
	if d := show(); d.Source != "default" || d.Value != "" || d.Row != "" || d.Level != "" {
		t.Fatalf("before any set: the default, no row: %+v", d)
	}
	c.MustLore("policy", "show", key).Says(t, key+"\t(unset)\tdefault")

	// A write asks; without a terminal and --yes it is E_NEEDS_CONFIRM.
	before := campTree(t, c.Dir)
	campRefusal(t, c, campExitConfirm, "E_NEEDS_CONFIRM", "--yes", "policy", "set", key, "365")
	campSameTree(t, "an unconfirmed policy set", before, campTree(t, c.Dir))

	w := campJSON[campPolicyWrite](t, c.Lore("policy", "set", key, "365", "--yes", "--json"))
	if !strings.HasPrefix(w.ID, "pol_") || w.Store != "repo" {
		t.Fatalf("policy set --json: the row id and store, got %+v", w)
	}
	if d := show(); d.Value != "365" || d.Level != "repo" || d.Row != w.ID || d.Store != "repo" {
		t.Errorf("after set: the repo row: %+v", d)
	}
	// Setting it again (same or new value) keeps one row.
	again := campJSON[campPolicyWrite](t, c.Lore("policy", "set", key, "30", "--yes", "--json"))
	if again.ID != w.ID {
		t.Errorf("a second set updates the same row: %s then %s", w.ID, again.ID)
	}
	if d := show(); d.Value != "30" {
		t.Errorf("the second value applies: %+v", d)
	}
	campRefusal(t, c, campExitConfirm, "E_NEEDS_CONFIRM", "--yes", "policy", "unset", key)
	u := campJSON[campPolicyWrite](t, c.Lore("policy", "unset", key, "--yes", "--json"))
	if u.ID != w.ID || u.Store != "repo" {
		t.Errorf("unset --json names the removed row: %+v, want %s", u, w.ID)
	}
	if d := show(); d.Source != "default" || d.Value != "" {
		t.Errorf("after unset: the default again: %+v", d)
	}
	campRefusal(t, c, campExitNotFound, "E_NOT_FOUND", "lore policy show --id "+key, "policy", "unset", key, "--yes")
	// The prefix form lists the family; an unknown prefix lists nothing.
	fam := campJSON[campPolicyShow](t, c.Lore("policy", "show", "trash", "--json"))
	if fam.Count != 2 || len(fam.Data) != 2 {
		t.Errorf("policy show trash: the two trash keys, got %+v", fam)
	}
	none := campJSON[campPolicyShow](t, c.Lore("policy", "show", "no.such.prefix", "--json"))
	if none.Count != 0 || none.Data == nil {
		t.Errorf("an unknown prefix: count 0 and an empty list (never null), got %s", c.Lore("policy", "show", "no.such.prefix", "--json").Stdout)
	}
}

func TestCampaignRender_PolicyBadInput(t *testing.T) {
	c := renderSolo(t)
	cases := []struct {
		name, key, value string
		hint             string
	}{
		{"unknown key", "no.such.key", "1", "lore policy show lists the keys"},
		{"flag", "delete.require_reason", "maybe", "true or false"},
		{"whole number", "trash.retention_days", "abc", "whole number"},
		{"range", "context.warn_at", "150", "from 0 to 100"},
		{"choice", "secrets.on_detect", "bogus", "refuse, ask, redact"},
		{"profile list", "context.targets", "codex,nope", "context.profiles"},
		{"unicode profile", "context.targets", "🚀", "context.profiles"},
		{"operation list", "agents.person_only", "trash.purge,nope.op", "noun.verb"},
		{"very long value", "secrets.on_detect", strings.Repeat("x", 10000), "refuse, ask, redact"},
	}
	before := campTree(t, c.Dir)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			campRefusal(t, c, campExitBadInput, "E_INVALID_INPUT", tc.hint, "policy", "set", tc.key, tc.value, "--yes")
		})
	}
	campRefusal(t, c, campExitBadInput, "E_INVALID_INPUT", "argument 2", "policy", "set", "trash.retention_days", "--yes")
	campRefusal(t, c, campExitNotFound, "E_NOT_FOUND", "", "policy", "set", "trash.retention_days", "5", "--to", "no-such-store", "--yes")
	campSameTree(t, "refused policy writes", before, campTree(t, c.Dir))
	// The negative number reaches the value check only after `--`.
	c.Lore("policy", "set", "--yes", "--", "trash.retention_days", "-3").Fails(t, campExitBadInput, "[E_INVALID_INPUT]", "hint: ", "whole number")
}

func TestCampaignRender_PolicyOverREST(t *testing.T) {
	c := renderSolo(t)
	srv := renderStartAPI(t, c)
	// Without yes the REST answer is the CLI's question.
	if r := srv.post(t, "/v1/policy/set", []byte(`{"key":"tasks.stale_days","value":"30"}`)); r.code != "E_NEEDS_CONFIRM" {
		t.Errorf("REST policy set without yes: E_NEEDS_CONFIRM, got %d %s", r.status, r.body)
	}
	r := srv.post(t, "/v1/policy/set", []byte(`{"key":"tasks.stale_days","value":"30","yes":true}`))
	var w campPolicyWrite
	if r.status != 200 || json.Unmarshal(r.body, &w) != nil || !strings.HasPrefix(w.ID, "pol_") {
		t.Fatalf("REST policy set: 200 with the row, got %d %s", r.status, r.body)
	}
	cli := campJSON[campPolicyShow](t, c.Lore("policy", "show", "tasks.stale_days", "--json"))
	if len(cli.Data) != 1 || cli.Data[0].Row != w.ID || cli.Data[0].Value != "30" {
		t.Errorf("the CLI sees the REST write: %+v", cli)
	}
	if bad := srv.post(t, "/v1/policy/set", []byte(`{"key":"tasks.stale_days","value":"abc","yes":true}`)); bad.status != 400 || bad.code != renderErrCode(c.Lore("policy", "set", "tasks.stale_days", "abc", "--yes")) {
		t.Errorf("REST and CLI refuse a bad value alike: %d %s", bad.status, bad.body)
	}
	un := srv.post(t, "/v1/policy/unset", []byte(`{"key":"tasks.stale_days","yes":true}`))
	if un.status != 200 || !strings.Contains(string(un.body), w.ID) {
		t.Errorf("REST policy unset names the row: %d %s", un.status, un.body)
	}
	if again := srv.post(t, "/v1/policy/unset", []byte(`{"key":"tasks.stale_days","yes":true}`)); again.code != "E_NOT_FOUND" {
		t.Errorf("REST unset twice: E_NOT_FOUND, got %d %s", again.status, again.body)
	}
}

func TestCampaignRender_PolicyOrgThenRepo(t *testing.T) {
	o := newOrgScen(t)
	const key = "delete.require_reason"
	o.app.MustLore("policy", "set", key, "true", "--to", orgRosterStore, "--yes")
	got := campJSON[campPolicyShow](t, o.app.Lore("policy", "show", key, "--json"))
	if len(got.Data) != 1 || got.Data[0].Value != "true" {
		t.Fatalf("the org value applies in the repo: %+v", got)
	}
	// A looser repo value is refused, names the level that set the rule
	// and the way out; nothing is written.
	before := campTree(t, o.app.Dir)
	campRefusal(t, o.app, campExitRefused, "E_POLICY_DENIED", "--to "+orgRosterStore, "policy", "set", key, "false", "--yes")
	o.app.Lore("policy", "set", key, "false", "--yes").Says(t, orgRosterStore+" sets true")
	campSameTree(t, "a refused looser policy set", before, campTree(t, o.app.Dir))
	after := campJSON[campPolicyShow](t, o.app.Lore("policy", "show", key, "--json"))
	if len(after.Data) != 1 || after.Data[0].Value != "true" || after.Data[0].Level == "repo" {
		t.Errorf("the org's rule still applies: %+v", after)
	}
	// As strict as the org is allowed at the repo; unsetting the org row
	// leaves the repo's own.
	o.app.MustLore("policy", "set", key, "true", "--yes")
	// delete.require_reason is on, so removing a policy row needs a
	// reason too: refused without one (the hint names --reason, which
	// policy unset takes), accepted with it.
	campRefusal(t, o.app, campExitBadInput, "E_REASON_REQUIRED", "--reason", "policy", "unset", key, "--to", orgRosterStore, "--yes")
	o.app.MustLore("policy", "unset", key, "--to", orgRosterStore, "--reason", "the repo keeps its own", "--yes")
	last := campJSON[campPolicyShow](t, o.app.Lore("policy", "show", key, "--json"))
	if len(last.Data) != 1 || last.Data[0].Value != "true" || last.Data[0].Level != "repo" {
		t.Errorf("after the org row goes, the repo row applies: %+v", last)
	}
}

func TestCampaignRender_PolicyTwoWritersAtOnce(t *testing.T) {
	w := newWorld(t)
	p := w.Human("person", "person@example.com")
	p.WriteConfig(renderSoloConfig())
	c := p.Clone(w.Remote(renderSlugOwn), "tool")
	c.MustLore("init", "--name=tool")
	res := w.Simultaneously(
		c.Prepare("policy", "set", "tasks.stale_days", "30", "--yes"),
		c.Prepare("policy", "set", "trash.retention_days", "90", "--yes"),
	)
	for _, r := range res {
		r.OK(t)
	}
	for key, want := range map[string]string{"tasks.stale_days": "30", "trash.retention_days": "90"} {
		got := campJSON[campPolicyShow](t, c.Lore("policy", "show", key, "--json"))
		if len(got.Data) != 1 || got.Data[0].Value != want || got.Data[0].Level != "repo" {
			t.Errorf("both concurrent writes land: %s = %+v, want %s", key, got.Data, want)
		}
	}
}
