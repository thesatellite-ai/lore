package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// acmeToken is a value only the custom rule of these tests matches; built
// at run time so no key-shaped literal sits in the source.
var acmeToken = "ACMETOK-" + strings.Repeat("7", 12)

// addRule writes a custom secret_rules row through a's write path.
func addRule(t *testing.T, a *App) {
	t.Helper()
	fields := textValues(t, map[string]string{"rule_id": "acme-token", "regex": `ACMETOK-[0-9]{12}`, "state": schema.SecretRuleOn})
	if _, err := a.Create(background(), store.SourceRepo, schema.TableSecretRules, fields); err != nil {
		t.Fatal(err)
	}
}

// addPolicy writes a policies row through a's write path.
func addPolicy(t *testing.T, a *App, key policy.Key, raw string) {
	t.Helper()
	v, err := value.JSONOf([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	fields := textValues(t, map[string]string{"key": string(key), "scope": "repo"})
	fields["value"] = v
	if _, err = a.Create(background(), store.SourceRepo, schema.TablePolicies, fields); err != nil {
		t.Fatal(err)
	}
}

// TestSettingsAddedWhileTheAppRunsApplyToTheNextWrite: a long-lived App
// (`lore api`) applies settings rows added after it opened to its very
// next write, whether they were written through the App itself or by
// another process on the same repo (a `lore secrets rule add` in a
// terminal): a new custom secret rule makes the next matching write ask
// (default on_detect), and a secrets.on_detect=refuse policy row then makes
// it refuse; a later rule-off row lets it through again.
func TestSettingsAddedWhileTheAppRunsApplyToTheNextWrite(t *testing.T) {
	for _, via := range []string{"same App", "another process"} {
		t.Run(via, func(t *testing.T) {
			repo := gitRepo(t)
			long := newTestApp(t, repo)
			withProject(t, long)
			writer := long
			if via == "another process" {
				writer = newTestApp(t, repo)
			}
			body := `{"body":"deploy with ` + acmeToken + `"}`
			if _, err := run(t, long, opsmemory.OpAdd, body, false); err != nil {
				t.Fatalf("before the rule exists the value is plain text: %v", err)
			}
			addRule(t, writer)
			_, err := run(t, long, opsmemory.OpAdd, body, false)
			if !errs.Has(err, errs.NeedsConfirm) || strings.Contains(err.Error(), acmeToken) {
				t.Fatalf("a rule added while the App runs must apply to the next write: %v", err)
			}
			addPolicy(t, writer, policy.KeyOnDetect, `"refuse"`)
			if _, err = run(t, long, opsmemory.OpAdd, body, true); !errs.Has(err, errs.SecretDetected) {
				t.Fatalf("a refuse policy added while the App runs must apply to the next write: %v", err)
			}
			off := textValues(t, map[string]string{"rule_id": "acme-token", "state": schema.SecretRuleOff})
			eng, err := writer.Engine(background())
			if err != nil {
				t.Fatal(err)
			}
			rules, err := eng.List(background(), store.SourceRepo, schema.TableSecretRules)
			if err != nil || len(rules) != 1 {
				t.Fatalf("rules %v %v", rules, err)
			}
			if _, err = writer.Update(background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableSecretRules, ID: rules[0].ID}, map[string]value.Value{"state": off["state"], "regex": value.Null{}}); err != nil {
				t.Fatal(err)
			}
			if _, err = run(t, long, opsmemory.OpAdd, body, false); err != nil {
				t.Fatalf("a rule turned off while the App runs must stop applying: %v", err)
			}
		})
	}
}

// TestClientRoutesReachTheWriteHook (CFG8, spec 9.2): routing.client_repos
// is applied on the App's write path (App.Create, which CLI and REST both
// reach through Run), not by one surface: the DefaultTarget hook of a
// create naming no target sees the effective routes of the policies rows
// at that moment (a row added after open included), and the row lands in
// the store the hook answers. A create naming its target skips the hook.
func TestClientRoutesReachTheWriteHook(t *testing.T) {
	repo := gitRepo(t)
	var seen []map[string]string
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: repo, Home: t.TempDir(), Actor: actor, DefaultTarget: func(_ context.Context, q WriteQuery) (store.Source, error) {
		seen = append(seen, q.Routes)
		return "", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	withProject(t, a)
	if _, err = run(t, a, opsmemory.OpAdd, `{"body":"before"}`, false); err != nil {
		t.Fatal(err)
	}
	addPolicy(t, a, policy.KeyClientRoutes, `["memory=northwind","memory=repo","task=repo"]`)
	if _, err = run(t, a, opsmemory.OpAdd, `{"body":"after"}`, false); err != nil {
		t.Fatal(err)
	}
	n := len(seen)
	if _, err = run(t, a, opsmemory.OpAdd, `{"body":"explicit","to":"repo"}`, false); err != nil {
		t.Fatal(err)
	}
	if len(seen) < 2 || len(seen[0]) != 0 {
		t.Fatalf("hook calls %v", seen)
	}
	last := seen[n-1]
	if last["memory"] != "northwind" || last["task"] != policy.ClientRouteRepo {
		t.Fatalf("the hook must see the effective routes (a store beats repo): %v", last)
	}
	if len(seen) != n {
		t.Fatalf("a create naming its target must not ask the hook: %v", seen)
	}
}
