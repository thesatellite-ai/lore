package wire

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// piiFile is a v0.1.17 ~/.lore/pii-patterns.txt: comments and blank lines
// are skipped, each other line is one pattern; the rule id is "user:" +
// the line number (v0.1.17 scrub.go naming).
const piiFile = "# my patterns\nACME-[0-9]{6}\n\n  emp_[a-z]{4}  \n"

// piiRules lists the personal store's secret rules as rule_id → regex.
func piiRules(t *testing.T, a *App) map[string]string {
	t.Helper()
	rows, err := a.List(background(), store.Source(opsstores.PersonalName), schema.TableSecretRules)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		if r.Fields["state"].String() != schema.SecretRuleOn {
			t.Errorf("rule %v is not on", r.Fields)
		}
		out[r.Fields["rule_id"].String()] = r.Fields["regex"].String()
	}
	return out
}

// TestMigratePIIPatterns (OV4, SECRETS 3.3): migrate run converts
// ~/.lore/pii-patterns.txt into personal secret_rules rows "user:<line>"
// (state on); --dry-run lists them and writes nothing; the run keeps the
// file as pii-patterns.txt.migrated; a second run (file back) adds only
// ids the store lacks; with no personal store the file is kept and the
// step says so; a pattern that does not compile is E_CONFIG_INVALID
// naming the line but never the pattern, and nothing is written.
func TestMigratePIIPatterns(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	home := t.TempDir()
	path := filepath.Join(home, piiPatternsFile)
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	steps := func(out string) []string { return migrateOut(t, out).Steps }

	// no personal store: kept, nothing written
	write(piiFile)
	a := openWith(t, repo, Options{Home: home})
	withProject(t, a)
	out, err := run(t, a, opsmigrate.OpRun, `{}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(steps(out), func(s string) bool { return strings.Contains(s, "kept: no personal store") }) {
		t.Fatalf("no personal store: %s", out)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("the file was moved with no store to receive it: %v", err)
	}

	if _, err = run(t, a, opsstores.OpInit, `{"name":"me","url":"`+fileURL(t.TempDir())+`"}`, false); err != nil {
		t.Fatal(err)
	}
	b := openWith(t, repo, Options{Home: home})

	out, err = run(t, b, opsmigrate.OpRun, `{"dry_run":true}`, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"user:2", "user:4"} {
		if !slices.ContainsFunc(steps(out), func(s string) bool { return strings.Contains(s, "rule "+id+" ") }) {
			t.Fatalf("dry run does not list %s: %s", id, out)
		}
	}
	if got := piiRules(t, b); len(got) != 0 {
		t.Fatalf("a dry run wrote rules: %v", got)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("a dry run moved the file")
	}

	if _, err = run(t, b, opsmigrate.OpRun, `{}`, true); err != nil {
		t.Fatal(err)
	}
	got := piiRules(t, b)
	if len(got) != 2 || got["user:2"] != "ACME-[0-9]{6}" || got["user:4"] != "emp_[a-z]{4}" {
		t.Fatalf("rules after run: %v", got)
	}
	if kept := string(mustRead(t, path+migratedSuffix)); kept != piiFile {
		t.Fatalf("kept file = %q", kept)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the original file is still in place")
	}

	// the file comes back with one new line: only the new id is added
	write(piiFile + "tok_[0-9]{8}\n")
	if _, err = run(t, b, opsmigrate.OpRun, `{}`, true); err != nil {
		t.Fatal(err)
	}
	if got = piiRules(t, b); len(got) != 3 || got["user:5"] != "tok_[0-9]{8}" {
		t.Fatalf("rules after a second run: %v", got)
	}

	// a broken pattern: refused naming the line, the pattern never echoed
	const brokenPattern = "acct_([unclosed"
	write("ok_[0-9]+\n" + brokenPattern + "\n")
	_, err = run(t, b, opsmigrate.OpRun, `{}`, true)
	if !errs.Has(err, errs.ConfigInvalid) || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), brokenPattern) {
		t.Fatalf("broken pattern: %v", err)
	}
	if got = piiRules(t, b); len(got) != 3 {
		t.Fatalf("a refused file wrote rules: %v", got)
	}
}
