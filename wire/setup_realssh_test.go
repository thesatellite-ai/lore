package wire_test

// The GitHub step against a ~/.ssh shaped like a real one (owner test
// 2026-11-07): the default host's key has no .pub, an alias's IdentityFile
// points at another machine's home, two accounts have ssh routes, a third
// only a gh login. Every earlier test world had a .pub for every key, so
// the setup the owner needed (github.com through a key without .pub) could
// not be saved: the page auto-filled the private key path and the
// validator refused it. These tests run the real ops — setup.plan's
// prefill, setup.check's route decision and validation, and the github
// apply — and assert that whatever the page is handed always validates.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/wire"
	"github.com/thesatellite-ai/lore/wire/internal/fakebin"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// realSSHConfig mirrors the real file's shape: github.com with a key that
// has no .pub, an alias whose IdentityFile is another machine's path, a
// second account's alias.
const realSSHConfig = `Host github.com
	IdentityFile ~/.ssh/main-key
Host octo-user-GitHub
	HostName github.com
	User octo-user
	IdentityFile /Users/elsewhere/.ssh/octo-user-GitHub
Host githubwork
	User git
	Hostname github.com
	IdentityFile ~/.ssh/work-key
`

// realFakeSSH greets per alias or -i key as GitHub would: an alias whose
// key file is missing is refused.
const realFakeSSH = `key=""; host=""; prev=""
for a in "$@"; do [ "$prev" = "-i" ] && key="$a"; prev="$a"; host="$a"; done
host="${host#git@}"
hi(){ echo "Hi $1! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1; }
if [ -n "$key" ]; then case "$(basename "$key")" in main-key*) hi octo-user;; work-key*) hi work-user;; *) echo "git@github.com: Permission denied (publickey)." >&2; exit 255;; esac; fi
case "$host" in
  github.com) hi octo-user;;
  githubwork) hi work-user;;
  octo-user-GitHub) echo "git@github.com: Permission denied (publickey)." >&2; exit 255;;
esac
echo "ssh: Could not resolve hostname $host" >&2; exit 255`

// realSSHWorld is an App whose HOME has that ~/.ssh (real ed25519 keys made
// by ssh-keygen, the same .pub present/missing pattern), the fake ssh first
// on PATH, and gh logins for octo-user (active), work-user and gh-only.
func realSSHWorld(t *testing.T) *opstest.H {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed: the fingerprint of a key without .pub needs it")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	bin := filepath.Join(home, "bin")
	sshDir := filepath.Join(home, ".ssh")
	ghDir := filepath.Join(home, ".config", "gh")
	for _, d := range []string{bin, sshDir, ghDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		fakebin.Write(t, path, body, mode)
	}
	write(filepath.Join(bin, "ssh"), "#!/bin/sh\n"+realFakeSSH+"\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	write(filepath.Join(sshDir, "config"), realSSHConfig, 0o600)
	write(filepath.Join(sshDir, "known_hosts"), "", 0o600)
	// Keys with and without .pub, as in the real folder.
	for name, keepPub := range map[string]bool{"main-key": false, "work-key": true, "octo-user-GitHub": true, "id_ed25519": true, "deploy-ci": true} {
		key := filepath.Join(sshDir, name)
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name, "-f", key).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen %s: %v %s", name, err, out)
		}
		if !keepPub {
			if err := os.Remove(key + onboard.PubKeySuffix); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(filepath.Join(ghDir, "hosts.yml"), "github.com:\n  users:\n    octo-user:\n    work-user:\n    gh-only:\n  user: octo-user\n", 0o600)
	return opstest.New(t)
}

// planGitHub is the github step's prefilled answers from setup.plan.
func planGitHub(t *testing.T, h *opstest.H, setup string) onboard.Answers {
	t.Helper()
	var out opssetup.PlanOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpPlan, `{"setup":"`+setup+`","kind":"solo"}`))), &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range out.Steps {
		if s.ID != onboard.StepGitHub {
			continue
		}
		a := onboard.Answers{}
		for _, q := range s.Questions {
			if !q.Value.IsZero() {
				a[q.ID] = onboard.Answer(q.Value)
			}
		}
		return a
	}
	t.Fatal("no github step in the plan")
	return nil
}

