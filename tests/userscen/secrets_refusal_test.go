//go:build userscen

package userscen

// Secret refusals through the real binary (docs/operations/secrets-and-pii.md
// Steps 1-2, Safety switches): every default rule stops a plain write with
// E_NEEDS_CONFIRM (exit 26), names the rule and the field, shows only a
// masked preview, writes nothing, and offers exactly the three answers;
// each answer stores what it promises; a bare --yes or a reasonless
// --not-a-secret is no answer; the value never reaches .lore/data, git
// objects on the remote, --json output or an error. The same scan guards
// every writer: memory, rule, decision, task, vars and metadata.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// secNextAnswers is the next.add_args_one_of every refusal carries, in the
// order the hint prints them.
var secNextAnswers = [][]string{
	{"--secret-ref", "<manager>://<entry>"},
	{"--secret-unstored"},
	{"--not-a-secret", "--reason", "<why>", "--yes"},
}

// Every default rule × plain memory add: refused, the rule named, the
// finding masked, nothing written, and the full value nowhere in the
// output (human form and --json both).
func TestSecretsRefusalEveryDefaultRule(t *testing.T) {
	s := newSecSolo(t)
	before := len(secDataFiles(t, s.c))
	for _, f := range secFixtures() {
		t.Run(f.rule, func(t *testing.T) {
			body := "staging note: " + f.value
			r := s.c.Lore("memory", "add", "--body", body)
			r.Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "looks like a secret ("+f.rule+")",
				"--secret-ref", "--secret-unstored", "--not-a-secret --reason")
			r.Never(t, f.value, secTail(f.value))

			j := s.c.Lore("memory", "add", "--body", body, "--json")
			e := secDecodeErr(t, j)
			if e.Error.Code != "E_NEEDS_CONFIRM" || e.Error.Exit != th.ExitNeedsConfirm || j.Code != th.ExitNeedsConfirm {
				t.Fatalf("json refusal code/exit: %+v (%s)", e.Error, j)
			}
			if len(e.Error.Findings) != 1 {
				t.Fatalf("one finding expected: %+v", e.Error.Findings)
			}
			fd := e.Error.Findings[0]
			if fd.Rule != f.rule || fd.Field != "body" || fd.Line != 1 || fd.N != 1 {
				t.Fatalf("finding: %+v", fd)
			}
			if !strings.HasSuffix(fd.Masked, "…") || len(fd.Masked) >= len(f.value) {
				t.Fatalf("masked preview must be a short prefix ending in …: %q", fd.Masked)
			}
			if len(e.Error.Next.AddArgsOneOf) != len(secNextAnswers) {
				t.Fatalf("next answers: %v", e.Error.Next.AddArgsOneOf)
			}
			for i, want := range secNextAnswers {
				if strings.Join(e.Error.Next.AddArgsOneOf[i], " ") != strings.Join(want, " ") {
					t.Fatalf("answer %d: %v want %v", i, e.Error.Next.AddArgsOneOf[i], want)
				}
			}
			j.Never(t, f.value, secTail(f.value))
		})
	}
	if after := len(secDataFiles(t, s.c)); after != before {
		t.Fatalf("a refused write wrote files: %d -> %d", before, after)
	}
}

// Every writer is guarded by the same scan: rule, decision, task title,
// a variable's value and a metadata value each meet the same question,
// with the field named.
func TestSecretsRefusalEveryWriter(t *testing.T) {
	s := newSecSolo(t)
	key := secAWSKey()
	task := secID(t, s.c.Lore("task", "add", "a task for metadata", "--json"))
	cases := []struct {
		name  string
		args  []string
		field string
	}{
		{"memory body", []string{"memory", "add", "--body", "k " + key}, "body"},
		{"rule body", []string{"rule", "add", "--body", "use " + key, "--to", "repo"}, "body"},
		{"decision", []string{"decision", "add", "--title", "keys", "--body", "use " + key}, "body"},
		{"task title", []string{"task", "add", "rotate " + key}, "title"},
		{"vars value", []string{"vars", "set", "deploy_key", key}, "value"},
		{"meta value", []string{"meta", "set", task, "vendor_key=" + key}, "meta.vendor_key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := s.c.Lore(c.args...)
			r.Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", c.field+" looks like a secret (aws_access_key)")
			r.Never(t, key)
		})
	}
	if hit := secDataHolding(t, s.c, key); len(hit) != 0 {
		t.Fatalf("refused writes left the key in .lore/data: %v", hit)
	}
	s.c.MustLore("vars", "list").Never(t, "deploy_key")
	s.c.MustLore("meta", "get", task).Never(t, "vendor_key")
}

