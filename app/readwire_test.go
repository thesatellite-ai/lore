package app

// readwire_test.go — owner bug 6 (2026-11-12): `lore member list` in a
// code repo whose hooks live in a committed .githooks folder
// (core.hooksPath) printed "lore sync: set up git for lore row files in
// this clone: hook blocks in .githooks (commit-msg, pre-commit)" and wrote
// those blocks. A read never wires git: not in a code repo (merge driver
// config, .gitattributes, hook blocks, info/exclude) and not in a store
// clone (merge driver, info/attributes, pre-commit, user.name/user.email).
// It prints at most one note naming the command that sets it up. A write wires, and what it
// says names the write, not `lore sync`.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadsNeverWireGit(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author().unscoped()
	home := w.env["HOME"]
	hosts := w.path("hosts")
	bare := filepath.Join(hosts, "acme", "notes.git")
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	w.git(t, bare, "init", "-q", "--bare", "-b", "main")
	gitcfg := "[user]\n\tname = Pat Example\n\temail = pat@example.com\n[init]\n\tdefaultBranch = main\n[url \"" + filepath.ToSlash(hosts) + "/\"]\n\tinsteadOf = https://github.com/\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitcfg), 0o600); err != nil {
		t.Fatal(err)
	}

	// a teammate's repo: lore set up, a store attached, rows written, its
	// own hooks committed under .githooks; pushed to a code remote
	team := w.path("team")
	if err := os.MkdirAll(filepath.Join(team, ".githooks"), 0o750); err != nil {
		t.Fatal(err)
	}
	w.git(t, team, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(team, ".githooks", "pre-commit"), []byte("#!/bin/sh\necho the project's own check\n"), 0o755); err != nil { // #nosec G306 -- a hook git must run
		t.Fatal(err)
	}
	w.mustLore(t, team, "init")
	w.mustLore(t, team, "store", "init", "notes", "https://github.com/acme/notes.git", "--yes")
	w.mustLore(t, team, "memory", "add", "--body", "the widget cache warms at boot")
	w.mustLore(t, team, "memory", "add", "--body", "the ledger keeps integer cents", "--to", "notes")
	w.git(t, team, "add", "-A")
	w.git(t, team, "-c", "core.hooksPath=/nonexistent", "commit", "-q", "-m", "lore")
	code := filepath.Join(hosts, "acme", "widgets.git")
	w.git(t, hosts, "clone", "-q", "--bare", team, code)

	// this person's fresh clone, hooks in the committed .githooks; lore's
	// clone of the store made fresh too (another machine)
	if err := os.RemoveAll(filepath.Join(home, ".lore", "stores")); err != nil {
		t.Fatal(err)
	}
	repo := w.path("repo")
	w.git(t, w.path("."), "clone", "-q", code, repo)
	w.git(t, repo, "config", "core.hooksPath", ".githooks")

	stores := filepath.Join(home, ".lore", "stores")
	view := func() string {
		return "repo:\n" + pfSnapshot(t, repo) + "\nstores:\n" + pfSnapshot(t, stores)
	}
	// the first read clones the store (lore's own clone, under ~/.lore):
	// a clone is not a read's to skip; what is compared is every read
	// after it
	fresh := pfSnapshot(t, repo)
	_, _, first := w.lore(t, repo, "memory", "list", "--store", "notes")
	if now := pfSnapshot(t, repo); now != fresh {
		t.Fatalf("the first read wrote into the repo:\n%s\nstderr:\n%s", pfDiff(fresh, now), first)
	}
	if b, err := os.ReadFile(filepath.Join(stores, "acme", "notes", ".git", "info", "attributes")); err == nil && strings.Contains(string(b), "*.json") {
		t.Fatalf("the read that cloned the store set the clone up for plain git:\n%s", b)
	}
	// a store clone with no git identity of its own (made by an older lore,
	// or the person removed it): a read writes none either
	storeClone := filepath.Join(stores, "acme", "notes")
	if strings.TrimSpace(w.git(t, storeClone, "config", "--local", "--get-regexp", "^user\\.")) == "" {
		t.Fatal("setup: the clone a read made has no identity (lore gives a clone it makes one)")
	}
	w.git(t, storeClone, "config", "--local", "--remove-section", "user")
	if err := os.Remove(filepath.Join(storeClone, ".git", "lore", "identity")); err != nil {
		t.Fatal(err)
	}
	before := view()
	reads := [][]string{
		{"member", "list"}, {"memory", "list"}, {"memory", "list", "--store", "notes"}, {"task", "list"},
		{"search", "widget"}, {"whoami"}, {"doctor"}, {"store", "list"}, {"store", "show"}, {"where"},
		{"rule", "list"}, {"sync", "status"},
	}
	notes := strings.Count(first, "git is not set up for lore's row files")
	if notes > 0 && !strings.Contains(first, "lore memory list: git is not set up") {
		t.Fatalf("the first read's note does not name its command:\n%s", first)
	}
	for _, args := range reads {
		c, out, errOut := w.lore(t, repo, args...)
		if c != 0 && args[0] != "doctor" { // doctor exits non-zero for its findings
			t.Fatalf("lore %v: exit %d\n%s\n%s", args, c, out, errOut)
		}
		if after := view(); after != before {
			t.Fatalf("the read lore %v wrote into the repo or the store clone:\n%s\nstderr:\n%s", args, pfDiff(before, after), errOut)
		}
		if strings.Contains(errOut, "lore sync: ") {
			t.Fatalf("the read lore %v printed a line prefixed with another command:\n%s", args, errOut)
		}
		if strings.Contains(errOut, "set up git") || strings.Contains(errOut, "set up the store clone") {
			t.Fatalf("the read lore %v says it set git up:\n%s", args, errOut)
		}
		if n := strings.Count(errOut, "git is not set up for lore's row files"); n > 0 {
			notes += n
			if want := "lore " + strings.Join(args, " ") + ": git is not set up"; !strings.Contains(errOut, want) || !strings.Contains(errOut, "`lore sync`") {
				t.Fatalf("the read's note does not name its command and `lore sync` (want %q):\n%s", want, errOut)
			}
		}
	}
	if notes != 1 {
		t.Fatalf("the reads printed %d notes about the unwired clone, want exactly one", notes)
	}

	// a write wires the repo and the store clone, and says so as itself
	c, out, errOut := w.lore(t, repo, "memory", "add", "--body", "the queue drains at night", "--to", "notes")
	if c != 0 {
		t.Fatalf("memory add: exit %d\n%s\n%s", c, out, errOut)
	}
	hooks := filepath.Join(repo, ".githooks")
	for _, want := range []string{"lore memory add: set up git for lore's row files in this clone:", "hook blocks in " + hooks} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("the write does not say %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "lore sync: set up") {
		t.Fatalf("the write's set-up line names lore sync:\n%s", errOut)
	}
	pc, err := os.ReadFile(filepath.Join(hooks, "pre-commit"))
	if err != nil || !strings.Contains(string(pc), "lore-sync") || !strings.Contains(string(pc), "the project's own check") {
		t.Fatalf("the write did not chain lore's block into .githooks/pre-commit (keeping the project's lines): %v\n%s", err, pc)
	}
	// and the store clone got its git identity with the write, said as
	// the write
	if got := strings.TrimSpace(w.git(t, storeClone, "config", "--local", "--get", "user.email")); got != "pat@example.com" || !strings.Contains(errOut, "lore memory add: store clone ") || !strings.Contains(errOut, ": git user set to ") {
		t.Fatalf("the write did not give the store clone its identity (user.email %q):\n%s", got, errOut)
	}
	// the store clone got its row attributes with the write (a test binary
	// is never written in as the merge driver, wire/handedit.go)
	attrs, err := os.ReadFile(filepath.Join(stores, "acme", "notes", ".git", "info", "attributes"))
	if err != nil || !strings.Contains(string(attrs), "*.json") {
		t.Fatalf("the write did not set the store clone's row attributes up: %v\n%s", err, attrs)
	}
}
