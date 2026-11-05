package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"saas/pkg/aicoder/gitsetup"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/merge3"
	"saas/pkg/aicoder/projresolve"
)

func TestSyncDataDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got, ok := syncDataDir(filepath.Join(root, ".lore", "lore.db"))
	if !ok || got != filepath.Join(root, ".lore", "data") {
		t.Fatalf("mode A path: %q %v", got, ok)
	}
	for _, p := range []string{filepath.Join(root, "custom.db"), filepath.Join(root, "x", "lore.db"), filepath.Join(root, ".lore", "other.db")} {
		if _, ok := syncDataDir(p); ok {
			t.Fatalf("%s must not sync", p)
		}
	}
}

func TestSyncApplies(t *testing.T) {
	root := t.TempDir()
	modeA := &projresolve.Context{Mode: projresolve.ModeA, DBPath: filepath.Join(root, ".lore", "lore.db")}
	if !syncApplies(modeA) {
		t.Fatal("mode A must sync")
	}
	if syncApplies(&projresolve.Context{Mode: projresolve.ModeB, DBPath: modeA.DBPath}) {
		t.Fatal("mode B must not sync")
	}
	if syncApplies(nil) {
		t.Fatal("nil context")
	}
	t.Setenv(envSync, envValueOff)
	if syncApplies(modeA) {
		t.Fatal("LORE_SYNC=0 must disable sync")
	}
}

