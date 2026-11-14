package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
)

// stderrOf is what the fixture's host printed so far.
func stderrOf(f ltFixture) string {
	if b, ok := f.s.h.cfg.Stderr.(*strings.Builder); ok {
		return b.String()
	}
	return ""
}

// markWired gives the fixture's clone lore's merge driver config, as a
// clone lore set up before holds (neverWired is then false).
func markWired(t *testing.T, f ltFixture) {
	t.Helper()
	if _, err := f.s.repo.Run(f.ctx, gitref.Invocation{Args: []string{"config", "--local", "merge." + mergeDriverLore + ".driver", "lore merge-driver %O %A %B %P"}}); err != nil {
		t.Fatal(err)
	}
}

// gitOut runs git in the fixture's repo and returns its output.
func gitOut(t *testing.T, f ltFixture, args ...string) string {
	t.Helper()
	out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: f.s.top, Args: args})
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// writeFile writes body at rel under the fixture's repo top.
func writeFile(t *testing.T, f ltFixture, rel, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(f.s.top, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// TestPreCommitLeavesRowsOutWhenNothingElseIsStaged (H-15): git decides
// whether a commit has anything in it before pre-commit runs, so with
// nothing else staged a plain `git commit` or `git commit -a` stops at
// "nothing added to commit" whatever the hook stages. The hook used to
// stage the new row and print "added 1 lore row file(s) to this commit"
// right before git's refusal; now it stages nothing and says how to commit
// the rows. With something else staged it stages them as before.
func TestPreCommitLeavesRowsOutWhenNothingElseIsStaged(t *testing.T) {
	f := newLiveFixture(t)
	f.s.release()
	t.Chdir(f.s.top)
	row := ".lore/data/memories/mem_1.json"
	writeFile(t, f, row, `{"a":1}`+"\n", 0o600)
	if err := f.s.h.preCommit(context.Background()); err != nil {
		t.Fatalf("pre-commit: %v", err)
	}
	out := stderrOf(f)
	if strings.Contains(out, "added 1 lore row file") || !strings.Contains(out, "1 lore row file(s) left out of this commit") || !strings.Contains(out, "git add .lore/data && git commit") {
		t.Fatalf("nothing else staged: the hook said %q, want the left-out line and no claim it added the row", out)
	}
	if staged := strings.TrimSpace(gitOut(t, f, "diff", "--cached", "--name-only")); staged != "" {
		t.Fatalf("nothing else staged: the hook staged %q", staged)
	}

	writeFile(t, f, "code.txt", "x\n", 0o600)
	gitT(t, f.s.top, "add", "code.txt")
	if err := f.s.h.preCommit(context.Background()); err != nil {
		t.Fatalf("pre-commit: %v", err)
	}
	if out := stderrOf(f); !strings.Contains(out, "added 1 lore row file(s) to this commit: "+row) {
		t.Fatalf("code staged too: the hook said %q, want the added line", out)
	}
	if staged := gitOut(t, f, "diff", "--cached", "--name-only"); !strings.Contains(staged, row) {
		t.Fatalf("code staged too: the row was not staged: %q", staged)
	}
}

// fakeKey is a made-up AWS-shaped key, built at run time.
var fakeKey = "AKIA" + strings.Repeat("Z", 16)

// leakStub is a LeakCheck that refuses any doc holding fakeKey, naming it
// as the real check's refusal does (first line, then finding lines).
func leakStub(_ context.Context, _ string, docs map[string][]byte) error {
	for p, b := range docs {
		if strings.Contains(string(b), fakeKey) {
			return errs.New(errs.LeakBlocked, "blocked, this push carries a secret:\n  "+p+":1 secret: rule aws_access_key")
		}
	}
	return nil
}

// TestLeakGuardOffIsSaidFirstAndScanned (H-18): lore's commit-msg,
// pre-commit and pre-push blocks removed from a clone lore wired (by hand,
// or a hook manager taking the hooks over). doctor's first warning says
// the leak guard is off and names `lore fix`, and names the secret a
// commit made meanwhile carries; the next command (a read) says it too,
// once; a write restores the blocks and says so. Only the pre-commit block
// gone (the pre-commit framework's script) is a warning as well.
func TestLeakGuardOffIsSaidFirstAndScanned(t *testing.T) {
	f := newLiveFixture(t)
	f.s.h.cfg.LeakCheck = leakStub
	markWired(t, f)
	f.s.ensureGitWiring(f.ctx, f.s.top, wireInstall)
	dir, err := f.s.hooksDir(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := missingGuardHooks(dir); len(got) != 0 {
		t.Fatalf("after the install the guard misses %v", got)
	}
	base, err := liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range base.Warnings {
		if strings.Contains(w, "leak guard") {
			t.Fatalf("guard in place, doctor warns %q", w)
		}
	}

	for _, h := range []string{hookCommitMsg, hookPreCommit, hookPrePush} {
		if err := os.Remove(filepath.Join(dir, h)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, f, "deploy/aws.env", "AWS_ACCESS_KEY_ID="+fakeKey+"\n", 0o600)
	gitT(t, f.s.top, "add", "deploy/aws.env")
	gitT(t, f.s.top, "-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "probe") // no hook of the test machine's lore runs

	base, err = liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Warnings) < 2 || !strings.Contains(base.Warnings[0], "leak guard off in this clone") || !strings.Contains(base.Warnings[0], "commits are not checked for secrets") || !strings.Contains(base.Warnings[0], "run `lore fix`") {
		t.Fatalf("guard off: doctor warnings %q, want the leak-guard warning first, naming lore fix", base.Warnings)
	}
	if !strings.Contains(base.Warnings[1], "deploy/aws.env:1") || strings.Contains(base.Warnings[1], "this push") {
		t.Fatalf("guard off: doctor's second warning %q, want the scanned commit's finding", base.Warnings[1])
	}
	if base.Status == "HEALTHY" {
		t.Fatalf("guard off: doctor status %s", base.Status)
	}

	// the next command, a read: said once per state, with the scan
	f.s.ensureGitWiring(f.ctx, f.s.top, wireRead)
	out := stderrOf(f)
	if !strings.Contains(out, "leak guard off in this clone") || !strings.Contains(out, "deploy/aws.env:1") {
		t.Fatalf("the next read said %q, want the guard line and the finding", out)
	}
	n := strings.Count(out, "leak guard off")
	f.s.ensureGitWiring(f.ctx, f.s.top, wireRead)
	if strings.Count(stderrOf(f), "leak guard off") != n {
		t.Fatalf("a second read in the same state repeated the guard line:\n%s", stderrOf(f))
	}

	// a write restores the blocks and says so
	f.s.ensureGitWiring(f.ctx, f.s.top, wireAuto)
	if got := missingGuardHooks(dir); len(got) != 0 {
		t.Fatalf("after a write the guard still misses %v", got)
	}
	if !strings.Contains(stderrOf(f), "restored now") {
		t.Fatalf("the write did not say it restored the guard:\n%s", stderrOf(f))
	}

	// the pre-commit framework's own script replaced lore's pre-commit
	writeFile(t, f, ".git/hooks/pre-commit", "#!/usr/bin/env bash\n# File generated by pre-commit\nexec true\n", 0o755)
	base, err = liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Warnings) == 0 || !strings.Contains(base.Warnings[0], "(pre-commit)") || !strings.Contains(base.Warnings[0], "broken lore row files are not refused") {
		t.Fatalf("pre-commit block gone: doctor warnings %q, want it named first", base.Warnings)
	}
}

// TestWriteCreatesNoHookFileInATrackedHooksFolder (H-19, stories H-1):
// with core.hooksPath at a folder of the work tree (husky's .husky), a
// write neither creates a hook file there nor appends lore's block to the
// team's own (either reaches every teammate with the next commit) and
// says which it left out; an install step (lore fix, which asks first)
// writes them and says they are new or changed files of the repo. A hooks
// folder git ignores is written as .git/hooks.
func TestWriteCreatesNoHookFileInATrackedHooksFolder(t *testing.T) {
	f := newLiveFixture(t)
	markWired(t, f)
	writeFile(t, f, ".husky/pre-commit", "#!/bin/sh\necho team check\n", 0o755)
	gitT(t, f.s.top, "add", ".husky")
	gitT(t, f.s.top, "commit", "-q", "-m", "husky")
	gitT(t, f.s.top, "config", "core.hooksPath", ".husky")
	dir := filepath.Join(f.s.top, ".husky")

	f.s.ensureGitWiring(f.ctx, f.s.top, wireAuto)
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("a write created hook files in the tracked .husky: %v", names)
	}
	pc, err := os.ReadFile(filepath.Join(dir, hookPreCommit))
	if err != nil || string(pc) != "#!/bin/sh\necho team check\n" {
		t.Fatalf("the write changed the team's tracked .husky/pre-commit: %v\n%s", err, pc)
	}
	if status := gitOut(t, f, "status", "--porcelain", "--", ".husky"); status != "" {
		t.Fatalf("a write changed the tracked .husky:\n%s", status)
	}
	out := stderrOf(f)
	if !strings.Contains(out, "lore did not create commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-push or add its block to pre-commit in "+dir) || !strings.Contains(out, "`lore fix`") {
		t.Fatalf("the write said %q, want the hooks it left out and lore fix", out)
	}
	if status := gitOut(t, f, "status", "--porcelain", "--untracked-files=all", "--", ".husky"); strings.Contains(status, "??") {
		t.Fatalf("new untracked files in .husky after a write:\n%s", status)
	}

	f.s.ensureGitWiring(f.ctx, f.s.top, wireInstall)
	if got := missingGuardHooks(dir); len(got) != 0 {
		t.Fatalf("after lore fix the guard misses %v", got)
	}
	if out := stderrOf(f); !strings.Contains(out, "new or changed files in "+dir+", a folder committed with the repo: commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push") {
		t.Fatalf("lore fix did not name the new files of the repo:\n%s", out)
	}

	// husky 9: core.hooksPath at an ignored folder inside .husky
	g := newLiveFixture(t)
	writeFile(t, g, ".husky/_/.gitignore", "*\n", 0o600)
	gitT(t, g.s.top, "config", "core.hooksPath", ".husky/_")
	g.s.ensureGitWiring(g.ctx, g.s.top, wireAuto)
	if got := missingGuardHooks(filepath.Join(g.s.top, ".husky", "_")); len(got) != 0 {
		t.Fatalf("an ignored hooks folder was not written: missing %v\n%s", got, stderrOf(g))
	}
}
