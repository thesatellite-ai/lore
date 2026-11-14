//go:build journeys && !windows

package journeys

// corrupt_test.go — damaged lore files say exactly what is wrong and how to
// fix it (round 3 track H, I-15). A deleted .lore folder was blamed on a
// sparse checkout and doctor said HEALTHY; a deleted _meta.json went
// unnoticed and was committed; a row file with a wrong or duplicate id was
// committed and then left out of every read; a broken row's show error
// named neither the file nor a next step; a Notepad byte order mark made a
// row vanish.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyDamagedLoreFiles(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	repo := lap.Clone(w.Remote("github.com/sam-dev/damaged-app"), "damaged-app")
	var memFile, memID string

	w.Step("day1/notes", func() {
		repo.WriteFile("main.go", "package main\n")
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "the cache warms on boot").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "retries back off").OK(t)
		repo.CommitAll("lore: adopt")
		files, err := filepath.Glob(filepath.Join(repo.Dir, ".lore", "data", "memories", "*.json"))
		if err != nil || len(files) != 2 {
			t.Fatalf("memory files %v: %v", files, err)
		}
		memFile, memID = files[0], strings.TrimSuffix(filepath.Base(files[0]), ".json")
	})

	w.Step("day2/deleted-lore-folder-is-named-not-sparse", func() {
		w.AdvanceDays(1)
		if err := os.RemoveAll(filepath.Join(repo.Dir, ".lore")); err != nil {
			t.Fatal(err)
		}
		lap.LoreIn(repo, "memory", "list").Refused(t, "is deleted from the work tree", "checkout HEAD -- .lore").Never(t, "sparse")
		lap.LoreIn(repo, "doctor").Refused(t, "is deleted from the work tree").Never(t, "HEALTHY")
		repo.Git("add", "-A")
		if r := repo.TryGit("commit", "-q", "-m", "tidy"); r.Code == 0 || !strings.Contains(r.Out(), "refusing to commit the deletion of the lore project folder .lore") {
			t.Fatalf("the deletion of .lore was committed: %s", r)
		}
		repo.Git("checkout", "HEAD", "--", ".lore")
		lap.LoreIn(repo, "memory", "list").OK(t).Says(t, "the cache warms on boot")
	})

	w.Step("day3/deleted-meta-is-flagged-and-restored", func() {
		w.AdvanceDays(1)
		meta := filepath.Join(repo.Dir, ".lore", "data", "_meta.json")
		if err := os.Remove(meta); err != nil {
			t.Fatal(err)
		}
		lap.LoreIn(repo, "doctor").Refused(t, "project_marker", "_meta.json is deleted", "checkout HEAD -- .lore/data/_meta.json")
		lap.LoreIn(repo, "doctor", "--fix", "--yes").Says(t, "_meta.json was deleted from the work tree; put it back from HEAD")
		if _, err := os.Stat(meta); err != nil {
			t.Fatalf("doctor --fix did not restore _meta.json: %v", err)
		}
	})

	w.Step("day4/wrong-id-is-explained-and-not-committed", func() {
		w.AdvanceDays(1)
		b, err := os.ReadFile(memFile)
		if err != nil {
			t.Fatal(err)
		}
		bad := memID[:len(memID)-4] + "ffff"
		jWrite(t, memFile, strings.Replace(string(b), memID, bad, 1))
		lap.LoreIn(repo, "memory", "show", "--id", memID).Refused(t, memID+".json", "not the one its file name says", "checkout -- .lore/data/memories/"+memID+".json").Never(t, "hand edit refused", "internal error")
		repo.Git("add", "-A")
		if r := repo.TryGit("commit", "-q", "-m", "tidy notes"); r.Code == 0 || !strings.Contains(r.Out(), "but its file name says "+memID) {
			t.Fatalf("a row file with a wrong id was committed: %s", r)
		}
		repo.Git("checkout", "HEAD", "--", ".lore")
	})

	w.Step("day5/notepad-bom-is-read-and-stripped", func() {
		w.AdvanceDays(1)
		b, err := os.ReadFile(memFile)
		if err != nil {
			t.Fatal(err)
		}
		jWrite(t, memFile, "\xef\xbb\xbf"+string(b))
		lap.LoreIn(repo, "memory", "list").OK(t).Says(t, "the cache warms on boot").Never(t, "invalid-file")
		lap.LoreIn(repo, "doctor", "--fix", "--yes").Says(t, "removed the UTF-8 byte order mark")
		if got, _ := os.ReadFile(memFile); strings.HasPrefix(string(got), "\xef\xbb\xbf") {
			t.Fatal("doctor --fix left the byte order mark")
		}
	})
}
