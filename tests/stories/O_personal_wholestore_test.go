//go:build stories && !windows

package stories

// O_personal_wholestore_test.go — story O, devco D-756 (bug hunt H-17, P-3
// of the personal knowledge base migration): a person keeps notes in folders
// of their personal store (`--to me/<folder>`), the store on a git repository
// of their own. `lore search --store me` and `lore sql run --store me` (no
// folder) answered nothing, and `lore sql run --store me` in a git repo lore
// is not set up in refused E_NOT_A_STORE, while `--store me/<folder>` found
// the rows: an agent had to ask once per folder. A read naming the whole
// personal store now covers every folder of it, from the home folder, a git
// repo lore is not set up in and a repo set up with lore alike.

import (
	"strings"
	"testing"
)

// TestStoryOPersonalWholeStore: `--store me` reads every folder of the
// personal store, from any folder (devco D-756).
func TestStoryOPersonalWholeStore(t *testing.T) {
	s := New(t, "O")
	mine := s.Remote("github.com/pw-reader/lore-me")
	plain := s.Seed(s.Remote("github.com/pw-reader/scratch"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/pw-reader/web"), func(dir string) { PlainRepo(t, dir) })
	ana := s.Person("Ana Brook", "ana@pw-reader.test")
	home := t.TempDir() // a plain folder, no git
	notes := map[string]string{"alpha": "kestrel alpha note", "beta": "kestrel beta note", "gamma": "kestrel gamma note"}

	s.Step("personal-store-with-three-folders", func() {
		ana.Lore(home, "store", "init", "--name", "me", "--url", mine.SSHURL(), "--yes").OK(t)
		for folder, body := range notes {
			ana.Lore(home, "memory", "add", "--body", body, "--to", "me/"+folder, "--create", "--yes").OK(t)
		}
		ana.Lore(home, "search", "--store", "me/alpha", "--query", "kestrel").OK(t).Says(t, "alpha note")
	})

	s.Step("store-me-reads-every-folder-from-any-folder", func() {
		untouched := ana.Clone(plain, "scratch").Dir // a git repo lore is not set up in
		wc := ana.Clone(web, "web")
		ana.LoreIn(wc, "init").OK(t)
		for _, place := range []struct{ name, dir string }{{"the home folder", home}, {"a git repo lore is not set up in", untouched}, {"a repo set up with lore", wc.Dir}} {
			for _, args := range [][]string{
				{"search", "--store", "me", "--query", "kestrel"},
				{"sql", "run", "--store", "me", "--query", "SELECT source, body FROM memories_all ORDER BY body"},
				{"memory", "list", "--store", "me"},
			} {
				r := ana.Lore(place.dir, args...)
				for folder, body := range notes {
					// a search snippet marks the matched word, so the check is
					// on the words after it
					if r.Code != 0 || !strings.Contains(r.Out(), strings.TrimPrefix(body, "kestrel ")) {
						t.Errorf("[O D-756] `lore %s` in %s misses the note in me/%s:\n%s", strings.Join(args, " "), place.name, folder, r)
					}
				}
			}
			// one folder named still reads that folder alone
			r := ana.Lore(place.dir, "sql", "run", "--store", "me/beta", "--query", "SELECT body FROM memories_all")
			if r.Code != 0 || !strings.Contains(r.Out(), "beta note") || strings.Contains(r.Out(), "alpha note") {
				t.Errorf("[O D-756] `lore sql run --store me/beta` in %s does not read me/beta alone:\n%s", place.name, r)
			}
		}
	})
}
