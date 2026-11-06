package host

// The v0.1.17 git wiring unit tests (saas/cmd/cli sync_wire_test.go
// TestHookLines, TestHookLinesCommitMsgCarriesTrailerThenLeakCheck,
// TestGitAttributeLines, TestHookMarkerPerProject,
// TestPreCommitLineAgainstOldAndNewLore, TestDataUnignoreKeepsOSJunkIgnored;
// sync_install_git_test.go TestHookLinesNamesAreSortable; saas/pkg/aicoder/
// gitsetup gitsetup_test.go TestEnsureBlock and the core.hooksPath half of
// TestOpen), ported before phase E onto the root wiring (gitwire.go,
// dataignore.go). The hook lines are the old binary's byte for byte, so the
// same promises hold: every hook is a no-op without lore on PATH, refresh
// hooks never fail git, pre-commit and the commit-msg leak check can
// refuse, an older lore is probed so it never blocks a commit, and a
// sub-project gets its own marker and a cd.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fakeLoreMode makes the fake lore executable for the hook's sh.
const fakeLoreMode = 0o700

// projectCd matches a project `cd "<rel>"` (not commit-msg's `cd "$(dirname`).
var projectCd = regexp.MustCompile(`cd "[^$]`)

func TestGitWireHookBlocks(t *testing.T) {
	top := hookBlocks("")
	for _, h := range []string{hookPreCommit, hookPostCheckout, hookPostMerge, hookPostRewrite, hookCommitMsg} {
		b := top[h]
		if len(b.lines) == 0 {
			t.Fatalf("missing %s", h)
		}
		if b.marker != gitBlockMarker {
			t.Fatalf("%s: top-level marker %q, want %q", h, b.marker, gitBlockMarker)
		}
		for _, l := range b.lines {
			if !strings.Contains(l, "command -v "+binaryName) {
				t.Fatalf("%s must be a no-op without lore installed: %s", h, l)
			}
			if projectCd.MatchString(l) {
				t.Fatalf("%s: no cd at the top level: %s", h, l)
			}
		}
	}
	for _, h := range []string{hookPostCheckout, hookPostMerge, hookPostRewrite} {
		if !strings.HasSuffix(top[h].lines[0], "|| true") {
			t.Fatalf("refresh hook %s must never fail git: %s", h, top[h].lines[0])
		}
	}
	pc := top[hookPreCommit].lines[0]
	if !strings.HasSuffix(pc, "|| exit $?") {
		t.Fatalf("pre-commit must be able to block: %s", pc)
	}
	if !strings.Contains(pc, "elif "+binaryName+" sync hook --help") {
		t.Fatalf("pre-commit must skip a lore too old to have `sync hook`: %s", pc)
	}
	sub := hookBlocks("services/api")
	if !strings.Contains(sub[hookPreCommit].lines[0], `cd "services/api" && `) {
		t.Fatalf("a sub-project must cd: %s", sub[hookPreCommit].lines[0])
	}
	// One marker per sub-project; the repo root keeps the plain marker;
	// post-commit and pre-push are repo-wide (the ride-along marker).
	if hookBlocks(".")[hookPreCommit].marker != gitBlockMarker {
		t.Fatal(`"." must keep the plain marker`)
	}
	if a, b := hookBlocks("a")[hookPreCommit].marker, hookBlocks("b")[hookPreCommit].marker; a == b {
		t.Fatalf("two sub-projects share marker %q", a)
	}
	if got := sub[hookPreCommit].marker; got != gitBlockMarker+":services/api" {
		t.Fatalf("sub-project marker %q", got)
	}
	for _, h := range []string{hookPostCommit, hookPrePush} {
		if sub[h].marker != rideHookMarker || len(sub[h].lines) == 0 {
			t.Fatalf("%s must be the repo-wide ride block: %+v", h, sub[h])
		}
	}
	names := make([]string, 0, len(top))
	for h := range top {
		names = append(names, h)
	}
	slices.Sort(names)
	if want := []string{"commit-msg", "post-checkout", "post-commit", "post-merge", "post-rewrite", "pre-commit", "pre-push"}; !slices.Equal(names, want) {
		t.Fatalf("hooks %v, want %v", names, want)
	}
}

