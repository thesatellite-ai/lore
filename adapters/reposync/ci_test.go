package reposync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ciRepo is a repository with a lore row file and a README on main, and a
// "feature" branch checked out. edit callbacks change files on each side.
func ciRepo(t *testing.T, onFeature, onMain func(dir string)) string {
	t.Helper()
	dir := gitInitRepo(t)
	run := func(args ...string) {
		t.Helper()
		gitT(t, dir, args...)
	}
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	writeRepoFile(t, dir, ".lore/data/memories/mem_1.json", "{\n  \"body\": \"v1\"\n}\n")
	writeRepoFile(t, dir, "README.md", "# app\n")
	writeRepoFile(t, dir, ".gitattributes", strings.Join(AttributeLines, "\n")+"\n")
	run("add", ".")
	run("commit", "-qm", "base")
	run("checkout", "-q", "-b", "feature")
	if onFeature != nil {
		onFeature(dir)
		run("commit", "-qam", "feature")
	}
	run("checkout", "-q", "main")
	if onMain != nil {
		onMain(dir)
		run("commit", "-qam", "main")
	}
	run("checkout", "-q", "feature")
	return dir
}

func writeRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func editMemory(body string) func(string) {
	return func(dir string) {
		path := filepath.Join(dir, ".lore", "data", "memories", "mem_1.json")
		if err := os.WriteFile(path, []byte("{\n  \"body\": \""+body+"\"\n}\n"), 0o600); err != nil {
			panic(err)
		}
	}
}

func editReadme(text string) func(string) {
	return func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(text), 0o600); err != nil {
			panic(err)
		}
	}
}

func TestRunCIMergeOutcomes(t *testing.T) {
	ctx := context.Background()
	t.Run("up to date", func(t *testing.T) {
		dir := ciRepo(t, editReadme("# feature\n"), nil)
		if res, err := CIMerge(ctx, CIMergeOptions{Dir: dir, Base: "main", DryRun: false}); err != nil || res.Outcome != UpToDate {
			t.Fatalf("%+v %v", res, err)
		}
	})
	t.Run("clean", func(t *testing.T) {
		dir := ciRepo(t, editReadme("# feature\n"), editMemory("main"))
		head := headOf(t, dir)
		if res, err := CIMerge(ctx, CIMergeOptions{Dir: dir, Base: "main", DryRun: false}); err != nil || res.Outcome != Clean {
			t.Fatalf("%+v %v", res, err)
		}
		if headOf(t, dir) != head {
			t.Fatal("a clean outcome must not commit anything")
		}
	})
	t.Run("code conflict", func(t *testing.T) {
		dir := ciRepo(t, editReadme("# feature\n"), editReadme("# main\n"))
		head := headOf(t, dir)
		res, err := CIMerge(ctx, CIMergeOptions{Dir: dir, Base: "main", DryRun: false})
		if err != nil || res.Outcome != NeedsHuman || strings.Join(res.Blocking, ",") != "README.md" || !strings.Contains(res.Reason, "outside lore data") {
			t.Fatalf("%+v %v", res, err)
		}
		if headOf(t, dir) != head {
			t.Fatal("needs-human must leave the branch unchanged")
		}
	})
	t.Run("lore conflict, dry run", func(t *testing.T) {
		dir := ciRepo(t, editMemory("feature"), editMemory("main"))
		head := headOf(t, dir)
		res, err := CIMerge(ctx, CIMergeOptions{Dir: dir, Base: "main", DryRun: true})
		if err != nil || res.Outcome != Merged || res.Commit != "" || strings.Join(res.Merged, ",") != ".lore/data/memories/mem_1.json" {
			t.Fatalf("%+v %v", res, err)
		}
		if headOf(t, dir) != head {
			t.Fatal("--dry-run changed the branch")
		}
	})
	t.Run("dirty work tree", func(t *testing.T) {
		dir := ciRepo(t, editMemory("feature"), editMemory("main"))
		writeRepoFile(t, dir, "scratch.txt", "x")
		if _, err := CIMerge(ctx, CIMergeOptions{Dir: dir, Base: "main", DryRun: false}); err == nil {
			t.Fatal("a dirty checkout must be refused")
		}
	})
	t.Run("not a repository", func(t *testing.T) {
		if _, err := CIMerge(ctx, CIMergeOptions{Dir: t.TempDir(), Base: "main", DryRun: false}); err == nil {
			t.Fatal("outside git must fail")
		}
	})
}

// When lore's driver cannot run (a driver program that does not exist), git leaves the file
// unmerged: the merge must be aborted and nothing committed.
func TestRunCIMergeAbortsWhenDriverFails(t *testing.T) {
	dir := ciRepo(t, editMemory("feature"), editMemory("main"))
	head := headOf(t, dir)
	res, err := CIMerge(context.Background(), CIMergeOptions{Dir: dir, Base: "main", Driver: "lore-not-installed"})
	if err != nil || res.Outcome != NeedsHuman || len(res.Blocking) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if headOf(t, dir) != head {
		t.Fatal("branch moved")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("merge left in progress")
	}
}

func TestCIMergeHelpers(t *testing.T) {
	t.Parallel()
	if got := ciMergeMessage("origin/main", 2); !strings.Contains(got, "origin/main") || !strings.Contains(got, "2 file(s)") {
		t.Fatal(got)
	}
	dir := gitInitRepo(t)
	if got := PathPrefix(dir, dir); got != "" {
		t.Fatalf("root prefix = %q", got)
	}
	sub := filepath.Join(dir, "svc", "api")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if got := PathPrefix(dir, sub); got != "svc/api/" {
		t.Fatalf("subdir prefix = %q", got)
	}
	if got := PathPrefix(dir, t.TempDir()); got != "" {
		t.Fatalf("outside the repo must fall back to the root: %q", got)
	}
}

// Every outcome and every status has a terminal rendering: adding a value
// without printing it would leave the user with no output at all.
func TestPrintersCoverEveryValue(t *testing.T) {
	for _, o := range Outcomes {
		if len(CIMergeLines(CIMergeResult{Outcome: o, Base: "origin/main", Reason: "r", Merged: []string{"a"}, Blocking: []string{"b"}}, false)) == 0 {
			t.Fatalf("outcome %q prints nothing", o)
		}
	}
	for _, s := range syncActionStatuses {
		if len(ActionLines(ActionResult{Status: s, Path: "p", Content: "c", LoreVersion: "v1.0.0", Branches: []string{"main"}})) == 0 {
			t.Fatalf("status %q prints nothing", s)
		}
	}
}
