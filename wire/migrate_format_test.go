package wire

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// TestMigrateRecordsFormat (FP-3, D14): the run that rewrites the rows
// records format 2, who ran it and when in _meta.json and leaves the
// signpost for lore 0.1.17 beside the rows, in the same change set (the
// data folder's files the person commits); a second run changes nothing;
// rollback takes both back with the rows.
func TestMigrateRecordsFormat(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	v1Row(t, repo, schema.TableMemories, journalMemID, `"project_id":"`+projectID(t, a)+`","body":"hello"`)
	metaPath := filepath.Join(repo, filepath.FromSlash(rowfs.RepoMetaPath))
	if _, err := os.Stat(metaPath); err != nil { // what lore 0.1.17's init left
		if err := os.WriteFile(metaPath, []byte(`{"_v": 1, "project_id": "`+projectID(t, a)+`"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if pf, err := ProjectFormatAt(repo); err != nil || pf.Format != rowfs.ProjectFormatV017 {
		t.Fatalf("before: %+v %v", pf, err)
	}
	sign := filepath.Join(repo, rowfs.LoreDir, rowfs.DataDir, rowfs.SignpostFile)

	raw, err := run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	out := migrateOut(t, raw)
	pf, err := ProjectFormatAt(repo)
	if err != nil || !pf.Recorded || pf.Format != rowfs.ProjectFormatCurrent || pf.MigratedAt == "" {
		t.Fatalf("after: %+v %v", pf, err)
	}
	if b, rerr := os.ReadFile(sign); rerr != nil || string(b) != rowfs.SignpostText {
		t.Fatalf("signpost: %q %v", b, rerr)
	}
	if !strings.Contains(strings.Join(out.Journal, "\n"), "format: the lore 0.1.18 format (format 2), recorded in") {
		t.Errorf("the run does not say it raised the format: %v", out.Journal)
	}
	if n := uncommittedData(t, repo); !strings.Contains(n, "_meta.json") || !strings.Contains(n, rowfs.SignpostFile) || !strings.Contains(n, journalMemID) {
		t.Errorf("rows, format and signpost are not one change set:\n%s", n)
	}
	meta := string(mustRead(t, filepath.Join(repo, filepath.FromSlash(rowfs.RepoMetaPath))))

	_, err = run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if again := string(mustRead(t, filepath.Join(repo, filepath.FromSlash(rowfs.RepoMetaPath)))); again != meta {
		t.Errorf("a second run changed _meta.json:\n%s\nwas\n%s", again, meta)
	}

	if _, err = run(t, a, opsmigrate.OpRollback, `{}`, false); err != nil {
		t.Fatal(err)
	}
	if pf, _ := ProjectFormatAt(repo); pf.Recorded || pf.Format != rowfs.ProjectFormatV017 {
		t.Errorf("after rollback: %+v", pf)
	}
	if _, serr := os.Stat(sign); serr == nil {
		t.Error("rollback left the signpost")
	}
}

// uncommittedData is git status of .lore/data in repo.
func uncommittedData(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all", "--", ".lore/data")
	cmd.Dir = repo
	cmd.Env = gitenv.Clean(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	return string(out)
}