func TestGitWireCommitMsgTrailerThenLeakCheck(t *testing.T) {
	for _, rel := range []string{"", "services/api"} {
		lines := hookBlocks(rel)[hookCommitMsg].lines
		if len(lines) != 2 {
			t.Fatalf("%q: commit-msg has %d lines, want trailer + leak check: %q", rel, len(lines), lines)
		}
		if !strings.Contains(lines[0], "task hook commit-msg") || !strings.HasSuffix(lines[0], "|| true") {
			t.Fatalf("%q: the first line adds the trailer and never fails: %s", rel, lines[0])
		}
		if !strings.Contains(lines[1], "check leaks --staged --commit-msg") || !strings.HasSuffix(lines[1], "|| exit $?") {
			t.Fatalf("%q: the second line runs the leak check and can refuse: %s", rel, lines[1])
		}
		if !strings.Contains(lines[1], binaryName+" check leaks --help") {
			t.Fatalf("%q: the leak check probes first, so an older lore never refuses: %s", rel, lines[1])
		}
	}
}

func TestGitWireAttributesAndDriverConfig(t *testing.T) {
	joined := strings.Join(gitAttributeLines, "\n")
	for _, want := range []string{"merge=" + mergeDriverLore, "eol=lf", "linguist-generated=true", loreMDRel + " ", "merge=" + mergeDriverKeepOurs} {
		if !strings.Contains(joined, want) {
			t.Fatalf("attributes missing %q:\n%s", want, joined)
		}
	}
	const bin = "'/opt/lore/bin/lore'"
	got := map[string]string{}
	for _, kv := range mergeDriverConfig(bin) {
		got[kv[0]] = kv[1]
	}
	if got["merge."+mergeDriverLore+".driver"] != bin+" merge-driver %O %A %B %P" {
		t.Fatalf("lore driver config: %v", got)
	}
	if got["merge."+mergeDriverKeepOurs+".driver"] != bin+" merge-driver --keep-ours %O %A %B %P" {
		t.Fatalf("keep-ours driver config: %v", got)
	}
}

