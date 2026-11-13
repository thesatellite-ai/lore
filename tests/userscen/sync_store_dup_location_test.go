//go:build userscen

package userscen

// sync_store_dup_location_test.go — two stores.yaml entries that attach the
// SAME store branch (issue #1): `stag` as `<url>?branch=staging` and
// `stag2` as `<url> --branch staging`. Both names read one place, so a read
// across every store must show each row of that place once, labelled with
// the FIRST name in stores.yaml order (`[stag]`), even for a row written
// `--to stag2`; before the fix `memory list` printed each row twice and
// `search` four times. Writes and reads that name `stag2` keep working.
//
// Matrix: list kinds (memory, rule, decision, pattern, task) × the row
// written through each name (stag, stag2) × output (text, --json), plus
// `search` over the same rows in text and --json.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// dupListOut is `lore <kind> list --json`: the rows and each row's store.
type dupListOut struct {
	Data []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Source struct {
			Store string `json:"store"`
		} `json:"_source"`
	} `json:"data"`
}

// dupSearchOut is `lore search --json`: the hits and each hit's store.
type dupSearchOut struct {
	Data struct {
		Hits []struct {
			ID     string `json:"id"`
			Entity string `json:"entity"`
			Source struct {
				Store string `json:"store"`
			} `json:"_source"`
		} `json:"hits"`
	} `json:"data"`
}

// dupKind is one list kind: how to add a row with a given text, and
// whether its text list prints the [store] label (pattern list prints the
// id and its tags only).
type dupKind struct {
	name     string
	add      func(text string) []string
	labelled bool
}

var dupKinds = []dupKind{
	{"memory", func(s string) []string { return []string{"memory", "add", s} }, true},
	{"rule", func(s string) []string { return []string{"rule", "add", s} }, true},
	{"decision", func(s string) []string { return []string{"decision", "add", s, "--title", s} }, true},
	{"pattern", func(s string) []string { return []string{"pattern", "add", s, "--title", s} }, false},
	{"task", func(s string) []string { return []string{"task", "add", s, "--commitment", "accepted"} }, true},
}

// dupFirstName is the first stores.yaml name of the shared place: every
// row read across stores is labelled with it.
const dupFirstName = "stag"

func TestSyncStores_DupLocationReadOnce(t *testing.T) {
	st := newSyncTeam(t, 1)
	a := st.clones[0]
	r := st.w.Remote(syncSlugStore)
	a.MustLore("store", "init", "notes", r.URL(), "--yes")
	a.MustLore("store", "init", "stag", r.URL()+"?branch=staging", "--yes")
	attachTwice(t, a, r.URL())

	// every kind gets one row through each name of the shared place, and
	// one in the store of its own (notes) as the control
	writers := []string{"stag", "stag2", "notes"}
	for _, k := range dupKinds {
		for _, to := range writers {
			a.MustLore(append(k.add(dupMarker(k.name, to)), "--to", to)...)
		}
	}
	wantStore := func(to string) string {
		if to == "notes" {
			return "notes"
		}
		return dupFirstName
	}

	for _, k := range dupKinds {
		t.Run(k.name, func(t *testing.T) {
			text := a.MustLore(k.name, "list").Stdout
			got := th.DecodeJSON[dupListOut](t, a.MustLore(k.name, "list", "--json"))
			idOf := map[string]string{} // writer -> the row's id
			for _, to := range writers {
				m := dupMarker(k.name, to)
				// --json: exactly one entry, its _source the first name
				var stores []string
				for _, d := range got.Data {
					if d.Title == m || d.Body == m {
						stores = append(stores, d.Source.Store)
						idOf[to] = d.ID
					}
				}
				if len(stores) != 1 || stores[0] != wantStore(to) {
					t.Fatalf("%s list --json: %q (written --to %s) in stores %v, want once in %s", k.name, m, to, stores, wantStore(to))
				}
				// text: exactly one line holds the row, labelled by the first name
				lines := linesWith(text, idOf[to])
				if len(lines) != 1 {
					t.Fatalf("%s list: %d lines for %s (written --to %s), want 1:\n%s", k.name, len(lines), idOf[to], to, text)
				}
				if label := "[" + wantStore(to) + "]"; k.labelled && !strings.Contains(lines[0], label) {
					t.Fatalf("%s list: line %q, want label %s", k.name, lines[0], label)
				}
			}
			// naming the later store still reads the place through that name
			named := a.MustLore(k.name, "list", "--store", "stag2").Stdout
			for _, to := range []string{"stag", "stag2"} {
				if l := linesWith(named, idOf[to]); len(l) != 1 || (k.labelled && !strings.Contains(l[0], "[stag2]")) {
					t.Fatalf("%s list --store stag2: %v, want one [stag2] line:\n%s", k.name, l, named)
				}
			}
		})
	}

	t.Run("search", func(t *testing.T) {
		const q = "dupmark"
		total := len(dupKinds) * len(writers)
		text := a.MustLore("search", q, "--limit", "100").Stdout
		got := th.DecodeJSON[dupSearchOut](t, a.MustLore("search", q, "--limit", "100", "--json"))
		if len(got.Data.Hits) != total {
			t.Fatalf("search --json: %d hits, want %d (one per row): %+v", len(got.Data.Hits), total, got.Data.Hits)
		}
		seen := map[string]bool{}
		for _, h := range got.Data.Hits {
			if seen[h.ID] {
				t.Fatalf("search --json: %s is a hit twice", h.ID)
			}
			seen[h.ID] = true
			if h.Source.Store != "notes" && h.Source.Store != dupFirstName {
				t.Fatalf("search --json: %s labelled %q, want notes or %s", h.ID, h.Source.Store, dupFirstName)
			}
			if l := linesWith(text, h.ID); len(l) != 1 || !strings.Contains(l[0], "["+h.Source.Store+"]") {
				t.Fatalf("search: %d lines for %s, want one labelled [%s]:\n%s", len(l), h.ID, h.Source.Store, text)
			}
		}
	})
}

// dupMarker is the unique text of the row of kind written --to to (one
// search word, dupmark, finds them all).
func dupMarker(kind, to string) string { return "dupmark " + kind + " via " + to }

// linesWith returns the lines of text that contain s.
func linesWith(text, s string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, s) {
			out = append(out, l)
		}
	}
	return out
}

// attachTwice gives a stores.yaml a second entry, stag2 (url --branch
// staging), for the place stag (url?branch=staging) attaches: `lore store
// add` refuses that now, naming stag, so the entry is written into the
// file the way a stores.yaml from before the refusal (or a hand edit)
// holds it, which must still load.
func attachTwice(t *testing.T, a *th.Clone, url string) {
	t.Helper()
	res := a.Lore("store", "add", "stag2", url, "--branch", "staging", "--yes")
	if res.Code == 0 || !strings.Contains(res.Stderr, "store stag already attaches") || !strings.Contains(res.Stderr, "lore store remove --name stag") {
		t.Fatalf("a second entry for one store branch must be refused, naming stag: code %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	body, ok := a.ReadFile(".lore/stores.yaml")
	if !ok {
		t.Fatal("no .lore/stores.yaml")
	}
	a.WriteFile(".lore/stores.yaml", strings.TrimRight(body, "\n")+"\n    - name: stag2\n      url: "+url+"\n      branch: staging\n")
}
