package host

// mergedriver_test.go — `lore merge-driver` as git calls it (B11 port of
// saas/cmd/cli sync_e2e_test.go TestE2E_SameRowMergeDriverAndConflict and
// TestE2E_NoDriverIsVisibleTextConflict): the command on its own over
// three files (the routing matrix of the file comment), then a real
// `git merge` of two branches with merge.lore.driver pointing at this test
// binary, with and without the driver configured.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// envMergeDriverHelper makes this test binary act as `lore merge-driver`
// (TestHelperMergeDriver), so a real git merge can call the driver.
const envMergeDriverHelper = "LORE_TEST_MERGE_DRIVER_HELPER"

var (
	mdT0     = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	mdActorA = mdMust(ids.Parse[ids.Actor]("act_01936f0000007a1b8c2d3e4f5a6b7c8d"))
	mdActorB = mdMust(ids.Parse[ids.Actor]("act_01936f0000007a1b8c2d3e4f5a6b7c8e"))
	mdRowID  = mdMust(ids.ParseAny("mem_01936f3300007d4e8f5a6b7c8d9e0f1a"))
)

func mdMust[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// memRow encodes a live memories row at wall offset ms by actor, with the
// given kind and body.
func memRow(t *testing.T, ms int, actor ids.ID[ids.Actor], kind, body string) []byte {
	t.Helper()
	r := row.Row{
		Table: schema.TableMemories, ID: mdRowID,
		HLC:       hlc.HLC{Wall: mdT0.Add(time.Duration(ms) * time.Millisecond), Actor: actor},
		CreatedAt: mdT0, CreatedBy: mdActorA,
		Fields: map[string]value.Value{"kind": mdMust(value.TextOf(kind)), "body": mdMust(value.TextOf(body))},
	}
	b, err := row.Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// runDriver writes base/ours/theirs to files and runs the command; it
// returns the merged %A, stderr and the command's error.
func runDriver(t *testing.T, repoPath string, base, ours, theirs []byte, flags ...string) ([]byte, string, error) {
	t.Helper()
	dir := t.TempDir()
	paths := map[string][]byte{"base": base, "ours": ours, "theirs": theirs}
	for name, b := range paths {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stderr bytes.Buffer
	cmd := MergeDriverCommand(&stderr)
	args := append(append([]string{}, flags...), filepath.Join(dir, "base"), filepath.Join(dir, "ours"), filepath.Join(dir, "theirs"))
	if repoPath != "" {
		args = append(args, repoPath)
	}
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.ExecuteContext(context.Background())
	out, rerr := os.ReadFile(filepath.Join(dir, "ours"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	return out, stderr.String(), err
}

func decodeMem(t *testing.T, b []byte) (kind, body string) {
	t.Helper()
	r, err := row.Decode(b)
	if err != nil {
		t.Fatalf("merged file is not a row: %v\n%s", err, b)
	}
	return r.Fields["kind"].String(), r.Fields["body"].String()
}

// TestMergeDriverCommand is the routing matrix: row field merge, text
// clash (markers, exit 1), both sides changing a non-text field (newer
// wins, said on stderr), a non-row side (git's line merge, clean and
// conflicting), --keep-ours, a folder file, and the argument count.
func TestMergeDriverCommand(t *testing.T) {
	if _, err := exec.LookPath(gitProgram); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	const rowPath = ".lore/data/memories/mem_01936f3300007d4e8f5a6b7c8d9e0f1a.json"
	base := memRow(t, 0, mdActorA, "core", "hello")

	t.Run("different fields merge cleanly", func(t *testing.T) {
		out, stderr, err := runDriver(t, rowPath, base, memRow(t, 5, mdActorA, "episodic", "hello"), memRow(t, 6, mdActorB, "core", "hello world"))
		if err != nil {
			t.Fatalf("clean merge failed: %v %s", err, stderr)
		}
		if k, b := decodeMem(t, out); k != "episodic" || b != "hello world" {
			t.Fatalf("driver lost a side: kind=%q body=%q", k, b)
		}
	})

	t.Run("same text field clashes with markers and exit 1", func(t *testing.T) {
		out, stderr, err := runDriver(t, rowPath, base, memRow(t, 5, mdActorA, "core", "main body"), memRow(t, 6, mdActorB, "core", "feature body"))
		var st ExitStatus
		if !errors.As(err, &st) || st != 1 {
			t.Fatalf("a text clash must exit 1 with no error line, got %v", err)
		}
		_, b := decodeMem(t, out)
		if !merge.HasMarkers(b) || !strings.Contains(b, "main body") || !strings.Contains(b, "feature body") {
			t.Fatalf("both sides must stay inside markers: %q", b)
		}
		if !strings.Contains(stderr, rowPath+" needs a manual merge") {
			t.Fatalf("stderr must name the file: %q", stderr)
		}
	})

	t.Run("both change a choice field: newer wins and says so", func(t *testing.T) {
		out, stderr, err := runDriver(t, rowPath, base, memRow(t, 5, mdActorA, "episodic", "hello"), memRow(t, 9, mdActorB, "semantic", "hello"))
		if err != nil {
			t.Fatalf("a choice clash is no conflict: %v", err)
		}
		if k, _ := decodeMem(t, out); k != "semantic" {
			t.Fatalf("the newer edit must win: kind=%q", k)
		}
		if !strings.Contains(stderr, "both sides changed kind; kept the newer edit") {
			t.Fatalf("a lost edit must be named on stderr: %q", stderr)
		}
	})

	t.Run("non-row side falls back to a line merge (clean)", func(t *testing.T) {
		b := []byte("one\ntwo\nthree\nfour\nfive\n")
		out, _, err := runDriver(t, ".lore/data/notes.txt", b, []byte("ONE\ntwo\nthree\nfour\nfive\n"), []byte("one\ntwo\nthree\nfour\nFIVE\n"))
		if err != nil || string(out) != "ONE\ntwo\nthree\nfour\nFIVE\n" {
			t.Fatalf("line merge: %v %q", err, out)
		}
	})

	t.Run("non-row side falls back to a line merge (conflict)", func(t *testing.T) {
		out, stderr, err := runDriver(t, ".lore/data/notes.txt", []byte("a\n"), []byte("ours\n"), []byte("theirs\n"))
		var st ExitStatus
		if !errors.As(err, &st) || st != 1 {
			t.Fatalf("a line conflict must exit 1, got %v", err)
		}
		if !bytes.Contains(out, []byte("<<<<<<< ours")) || !bytes.Contains(out, []byte("theirs")) || !strings.Contains(stderr, "line conflict markers") {
			t.Fatalf("line markers missing: %q %q", out, stderr)
		}
	})

	t.Run("keep-ours leaves ours as it is", func(t *testing.T) {
		ours := []byte("# LORE.md ours\n")
		out, _, err := runDriver(t, ".lore/LORE.md", []byte("# base\n"), ours, []byte("# theirs\n"), "--keep-ours")
		if err != nil || !bytes.Equal(out, ours) {
			t.Fatalf("keep-ours: %v %q", err, out)
		}
	})

	t.Run("folder file is merged as a folder file", func(t *testing.T) {
		_, _, err := runDriver(t, ".lore/data/_meta.json", []byte("{}"), []byte("not json"), []byte("{}"))
		if err == nil || !strings.Contains(err.Error(), "_meta.json is not a JSON object") {
			t.Fatalf("a folder file must go through the folder merge (its refusal names the file): %v", err)
		}
	})

	t.Run("too few arguments", func(t *testing.T) {
		cmd := MergeDriverCommand(&bytes.Buffer{})
		cmd.SetArgs([]string{"a", "b"})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil {
			t.Fatal("two arguments must be refused")
		}
	})
}

// TestHelperMergeDriver is not a test: under envMergeDriverHelper it is the
// merge driver git runs (args after "--"), exiting with the command's code.
func TestHelperMergeDriver(t *testing.T) {
	if os.Getenv(envMergeDriverHelper) != "1" {
		t.Skip("helper process for TestMergeDriverThroughGit")
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	cmd := MergeDriverCommand(os.Stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	var st ExitStatus
	switch {
	case err == nil:
		os.Exit(0)
	case errors.As(err, &st):
		os.Exit(int(st))
	default:
		_, _ = os.Stderr.WriteString(err.Error() + "\n") // the exit code still tells git
		os.Exit(2)
	}
}

// TestMergeDriverThroughGit runs real merges of a row file: with
// merge.lore.driver set, two branches editing different fields merge
// cleanly and the same text field stops the merge with markers inside the
// JSON value; without the driver (GitHub knows no custom drivers) the same
// clash is a plain text conflict a person can settle by taking a side.
func TestMergeDriverThroughGit(t *testing.T) {
	if _, err := exec.LookPath(gitProgram); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const rel = ".lore/data/memories/mem_01936f3300007d4e8f5a6b7c8d9e0f1a.json"
	setup := func(t *testing.T, withDriver bool) (dir string, git func(...string) (string, error)) {
		t.Helper()
		dir = t.TempDir()
		env := append(os.Environ(), envMergeDriverHelper+"=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		git = func(args ...string) (string, error) {
			c := exec.Command(gitProgram, args...)
			c.Dir, c.Env = dir, env
			out, err := c.CombinedOutput()
			return string(out), err
		}
		must := func(args ...string) {
			if out, err := git(args...); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		must("init", "-q", "-b", "main")
		must("config", "user.email", "t@example.com")
		must("config", "user.name", "t")
		must("config", "commit.gpgsign", "false")
		if withDriver {
			must("config", "merge.lore.name", "lore row merge")
			must("config", "merge.lore.driver", "'"+self+"' -test.run=^TestHelperMergeDriver$ -- %O %A %B %P")
		}
		write := func(name string, b []byte) {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		write(".gitattributes", []byte(".lore/data/** merge=lore\n"))
		write(rel, memRow(t, 0, mdActorA, "core", "hello"))
		must("add", "-A")
		must("commit", "-qm", "base")
		return dir, git
	}
	branchEdit := func(t *testing.T, dir string, git func(...string) (string, error), feat, main []byte) (string, error) {
		t.Helper()
		step := func(args ...string) {
			if out, err := git(args...); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		step("checkout", "-q", "-b", "feat")
		if err := os.WriteFile(filepath.Join(dir, rel), feat, 0o600); err != nil {
			t.Fatal(err)
		}
		step("commit", "-qam", "feat")
		step("checkout", "-q", "main")
		if err := os.WriteFile(filepath.Join(dir, rel), main, 0o600); err != nil {
			t.Fatal(err)
		}
		step("commit", "-qam", "main")
		return git("merge", "--no-edit", "feat")
	}
	read := func(t *testing.T, dir string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("driver: different fields merge", func(t *testing.T) {
		dir, git := setup(t, true)
		if out, err := branchEdit(t, dir, git, memRow(t, 6, mdActorB, "core", "hello world"), memRow(t, 5, mdActorA, "episodic", "hello")); err != nil {
			t.Fatalf("different fields must merge cleanly: %v %s", err, out)
		}
		if k, b := decodeMem(t, read(t, dir)); k != "episodic" || b != "hello world" {
			t.Fatalf("driver lost a side: kind=%q body=%q", k, b)
		}
	})

	t.Run("driver: same field conflicts inside the value", func(t *testing.T) {
		dir, git := setup(t, true)
		out, err := branchEdit(t, dir, git, memRow(t, 6, mdActorB, "core", "feature body"), memRow(t, 5, mdActorA, "core", "main body"))
		if err == nil {
			t.Fatalf("a same-field edit must conflict: %s", out)
		}
		if !strings.Contains(out, "needs a manual merge (markers are inside the JSON values)") {
			t.Fatalf("git must show the driver's line: %s", out)
		}
		_, b := decodeMem(t, read(t, dir)) // still one JSON row, markers in the value
		if !merge.HasMarkers(b) || !strings.Contains(b, "main body") || !strings.Contains(b, "feature body") {
			t.Fatalf("markers missing from the value: %q", b)
		}
	})

	t.Run("no driver: a visible text conflict, settled by taking a side", func(t *testing.T) {
		dir, git := setup(t, false)
		theirs := memRow(t, 6, mdActorB, "core", "theirs")
		if out, err := branchEdit(t, dir, git, theirs, memRow(t, 5, mdActorA, "core", "ours")); err == nil {
			t.Fatalf("expected a text conflict without the driver: %s", out)
		}
		if b := read(t, dir); !bytes.Contains(b, []byte("<<<<<<<")) {
			t.Fatalf("git's own line markers expected: %s", b)
		}
		if out, err := git("checkout", "--theirs", "--", ".lore/data"); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		if _, b := decodeMem(t, read(t, dir)); b != "theirs" {
			t.Fatalf("taken side not in the file: %q", b)
		}
	})
}
