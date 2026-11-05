package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"

	"saas/pkg/aicoder/leakcheck"
)

// wordScanner flags every occurrence of one word under rule "w".
type wordScanner struct{ word string }

func (w wordScanner) Scan(field, text string) []scan.Finding {
	var out []scan.Finding
	for off := 0; ; {
		i := strings.Index(text[off:], w.word)
		if i < 0 {
			return out
		}
		at := off + i
		out = append(out, scan.Finding{RuleID: "w", Field: field, Offset: at, Length: len(w.word), Preview: "x…", MatchHash: scan.HashMatch(w.word)})
		off = at + len(w.word)
	}
}

const testRowID = "mem_01a1a8fe9b48787184aa0b4b6386eef4"

func memFile(body string) []byte {
	return []byte(`{"_v":2,"_table":"memories","_hlc":"2026-11-04T11:43:11.944Z/0000/act_afbe7ed1d969752697eebecaac20e9dc","id":"` + testRowID + `","body":"` + body + `"}`)
}

// TestCoreSecretsAllowFromCommittedEntry: a row field finding is dropped
// only by an entry naming the same row, field, rule and hash; any part
// off keeps it (fail closed). Non-row text never uses an entry.
func TestCoreSecretsAllowFromCommittedEntry(t *testing.T) {
	t.Parallel()
	id, err := ids.ParseAny(testRowID)
	if err != nil {
		t.Fatal(err)
	}
	h := scan.HashMatch("tok")
	exact := scan.Allow{RowID: id, Field: "body", RuleID: "w", MatchHash: h}
	item := leakcheck.Item{Path: ".lore/data/memories/" + testRowID + ".json", Content: memFile("a tok here")}
	cases := []struct {
		name  string
		allow []scan.Allow
		hits  int
	}{
		{"none", nil, 1},
		{"exact", []scan.Allow{exact}, 0},
		{"other field", []scan.Allow{{RowID: id, Field: "title", RuleID: "w", MatchHash: h}}, 1},
		{"other rule", []scan.Allow{{RowID: id, Field: "body", RuleID: "x", MatchHash: h}}, 1},
		{"other hash", []scan.Allow{{RowID: id, Field: "body", RuleID: "w", MatchHash: h + 1}}, 1},
	}
	for _, c := range cases {
		cs := &coreSecrets{sc: wordScanner{"tok"}, allows: c.allow}
		got := cs.scanItem(item)
		if len(got) != c.hits {
			t.Errorf("%s: %d hits, want %d", c.name, len(got), c.hits)
		}
		if c.hits == 1 && (got[0].Field != "body" || got[0].Hash != h) {
			t.Errorf("%s: hit field %q hash %d", c.name, got[0].Field, got[0].Hash)
		}
	}
	cs := &coreSecrets{sc: wordScanner{"tok"}, allows: []scan.Allow{exact}}
	if got := cs.scanItem(leakcheck.Item{Path: "notes.txt", Content: memFile("a tok here")}); len(got) != 1 {
		t.Errorf("a non-row path used an allow entry: %d hits", len(got))
	}
}

// TestAllowOfFile reads a sal_ row file and refuses incomplete ones.
func TestAllowOfFile(t *testing.T) {
	t.Parallel()
	file := func(extra string) []byte {
		return []byte(`{"_v":2,"_table":"secret_allows","_hlc":"2026-11-04T11:43:11.944Z/0000/act_afbe7ed1d969752697eebecaac20e9dc","id":"sal_01a1a8fe9bb77ecb9c16c49daad5e466","row_id":"` + testRowID + `","field":"body","rule_id":"w"` + extra + `}`)
	}
	a, ok := allowOfFile(file(`,"match_hash":` + strconv.Itoa(7)))
	if !ok || a.MatchHash != 7 || a.Field != "body" || a.RuleID != "w" || a.RowID.String() != testRowID {
		t.Fatalf("got %+v ok=%v", a, ok)
	}
	for _, bad := range []string{"", `,"match_hash":-1`, `,"match_hash":4294967296`, `,"match_hash":7,"_deleted":true`} {
		if _, ok := allowOfFile(file(bad)); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}
