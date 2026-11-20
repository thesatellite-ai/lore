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

	// devco D-771 (hunt H-30): a read naming no store points at --store me,
	// so the next call answers. Before it the home folder's refusal named
	// only --store <org>, and a lore repo's empty search said "(no
	// matches)" with no word that the personal store's folders were unread.
	s.Step("plain-read-names-store-me", func() {
		wc := ana.Clone(web, "web-plain")
		ana.LoreIn(wc, "init").OK(t)
		for _, args := range [][]string{
			{"search", "--query", "kestrel"},
			{"sql", "run", "--query", "SELECT body FROM memories_all"},
			{"memory", "list"},
		} {
			r := ana.Lore(home, args...)
			if r.Code == 0 || !strings.Contains(r.Out(), "hint: add --store me to read your personal store") {
				t.Errorf("[O D-771] `lore %s` in the home folder does not name --store me:\n%s", strings.Join(args, " "), r)
			}
			// the printed step, exactly as printed, answers there
			r = ana.Lore(home, append(args, "--store", "me")...)
			if r.Code != 0 || !strings.Contains(r.Out(), "alpha note") {
				t.Errorf("[O D-771] `lore %s --store me` in the home folder misses the notes:\n%s", strings.Join(args, " "), r)
			}
		}
		for _, args := range [][]string{{"search", "--query", "kestrel"}, {"memory", "search", "--query", "kestrel"}} {
			r := ana.LoreIn(wc, args...)
			if r.Code != 0 || !strings.Contains(r.Out(), "(no matches)") || !strings.Contains(r.Out(), "note: your personal store's folders (me/") || !strings.Contains(r.Out(), "add --store me to the same command") {
				t.Errorf("[O D-771] an empty `lore %s` in a lore repo does not say the personal store's folders were not read:\n%s", strings.Join(args, " "), r)
			}
			r = ana.LoreIn(wc, append(args, "--store", "me")...)
			if r.Code != 0 || !strings.Contains(r.Out(), "alpha note") || strings.Contains(r.Out(), "were not read") {
				t.Errorf("[O D-771] `lore %s --store me` in a lore repo misses the notes:\n%s", strings.Join(args, " "), r)
			}
		}
	})
}