func TestNeedsMaterialize(t *testing.T) {
	root := t.TempDir()
	if needsMaterialize(root) {
		t.Fatal("no .lore/data")
	}
	data := filepath.Join(root, ".lore", "data")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, lsync.MetaFileName), []byte(`{"_v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !needsMaterialize(root) {
		t.Fatal("data without DB must materialize")
	}
	if err := os.WriteFile(filepath.Join(root, ".lore", "lore.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if needsMaterialize(root) {
		t.Fatal("Mode B pointer present: never materialize")
	}
	if err := os.Remove(filepath.Join(root, ".lore", "lore.toml")); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envSync, envValueOff)
	if needsMaterialize(root) {
		t.Fatal("LORE_SYNC=0 must not materialize")
	}
}

func TestParseCutoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Time
	}{
		{"2026-01-31", time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)},
		{"90d", now.Add(-90 * 24 * time.Hour)},
		{"720h", now.Add(-720 * time.Hour)},
		{"0d", now},
	} {
		got, err := parseCutoff(tc.in, now)
		if err != nil || !got.Equal(tc.want) {
			t.Fatalf("%s: %v %v", tc.in, got, err)
		}
	}
	for _, bad := range []string{"", "x", "-5d", "abcd", "-1h"} {
		if _, err := parseCutoff(bad, now); err == nil {
			t.Fatalf("%q must fail", bad)
		}
	}
}

func TestHookLines(t *testing.T) {
	t.Parallel()
	top := hookLines("")
	for _, h := range []string{"pre-commit", "post-checkout", "post-merge", "post-rewrite"} {
		if len(top[h]) == 0 {
			t.Fatalf("missing %s", h)
		}
		if !strings.Contains(top[h][0], "command -v "+BinaryName) {
			t.Fatalf("%s must be a no-op without lore installed: %s", h, top[h][0])
		}
		if strings.Contains(top[h][0], "cd ") {
			t.Fatalf("no cd at toplevel: %s", top[h][0])
		}
	}
	if !strings.Contains(top["post-merge"][0], "|| true") {
		t.Fatal("refresh hooks must never fail git")
	}
	if !strings.Contains(top["pre-commit"][0], "|| exit $?") {
		t.Fatal("pre-commit must be able to block")
	}
	if !strings.Contains(top["pre-commit"][0], "if "+BinaryName+" sync hook --help") {
		t.Fatal("pre-commit must skip a lore too old to have `sync hook`, or it blocks every commit")
	}
	sub := hookLines("services/api")
	if !strings.Contains(sub["pre-commit"][0], `cd "services/api"`) {
		t.Fatalf("subdir project must cd: %s", sub["pre-commit"][0])
	}
}

// commit-msg carries two lines from two features: the Lore-Task trailer
// (F9, never fails a commit) and then the F16 leak check over the final
// message (may refuse). Both must survive, in that order, at the top level
// and in a monorepo sub-project.
func TestHookLinesCommitMsgCarriesTrailerThenLeakCheck(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{"", "services/api"} {
		lines := hookLines(rel)[hookCommitMsg]
		if len(lines) != 2 {
			t.Fatalf("%q: commit-msg has %d lines, want trailer + leak check: %q", rel, len(lines), lines)
		}
		if !strings.Contains(lines[0], "task hook commit-msg") || !strings.HasSuffix(lines[0], "|| true") {
			t.Fatalf("%q: first line must add the trailer and never fail: %s", rel, lines[0])
		}
		if !strings.Contains(lines[1], "check leaks --staged --commit-msg") || !strings.HasSuffix(lines[1], "|| exit $?") {
			t.Fatalf("%q: second line must run the leak check and be able to refuse: %s", rel, lines[1])
		}
		if !strings.Contains(lines[1], BinaryName+" check leaks --help") {
			t.Fatalf("%q: leak check must probe so an older lore never refuses a commit: %s", rel, lines[1])
		}
	}
}

func TestGitAttributeLines(t *testing.T) {
	t.Parallel()
	joined := strings.Join(gitAttributeLines, "\n")
	for _, want := range []string{"merge=" + mergeDriverLore, "eol=lf", "linguist-generated=true", loreMDRel + " ", "merge=" + mergeDriverKeepOurs} {
		if !strings.Contains(joined, want) {
			t.Fatalf("attributes missing %q:\n%s", want, joined)
		}
	}
	cfg := mergeDriverConfig()
	found := false
	for _, kv := range cfg {
		if kv[0] == "merge."+mergeDriverLore+".driver" && strings.Contains(kv[1], "merge-driver %O %A %B %P") {
			found = true
		}
	}
	if !found {
		t.Fatalf("driver config wrong: %v", cfg)
	}
}

func TestBrokenLoreFile(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		`{"_v":1,"body":"fine"}`: "",
		`{bad`:                   "invalid JSON",
		"<<<<<<< HEAD\n{}\n=======\n{}\n>>>>>>> x\n": "conflict markers",
	} {
		if got := brokenLoreFile([]byte(in)); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
	inValue := `{"_v":1,"body":` + jsonQuote(merge3.MarkConflict("a", "b")) + `}`
	if got := brokenLoreFile([]byte(inValue)); !strings.HasPrefix(got, "conflict markers in body") {
		t.Fatalf("markers inside a JSON string must be caught: %q", got)
	}
}

func jsonQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "\n", `\n`) + `"`
}

func TestHookMarkerPerProject(t *testing.T) {
	t.Parallel()
	if hookMarker("") != gitBlockMarker || hookMarker(".") != gitBlockMarker {
		t.Fatal("repo-root project keeps the plain marker")
	}
	if hookMarker("a") == hookMarker("b") || hookMarker("svc/api") != gitBlockMarker+":svc/api" {
		t.Fatal("each sub-project needs its own marker")
	}
}

// Runs the real pre-commit line against stand-in lore binaries: one too old
// to know `sync hook` (found migrating a real repo, where it blocked every
// commit of teammates who had not upgraded), and a current one that refuses.
func TestPreCommitLineAgainstOldAndNewLore(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	line := hookLines("")["pre-commit"][0]
	for _, tc := range []struct {
		name, script string
		wantCode     int
		wantStderr   string
	}{
		{"too old", "#!/bin/sh\necho 'unknown command \"sync\"' >&2\nexit 1\n", 0, "upgrade it"},
		{"current, refuses", "#!/bin/sh\n[ \"$3\" = \"--help\" ] && exit 0\necho refused >&2\nexit 3\n", 3, "refused"},
		{"current, accepts", "#!/bin/sh\nexit 0\n", 0, ""},
	} {
		dir := t.TempDir()
		writeExecutable(t, filepath.Join(dir, BinaryName), tc.script)
		cmd := testCmd(t, "sh", "-c", line)
		cmd.Env = []string{"PATH=" + dir + string(os.PathListSeparator) + "/usr/bin:/bin"}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != tc.wantCode || !strings.Contains(stderr.String(), tc.wantStderr) {
			t.Fatalf("%s: exit %d stderr %q", tc.name, code, stderr.String())
		}
	}
}

// writeExecutable writes a stand-in program that the test execs right away.
// It holds syscall.ForkLock while the file is open for writing: a parallel
// test that forks in that window would inherit the write descriptor until its
// child execs, and exec'ing the script then fails with ETXTBSY ("text file
// busy", exit 126). The hook's probe hides that stderr, so the failure read as
// "lore too old" in a loaded full-suite run while the test passed alone.
func writeExecutable(t testing.TB, path, script string) {
	t.Helper()
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, []byte(script), testExecMode)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
}

// The .gitignore exception lore adds re-includes everything under
// .lore/data; OS clutter there must stay ignored, and lore files must not.
func TestDataUnignoreKeepsOSJunkIgnored(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("_*\n.DS_Store\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	changed, still, err := ensureDataNotIgnored(ctx, dir)
	if err != nil || !changed || len(still) != 0 {
		t.Fatalf("re-include: %v %v %v", changed, still, err)
	}
	ignored, err := gitsetup.IgnoredPaths(ctx, dir, []string{
		".lore/data/.DS_Store", ".lore/data/memories/.DS_Store", ".lore/data/memories/._mem_1.json",
		".lore/data/Thumbs.db", ".lore/data/_meta.json", ".lore/data/memories/mem_1.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, junk := range []string{".lore/data/.DS_Store", ".lore/data/memories/.DS_Store", ".lore/data/memories/._mem_1.json", ".lore/data/Thumbs.db"} {
		if _, ok := ignored[junk]; !ok {
			t.Fatalf("%s must stay ignored: %v", junk, ignored)
		}
	}
	for _, real := range []string{".lore/data/_meta.json", ".lore/data/memories/mem_1.json"} {
		if rule, ok := ignored[real]; ok {
			t.Fatalf("%s must be committed, ignored by %s", real, rule)
		}
	}
}
