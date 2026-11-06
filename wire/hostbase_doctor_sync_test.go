package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// Row files the [sync] tests plant (any 32 hex digits form a valid id).
const (
	syncBrokenRow = "memories/mem_01a1a59b891c70b789144cd3e711fd01.json"
	syncMarkedRow = "memories/mem_01a1a59b891c70b789144cd3e711fd02.json"
)

// writeDataFile writes rel (slash path under .lore/data) in repo.
func writeDataFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	p := filepath.Join(repo, rowfs.LoreDir, rowfs.DataDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorSyncSection (E-GAPS-1 G4): the [sync] section of a binary
// without lore.db is filled from the row files and git, never from a
// lore.db counter: a fresh committed project is clean; a merge driver in
// git config, an uncommitted file, a broken row file and a row file with
// conflict markers each show in their field; a gitignored .lore/data
// degrades (a repo store needs a git work tree, so in_git_repo is true in
// every project this binary opens). The JSON never carries the lore.db keys.
// Break-verified: with b.Sync left nil in CoreDoctorBase every case fails
// ("no [sync] section"); with the ignored entry counted as uncommitted the
// gitignored case fails.
func TestDoctorSyncSection(t *testing.T) {
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	gitIn(t, repo, nil, "add", "-A")
	gitIn(t, repo, []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"},
		"commit", "-q", "-m", "init")

	b := doctorBase(t, a)
	s := b.Sync
	if s == nil {
		t.Fatal("no [sync] section in a project")
	}
	if !s.InGitRepo || !s.DataDirPresent || s.TrackedFiles == 0 || s.ProjectFiles != 1 || s.Uncommitted != 0 ||
		s.OpenConflicts != 0 || len(s.FileErrors) != 0 || s.DataIgnored || s.MergeDriverOK || !s.NoLoreDB {
		t.Errorf("fresh committed project: %+v", *s)
	}
	if !hasLine(b.Notes, "merge driver not configured") || b.Status != opsdoctor.StatusHealthy {
		t.Errorf("no merge driver: a note, not a warning: status %s notes %v warnings %v", b.Status, b.Notes, b.Warnings)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err = json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"in_git_repo", "merge_driver_configured", "data_dir_present", "data_dir_gitignored", "tracked_files", "uncommitted_files", "open_conflicts", "project_files"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("json misses %s: %s", k, raw)
		}
	}
	for _, k := range []string{"baselined", "pending_exports", "trash_rows", "errors", "dangling_references", "duplicate_groups"} {
		if _, ok := keys[k]; ok {
			t.Errorf("json carries the lore.db key %s: %s", k, raw)
		}
	}

	gitIn(t, repo, nil, "config", mergeDriverConfigKey, "lore merge-driver %O %A %B %P")
	writeDataFile(t, repo, syncBrokenRow, "not json")
	writeDataFile(t, repo, syncMarkedRow, "<<<<<<< HEAD\n{}\n=======\n{}\n>>>>>>> feat\n")
	s = doctorBase(t, a).Sync
	if s == nil || !s.MergeDriverOK || s.Uncommitted != 2 || s.OpenConflicts != 1 || len(s.FileErrors) != 2 {
		t.Fatalf("driver + two new files (one marked): %+v", s)
	}
	kinds := map[string]string{}
	for _, f := range s.FileErrors {
		kinds[f.Path] = f.Kind
	}
	if kinds[".lore/data/"+syncBrokenRow] != opsdoctor.FileErrorInvalid || kinds[".lore/data/"+syncMarkedRow] != opsdoctor.FileErrorConflict {
		t.Errorf("file error kinds: %+v", s.FileErrors)
	}

	if err = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".lore/data/memories/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b = doctorBase(t, a)
	if b.Sync == nil || !b.Sync.DataIgnored || b.Sync.Uncommitted != 0 || b.Status == opsdoctor.StatusHealthy || !hasLine(b.Warnings, ".lore/data is gitignored") {
		t.Errorf("ignored data: sync %+v status %s warnings %v", b.Sync, b.Status, b.Warnings)
	}

}
