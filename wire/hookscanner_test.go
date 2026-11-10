package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// ghToken matches the default github-pat rule; built at run time so no
// key-shaped literal sits in the source.
var ghToken = "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "iJ9kLm"

// hits reports whether sc finds rule in text.
func hits(sc scan.Scanner, rule, text string) bool {
	for _, f := range sc.Scan("body", text) {
		if f.RuleID == rule {
			return true
		}
	}
	return false
}

// TestHookScannerKeepsTeamRules (BATCH 8): a rule turned off in the
// personal store `me` still fires in HookScanner (the git hooks, export,
// lore check leaks) while OutputScanner drops it; a team (repo) off does
// switch it off in HookScanner; a rule the personal store adds still
// applies to the hooks.
func TestHookScannerKeepsTeamRules(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	home := t.TempDir()
	if _, err := run(t, openWith(t, repo, Options{Home: home}), opsstores.OpInit, `{"name":"me","url":"`+fileURL(t.TempDir())+`"}`, false); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, repo, Options{Home: home}) // a fresh App sees `me`
	const rule = "github-pat"
	sc, err := a.HookScanner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hits(sc, rule, ghToken) {
		t.Fatalf("fixture: %s does not match the default rule", rule)
	}
	me := store.Source(opsstores.PersonalName)
	for _, f := range []map[string]string{
		{"rule_id": rule, "state": schema.SecretRuleOff},
		{"rule_id": "acme-token", "regex": `ACMETOK-[0-9]{12}`, "state": schema.SecretRuleOn},
	} {
		if _, err = a.Create(ctx, me, schema.TableSecretRules, textValues(t, f)); err != nil {
			t.Fatal(err)
		}
	}
	if sc, err = a.HookScanner(ctx); err != nil {
		t.Fatal(err)
	}
	if !hits(sc, rule, ghToken) {
		t.Fatal("a personal rule-off must not switch the rule off in the hooks")
	}
	if !hits(sc, "acme-token", "x "+acmeToken) {
		t.Fatal("a rule the personal store adds applies to the hooks too")
	}
	out, err := a.OutputScanner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hits(out, rule, ghToken) {
		t.Fatal("fixture: the personal off applies to the person's own scanner")
	}
	if _, err = a.Create(ctx, store.SourceRepo, schema.TableSecretRules, textValues(t, map[string]string{"rule_id": rule, "state": schema.SecretRuleOff})); err != nil {
		t.Fatal(err)
	}
	if sc, err = a.HookScanner(ctx); err != nil {
		t.Fatal(err)
	}
	if hits(sc, rule, ghToken) {
		t.Fatal("a team rule-off switches the rule off in the hooks")
	}
}
