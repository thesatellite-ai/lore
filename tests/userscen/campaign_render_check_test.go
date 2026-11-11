//go:build userscen

package userscen

// campaign_render_check_test.go — `lore check leaks` and `lore check
// pr-body` as a person and an agent run them (docs/team/agents.md "posting
// PR text"):
//   - the clean answers, text and --json (mode / clean / message);
//   - a staged secret and a secret in the commit message block with
//     E_LEAK_BLOCKED (exit 19), the file:line, and the vault hint;
//   - a body or commit message file that cannot be read names the file and
//     the reason, with a hint (missing file, a folder, an unreadable file);
//   - the flag combinations the op refuses; odd bodies (empty, unicode,
//     10k chars, stdin);
//   - the same answers over REST (`lore api`).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// campPRBody is `lore check pr-body --json`.
type campPRBody struct {
	Clean   bool   `json:"clean"`
	Message string `json:"message"`
}

// campLeaks is `lore check leaks --json`.
type campLeaks struct {
	Mode    string `json:"mode"`
	Message string `json:"message"`
}

// campFakeToken is shaped like a GitHub token so the gitleaks rule fires;
// it was never issued by anyone.
const campFakeToken = "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789AB"

func TestCampaignRender_CheckPRBodyBodies(t *testing.T) {
	c := renderSolo(t)
	bodies := map[string]string{
		"plain":   "Fixes the parser bug.\n",
		"empty":   "",
		"unicode": "修复 🚀 — שלום עולם\n",
		"long":    strings.Repeat("word ", 2000),
	}
	for name, b := range bodies {
		f := filepath.Join(c.Dir, "body-"+name+".md")
		if err := os.WriteFile(f, []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
		c.MustLore("check", "pr-body", f).Says(t, "✓ nothing internal found")
		got := campJSON[campPRBody](t, c.Lore("check", "pr-body", f, "--json"))
		if !got.Clean || !strings.HasPrefix(got.Message, "✓ nothing internal found") {
			t.Errorf("%s body --json: clean with the message, got %+v", name, got)
		}
	}
	// The same body from stdin (gh pr view … | lore check pr-body -).
	cmd := c.Command("check", "pr-body", "-")
	cmd.Stdin = strings.NewReader(bodies["unicode"])
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "✓ nothing internal found") {
		t.Errorf("pr-body - reads stdin: %v %s", err, out)
	}
}

func TestCampaignRender_CheckUnreadableFiles(t *testing.T) {
	c := renderSolo(t)
	dir := filepath.Join(c.Dir, "a-folder")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(c.Dir, "locked.md")
	if err := os.WriteFile(locked, []byte("x\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		args   []string
		reason string
		hint   string
	}{
		{"pr-body missing file", []string{"check", "pr-body", "no-such-body.md"}, "no-such-body.md: no such file or directory", "or - to read the body from stdin"},
		{"pr-body a folder", []string{"check", "pr-body", dir}, "is a directory", "or - to read the body from stdin"},
		{"pr-body unreadable", []string{"check", "pr-body", locked}, "permission denied", "or - to read the body from stdin"},
		{"commit-msg missing file", []string{"check", "leaks", "--commit-msg", "no-such-msg"}, "no-such-msg: no such file or directory", "COMMIT_EDITMSG"},
		{"commit-msg a folder", []string{"check", "leaks", "--commit-msg", dir}, "is a directory", "COMMIT_EDITMSG"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.reason == "permission denied" && !hasModeBits {
				t.Skip("Windows has no mode bits: a 0o000 file stays readable")
			}
			campRefusal(t, c, campExitBadInput, "E_INVALID_INPUT", tc.hint, tc.args...)
			c.Lore(tc.args...).Says(t, tc.reason)
		})
	}
}

