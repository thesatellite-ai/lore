package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/lorecfg"

	"github.com/spf13/cobra"
)

// twoOrgConfig is a HOME with two companies, a staging entry on the first
// company's store, and a "shared" client both companies list (§9.3 E8).
const twoOrgConfig = `
orgs:
  - name: northwind
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores: [{url: "git@github.com:northwind/lore", org_folder: org}]
    own:   [github.com/northwind/*]
    repos: [github.com/acme/*, github.com/shared/*]
  - name: northwind-stag
    identity: {key: "human:alice@northwind.example", name: "Alice Doe"}
    stores: [{url: "git@github.com:northwind/lore", branch: staging, org_folder: org}]
    repos: [github.com/acme/*]
  - name: betalabs
    identity: {key: "human:alice@betalabs.example", name: "Alice Doe"}
    stores: [{url: "git@github.com:betalabs/lore", org_folder: org}]
    own:   [github.com/betalabs/*]
    repos: [github.com/shared/*]
`

func writeHomeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".lore")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// whoamiOf runs `lore whoami --json` and decodes its data.
func whoamiOf(t *testing.T, c *clone, env ...string) (whoamiJSON, result) {
	t.Helper()
	old := c.env
	c.env = append(append([]string{}, old...), env...)
	r := c.loreAny("whoami", "--json")
	c.env = old
	var env1 struct {
		Data whoamiJSON `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env1); err != nil {
		t.Fatalf("whoami --json: %v\nstdout: %s\nstderr: %s", err, r.stdout, r.stderr)
	}
	return env1.Data, r
}

// TestE8OrgResolutionTwoOrgHome: one person, two companies, a staging
// entry, an ambiguous repo and a fork — `lore whoami` in each, with the
// real binary and an isolated HOME. The stores the config lists are never
// created, so rule 4 reports the link status unknown (writes need --to).
func TestE8OrgResolutionTwoOrgHome(t *testing.T) {
	w := newWorld(t)
	writeHomeConfig(t, w.home, twoOrgConfig)

	acme := w.newClone("acme")
	acme.git("remote", "add", "hosted", "git@github.com:acme/app.git")
	acme.lore("init")

	got, r := whoamiOf(t, acme)
	if r.code != 0 || got.Org != "northwind" || got.Rule != 5 || got.RepoRole != "client" ||
		got.Identity.StableKey != "human:alice@northwind.example" || got.Identity.Source != "org entry (config.yaml)" {
		t.Fatalf("acme whoami = %+v (exit %d, %s)", got, r.code, r.stderr)
	}
	// the local-path origin cannot identify a repo; it is reported, never matched
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "not a hosted remote") {
		t.Errorf("local origin not reported: %v", got.Warnings)
	}

	// staging only on purpose
	got, _ = whoamiOf(t, acme, "LORE_ORG=northwind-stag")
	if got.Org != "northwind-stag" || got.Rule != 1 {
		t.Fatalf("LORE_ORG staging = %+v", got)
	}
	// an agent session acts as itself; a human key in LORE_ACTOR is ignored
	got, _ = whoamiOf(t, acme, "LORE_ACTOR=agent:frontend-2", "LORE_SESSION=s-7f2e")
	if got.Identity.StableKey != "agent:frontend-2" || got.Identity.Session != "s-7f2e" {
		t.Fatalf("agent whoami = %+v", got.Identity)
	}
	got, _ = whoamiOf(t, acme, "LORE_ACTOR=human:intruder@else.example")
	if got.Identity.StableKey != "human:alice@northwind.example" {
		t.Fatalf("human LORE_ACTOR must be ignored in an org: %+v", got.Identity)
	}

	// The org's stores were never created, so lore cannot tell whether a
	// link makes this a client repo: a write without --to is refused
	// (F10 fail-closed rule, E_LINK_UNKNOWN) and nothing is written.
	r = acme.loreAny("memory", "add", "--body", "client is slow to pay")
	if r.code == 0 || !strings.Contains(r.stderr, "E_LINK_UNKNOWN") {
		t.Fatalf("write with unreadable stores: exit %d, %s", r.code, r.stderr)
	}
	if out := acme.lore("memory", "list").stdout; strings.Contains(out, "client is slow to pay") {
		t.Fatalf("a refused write left a row behind: %s", out)
	}
	// choosing the target explicitly works, and the write is stamped with
	// the org identity, not git's user.email
	acme.lore("memory", "add", "--body", "client is slow to pay", "--to", lorecfg.TargetRepo)
	if out := acme.lore("actor", "list").stdout; !strings.Contains(out, "Alice Doe") || strings.Contains(out, "acme@example.com") {
		t.Fatalf("actor list = %s", out)
	}

	// an ambiguous repo: init refuses and leaves nothing behind
	shared := w.newClone("shared")
	shared.git("remote", "add", "hosted", "git@github.com:shared/app.git")
	r = shared.loreAny("init")
	if r.code == 0 || !strings.Contains(r.stderr+r.stdout, "E_ORG_AMBIGUOUS") {
		t.Fatalf("ambiguous init exit %d: %s %s", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(shared.dir, ".lore", "lore.db")); !os.IsNotExist(err) {
		t.Fatalf("refused init left .lore/lore.db behind (%v)", err)
	}
	shared.env = []string{"LORE_ORG=betalabs"}
	shared.lore("init")
	shared.env = nil
	shared.lore("org", "use", "betalabs")
	got, r = whoamiOf(t, shared)
	if r.code != 0 || got.Org != "betalabs" || got.Rule != 2 {
		t.Fatalf("after org use: %+v (%s)", got, r.stderr)
	}
	shared.lore("org", "use", "--clear")
	if r := shared.loreAny("memory", "add", "--body", "x"); r.code == 0 {
		t.Fatal("a write in an ambiguous repo must be refused")
	}

	// committed .lore/org.yaml: refused in a client repo
	if r := acme.loreAny("org", "set", "northwind", "--commit"); r.code == 0 {
		t.Fatal("org set --commit accepted in a client repo")
	}
}

// TestResolveWriteTarget drives the --to / --everywhere wiring in process:
// placement by where you are, orgs never mix, the --everywhere confirm.
func TestResolveWriteTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LORE_ORG", "")
	t.Setenv("LORE_ACTOR", "")
	writeHomeConfig(t, home, twoOrgConfig)
	repo := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:acme/app.git"}} {
		if out, err := testCmd(t, "git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	t.Chdir(repo)
	ctx := context.Background()

	cases := []struct {
		flags  toFlags
		text   string
		folder string // personal folder, "" = not personal
		bad    string // substring of the refusal
	}{
		{flags: toFlags{To: "me"}, text: "ask Raj about betalabs", folder: "northwind"},
		{flags: toFlags{To: "me/northwind"}, folder: "northwind"},
		{flags: toFlags{To: "me", Everywhere: true}, text: "I prefer tabs", folder: lorecfg.PersonalEverywhere},
		{flags: toFlags{To: "me", Everywhere: true}, text: "betalabs invoices", bad: "--yes"},
		{flags: toFlags{To: "me", Everywhere: true, Yes: true}, text: "betalabs invoices", folder: lorecfg.PersonalEverywhere},
		{flags: toFlags{To: "betalabs"}, bad: "another org"},
		{flags: toFlags{To: "me/betalabs"}, bad: "another org"},
		{flags: toFlags{To: "repo", Everywhere: true}, bad: "--everywhere needs --to me"},
		{flags: toFlags{To: "northwind/_root"}, bad: "reserved"},
		{flags: toFlags{To: "repo"}},
		{flags: toFlags{To: "northwind/acme"}},
		{flags: toFlags{}},
	}
	for _, c := range cases {
		wt, err := resolveWriteTarget(ctx, c.flags, c.text)
		if c.bad != "" {
			if err == nil || !strings.Contains(err.Error(), c.bad) {
				t.Errorf("%+v: err = %v, want %q", c.flags, err, c.bad)
			}
			continue
		}
		if err != nil {
			t.Errorf("%+v: %v", c.flags, err)
			continue
		}
		switch {
		case c.folder == "" && wt.Personal != nil:
			t.Errorf("%+v: unexpected personal placement %+v", c.flags, wt.Personal)
		case c.folder != "" && (wt.Personal == nil || wt.Personal.Folder != c.folder):
			t.Errorf("%+v: placement %+v, want %s", c.flags, wt.Personal, c.folder)
		}
	}

	// no org here: --to me goes to everywhere/
	scratch := t.TempDir()
	t.Chdir(scratch)
	wt, err := resolveWriteTarget(ctx, toFlags{To: "me"}, "note")
	if err != nil || wt.Personal == nil || wt.Personal.Folder != lorecfg.PersonalEverywhere {
		t.Fatalf("no-org --to me = %+v, %v", wt.Personal, err)
	}
}

// TestBindToFlags: the shared flags parse into toFlags under the names the
// spec gives them (§1 Flags).
func TestBindToFlags(t *testing.T) {
	for _, withYes := range []bool{true, false} {
		var f toFlags
		cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
		bindToFlags(cmd, &f, withYes)
		args := []string{"--" + flagTo, "me/northwind", "--" + flagEverywhere, "--" + flagForceOrg}
		if withYes {
			args = append(args, "--"+flagYes)
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if f.To != "me/northwind" || !f.Everywhere || !f.ForceOrg || f.Yes != withYes {
			t.Fatalf("withYes=%v: %+v", withYes, f)
		}
		if !withYes && cmd.Flags().Lookup(flagYes) != nil {
			t.Fatal("--yes bound although the command owns it")
		}
	}
}
