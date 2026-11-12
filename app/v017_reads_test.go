package app

// v017_reads_test.go — owner report 2026-11-11: `lore task list` in a
// v0.1.17 repo printed "re-included .lore/data in .gitignore — commit it"
// and changed .gitignore. A read never writes into the repo: every read
// command leaves what git sees (status, every tracked and untracked file
// byte for byte) as it was; the doctor names what is missing and the
// command that fixes it; doctor --fix and a write do the rewrite.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A v0.1.17 repo as its users have it (shapes copied from a lore 0.1.17
// checkout): v1 row files, its .gitignore block, its .gitattributes, and
// one rule of the repo's own (`*.json`) that hides the row files.
const (
	v017ReadsProject = "prj_01a1cb57169c7c73a3a5dd7f0e1b8c6d"
	v017ReadsMemory  = "mem_01a1cb5743e371b7bab946c6cf489ddf"
	v017ReadsIgnore  = "# lore\n.lore/lore.db\n.lore/lore.db-shm\n.lore/lore.db-wal\n.lore/lore.db.broken.*\n.lore/state/\n.lore/backups/\n*.json\n"
	v017ReadsAttrs   = "# >>> lore-sync >>>\n.lore/data/**/*.json text eol=lf merge=lore linguist-generated=true\n.lore/data/*.json text eol=lf merge=lore linguist-generated=true\n.lore/LORE.md text eol=lf merge=lore-keep-ours linguist-generated=true\n# <<< lore-sync <<<\n"
)

// v017ReadsFiles are the repo's files (slash path → content).
var v017ReadsFiles = map[string]string{
	".gitignore":            v017ReadsIgnore,
	".gitattributes":        v017ReadsAttrs,
	"README.md":             "the widget service\n",
	".lore/data/_meta.json": `{"_v": 1, "project_id": "` + v017ReadsProject + `"}` + "\n",
	".lore/data/projects/" + v017ReadsProject + ".json": `{"_table": "projects", "_v": 1, "archived_at": null, "created_at": "2026-11-03T10:00:00.000000000Z", "id": "` + v017ReadsProject +
		`", "name": "widgets", "origin_url": null, "updated_at": "2026-11-03T10:00:00.000000000Z"}` + "\n",
	".lore/data/memories/" + v017ReadsMemory + ".json": `{"_table": "memories", "_v": 1, "body": "the widget cache warms at boot", "created_at": "2026-11-03T10:00:01.000000000Z", "id": "` + v017ReadsMemory +
		`", "kind": "retrieved", "project_id": "` + v017ReadsProject + `", "source_kind": "manual", "trust_score": 0.5, "updated_at": "2026-11-03T10:00:01.000000000Z"}` + "\n",
}

// gitView is what git sees of dir: `git status` with every untracked
// file, and the hash of every tracked or untracked (not ignored) file.
// Files git ignores (lore's cache and lock) are not part of it.
func gitView(t *testing.T, w *cliWorld, dir string) string {
	t.Helper()
	status := w.git(t, dir, "status", "--porcelain=v1", "--untracked-files=all")
	files := strings.Split(strings.TrimSpace(w.git(t, dir, "ls-files", "-co", "--exclude-standard")), "\n")
	slices.Sort(files)
	var b strings.Builder
	b.WriteString(status + "\n")
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		b.WriteString(hex.EncodeToString(sum[:]) + "  " + f + "\n")
	}
	return b.String()
}

// TestReadsLeaveAV017RepoAlone: see the file comment.
func TestReadsLeaveAV017RepoAlone(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author().unscoped()
	dir := w.root
	w.git(t, dir, "init", "-q", "-b", "main")
	for p, content := range v017ReadsFiles {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.git(t, dir, "add", "-f", "-A")
	w.git(t, dir, "commit", "-q", "-m", "v0.1.17")
	before := gitView(t, w, dir)
	reads := [][]string{
		{"task", "list"}, {"memory", "list"}, {"show", v017ReadsMemory}, {"memory", "show", v017ReadsMemory},
		{"search", "widget"}, {"whoami"}, {"doctor"}, {"migrate", "status"}, {"sync", "status"}, {"search", "status"},
		{"store", "list"}, {"store", "show"}, {"rule", "list"}, {"mission", "list"}, {"decision", "list"}, {"tag", "list"},
		{"project", "list"}, {"task", "list", "--json"}, {"doctor", "--json"},
	}
	for _, args := range reads {
		code, out, errOut := w.lore(t, dir, args...)
		if code != 0 && args[0] != "doctor" { // doctor exits non-zero for its findings
			t.Fatalf("lore %v: exit %d\n%s\n%s", args, code, out, errOut)
		}
		if after := gitView(t, w, dir); after != before {
			t.Fatalf("lore %v changed the repo:\nbefore:\n%s\nafter:\n%s\nstderr:\n%s", args, before, after, errOut)
		}
		if strings.Contains(errOut, "commit it") {
			t.Fatalf("lore %v asked for a commit of a file it must not have written:\n%s", args, errOut)
		}
	}
	// the reads said what is wrong and how to fix it
	_, out, errOut := w.lore(t, dir, "doctor")
	for _, want := range []string{"`lore doctor --fix` re-includes them in .gitignore", ".gitignore does not list lore's local files (", "`lore doctor --fix` adds them"} {
		if !strings.Contains(out+errOut, want) {
			t.Errorf("doctor does not say %q:\n%s\n%s", want, out, errOut)
		}
	}
	// doctor --fix does the rewrite the reads left alone
	w.lore(t, dir, "doctor", "--fix")
	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"!.lore/data/", ".lore/lock"} {
		if !strings.Contains(string(gi), want) {
			t.Errorf("doctor --fix did not add %q to .gitignore:\n%s", want, gi)
		}
	}
	if !strings.HasPrefix(string(gi), v017ReadsIgnore) {
		t.Errorf("doctor --fix rewrote the person's own lines:\n%s", gi)
	}
}