func TestCampaignRender_CheckLeaksModes(t *testing.T) {
	c := renderSolo(t)
	// Nothing staged: the hook answer is silent and clean.
	got := campJSON[campLeaks](t, c.Lore("check", "leaks", "--json"))
	if got.Mode != "commit" {
		t.Errorf("check leaks --json: mode commit, got %+v", got)
	}
	ci := campJSON[campLeaks](t, c.Lore("check", "leaks", "--ci", "--json"))
	if ci.Mode != "ci" || !strings.HasPrefix(ci.Message, "✓ lore check leaks --ci") {
		t.Errorf("check leaks --ci --json: mode ci with the message, got %+v", ci)
	}
	// The flag combinations the op refuses, with their hints.
	for _, args := range [][]string{
		{"check", "leaks", "--ci", "--as-client"},
		{"check", "leaks", "--rows", "--ci"},
		{"check", "leaks", "--rows", "--staged"},
	} {
		campRefusal(t, c, campExitBadInput, "E_INVALID_INPUT", "", args...)
		c.Lore(args...).Says(t, "hint: ")
	}
	// A staged secret blocks the commit: the file and line, the vault hint.
	c.WriteFile("conf/app.env", "TOKEN="+campFakeToken+"\n")
	c.Git("add", "conf/app.env")
	before := campTree(t, c.Dir)
	r := c.Lore("check", "leaks")
	r.Fails(t, campExitGuard, "[E_LEAK_BLOCKED]", "conf/app.env:1", "github_token", "hint: store the value in your vault")
	r.Never(t, campFakeToken)
	campSameTree(t, "check leaks writes nothing", before, campTree(t, c.Dir))
	jr := c.Lore("check", "leaks", "--json")
	var e campErr
	if err := json.Unmarshal([]byte(jr.Stdout), &e); err != nil || e.Error.Code != "E_LEAK_BLOCKED" || e.Error.Exit != campExitGuard {
		t.Errorf("check leaks --json on a secret: the E_LEAK_BLOCKED envelope: %v %s", err, jr)
	}
	if strings.Contains(jr.Out(), campFakeToken) {
		t.Error("the JSON envelope never repeats the secret")
	}
	c.Git("rm", "-q", "--cached", "conf/app.env")
	// A secret in the commit message alone.
	msg := filepath.Join(c.Dir, ".git", "COMMIT_EDITMSG")
	if err := os.WriteFile(msg, []byte("add config\n\ntoken "+campFakeToken+"\n# a git comment "+campFakeToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.Lore("check", "leaks", "--commit-msg", ".git/COMMIT_EDITMSG").Fails(t, campExitGuard, "[E_LEAK_BLOCKED]", "commit message:3").Never(t, campFakeToken)
	// A clean message passes (git's comment lines are not checked).
	if err := os.WriteFile(msg, []byte("add config\n# "+campFakeToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.MustLore("check", "leaks", "--commit-msg", ".git/COMMIT_EDITMSG")
}

func TestCampaignRender_CheckOverREST(t *testing.T) {
	c := renderSolo(t)
	srv := renderStartAPI(t, c)
	f := filepath.Join(c.Dir, "body.md")
	if err := os.WriteFile(f, []byte("Fixes the parser.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cli := campJSON[campPRBody](t, c.Lore("check", "pr-body", f, "--json"))
	body, _ := json.Marshal(map[string]string{"file": f})
	rep := srv.post(t, "/v1/checks/pr_body", body)
	var rest campPRBody
	if rep.status != 200 || json.Unmarshal(rep.body, &rest) != nil || rest != cli {
		t.Errorf("REST pr_body answers as the CLI: %d %s, cli %+v", rep.status, rep.body, cli)
	}
	missing, _ := json.Marshal(map[string]string{"file": filepath.Join(c.Dir, "gone.md")})
	if r := srv.post(t, "/v1/checks/pr_body", missing); r.status != 400 || r.code != "E_INVALID_INPUT" || !strings.Contains(string(r.body), "no such file") {
		t.Errorf("REST pr_body on a missing file: 400 E_INVALID_INPUT naming the reason, got %d %s", r.status, r.body)
	}
	cliCI := campJSON[campLeaks](t, c.Lore("check", "leaks", "--ci", "--json"))
	rl := srv.post(t, "/v1/checks/leaks", []byte(`{"ci":true}`))
	var restCI campLeaks
	if rl.status != 200 || json.Unmarshal(rl.body, &restCI) != nil || restCI != cliCI {
		t.Errorf("REST leaks --ci answers as the CLI: %d %s, cli %+v", rl.status, rl.body, cliCI)
	}
	if r := srv.post(t, "/v1/checks/leaks", []byte(`{"ci":true,"as_client":true}`)); r.status != 400 || r.code != "E_INVALID_INPUT" {
		t.Errorf("REST leaks --ci --as-client: 400 E_INVALID_INPUT, got %d %s", r.status, r.body)
	}
}
