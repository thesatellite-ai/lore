package wire_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// stageFile writes rel under the repo and stages it.
func stageFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	p := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	allowGit(t, repo, "add", "--", rel)
}

// madeUpPass is a real-looking password built at run time, so no URL with
// a password literal sits in the repo.
func madeUpPass() string { return "Zq7" + strings.Repeat("x9K", 4) }

// TestCommitCheckPlaceholderPasses (user report, part a): a staged file
// whose URL or password field holds only a placeholder (${X}, $X, {{ x }},
// <x>, ***) passes the commit check; a real-looking password in the same
// URL is still refused. Break-verified: with isPlaceholder always false the
// quoted password fields are refused.
func TestCommitCheckPlaceholderPasses(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	pw := "pass" + "word" // joined at run time (CLAUDE.md convention 8)
	clean := "DATABASE_URL=postgres://app:${DB_PASSWORD}@db/app\n" +
		"CACHE_URL=redis://app:$CACHE_PASS@cache/0\n" +
		pw + `: "${DB_PASSWORD}"` + "\n" +
		pw + `: "{{ .Values.db.pass }}"` + "\n" +
		pw + `: "<your-db-pass>"` + "\n" +
		pw + `: "********"` + "\n"
	stageFile(t, h.Repo, "deploy/app.env", clean)
	if err := h.App.HookSecrets(ctx, h.Repo, commitWhere, map[string][]byte{"deploy/app.env": []byte(clean)}); err != nil {
		t.Fatalf("placeholders: err = %v, want pass", err)
	}
	real := "DATABASE_URL=postgres://app:" + madeUpPass() + "@db/app\n"
	err := h.App.HookSecrets(ctx, h.Repo, commitWhere, map[string][]byte{"deploy/app.env": []byte(real)})
	if !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "rule url_with_password") {
		t.Fatalf("real password in the URL: err = %v, want E_LEAK_BLOCKED by url_with_password", err)
	}
}

// TestCommitCheckHonoursGitleaksIgnore (user report, part b; D17): a
// staged .gitleaksignore fingerprint in gitleaks' staged form
// (<path>:<rule>:<line>), naming lore's rule or the gitleaks rule it
// covers, lets that one finding through; a commit-pinned line
// (<commit>:<path>:<rule>:<line>) alone does not, a finding on another
// line is still refused, and an unstaged .gitleaksignore counts for
// nothing. Break-verified: with gitleaksIgnore.drop returning every hit
// the ignored commit is refused; with commit-pinned lines kept the
// commit-form case passes.
func TestCommitCheckHonoursGitleaksIgnore(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time
	tok := "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "Jk9LmN"
	conf := "name: app\naws: " + key + "\n"
	notes := "token = " + tok + "\n"
	stageFile(t, h.Repo, "conf/app.yaml", conf)
	stageFile(t, h.Repo, "notes.md", notes)
	docs := map[string][]byte{"conf/app.yaml": []byte(conf), "notes.md": []byte(notes)}
	writeIgnore := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(h.Repo, ".gitleaksignore"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pinned := "# accepted findings\n" +
		"conf/app.yaml:aws_access_key:2\n" +
		"0123456789abcdef0123456789abcdef01234567:notes.md:github-pat:1\n"
	writeIgnore(pinned)
	if err := h.App.HookSecrets(ctx, h.Repo, commitWhere, docs); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("unstaged .gitleaksignore: err = %v, want E_LEAK_BLOCKED", err)
	}
	allowGit(t, h.Repo, "add", ".gitleaksignore")
	err := h.App.HookSecrets(ctx, h.Repo, commitWhere, docs)
	if !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "notes.md:1") || strings.Contains(err.Error(), "conf/app.yaml:2") {
		t.Fatalf("commit-pinned line only: err = %v, want only notes.md:1 refused", err)
	}
	writeIgnore(pinned + "notes.md:github-pat:1\n")
	allowGit(t, h.Repo, "add", ".gitleaksignore")
	if err := h.App.HookSecrets(ctx, h.Repo, commitWhere, docs); err != nil {
		t.Fatalf("ignored findings: err = %v, want pass", err)
	}
	moved := "aws: " + key + "\n" // the same key on line 1: not the fingerprint
	err = h.App.HookSecrets(ctx, h.Repo, commitWhere, map[string][]byte{"conf/app.yaml": []byte(moved)})
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("finding on another line: err = %v, want E_LEAK_BLOCKED", err)
	}
}

// TestRefusalNamesAllowLine (user report, part c): a refused commit names,
// under each finding, the exact `lore secrets allow --file … --rule …
// --match-hash … --reason "<why>"` that accepts it, and the hint points at
// that line rather than a row-id template; running the printed line (with
// a reason) lets the commit through. Break-verified: with allowLineOf
// returning "" the line is missing.
func TestRefusalNamesAllowLine(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	body := "DATABASE_URL=postgres://app:" + madeUpPass() + "@db/app\n"
	stageFile(t, h.Repo, "deploy/app.env", body)
	docs := map[string][]byte{"deploy/app.env": []byte(body)}
	err := h.App.HookSecrets(ctx, h.Repo, commitWhere, docs)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.LeakBlocked {
		t.Fatalf("err = %v, want E_LEAK_BLOCKED", err)
	}
	m := regexp.MustCompile(`not a secret\? lore secrets allow --file deploy/app\.env --rule url_with_password --match-hash (sha256:[0-9a-f]+) --reason "<why>"`).FindStringSubmatch(e.Error())
	if m == nil {
		t.Fatalf("no ready allow line:\n%v", e)
	}
	if strings.Contains(e.Hint, "<row id>") || !strings.Contains(e.Hint, "not a secret?") {
		t.Fatalf("hint does not point at the printed line: %s", e.Hint)
	}
	out := h.MustYes("secrets.allow", `{"file":"`+filepath.Join(h.Repo, "deploy/app.env")+`","rule":"url_with_password","match_hash":"`+m[1]+`","reason":"sample URL"}`)
	allowGit(t, h.Repo, "add", "--", allowRowFile(t, h.Repo, "secret_allows", h.ID(out)))
	if err := h.App.HookSecrets(ctx, h.Repo, commitWhere, docs); err != nil {
		t.Fatalf("after the printed allow line: err = %v, want pass", err)
	}
}
