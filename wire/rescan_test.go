package wire

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRescanRowsFindsOldRowsWithNewRules (SQ12): a row written before a
// rule existed is clean at write time; once the team adds the rule, the
// every-row re-scan refuses with E_LEAK_BLOCKED, names the row, field and
// rule with the masked preview (never the matched text) and the exact
// allow command; an allow entry for that finding makes the check clean.
func TestRescanRowsFindsOldRowsWithNewRules(t *testing.T) {
	ctx := background()
	a := openWith(t, gitRepo(t), Options{Home: t.TempDir()})
	wantClean(t, a, "empty repo")
	text := "deploy with " + acmeToken
	w, err := a.Create(ctx, store.SourceRepo, schema.TableProjects, textValues(t, map[string]string{"name": "hub", "origin_url": text}))
	if err != nil {
		t.Fatal(err) // no rule matches yet: the write is clean
	}
	wantClean(t, a, "before the rule")
	const rule = "acme-token"
	if _, err = a.Create(ctx, store.SourceRepo, schema.TableSecretRules, textValues(t, map[string]string{"rule_id": rule, "regex": `ACMETOK-[0-9]{12}`, "state": schema.SecretRuleOn})); err != nil {
		t.Fatal(err)
	}
	_, err = a.RescanRows(ctx)
	if errs.CodeOf(err) != errs.LeakBlocked {
		t.Fatalf("after the rule: want E_LEAK_BLOCKED, got %v", err)
	}
	id := w.Row.ID.String()
	for _, want := range []string{id, "origin_url", rule, "1 stored value", "--match-hash"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("report misses %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), acmeToken) {
		t.Fatal("the report prints the matched text")
	}

	sc, err := a.HookScanner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fs := sc.Scan("origin_url", text)
	if len(fs) != 1 {
		t.Fatalf("fixture: %d findings", len(fs))
	}
	vals := textValues(t, map[string]string{"row_id": id, "field": "origin_url", "rule_id": rule, "reason": "test fixture"})
	if vals["match_hash"], err = value.NumberOf(strconv.FormatUint(uint64(fs[0].MatchHash), 10)); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Create(ctx, store.SourceRepo, schema.TableSecretAllows, vals); err != nil {
		t.Fatal(err)
	}
	wantClean(t, a, "after the allow entry")
}

// wantClean fails unless the every-row re-scan of a is clean.
func wantClean(t *testing.T, a *App, stage string) {
	t.Helper()
	if msg, err := a.RescanRows(background()); err != nil || msg != rescanClean {
		t.Fatalf("%s: %q, %v", stage, msg, err)
	}
}