// The three answers and the non-answers: --secret-ref keeps a reference,
// --secret-unstored keeps a placeholder and adds the store-then-rotate
// task, --not-a-secret --reason --yes keeps the text; a bare --yes, a
// --not-a-secret without --reason, a reason without --yes (no terminal)
// and a malformed --secret-ref are refused with their own next step.
func TestSecretsAnswers(t *testing.T) {
	s := newSecSolo(t)
	key := secAWSKey()
	body := "Deploy key for staging: " + key

	s.c.Lore("memory", "add", "--body", body, "--yes").
		Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "--secret-ref").Never(t, key)
	s.c.Lore("memory", "add", "--body", body, "--not-a-secret", "--yes").
		Fails(t, 20, "E_INVALID_INPUT", "--not-a-secret needs a typed reason", `--reason "<why it is not a secret>"`).Never(t, key)
	s.c.Lore("memory", "add", "--body", body, "--not-a-secret", "--reason", "docs sample").
		Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "keep it as not a secret (docs sample)", "rerun with --yes").Never(t, key)
	s.c.Lore("memory", "add", "--body", body, "--secret-ref", "://x").
		Fails(t, 20, "E_REF_SYNTAX", "is not manager://locator").Never(t, key)
	for _, bad := range []string{"notaref", "gopass:/x", ""} {
		s.c.Lore("memory", "add", "--body", body, "--secret-ref", bad).
			Fails(t, 20, "E_INVALID_INPUT", "--secret-ref needs <manager>://<locator>").Never(t, key)
	}
	if hit := secDataHolding(t, s.c, key); len(hit) != 0 {
		t.Fatalf("non-answers wrote the key: %v", hit)
	}

	// --secret-ref
	refID := secID(t, s.c.Lore("memory", "add", "--body", body, "--secret-ref", "gopass://acme/staging/deploy", "--json").Never(t, key))
	s.c.MustLore("show", refID).Says(t, "Deploy key for staging: ${secret:gopass://acme/staging/deploy}").Never(t, key)

	// --secret-unstored and its task
	unID := secID(t, s.c.Lore("memory", "add", "--body", body, "--secret-unstored", "--json").Never(t, key))
	s.c.MustLore("show", unID).Says(t, "Deploy key for staging: ${secret:unstored}").Never(t, key)
	s.c.MustLore("task", "list").Says(t, "Store the secret redacted from "+unID+", then rotate it")

	// --not-a-secret keeps the text as typed (docs Step 2).
	nsID := secID(t, s.c.Lore("memory", "add", "--body", body, "--not-a-secret", "--reason", "AWS documentation example key", "--yes", "--json"))
	s.c.MustLore("show", nsID).Says(t, key)

	// secrets list sees the reference and the placeholder, never the kept text.
	type ref struct {
		Ref     string `json:"ref"`
		Manager string `json:"manager"`
		Locator string `json:"locator"`
		Row     string `json:"row"`
		Table   string `json:"table"`
		Field   string `json:"field"`
	}
	type list struct {
		Count int   `json:"count"`
		Data  []ref `json:"data"`
	}
	l := th.DecodeJSON[list](t, s.c.Lore("secrets", "list", "--json"))
	if l.Count != 2 || len(l.Data) != 2 {
		t.Fatalf("two references expected: %+v", l)
	}
	byRow := map[string]ref{}
	for _, d := range l.Data {
		byRow[d.Row] = d
	}
	if g := byRow[refID]; g.Ref != "gopass://acme/staging/deploy" || g.Manager != "gopass" || g.Locator != "acme/staging/deploy" || g.Table != "memories" || g.Field != "body" {
		t.Fatalf("reference row: %+v", g)
	}
	if u := byRow[unID]; u.Ref != "unstored" || u.Field != "body" {
		t.Fatalf("unstored row: %+v", u)
	}
	un := th.DecodeJSON[list](t, s.c.Lore("secrets", "list", "--unstored", "--json"))
	if un.Count != 1 || un.Data[0].Row != unID {
		t.Fatalf("--unstored keeps only the placeholder: %+v", un)
	}
	gp := th.DecodeJSON[list](t, s.c.Lore("secrets", "list", "--manager", "gopass", "--json"))
	if gp.Count != 1 || gp.Data[0].Row != refID {
		t.Fatalf("--manager gopass keeps only the gopass reference: %+v", gp)
	}
	op := th.DecodeJSON[list](t, s.c.Lore("secrets", "list", "--manager", "op", "--json"))
	if op.Count != 0 {
		t.Fatalf("--manager op matches nothing: %+v", op)
	}

	// show --expand masks a reference; secrets show never reveals without --reveal.
	s.c.MustLore("memory", "show", refID, "--expand").Says(t, "Deploy key for staging: ••••••", "not revealed").Never(t, key)
	s.c.MustLore("secrets", "show", refID).Says(t, "gopass://acme/staging/deploy", "Not revealed (add --reveal)").Never(t, key)
}

