package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// entry is one rule row at a level, for resolveRules.
func entry(src store.Source, level policy.Level, id, state, regex string) ruleEntry {
	return ruleEntry{store: src, level: level, row: gitleaks.RuleRow{RuleID: id, State: state, Regex: regex}}
}

// TestResolveRulesMatrix (OV45, D9, BATCH 8): one row per rule id; the
// nearest team row that keeps the rule wins (a nearer "changed" beats a
// farther one, whatever the store names); on beats off among team rows;
// all-off is off; a personal off counts only in the person's own scanner;
// a personal change of a default never reaches the shared scanner, a
// personal ADDED rule does; same-level rows tie-break by store name.
func TestResolveRulesMatrix(t *testing.T) {
	base := []scan.Rule{{ID: "aws-access-token"}, {ID: "github-pat"}}
	const on, off, changed = schema.SecretRuleOn, schema.SecretRuleOff, schema.SecretRuleChanged
	cases := []struct {
		name    string
		entries []ruleEntry
		own     bool
		want    map[string]string // rule id → "state/regex"; absent = no row
	}{
		{name: "nearer changed beats farther changed", entries: []ruleEntry{
			entry("zz", policy.LevelOrg, "aws-access-token", changed, "FAR"),
			entry("aa", policy.LevelFolder, "aws-access-token", changed, "NEAR"),
		}, want: map[string]string{"aws-access-token": changed + "/NEAR"}},
		{name: "repo beats store root", entries: []ruleEntry{
			entry("kb", policy.LevelStoreRoot, "github-pat", changed, "ROOT"),
			entry(store.SourceRepo, policy.LevelRepo, "github-pat", changed, "REPO"),
		}, want: map[string]string{"github-pat": changed + "/REPO"}},
		{name: "nearer off, farther changed: on wins", entries: []ruleEntry{
			entry(store.SourceRepo, policy.LevelRepo, "github-pat", off, ""),
			entry("kb", policy.LevelOrg, "github-pat", changed, "ORG"),
		}, want: map[string]string{"github-pat": changed + "/ORG"}},
		{name: "all team off", entries: []ruleEntry{
			entry(store.SourceRepo, policy.LevelRepo, "github-pat", off, ""),
			entry("kb", policy.LevelOrg, "github-pat", off, ""),
		}, want: map[string]string{"github-pat": off + "/"}},
		{name: "same level ties by store name", entries: []ruleEntry{
			entry("kb2", policy.LevelStoreRoot, "github-pat", changed, "B"),
			entry("kb1", policy.LevelStoreRoot, "github-pat", changed, "A"),
		}, want: map[string]string{"github-pat": changed + "/A"}},
		{name: "personal off, shared scanner", entries: []ruleEntry{
			entry("me", policy.LevelPersonal, "github-pat", off, ""),
		}, want: map[string]string{}},
		{name: "personal off, own scanner", own: true, entries: []ruleEntry{
			entry("me", policy.LevelPersonal, "github-pat", off, ""),
			entry("kb", policy.LevelOrg, "github-pat", on, ""),
		}, want: map[string]string{"github-pat": off + "/"}},
		{name: "personal change of a default, shared", entries: []ruleEntry{
			entry("me", policy.LevelPersonal, "github-pat", changed, "MINE"),
		}, want: map[string]string{}},
		{name: "personal change of a default, own", own: true, entries: []ruleEntry{
			entry("me", policy.LevelPersonal, "github-pat", changed, "MINE"),
		}, want: map[string]string{"github-pat": changed + "/MINE"}},
		{name: "personal added rule reaches shared", entries: []ruleEntry{
			entry("me", policy.LevelPersonal, "acme", on, "ACME-[0-9]+"),
		}, want: map[string]string{"acme": on + "/ACME-[0-9]+"}},
		{name: "personal added rule without regex", entries: []ruleEntry{
			entry("me", policy.LevelPersonal, "acme", on, ""),
		}, want: map[string]string{}},
		{name: "team row beats personal row, shared", entries: []ruleEntry{
			entry("me", policy.LevelPersonal, "acme", on, "MINE"),
			entry("kb", policy.LevelOrg, "acme", on, "TEAM"),
		}, want: map[string]string{"acme": on + "/TEAM"}},
		{name: "no rows", want: map[string]string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]string{}
			for _, r := range resolveRules(c.entries, base, c.own) {
				if _, dup := got[r.RuleID]; dup {
					t.Fatalf("two rows for %s", r.RuleID)
				}
				got[r.RuleID] = r.State + "/" + r.Regex
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

// TestPersonalOffOnlyCoversOwnWrites (OV45, BATCH 8): with the person's
// personal store turning github-pat off, the person's own write passes,
// while a `lore api` write stamped as someone else (a shared server) is
// still refused for the same text.
func TestPersonalOffOnlyCoversOwnWrites(t *testing.T) {
	a := scannerRulesApp(t)
	withProject(t, a)
	// both rules that match a GitHub token: gitleaks' and lore's own
	for _, id := range []string{"github-pat", "github_token"} {
		putRule(t, a, store.Source(opsstores.PersonalName), map[string]string{"rule_id": id, "state": schema.SecretRuleOff})
	}
	addPolicy(t, a, policy.KeyOnDetect, `"refuse"`)
	e, ok := a.Registry().Lookup(opsmemory.OpAdd)
	if !ok {
		t.Fatal("no memory.add")
	}
	body := []byte(`{"body":"token ` + ghToken + `"}`)
	other, err := ids.Parse[ids.Actor](otherActor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Run(background(), e, op.JSONDecoder{Body: body}, op.Caller{Actor: other}); !errs.Has(err, errs.SecretDetected) {
		t.Fatalf("someone else's write must keep the rule: %v", err)
	}
	if _, err = a.Run(background(), e, op.JSONDecoder{Body: body}, op.Caller{}); err != nil {
		t.Fatalf("the person's own write honours the personal off: %v", err)
	}
}