// TestGitWirePreCommitAgainstOldAndNewLore runs the real pre-commit line
// under sh with a fake lore on PATH: too old (no `sync hook`) passes with
// an "upgrade it" note, a current lore refusing blocks with its exit code,
// a current lore accepting passes, and an old lore facing `_v: 2` rows
// blocks the commit naming the version to install.
func TestGitWirePreCommitAgainstOldAndNewLore(t *testing.T) {
	if runtime.GOOS == "windows" {
		return // the hook line is POSIX sh; git for Windows runs it in its own sh
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Fatalf("no sh on PATH: %v", err)
	}
	line := hookBlocks("")[hookPreCommit].lines[0]
	const v2Row = `{"_v": 2, "id": "mem_1"}` + "\n"
	for _, tc := range []struct {
		name, script string
		v2Rows       bool
		wantCode     int
		wantStderr   string
	}{
		{name: "too old", script: "#!/bin/sh\necho 'unknown command \"sync\"' >&2\nexit 1\n", wantStderr: "upgrade it"},
		{name: "current, refuses", script: "#!/bin/sh\n[ \"$3\" = \"--help\" ] && exit 0\n[ \"$2\" = \"--help\" ] && exit 0\necho refused >&2\nexit 3\n", wantCode: 3, wantStderr: "refused"},
		{name: "current, accepts", script: "#!/bin/sh\nexit 0\n"},
		{name: "old lore, v2 rows", script: "#!/bin/sh\n[ \"$1\" = \"migrate\" ] && exit 1\nexit 0\n", v2Rows: true, wantCode: 1, wantStderr: "install lore 0.1.18 or newer"},
		{name: "old lore, no v2 rows", script: "#!/bin/sh\n[ \"$1\" = \"migrate\" ] && exit 1\nexit 0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			fake := filepath.Join(bin, binaryName)
			if err := os.WriteFile(fake, []byte(tc.script), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(fake, fakeLoreMode); err != nil { // the hook runs it from PATH
				t.Fatal(err)
			}
			work := t.TempDir()
			if tc.v2Rows {
				rows := filepath.Join(work, ".lore", "data", "memories")
				if err := os.MkdirAll(rows, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(rows, "mem_1.json"), []byte(v2Row), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(context.Background(), "sh") // the hook line on stdin: sh runs it as a script
			cmd.Stdin = strings.NewReader(line + "\n")
			cmd.Dir = work
			cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin"}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			code := 0
			var ee *exec.ExitError
			if rerr := cmd.Run(); errors.As(rerr, &ee) {
				code = ee.ExitCode()
			} else if rerr != nil {
				t.Fatal(rerr)
			}
			if code != tc.wantCode || !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Fatalf("exit %d stderr %q, want exit %d with %q", code, stderr.String(), tc.wantCode, tc.wantStderr)
			}
		})
	}
}

// TestGitWireEnsureFileBlock: the block is appended after hand content
// (a missing final newline added), a second run is a no-op, changed lines
// replace the block in place, one block per marker, hand lines kept.
func TestGitWireEnsureFileBlock(t *testing.T) {
	p := filepath.Join(t.TempDir(), gitAttributesFile)
	if err := os.WriteFile(p, []byte("*.png binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if err := ensureFileBlock(p, "lore", []string{"a merge=x"}); err != nil {
		t.Fatal(err)
	}
	first := read()
	st1, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureFileBlock(p, "lore", []string{"a merge=x"}); err != nil {
		t.Fatal(err)
	}
	st2, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if read() != first || !st2.ModTime().Equal(st1.ModTime()) {
		t.Fatal("a second run with the same lines rewrote the file")
	}
	if err := ensureFileBlock(p, "other", []string{"b merge=z"}); err != nil {
		t.Fatal(err)
	}
	if err := ensureFileBlock(p, "lore", []string{"a merge=y"}); err != nil {
		t.Fatal(err)
	}
	s := read()
	if !strings.HasPrefix(s, "*.png binary\n# >>> lore >>>\n") || strings.Count(s, "# >>> lore >>>") != 1 ||
		!strings.Contains(s, "a merge=y") || strings.Contains(s, "merge=x") || !strings.Contains(s, "b merge=z") {
		t.Fatalf("content = %q", s)
	}
}

// TestGitWireHooksDirHonoursHooksPath: hooks go to .git/hooks, or to
// core.hooksPath (relative to the top) when it is set; never repointed.
func TestGitWireHooksDirHonoursHooksPath(t *testing.T) {
	f := newLiveFixture(t)
	defer f.s.release()
	ctx := context.Background()
	dir, err := f.s.hooksDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(f.s.top, ".git", "hooks"); dir != want {
		t.Fatalf("hooks dir %q, want %q", dir, want)
	}
	gitT(t, f.s.top, "config", "core.hooksPath", ".githooks")
	dir, err = f.s.hooksDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(f.s.top, ".githooks"); dir != want {
		t.Fatalf("hooks dir with core.hooksPath %q, want %q", dir, want)
	}
}

// TestGitWireDataUnignoreKeepsOSJunkIgnored: re-including .lore/data never
// re-includes OS junk (.DS_Store, ._ files, Thumbs.db); real rows are
// committed.
func TestGitWireDataUnignoreKeepsOSJunkIgnored(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("_*\n.DS_Store\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, still, err := EnsureDataNotIgnored(context.Background(), dir)
	if err != nil || !changed || len(still) != 0 {
		t.Fatalf("re-include: changed=%v still=%v err=%v", changed, still, err)
	}
	ignored := func(p string) bool {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", "check-ignore", "-q", "--no-index", "--stdin") // the path on stdin: same exit codes
		cmd.Stdin = strings.NewReader(p + "\n")
		cmd.Dir = dir
		err := cmd.Run()
		var ee *exec.ExitError
		switch {
		case err == nil:
			return true
		case errors.As(err, &ee) && ee.ExitCode() == 1:
			return false
		default:
			t.Fatalf("check-ignore %s: %v", p, err)
			return false
		}
	}
	for _, junk := range []string{".lore/data/.DS_Store", ".lore/data/memories/.DS_Store", ".lore/data/memories/._mem_1.json", ".lore/data/Thumbs.db"} {
		if !ignored(junk) {
			t.Errorf("%s must stay ignored", junk)
		}
	}
	for _, row := range []string{".lore/data/_meta.json", ".lore/data/memories/mem_1.json"} {
		if ignored(row) {
			t.Errorf("%s must be committed", row)
		}
	}
}