// The value never reaches git: after the reference and the placeholder
// rows are committed and pushed, no object on the remote holds the key or
// its tail; the --json and human outputs of every read never print it.
func TestSecretsNeverInGitObjects(t *testing.T) {
	s := newSecSolo(t)
	keys := []string{}
	for i, f := range secFixtures() {
		ans := []string{"--secret-unstored"}
		if i%2 == 0 {
			ans = []string{"--secret-ref", "gopass://acme/" + f.rule}
		}
		s.c.MustLore(append([]string{"memory", "add", "--body", "note " + f.value}, ans...)...)
		keys = append(keys, f.value)
	}
	s.c.MustLore("vars", "set", "db_host", "db.example.com")
	s.c.CommitAll("lore rows")
	s.c.AdminPush("origin", "main")
	s.c.MustLore("sync")
	for _, k := range keys {
		if hit := secDataHolding(t, s.c, k); len(hit) != 0 {
			t.Fatalf("key in .lore/data: %v", hit)
		}
	}
	if found := s.r.AllObjectsContain(keys...); len(found) != 0 {
		t.Fatalf("remote git objects hold secret values: %d of them", len(found))
	}
	tails := make([]string, 0, len(keys))
	for _, k := range keys {
		tails = append(tails, secTail(k))
	}
	if found := s.r.AllObjectsContain(tails...); len(found) != 0 {
		t.Fatalf("remote git objects hold secret tails: %d of them", len(found))
	}
	for _, args := range [][]string{
		{"memory", "list"}, {"memory", "list", "--json"}, {"secrets", "list"}, {"secrets", "list", "--json"},
		{"task", "list"}, {"task", "list", "--json"}, {"search", "note"}, {"render"}, {"doctor"},
	} {
		s.c.Lore(args...).Never(t, keys...)
	}
}

