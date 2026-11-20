//go:build stories && !windows

package stories

// O_personal_writenote_test.go — story O, devco D-769 (bug hunt H-28, a
// follow-up of H-16): a person writes notes to a folder of their personal
// store (`--to me/<folder>`) from a repo set up with lore that does not
// attach that folder. The write's note told them to run `lore store add
// --name <name> --url <store-url> --folder <folder>`; following it commits
// the personal store's machine-local url into the repo's shared
// .lore/stores.yaml. The note now says the folder is the personal store's,
// names the read of the kind written with `--store me/<folder>`, and that
// read, run exactly where it was printed, finds the row; stores.yaml is
// never touched.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStoryOPersonalWriteNote: a `--to me/<folder>` write in a repo names
// the personal read, never `lore store add` (devco D-769).
func TestStoryOPersonalWriteNote(t *testing.T) {
	s := New(t, "O")
	mine := s.Remote("github.com/pn-writer/lore-me")
	web := s.Seed(s.Remote("github.com/pn-writer/web"), func(dir string) { PlainRepo(t, dir) })
	ana := s.Person("Ana Brook", "ana@pn-writer.test")
	home := t.TempDir()

	s.Step("personal-folder-write-names-the-personal-read", func() {
		ana.Lore(home, "store", "init", "--name", "me", "--url", mine.SSHURL(), "--yes").OK(t)
		wc := ana.Clone(web, "web")
		ana.LoreIn(wc, "init").OK(t)
		storesYAML := filepath.Join(wc.Dir, ".lore", "stores.yaml")
		before, _ := os.ReadFile(storesYAML)
		for _, w := range []struct{ write, read []string }{
			{[]string{"memory", "add", "--body", "heron vpn needs the second factor", "--to", "me/notes", "--create"}, []string{"memory", "list", "--store", "me/notes"}},
			{[]string{"task", "add", "--title", "heron rotate the vpn key", "--to", "me/notes", "--yes"}, []string{"task", "list", "--store", "me/notes"}},
		} {
			r := ana.LoreIn(wc, w.write...)
			printed := "lore " + strings.Join(w.read, " ")
			if r.Code != 0 || !strings.Contains(r.Out(), "me/notes is a folder of your personal store") || !strings.Contains(r.Out(), ": "+printed) {
				t.Errorf("[O D-769] `lore %s` in a repo does not name the personal read %q:\n%s", strings.Join(w.write, " "), printed, r)
			}
			if strings.Contains(r.Out(), "store add") || strings.Contains(r.Out(), "<store-url>") {
				t.Errorf("[O D-769] `lore %s` tells the person to attach the personal store's machine-local url to the repo:\n%s", strings.Join(w.write, " "), r)
			}
			// the printed read, run exactly where it was printed
			if rr := ana.LoreIn(wc, w.read...); rr.Code != 0 || !strings.Contains(rr.Out(), "heron") {
				t.Errorf("[O D-769] the printed `%s` in the repo misses the row:\n%s", printed, rr)
			}
		}
		after, _ := os.ReadFile(storesYAML)
		if string(before) != string(after) || strings.Contains(string(after), "lore-me") {
			t.Errorf("[O D-769] a --to me/<folder> write changed the repo's stores.yaml:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
}
