//go:build journeys && !windows

package journeys

// rowcite_build_paths_test.go — devco:D-1021 (bug hunt H-283): a note
// citing ${row:<id>} is found by `lore search` through the cited note's
// words (the filled-in text), and that answer must not depend on how the
// cache was built. A note written in me/archive citing a me/work note was
// found that way only after the cache was built from nothing: the write
// opened me/archive alone and filled in nothing. And a citation whose
// note went to the trash kept no line to find it again, so `lore trash
// restore` left it unfilled until `lore search rebuild`. Here a seeded
// run of writes, edits, deletes, restores and moves of notes and citing
// notes across three folders of a personal store compares every search
// answer, and the filled-in text and reference lines of every folder,
// with those of a cache built from nothing.

import (
	"fmt"
	"math/rand/v2"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestJourneyRowCitationsAnswerTheSameEveryBuild: search answers and
// row_expanded/row_refs from the cache built write by write equal those of
// a cache built from nothing, through random writes of notes and their
// citers (devco:D-1021).
func TestJourneyRowCitationsAnswerTheSameEveryBuild(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	memID := regexp.MustCompile(`"id": "(mem_[0-9a-f]{32})"`)
	folders := []string{"work", "archive", "notes"}
	words := []string{"gateway", "proxy", "ledger", "orbit", "widget"}
	reads := [][]string{
		{"sql", "--store", "me", "--query", "SELECT entity_table, entity_id, field, text FROM row_expanded ORDER BY 1, 2, 3", "--json"},
		{"sql", "--store", "me", "--query", "SELECT entity_table, entity_id, field, ref_id FROM row_refs ORDER BY 1, 2, 3, 4", "--json"},
	}
	for _, q := range words {
		reads = append(reads, []string{"search", "--query", q, "--json"})
	}
	answers := func() []string {
		t.Helper()
		var out []string
		for _, args := range reads {
			out = append(out, lap.Lore(lap.Home, args...).OK(t).Stdout)
		}
		return out
	}
	// compare reads the answers of the cache the writes built, then of a
	// cache built from nothing, then puts the first cache back so the
	// writes go on building it row by row.
	compare := func(step string) {
		t.Helper()
		before := answers()
		cache, keep := lap.LorePath("cache"), lap.LorePath("cache.kept")
		if err := os.Rename(cache, keep); err != nil {
			t.Fatal(err)
		}
		fresh := answers()
		if err := os.RemoveAll(cache); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(keep, cache); err != nil {
			t.Fatal(err)
		}
		for i := range reads {
			if before[i] != fresh[i] {
				t.Errorf("[D-1021] %s: lore %v answers differently from a cache built from nothing:\nwrite by write: %s\nfrom nothing:   %s", step, reads[i], before[i], fresh[i])
			}
		}
	}

	type note struct {
		id, folder string
		trashed    bool
	}
	var notes []note
	add := func(body, folder string) {
		t.Helper()
		out := lap.Lore(lap.Home, "memory", "add", "--body", body, "--to", "me/"+folder, "--json").OK(t).Stdout
		m := memID.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no id in: %s", out)
		}
		notes = append(notes, note{id: m[1], folder: folder})
	}
	live := func(r *rand.Rand) int {
		var at []int
		for i, n := range notes {
			if !n.trashed {
				at = append(at, i)
			}
		}
		if len(at) == 0 {
			return -1
		}
		return at[r.IntN(len(at))]
	}
	sentence := func(r *rand.Rand) string {
		a, b := words[r.IntN(len(words))], words[r.IntN(len(words))]
		return fmt.Sprintf("The %s talks to the %s every %d s.", a, b, 10+r.IntN(50))
	}

	w.Step("day1/store-and-folders", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		for _, f := range folders {
			lap.Lore(lap.Home, "store", "folder", "add", "me", f).OK(t)
		}
	})
	w.Step("day1/the-two-shapes-of-the-finding", func() {
		add("Gateway timeouts are 30 s.", "work")
		add("See ${row:"+notes[0].id+"} before a deploy.", "archive")
		compare("a me/archive note citing a me/work note")
		lap.Lore(lap.Home, "memory", "delete", "--id", notes[0].id, "--yes").OK(t)
		lap.Lore(lap.Home, "trash", "restore", "--id", notes[0].id, "--yes").OK(t)
		compare("the cited note deleted and restored")
	})
	w.Step("day2/random-writes", func() {
		r := rand.New(rand.NewPCG(1021, 283))
		for i := range 24 {
			switch k := r.IntN(10); {
			case k < 3 || len(notes) < 2:
				add(sentence(r), folders[r.IntN(len(folders))])
			case k < 5:
				if j := live(r); j >= 0 {
					add("Read ${row:"+notes[j].id+"} first.", folders[r.IntN(len(folders))])
				}
			case k < 7:
				if j := live(r); j >= 0 {
					lap.Lore(lap.Home, "memory", "edit", "--id", notes[j].id, "--body", sentence(r)).OK(t)
				}
			case k < 8:
				if j := live(r); j >= 0 {
					lap.Lore(lap.Home, "memory", "delete", "--id", notes[j].id, "--yes").OK(t)
					notes[j].trashed = true
				}
			case k < 9:
				if j := slices.IndexFunc(notes, func(n note) bool { return n.trashed }); j >= 0 {
					lap.Lore(lap.Home, "trash", "restore", "--id", notes[j].id, "--yes").OK(t)
					notes[j].trashed = false
				}
			default:
				if j := live(r); j >= 0 {
					to := folders[r.IntN(len(folders))]
					if to != notes[j].folder {
						lap.Lore(lap.Home, "move", "--id", notes[j].id, "--to", "me/"+to, "--yes").OK(t)
						notes[j].folder = to
					}
				}
			}
			if i%6 == 5 {
				compare(fmt.Sprintf("after random write %d (%s)", i+1, strings.Join(folders, ",")))
			}
		}
	})
}