// The commit hook checks every staged file again (SECRETS 19.4): a plain
// file holding a key and a .lore/data row file edited by hand to hold one
// are refused with E_LEAK_BLOCKED naming file, line and rule but never the
// value, and nothing is committed. A row kept with --not-a-secret carries
// its secret_allows entry, which the hook stages with the row and honours
// (only allow entries in the index count), so that commit passes.
func TestSecretsCommitHookChecksEveryFile(t *testing.T) {
	s := newSecSolo(t)
	key := secAWSKey()
	s.c.CommitAll("lore init")
	head := s.c.Head()

	s.c.WriteFile("README.md", "deploy key "+key+"\n")
	s.c.Git("add", "README.md")
	r := s.c.TryGit("commit", "-q", "-m", "readme with a key")
	if r.Code == 0 || !strings.Contains(r.Out(), "E_LEAK_BLOCKED") || !strings.Contains(r.Out(), "README.md:1") ||
		!strings.Contains(r.Out(), "rule aws_access_key") || !strings.Contains(r.Out(), "lore secrets allow") {
		t.Fatalf("the hook must refuse a plain file with a key, naming file:line, rule and the way out: %s", r)
	}
	if strings.Contains(r.Out(), key) {
		t.Fatalf("the refusal printed the key: %s", r)
	}
	if s.c.Head() != head {
		t.Fatal("a refused commit moved HEAD")
	}
	s.c.Git("rm", "-q", "--cached", "README.md")
	s.c.Git("clean", "-fq", "README.md")

	// A row file edited by hand: no allow entry covers it.
	id := secID(t, s.c.Lore("memory", "add", "--body", "Deploy key: "+key, "--secret-unstored", "--json"))
	files := secDataHolding(t, s.c, id)
	var rowFile string
	for _, f := range files {
		if strings.Contains(filepath.ToSlash(f), "/memories/") {
			rowFile = f
		}
	}
	if rowFile == "" {
		t.Fatalf("no row file for %s in %v", id, files)
	}
	b, err := os.ReadFile(rowFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rowFile, []byte(strings.Replace(string(b), "${secret:unstored}", key, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	s.c.Git("add", "-A")
	r = s.c.TryGit("commit", "-q", "-m", "hand-edited row")
	if r.Code == 0 || !strings.Contains(r.Out(), "E_LEAK_BLOCKED") || !strings.Contains(r.Out(), "(field body)") {
		t.Fatalf("the hook must refuse a hand-edited row file holding a key: %s", r)
	}
	if strings.Contains(r.Out(), key) || s.c.Head() != head {
		t.Fatalf("refusal printed the key or committed: %s", r)
	}
	if err := os.WriteFile(rowFile, b, 0o600); err != nil {
		t.Fatal(err)
	}

	// --not-a-secret: the allow entry rides along and the commit passes.
	ns := secID(t, s.c.Lore("memory", "add", "--body", "Sample key: "+key, "--not-a-secret", "--reason", "docs sample", "--yes", "--json"))
	s.c.Git("add", "-A")
	s.c.Git("commit", "-q", "-m", "row kept as not a secret")
	tree := s.c.Git("ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(tree, "secret_allows/") || !strings.Contains(tree, ns) {
		t.Fatalf("the kept row and its allow entry are committed together: %s", tree)
	}
}

// Custom rules (docs "Add your own patterns"): add asks first, then a
// later write is refused under the rule's id; a second add of the same id
// is refused with the edit command; a bad regex is refused; off needs a
// reason and lets the text through; on refuses again; the old pii-pattern
// verbs are refused naming their replacement; pii-pattern list is empty.
func TestSecretsCustomRuleLifecycle(t *testing.T) {
	s := newSecSolo(t)
	inv := "INV-" + "123456"
	s.c.Lore("secrets", "rule", "add", "invoice-number", "--regex", "INV-[0-9]{6}", "--description", "our invoice numbers").
		Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "Add secret rule invoice-number in repo?")
	s.c.MustLore("memory", "add", "--body", "Invoice "+inv+" before the rule")
	s.c.MustLore("secrets", "rule", "add", "invoice-number", "--regex", "INV-[0-9]{6}", "--description", "our invoice numbers", "--yes").
		Says(t, "invoice-number added in this repo")
	s.c.Lore("secrets", "rule", "add", "invoice-number", "--regex", "INV-[0-9]{6}", "--yes").
		Fails(t, 20, "E_INVALID_INPUT", "already has a row in this repo", "lore secrets rule edit invoice-number")
	s.c.Lore("secrets", "rule", "add", "bad-re", "--regex", "INV-[", "--yes").
		Fails(t, 20, "E_INVALID_INPUT", "--regex is not an RE2 pattern")

	type rule struct {
		RuleID      string `json:"rule_id"`
		State       string `json:"state"`
		Level       string `json:"level"`
		Store       string `json:"store"`
		Regex       string `json:"regex"`
		Description string `json:"description"`
	}
	type rules struct {
		Count int    `json:"count"`
		Data  []rule `json:"data"`
	}
	rs := th.DecodeJSON[rules](t, s.c.Lore("secrets", "rules", "--json"))
	if rs.Count != 1 || rs.Data[0].RuleID != "invoice-number" || rs.Data[0].State != "on" || rs.Data[0].Level != "repo" || rs.Data[0].Regex != "INV-[0-9]{6}" {
		t.Fatalf("rules after add: %+v", rs)
	}

	s.c.Lore("memory", "add", "--body", "Invoice "+inv+" is open").
		Fails(t, th.ExitNeedsConfirm, "looks like a secret (invoice-number)").Never(t, inv)
	s.c.Lore("secrets", "rule", "off", "invoice-number").Fails(t, 20, "E_INVALID_INPUT", "missing required field reason", "pass --reason <reason>")
	s.c.MustLore("secrets", "rule", "off", "invoice-number", "--reason", "test numbers only", "--yes").Says(t, "invoice-number off")
	rs = th.DecodeJSON[rules](t, s.c.Lore("secrets", "rules", "--json"))
	if rs.Count != 1 || rs.Data[0].State != "off" {
		t.Fatalf("rule off: %+v", rs)
	}
	s.c.MustLore("memory", "add", "--body", "Invoice "+inv+" with the rule off")
	s.c.MustLore("secrets", "rule", "on", "invoice-number", "--yes").Says(t, "invoice-number on")
	s.c.Lore("memory", "add", "--body", "Invoice INV-654321 is open").Fails(t, th.ExitNeedsConfirm, "(invoice-number)")

	for verb, repl := range map[string]string{"add": "lore secrets rule add", "enable": "lore secrets rule on", "disable": "lore secrets rule off"} {
		s.c.Lore("pii-pattern", verb).Fails(t, 20, "E_INVALID_INPUT", "lore pii-pattern "+verb+" was replaced by "+repl, "`"+repl+"`")
	}
	s.c.Lore("pii-pattern", "add", "--name", "x", "--regex", "y").Fails(t, 20, "was replaced by lore secrets rule add")
	// v0.1.17 users also typed the name positionally: same refusal (INTEG-QUESTIONS-10).
	s.c.Lore("pii-pattern", "add", "invoice", "--regex", "y").Fails(t, 20, "was replaced by lore secrets rule add")
	s.c.MustLore("pii-pattern", "list").Says(t, "(no patterns)")
}

// Ordinary text is not a secret: placeholders, a bare user@host, an
// all-letters password followed by prose, ${secret:…} references written
// by hand and ${var:…} references pass without a question. The commit
// hook agrees with the write scanner (INTEG-QUESTIONS-4): the rows and a
// plain file holding the same texts commit with no E_LEAK_BLOCKED, so a
// note lore wrote fine never blocks a commit.
func TestSecretsNoFalsePositivesOnPlainText(t *testing.T) {
	s := newSecSolo(t)
	bodies := []string{
		"The staging deploy key is in the team vault under staging/deploy",
		"connect with ssh deploy@db.example.com",
		"password=<from the vault>",
		"postgres://app:" + "${var:db_pass}" + "@db.example.com/app",
		"the token is ${secret:gopass://acme/hub/token}",
		"AKIA is the prefix of an AWS key id",
		"日本語のメモ: パスワードは保管庫にあります",
		strings.Repeat("long plain words ", 400),
	}
	for _, body := range bodies {
		s.c.MustLore("memory", "add", "--body", body)
	}
	s.c.WriteFile("notes.md", strings.Join(bodies, "\n")+"\n")
	s.c.Git("add", "-A")
	if r := s.c.TryGit("commit", "-q", "-m", "placeholders only"); r.Code != 0 {
		t.Fatalf("the commit hook must accept what the write scanner accepted: %s", r)
	}
}

// A default rule switched off and back on scans again (`lore secrets rule
// on` trashes the off row): after on, a key is refused on every writer,
// and `lore secrets rules` agrees with what the scanner does. Before the
// fix the scanner kept applying the trashed off row, so a raw key was
// stored and printed with no question while the rule list showed nothing.
func TestSecretsRuleOnAfterOffScansAgain(t *testing.T) {
	s := newSecSolo(t)
	key := secAWSKey()
	s.c.Lore("secrets", "rule", "off", "aws_access_key", "--reason", "sample keys only").
		Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
	s.c.MustLore("secrets", "rule", "off", "aws_access_key", "--reason", "sample keys only", "--yes").Says(t, "aws_access_key off in this repo")
	s.c.MustLore("secrets", "rules").Says(t, "aws_access_key", "off")
	s.c.MustLore("memory", "add", "--body", "while off: "+key)
	s.c.MustLore("secrets", "rule", "on", "aws_access_key", "--yes").Says(t, "aws_access_key on in this repo")
	s.c.MustLore("secrets", "rules").Never(t, "aws_access_key")
	s.c.Lore("memory", "add", "--body", "after on: "+key).
		Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "(aws_access_key)").Never(t, key)
	s.c.Lore("vars", "set", "deploy_key", key).
		Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "(aws_access_key)").Never(t, key)
	s.c.MustLore("vars", "list").Never(t, key)
	// Twice off/on keeps working.
	s.c.MustLore("secrets", "rule", "off", "aws_access_key", "--reason", "again", "--yes")
	s.c.MustLore("secrets", "rule", "on", "aws_access_key", "--yes")
	s.c.Lore("memory", "add", "--body", "after on again: "+key).Fails(t, th.ExitNeedsConfirm, "(aws_access_key)")
}