// checkGitHub runs setup.check on the github step.
func checkGitHub(t *testing.T, h *opstest.H, setup string, a onboard.Answers) opssetup.CheckOut {
	t.Helper()
	body, err := json.Marshal(opssetup.CheckIn{Setup: setup, Step: string(onboard.StepGitHub), Answers: a})
	if err != nil {
		t.Fatal(err)
	}
	var out opssetup.CheckOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpCheck, string(body)))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGitHubStepOnRealSSHShape(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	h := realSSHWorld(t)
	ctx := context.Background()

	// The prefill alone: the account is gh's active login, and nothing the
	// validator could refuse is filled (no key path guessed, no alias).
	pre := planGitHub(t, h, "personal")
	if pre[onboard.QIDGitHubAccount].Text != "octo-user" {
		t.Fatalf("prefilled account = %+v", pre)
	}
	for _, id := range []string{onboard.QIDHostAlias, onboard.QIDSSHKey, onboard.QIDRoute} {
		if pre[id].Text != "" {
			t.Errorf("prefill guessed %s = %q", id, pre[id].Text)
		}
	}

	cases := []struct {
		name    string
		answers func(onboard.Answers) onboard.Answers
		// the derived route and what the saved block holds
		route       onboard.GitRoute
		alias, key  string
		fingerprint bool
		errHas      string // a field error on the account instead
	}{
		{name: "prefill as is: github.com through a key without .pub",
			answers: func(a onboard.Answers) onboard.Answers { return a },
			route:   onboard.RouteSSH, alias: "github.com", fingerprint: true},
		{name: "second account: its alias and .pub",
			answers: func(a onboard.Answers) onboard.Answers {
				a[onboard.QIDGitHubAccount] = onboard.Answer{Text: "work-user"}
				return a
			},
			route: onboard.RouteSSH, alias: "githubwork", key: "~/.ssh/work-key.pub", fingerprint: true},
		{name: "no ssh route: https with the gh login",
			answers: func(a onboard.Answers) onboard.Answers {
				a[onboard.QIDGitHubAccount] = onboard.Answer{Text: "gh-only"}
				return a
			},
			route: onboard.RouteHTTPS},
		{name: "neither: the fix, nothing saved",
			answers: func(a onboard.Answers) onboard.Answers {
				a[onboard.QIDGitHubAccount] = onboard.Answer{Text: "nobody-user"}
				return a
			},
			errHas: "gh auth login"},
		{name: "details: a private key path without .pub",
			answers: func(a onboard.Answers) onboard.Answers {
				a[onboard.QIDSSHKey] = onboard.Answer{Text: "~/.ssh/main-key"}
				return a
			},
			route: onboard.RouteSSH, key: "~/.ssh/main-key", fingerprint: true},
		{name: "details: a .pub path",
			answers: func(a onboard.Answers) onboard.Answers {
				a[onboard.QIDGitHubAccount] = onboard.Answer{Text: "work-user"}
				a[onboard.QIDHostAlias] = onboard.Answer{Text: "githubwork"}
				a[onboard.QIDSSHKey] = onboard.Answer{Text: "~/.ssh/work-key.pub"}
				return a
			},
			route: onboard.RouteSSH, alias: "githubwork", key: "~/.ssh/work-key.pub", fingerprint: true},
		{name: "details: the broken alias is refused by its check, never by a field rule",
			answers: func(a onboard.Answers) onboard.Answers {
				a[onboard.QIDHostAlias] = onboard.Answer{Text: "octo-user-GitHub"}
				return a
			},
			route: onboard.RouteSSH, alias: "octo-user-GitHub"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setup := "s" + string(rune('a'+i))
			a := c.answers(planGitHub(t, h, setup))
			out := checkGitHub(t, h, setup, a)
			if c.errHas != "" {
				if len(out.FieldErrors) != 1 || out.FieldErrors[0].Question != onboard.QIDGitHubAccount || !strings.Contains(out.FieldErrors[0].Message, c.errHas) {
					t.Fatalf("field errors = %+v", out.FieldErrors)
				}
				return
			}
			if len(out.FieldErrors) != 0 {
				t.Fatalf("auto-filled answers refused: %+v", out.FieldErrors)
			}
			// The derived answers (kept in progress) saved through the applier.
			var plan opssetup.PlanOut
			if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpPlan, `{"setup":"`+setup+`"}`))), &plan); err != nil {
				t.Fatal(err)
			}
			var derived onboard.Answers
			for _, p := range plan.Progress {
				if p.Step == onboard.StepGitHub {
					derived = p.Answers
				}
			}
			if onboard.GitRoute(derived[onboard.QIDRoute].Text) != c.route || derived[onboard.QIDHostAlias].Text != c.alias {
				t.Fatalf("derived = %+v, want route %s alias %q", derived, c.route, c.alias)
			}
			ap, err := h.App.SetupApplier(ctx)
			if err != nil {
				t.Fatal(err)
			}
			res, err := ap.Apply(ctx, opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: setup, Kind: onboard.KindSolo},
				Step: onboard.StepGitHub, Apply: onboard.ApplyGitHub, Answers: derived})
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			g := res.Setup.GitHub
			if g.Route != c.route || g.HostAlias != c.alias || g.SSHKey != c.key || (g.KeyFingerprint != "") != c.fingerprint {
				t.Errorf("saved github = %+v, want route %s alias %q key %q fingerprint %v", g, c.route, c.alias, c.key, c.fingerprint)
			}
			if c.fingerprint && !strings.HasPrefix(g.KeyFingerprint, "SHA256:") {
				t.Errorf("fingerprint = %q", g.KeyFingerprint)
			}
			if _, err := wire.ValidateSetupsConfig(h.Home); err != nil {
				t.Errorf("saved config does not validate: %v", err)
			}
			// Doctor's github.key_matches on the saved block: the chosen
			// route logs in as the account (the broken alias fails).
			pr, err := h.App.SetupProber(ctx)
			if err != nil {
				t.Fatal(err)
			}
			km := pr.Probe(ctx, []opssetup.ProbeRequest{{Check: onboard.Check{ID: onboard.CheckGitHubKeyMatches, Level: onboard.LevelError}, Setup: res.Setup}})[0]
			want := onboard.StatusOK
			if c.alias == "octo-user-GitHub" {
				want = onboard.StatusFail
			}
			if km.Status != want {
				t.Errorf("key_matches = %s %q, want %s", km.Status, km.Detail, want)
			}
		})
	}
}

