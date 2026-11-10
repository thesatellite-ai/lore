//go:build userscen

package userscen

// check_prbody_secret_test.go — `lore check pr-body` runs the same secret
// rules as `lore check leaks`, in every repo (an own repo too, where the
// F16 internal-row check does not apply): a token-shaped string in the
// body is E_LEAK_BLOCKED (exit 19) with the line and the rule, and the
// value itself is never printed. The same over stdin, --json and REST.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prBodyFakeToken is shaped like a GitHub token, assembled at run time so
// no token-shaped literal sits in the source; nobody ever issued it.
func prBodyFakeToken() string {
	var b strings.Builder
	b.WriteString("gh")
	b.WriteString("p_")
	for i := range 36 {
		b.WriteByte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJ0123456789"[(i*7)%46])
	}
	return b.String()
}

func TestCheckPRBody_SecretBlocks(t *testing.T) {
	c := renderSolo(t)
	tok := prBodyFakeToken()
	for name, body := range map[string]string{
		"first line": tok + "\n",
		"in prose":   "Fixes the parser.\n\nDeploy with TOKEN=" + tok + " please\n",
		"in code":    "```\nexport GH=" + tok + "\n```\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := filepath.Join(c.Dir, "body.md")
			if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			r := c.Lore("check", "pr-body", f)
			r.Fails(t, campExitGuard, "[E_LEAK_BLOCKED]", "PR body:", "github_token", "hint: store the value in your vault")
			r.Never(t, tok)
			jr := c.Lore("check", "pr-body", f, "--json")
			jr.Never(t, tok)
			var e campErr
			if err := json.Unmarshal([]byte(jr.Stdout), &e); err != nil || e.Error.Code != "E_LEAK_BLOCKED" || e.Error.Exit != campExitGuard {
				t.Errorf("check pr-body --json on a secret: the E_LEAK_BLOCKED envelope: %v %s", err, jr)
			}
			cmd := c.Command("check", "pr-body", "-")
			cmd.Stdin = strings.NewReader(body)
			out, _ := cmd.CombinedOutput()
			if !strings.Contains(string(out), "[E_LEAK_BLOCKED]") || strings.Contains(string(out), tok) {
				t.Errorf("check pr-body - (stdin): blocked, value not echoed: %s", strings.ReplaceAll(string(out), tok, "<TOKEN>"))
			}
		})
	}
	// A clean body still passes.
	f := filepath.Join(c.Dir, "clean.md")
	if err := os.WriteFile(f, []byte("Fixes the parser; see ghp_ docs.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.MustLore("check", "pr-body", f).Says(t, "✓ nothing internal found")
	// REST answers as the CLI.
	srv := renderStartAPI(t, c)
	if err := os.WriteFile(f, []byte("token "+tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"file": f})
	rr := srv.post(t, "/v1/checks/pr_body", body)
	if rr.code != "E_LEAK_BLOCKED" || strings.Contains(string(rr.body), tok) {
		t.Errorf("REST pr_body with a secret: E_LEAK_BLOCKED, value not echoed; got %d %s", rr.status, strings.ReplaceAll(string(rr.body), tok, "<TOKEN>"))
	}
}
