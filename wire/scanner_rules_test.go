package wire

import (
	"testing"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// scannerRulesApp is an App with the personal store `me` and a team store
// `kb` attached, for the rule-merge tests below.
func scannerRulesApp(t *testing.T) *App {
	t.Helper()
	repo := gitRepo(t)
	home := t.TempDir()
	for _, n := range []string{opsstores.PersonalName, "kb"} {
		if _, err := run(t, openWith(t, repo, Options{Home: home}), opsstores.OpInit, `{"name":"`+n+`","url":"`+fileURL(t.TempDir())+`"}`, false); err != nil {
			t.Fatal(err)
		}
	}
	return openWith(t, repo, Options{Home: home})
}

// putRule writes one secret_rules row to src.
func putRule(t *testing.T, a *App, src store.Source, f map[string]string) {
	t.Helper()
	if _, err := a.Create(background(), src, schema.TableSecretRules, textValues(t, f)); err != nil {
		t.Fatal(err)
	}
}

// writeScanner is the engine's write scanner over every attached store
// (what wire/open.go builds).
func writeScanner(t *testing.T, a *App) *liveScanner {
	t.Helper()
	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	s, err := newScanner(background(), st.byName, withFolders(st.folders))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestScannerPersonalOffKeepsWriteRule (OV45-1, BATCH 8): a personal off
// never removes the rule from the write scanner; the engine's IsOff(own)
// decides, so a shared `lore api` write (someone else's) keeps the rule.
// A personal added rule still applies.
func TestScannerPersonalOffKeepsWriteRule(t *testing.T) {
	t.Parallel()
	a := scannerRulesApp(t)
	me := store.Source(opsstores.PersonalName)
	putRule(t, a, me, map[string]string{"rule_id": "github-pat", "state": schema.SecretRuleOff})
	putRule(t, a, me, map[string]string{"rule_id": "acme-token", "regex": `ACMETOK-[0-9]{12}`, "state": schema.SecretRuleOn})
	s := writeScanner(t, a)
	if !hits(s, "github-pat", ghToken) {
		t.Fatal("a personal off must not remove the rule from the write scanner")
	}
	if !hits(s, "acme-token", "x "+acmeToken) {
		t.Fatal("a personal added rule applies to writes")
	}
	own, err := a.OutputScanner(background())
	if err != nil {
		t.Fatal(err)
	}
	if hits(own, "github-pat", ghToken) {
		t.Fatal("the person's own output scanner honours the personal off")
	}
}

// TestScannerSameRuleTwoStores (OV45-2): the same rule id in two stores
// must not fail every write; off in both is off, and when one team store
// says off and another on, on wins in the scanner (the engine's IsOff
// still applies the off to writes).
func TestScannerSameRuleTwoStores(t *testing.T) {
	t.Parallel()
	a := scannerRulesApp(t)
	putRule(t, a, store.SourceRepo, map[string]string{"rule_id": "github-pat", "state": schema.SecretRuleOff})
	putRule(t, a, "kb", map[string]string{"rule_id": "github-pat", "state": schema.SecretRuleOff})
	putRule(t, a, store.SourceRepo, map[string]string{"rule_id": "aws-access-token", "state": schema.SecretRuleOff})
	putRule(t, a, "kb", map[string]string{"rule_id": "aws-access-token", "state": schema.SecretRuleOn})
	s := writeScanner(t, a)
	if hits(s, "github-pat", ghToken) {
		t.Fatal("a rule off in every team store is off")
	}
	awsKey := "AKIA" + "QX7Z" + "P2LM" + "N4RT" + "B6WY"
	if !hits(s, "aws-access-token", "key "+awsKey) {
		t.Fatal("on wins over off in the scanner when team stores disagree")
	}
	hs, err := a.HookScanner(background())
	if err != nil {
		t.Fatal(err)
	}
	if hits(hs, "github-pat", ghToken) {
		t.Fatal("hooks: a rule off in every team store is off")
	}
}