// Every step's prefill passes that step's own validation (owner rule:
// never prefill what the page then refuses). A saved setup already covers
// ~/code/proj/** and the page starts in ~/code/proj, so the folders
// prefill (the start folder) would tie with it (realmac finding 14); the
// personal store step's starting answer is never "same as my other
// setups" without a personal.store (finding 12).
func TestEveryStepPrefillValidates(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	realSSHWorld(t) // HOME, ~/.ssh, gh logins; the App below starts in ~/code/proj
	home := os.Getenv("HOME")
	start := filepath.Join(home, "code", "proj")
	if err := os.MkdirAll(start, 0o700); err != nil {
		t.Fatal(err)
	}
	// lore setup's start folder is the App's work folder.
	h := opstest.New(t, func(o *wire.Options) { o.WorkDir = start })
	// work has its GitHub step saved, so the store url is suggested
	// (git@githubwork:work-user/lore-me.git) and the store choice must
	// start on "existing"; it has no folders yet.
	cfg := "setups:\n  - name: personal\n    kind: solo\n    default: true\n    folders: [\"~/code/proj/**\"]\n    github: {user: octo-user, route: ssh, host_alias: github.com}\n" +
		"  - name: work\n    kind: solo\n    github: {user: work-user, route: ssh, host_alias: githubwork}\n"
	if err := os.WriteFile(filepath.Join(h.Home, wire.ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var plan opssetup.PlanOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpPlan, `{"setup":"work","kind":"solo"}`))), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) < 5 {
		t.Fatalf("plan has %d steps", len(plan.Steps))
	}
	for _, s := range plan.Steps {
		a := onboard.Answers{}
		for _, q := range s.Questions {
			if !q.Value.IsZero() {
				a[q.ID] = onboard.Answer(q.Value)
			}
		}
		if s.ID == onboard.StepPersonalStore {
			// A required choice always has a starting answer, never
			// inherit without a personal.store (realmac 12, NEW-1).
			switch ps := a[onboard.QIDPersonalStore].Text; {
			case ps == "":
				t.Errorf("personal_store has no starting answer (required): %+v", a)
			case ps == onboard.PersonalStoreInherit:
				t.Errorf("personal_store prefilled inherit with no personal.store")
			case ps == onboard.PersonalStoreExisting && a[onboard.QIDStoreURL].Text == "":
				t.Errorf("personal_store existing with no url: %+v", a)
			}
		}
		if s.ID == onboard.StepPersonalStore && (a[onboard.QIDPersonalStore].Text != onboard.PersonalStoreExisting || a[onboard.QIDStoreURL].Text != "git@githubwork:work-user/lore-me.git") {
			t.Errorf("personal store starts at %+v, want existing with the suggested url", a)
		}
		if len(a) == 0 {
			continue // nothing prefilled: nothing to refuse
		}
		body, err := json.Marshal(opssetup.CheckIn{Setup: "work", Step: string(s.ID), Answers: a})
		if err != nil {
			t.Fatal(err)
		}
		var out opssetup.CheckOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpCheck, string(body)))), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.FieldErrors) != 0 {
			t.Errorf("step %s: prefilled %+v refused: %+v", s.ID, a, out.FieldErrors)
		}
		for _, c := range out.Checks {
			if c.Check == onboard.CheckFoldersOverlap && c.Status == onboard.StatusFail {
				t.Errorf("step %s: prefilled %+v fails its own %s: %s", s.ID, a, c.Check, c.Detail)
			}
		}
	}
}

