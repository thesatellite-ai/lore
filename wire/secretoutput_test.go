package wire

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// leakFixtures are obviously fake secrets of several kinds, assembled at
// run time so the repo's own leak checks never see a literal. value is
// the sensitive part whose characters must never be printed; line is how
// it sits in a note.
type leakFixture struct{ name, value, line string }

func leakFixtures() []leakFixture {
	openai := "Q7wE9rT2yU4iO6pA8sD1fG3hJ5kL0zXc" + "Vb2Nm4Qw6Er8"
	pgPass := "Hx7Kq2" + "Lp9Zr4Wd"
	generic := "Zk3Qp9Lm2Xv8" + "Rt5Wn7Yb4Hc6Jd1Fg0Ks"
	aws := "AKIA" + "Q7WE9RT2YU4IO6PA"
	return []leakFixture{
		{"openai", openai, "key sk-" + "proj-" + openai}, // the format prefix is checked whole below
		{"postgres", pgPass, "dsn postgres" + "://appuser:" + pgPass + "@db.example.test:5432/app"},
		{"generic", generic, `api_key = "` + generic + `"`},
		{"aws", aws, "aws " + aws},
	}
}

// assertNoSecretChars fails when out holds any substring of length >= 4
// of a fixture value, except one the allowed text (file names, rule ids)
// holds itself.
func assertNoSecretChars(t *testing.T, what, out, allowed string, fx []leakFixture) {
	t.Helper()
	for _, f := range fx {
		for n := 4; n <= len(f.value); n++ {
			for i := 0; i+n <= len(f.value); i++ {
				sub := f.value[i : i+n]
				if strings.Contains(out, sub) && !strings.Contains(allowed, sub) {
					t.Fatalf("%s: output carries %d characters of the %s fixture at offset %d", what, n, f.name, i)
				}
			}
		}
	}
	if strings.Contains(out, "sk-"+"proj-") || strings.Contains(out, "appuser:") {
		t.Fatalf("%s: output carries a secret's format prefix", what)
	}
}

// TestHookOutputCarriesNoSecretChars (item 29): the commit hook's refusal
// (text and its JSON form) and the audit rows it writes name rule, file,
// line, field and a match hash, never any part of a matched value.
func TestHookOutputCarriesNoSecretChars(t *testing.T) {
	t.Parallel()
	fx := leakFixtures()
	a := scannerRulesApp(t)
	withProject(t, a)
	docs := map[string][]byte{}
	allowed := ""
	for _, f := range fx {
		p := "notes/" + f.name + ".txt"
		docs[p] = []byte("first line\n" + f.line + "\n")
		allowed += p + " "
	}
	var body strings.Builder
	for _, f := range fx {
		body.WriteString(f.line + "\\n")
	}
	row := ".lore/data/memories/mem_0190f0f0f0f07000800000000000beef.json"
	docs[row] = []byte(`{"_table":"memories","_v":1,"id":"mem_0190f0f0f0f07000800000000000beef","body":"` + body.String() + `"}` + "\n")
	allowed += row + " "
	err := a.HookSecrets(background(), a.opts.RepoDir, LeakWhereCommit, docs)
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("err = %v, want E_LEAK_BLOCKED", err)
	}
	for _, f := range fx {
		if !strings.Contains(err.Error(), "notes/"+f.name+".txt") {
			t.Fatalf("the %s fixture was not reported: %v", f.name, err)
		}
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatal("not an *errs.Error")
	}
	js, jerr := json.Marshal(e)
	if jerr != nil {
		t.Fatal(jerr)
	}
	rules := "aws_access_key openai generic-api-key postgres url_with_password"
	assertNoSecretChars(t, "hook text", err.Error()+" "+e.Hint, allowed+rules, fx)
	assertNoSecretChars(t, "hook json", string(js), allowed+rules, fx)
	if audit, rerr := os.ReadFile(filepath.Join(a.opts.RepoDir, filepath.FromSlash(AuditFile))); rerr == nil {
		assertNoSecretChars(t, "hook audit", string(audit), allowed+rules, fx)
	}
	if !strings.Contains(err.Error(), "match sha256:") {
		t.Fatalf("refusal names no match hash: %v", err)
	}
}

// TestWriteRefusalCarriesNoSecretChars (item 29): a write the scanner
// refuses (on_detect refuse) and one it asks about (ask, the masked
// findings of E_NEEDS_CONFIRM) print no part of a matched value, in the
// error text or its JSON form.
func TestWriteRefusalCarriesNoSecretChars(t *testing.T) {
	t.Parallel()
	fx := leakFixtures()
	lines := make([]string, 0, len(fx))
	for _, f := range fx {
		lines = append(lines, f.line)
	}
	input, err := json.Marshal(map[string]string{"body": strings.Join(lines, "\n")})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"refuse", "ask"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			a := scannerRulesApp(t)
			withProject(t, a)
			addPolicy(t, a, policy.KeyOnDetect, `"`+mode+`"`)
			e, ok := a.Registry().Lookup(opsmemory.OpAdd)
			if !ok {
				t.Fatal("no memory.add")
			}
			_, werr := a.Run(background(), e, op.JSONDecoder{Body: input}, op.Caller{})
			if !errs.Has(werr, errs.SecretDetected) && !errs.Has(werr, errs.NeedsConfirm) {
				t.Fatalf("err = %v, want a secret refusal", werr)
			}
			var le *errs.Error
			if !errors.As(werr, &le) {
				t.Fatal("not an *errs.Error")
			}
			if mode == "ask" && len(le.Findings) < len(fx) {
				t.Fatalf("findings = %d, want >= %d", len(le.Findings), len(fx))
			}
			js, jerr := json.Marshal(le)
			if jerr != nil {
				t.Fatal(jerr)
			}
			allowed := "body aws_access_key openai generic-api-key postgres url_with_password"
			assertNoSecretChars(t, mode+" text", werr.Error()+" "+le.Hint, allowed, fx)
			assertNoSecretChars(t, mode+" json", string(js), allowed, fx)
		})
	}
}
