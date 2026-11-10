package lint

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fakeSecret is the value the fake scanner matches (not key-shaped).
const fakeSecret = "HUSH-1234"

// fakeScanner finds fakeSecret, rule "hush", hash 7, masked preview.
type fakeScanner struct{}

func (fakeScanner) Scan(field, text string) []scan.Finding {
	if !strings.Contains(text, fakeSecret) {
		return nil
	}
	return []scan.Finding{{RuleID: "hush", Field: field, Line: 1, Preview: "HU…", MatchHash: 7}}
}

// TestSecretFindings (SQ12): text, list items and json are re-scanned;
// a secret_allows row for (row, field, rule, hash) drops exactly its
// finding; secret_rules rows are skipped; no finding carries the value.
func TestSecretFindings(t *testing.T) {
	m1, m2 := id(t, ids.PrefixMemory, 1), id(t, ids.PrefixMemory, 2)
	list, err := value.ListOf(value.KindText, tx(t, "a"), tx(t, "x "+fakeSecret))
	if err != nil {
		t.Fatal(err)
	}
	mem := func(i ids.AnyID, f map[string]value.Value) sourced {
		return sourced{src: store.SourceRepo, r: row.Row{Table: schema.TableMemories, ID: i, Fields: f}}
	}
	seven, err := value.NumberOf("7")
	if err != nil {
		t.Fatal(err)
	}
	allow := sourced{src: store.SourceRepo, r: row.Row{Table: schema.TableSecretAllows, ID: id(t, ids.PrefixMemory, 9), Fields: map[string]value.Value{
		fAllowRowID: tx(t, m2.String()), fAllowField: tx(t, "body"), fAllowRule: tx(t, "hush"), fAllowHash: seven}}}
	all := map[row.Table][]sourced{
		schema.TableMemories: {
			mem(m1, map[string]value.Value{"body": tx(t, "deploy "+fakeSecret), "tags": list}),
			mem(m2, map[string]value.Value{"body": tx(t, fakeSecret), "title": tx(t, fakeSecret)}),
		},
		schema.TableSecretAllows: {allow},
		schema.TableSecretRules:  {mem(id(t, ids.PrefixMemory, 8), map[string]value.Value{"regex": tx(t, fakeSecret)})},
	}
	got := secretFindings(fakeScanner{}, all)
	var where []string
	for _, f := range got {
		if f.Code != SecretFound || strings.Contains(f.Message+f.Fix, fakeSecret) {
			t.Fatalf("bad finding %+v", f)
		}
		where = append(where, f.ID+" "+strings.SplitN(f.Message, " ", 2)[0])
	}
	want := []string{m1.String() + " body", m1.String() + " tags", m2.String() + " title"}
	if strings.Join(where, ",") != strings.Join(want, ",") {
		t.Fatalf("findings at %v, want %v", where, want)
	}
}