// The folders prefill is the start folder itself (or its git repo), never
// its parent, and nothing at the home folder (realmac M1: started in
// ~/Downloads/lore1, the step prefilled ~/Downloads/**).
func TestFoldersPrefillIsTheStartFolder(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	realSSHWorld(t) // HOME, ~/.ssh, gh logins
	home := os.Getenv("HOME")
	repo := filepath.Join(home, "code", "proj")
	for _, d := range []string{filepath.Join(home, "Downloads", "lore1"), filepath.Join(repo, "src")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for _, c := range []struct{ name, start, want string }{
		{"not a repo", filepath.Join(home, "Downloads", "lore1"), "~/Downloads/lore1/**"},
		{"inside a repo", filepath.Join(repo, "src"), "~/code/proj/**"},
		{"the home folder", home, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			// lore setup's start folder is the App's work folder.
			h := opstest.New(t, func(o *wire.Options) { o.WorkDir = c.start })
			var plan opssetup.PlanOut
			if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpPlan, `{"setup":"work","kind":"solo"}`))), &plan); err != nil {
				t.Fatal(err)
			}
			got := "(no folders question)"
			for _, s := range plan.Steps {
				for _, q := range s.Questions {
					if q.ID == onboard.QIDFolders {
						got = strings.Join(q.Value.List, ",")
					}
				}
			}
			if got != c.want {
				t.Errorf("folders prefill = %q, want %q", got, c.want)
			}
		})
	}
}
