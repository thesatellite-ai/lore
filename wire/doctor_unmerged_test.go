package wire

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorSeesUnmergedLoreFiles (E-8): a merge that stops on a
// modify/delete conflict in .lore/data (no conflict markers) is seen: the
// unmerged lore file is listed, the stopped operation is named, and the
// line gives the way out (abort, lore sync, merge again without the
// merge driver; resolve and continue with it).
func TestDoctorSeesUnmergedLoreFiles(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Ann", "-c", "user.email=ann@example.test"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(repo, ".nogit"), "GIT_CONFIG_NOSYSTEM=1")
		_ = cmd.Run() // the merge is expected to stop
	}
	row := filepath.Join(repo, ".lore", "data", "memories", "mem_01a1a59b891c70b789144cd3e711fc31.json")
	if err := os.MkdirAll(filepath.Dir(row), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(row, []byte("{\"body\":\"one\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "side")
	git("rm", "-q", row)
	git("commit", "-qm", "delete")
	git("checkout", "-q", "-")
	if err := os.WriteFile(row, []byte("{\"body\":\"two\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-qam", "edit")
	git("merge", "side")
	files := unmergedLoreFiles(ctx, repo, ".lore/data")
	if len(files) != 1 || !strings.HasSuffix(files[0], "mem_01a1a59b891c70b789144cd3e711fc31.json") {
		t.Fatalf("unmerged lore files %v, want the deleted-and-edited memory", files)
	}
	op := gitOpInProgress(ctx, repo)
	if op != "merge" {
		t.Fatalf("operation %q, want merge", op)
	}
	if got := unmergedLine(op, files, false); !strings.Contains(got, "git merge --abort, then lore sync") {
		t.Fatalf("line without the driver: %q", got)
	}
	if got := unmergedLine(op, files, true); !strings.Contains(got, "git merge --continue") {
		t.Fatalf("line with the driver: %q", got)
	}
}
