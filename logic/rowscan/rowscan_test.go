package rowscan_test

// Port of saas/cmd/cli secret_scanner_core_test.go (B11) to the core's one
// every-row scan: a row field finding is dropped only by a secret_allows
// row naming the same row, field, rule and hash (any part off keeps it:
// fail closed), an allow row that is deleted, incomplete or out of range
// allows nothing, and deleted rows and secret_rules patterns are never
// scanned.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/rowscan"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
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

const (
	memID   = "mem_01a1a8fe9b48787184aa0b4b6386eef4"
	otherID = "mem_01a1a8fe9b48787184aa0b4b6386eef5"
	allowID = "sal_01a1a8fe9bb77ecb9c16c49daad5e466"
	ruleID  = "srl_01a1a8fe9bb77ecb9c16c49daad5e467"
)

func mustID(t *testing.T, s string) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func text(t *testing.T, s string) value.Text {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// allowRow is a secret_allows row; hash < 0 leaves match_hash out.
func allowRow(t *testing.T, target, field, rule string, hash int64) rowscan.Row {
	t.Helper()
	f := map[string]value.Value{"row_id": text(t, target), "field": text(t, field), "rule_id": text(t, rule)}
	if hash >= 0 {
		f["match_hash"] = value.Int(hash)
	}
	return rowscan.Row{Source: "repo", Row: row.Row{ID: mustID(t, allowID), Fields: f}}
}

func memRow(t *testing.T, id, body string) rowscan.Row {
	t.Helper()
	return rowscan.Row{Source: "repo", Row: row.Row{ID: mustID(t, id), Fields: map[string]value.Value{"body": text(t, body)}}}
}

func TestScanAllowFromCommittedEntry(t *testing.T) {
	t.Parallel()
	h := int64(scan.HashMatch("tok"))
	cases := []struct {
		name  string
		allow []rowscan.Row
		hits  int
	}{
		{"none", nil, 1},
		{"exact", []rowscan.Row{allowRow(t, memID, "body", "w", h)}, 0},
		{"other row", []rowscan.Row{allowRow(t, otherID, "body", "w", h)}, 1},
		{"other field", []rowscan.Row{allowRow(t, memID, "title", "w", h)}, 1},
		{"other rule", []rowscan.Row{allowRow(t, memID, "body", "x", h)}, 1},
		{"other hash", []rowscan.Row{allowRow(t, memID, "body", "w", h+1)}, 1},
		{"no hash", []rowscan.Row{allowRow(t, memID, "body", "w", -1)}, 1},
		{"hash past uint32 (truncates to the real hash)", []rowscan.Row{allowRow(t, memID, "body", "w", h+1<<32)}, 1},
		{"bad row id", []rowscan.Row{allowRow(t, "not-an-id", "body", "w", h)}, 1},
	}
	for _, c := range cases {
		rows := map[row.Table][]rowscan.Row{
			schema.TableMemories:     {memRow(t, memID, "a tok here")},
			schema.TableSecretAllows: c.allow,
		}
		got := rowscan.Scan(wordScanner{"tok"}, rows)
		if len(got) != c.hits {
			t.Errorf("%s: %d hits, want %d", c.name, len(got), c.hits)
			continue
		}
		if c.hits == 1 && (got[0].Finding.Field != "body" || int64(got[0].Finding.MatchHash) != h || got[0].ID.String() != memID || got[0].Table != schema.TableMemories) {
			t.Errorf("%s: hit %+v", c.name, got[0])
		}
	}

	// A deleted allow row allows nothing.
	dead := allowRow(t, memID, "body", "w", h)
	dead.Row.Deleted = true
	if got := rowscan.Scan(wordScanner{"tok"}, map[row.Table][]rowscan.Row{
		schema.TableMemories: {memRow(t, memID, "a tok here")}, schema.TableSecretAllows: {dead},
	}); len(got) != 1 {
		t.Errorf("a deleted allow row allowed a finding: %d hits", len(got))
	}
}

// TestScanSkipsDeletedRowsAndRulePatterns: a tombstone prints nowhere and
// a secret_rules row holds patterns, not values; every text item of a list
// is scanned.
func TestScanSkipsDeletedRowsAndRulePatterns(t *testing.T) {
	t.Parallel()
	dead := memRow(t, memID, "a tok here")
	dead.Row.Deleted = true
	rule := rowscan.Row{Source: "repo", Row: row.Row{ID: mustID(t, ruleID), Fields: map[string]value.Value{"regex": text(t, "tok")}}}
	list, err := value.ListOf(value.KindText, text(t, "one tok"), text(t, "two tok"))
	if err != nil {
		t.Fatal(err)
	}
	live := rowscan.Row{Source: "repo", Row: row.Row{ID: mustID(t, otherID), Fields: map[string]value.Value{"tags": list}}}
	got := rowscan.Scan(wordScanner{"tok"}, map[row.Table][]rowscan.Row{
		schema.TableMemories:    {dead, live},
		schema.TableSecretRules: {rule},
	})
	if len(got) != 2 {
		t.Fatalf("want the 2 list items of the live row only, got %+v", got)
	}
	for _, h := range got {
		if h.ID.String() != otherID {
			t.Fatalf("scanned a skipped row: %+v", h)
		}
	}
}
