package wire_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestSecretsAllowFile (owner item): a finding in an ordinary repo file is
// allowed by `secrets allow --file` (path + rule + hash, the fingerprint a
// refusal prints) and the commit check honours the staged entry for that
// file only; another value in the same file, or the same value in another
// file, still blocks. Before any commit (no HEAD) the check runs.
// Break-verified: with fileAllowed returning every finding the allowed case
// fails; with it dropping every finding the "still blocks" cases fail.
func TestSecretsAllowFile(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time
	other := "AKIA" + strings.Repeat("Z", 16)
	cfg := filepath.Join(h.Repo, "conf", "app.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("aws: "+key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	allowGit(t, h.Repo, "add", "conf/app.yaml")
	check := func(docs map[string][]byte) error {
		return h.App.HookSecrets(ctx, h.Repo, commitWhere, docs)
	}
	first := map[string][]byte{"conf/app.yaml": []byte("aws: " + key + "\n")}
	err := check(first) // no HEAD yet: a first commit is checked
	if !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "lore secrets allow --file conf/app.yaml --rule aws_access_key --match-hash "+scan.Mask(key)) {
		t.Fatalf("first commit: err = %v, want E_LEAK_BLOCKED with the ready allow line", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatal("the refusal shows the value")
	}

	out := h.MustYes("secrets.allow", `{"file":"`+cfg+`","rule":"aws_access_key","match_hash":"`+scan.Mask(key)+`","reason":"sample key in a fixture"}`)
	allowGit(t, h.Repo, "add", "--", allowRowFile(t, h.Repo, "secret_allows", h.ID(out)))
	if err := check(first); err != nil {
		t.Fatalf("allowed file finding: err = %v, want pass", err)
	}
	still := map[string]map[string][]byte{
		"another value in that file": {"conf/app.yaml": []byte("aws: " + key + "\nold: " + other + "\n")},
		"that value in another file": {"conf/other.yaml": []byte("aws: " + key + "\n")},
	}
	for name, docs := range still {
		if err := check(docs); !errs.Has(err, errs.LeakBlocked) {
			t.Errorf("%s: err = %v, want E_LEAK_BLOCKED", name, err)
		}
	}
	got := h.Fails("secrets.allow", `{"row_id":"app.yaml","rule":"aws_access_key","match_hash":"1","reason":"x"}`, errs.InvalidID)
	if !strings.Contains(got, "--file <path> --rule <rule> --match-hash") {
		t.Fatalf("a non-row id does not name the --file form: %s", got)
	}
	h.Fails("secrets.allow", `{"file":"../outside.yaml","rule":"aws_access_key","match_hash":"1","reason":"x"}`, errs.InvalidInput)
}

// TestHookScannerFailureNamesCause: a repo .gitleaks.toml lore cannot read
// refuses the check with the reason, never a bare internal error.
func TestHookScannerFailureNamesCause(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	if err := os.WriteFile(filepath.Join(h.Repo, ".gitleaks.toml"), []byte("[[rules]\nid = \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := h.App.HookSecrets(context.Background(), h.Repo, commitWhere, map[string][]byte{"a.txt": []byte("hello\n")})
	if err == nil || errs.Has(err, errs.Internal) || !strings.Contains(err.Error(), "gitleaks") {
		t.Fatalf("err = %v, want a refusal naming .gitleaks.toml", err)
	}
}

// TestCommitCheckOneHitPerValue (W-9): a token both lore's rule and the
// gitleaks rule it covers match is listed once, under lore's rule, every
// run; an allow entry written for that rule accepts the value whatever
// other rule also matches it. Break-verified: without oneHitPerValue the
// refusal lists the value twice; without sameValueAllowed the allowed
// value still blocks under the other rule.
func TestCommitCheckOneHitPerValue(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	tok := "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "Jk9LmN" // built at run time
	docs := map[string][]byte{"notes.md": []byte("token = " + tok + "\n")}
	if err := os.WriteFile(filepath.Join(h.Repo, "notes.md"), docs["notes.md"], 0o600); err != nil {
		t.Fatal(err)
	}
	allowGit(t, h.Repo, "add", "notes.md")
	for range 3 {
		err := h.App.HookSecrets(ctx, h.Repo, commitWhere, docs)
		if !errs.Has(err, errs.LeakBlocked) {
			t.Fatalf("err = %v, want E_LEAK_BLOCKED", err)
		}
		if n := strings.Count(err.Error(), "secret: rule"); n != 1 || !strings.Contains(err.Error(), "rule github_token ") {
			t.Fatalf("the token is listed %d times (want once, as github_token):\n%v", n, err)
		}
	}
	out := h.MustYes("secrets.allow", `{"file":"`+filepath.Join(h.Repo, "notes.md")+`","rule":"github_token","match_hash":"`+scan.Mask(tok)+`","reason":"sample token"}`)
	allowGit(t, h.Repo, "add", "--", allowRowFile(t, h.Repo, "secret_allows", h.ID(out)))
	if err := h.App.HookSecrets(ctx, h.Repo, commitWhere, docs); err != nil {
		t.Fatalf("allowed token: err = %v, want pass", err)
	}
}
