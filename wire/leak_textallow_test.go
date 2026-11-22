package wire

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// mapLeakReader serves fixed rows per store and table.
type mapLeakReader map[store.Source]map[row.Table][]row.Row

func (m mapLeakReader) StoreIdentities(context.Context) ([]StoreIdentity, error) { return nil, nil }
func (m mapLeakReader) StoreClass(context.Context, store.Source) (direction.Class, error) {
	return "", nil
}
func (m mapLeakReader) List(_ context.Context, src store.Source, t row.Table) ([]row.Row, error) {
	return m[src][t], nil
}

func testRowID(t *testing.T, prefix string, n int) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny(fmt.Sprintf("%s_019a0000000070008000%012x", prefix, n))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestLeakTextAllowsOnlyFromTheNotesStore (#129, devco:D-906): a text
// allow counts only when it sits in the store of the note it names; an
// allow written anywhere else (a client folder, another store) is never
// read, nor a secret allow, nor a deleted one.
func TestLeakTextAllowsOnlyFromTheNotesStore(t *testing.T) {
	t.Parallel()
	note, other := testRowID(t, "dec", 1), testRowID(t, "dec", 2)
	allow := func(n int, rowID ids.AnyID, rule string, deleted bool) row.Row {
		ref, err := value.RefOf(rowID)
		if err != nil {
			t.Fatal(err)
		}
		txt := func(s string) value.Text {
			v, terr := value.TextOf(s)
			if terr != nil {
				t.Fatal(terr)
			}
			return v
		}
		return row.Row{Table: schema.TableSecretAllows, ID: testRowID(t, "sal", n), Deleted: deleted, Fields: map[string]value.Value{
			textAllowRowID: ref, textAllowRule: txt(rule), textAllowHash: value.Int(42), textAllowReason: txt("approved copy"), textAllowPath: txt("copy.txt")}}
	}
	r := mapLeakReader{
		"acme": {schema.TableSecretAllows: {allow(1, note, leakcheck.TextAllowRule, false), allow(2, other, leakcheck.TextAllowRule, false),
			allow(3, note, "generic-api-key", false), allow(4, note, leakcheck.TextAllowRule, true)}},
		"elsewhere": {schema.TableSecretAllows: {allow(5, other, leakcheck.TextAllowRule, false)}},
	}
	rows := []leakcheck.IndexRow{{ID: note.String(), Table: "decisions", Source: "acme"}, {ID: other.String(), Table: "decisions", Source: "beta"}}
	got, err := leakTextAllows(context.Background(), r, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != testRowID(t, "sal", 1).String() || got[0].RowID != note.String() || got[0].Hash != 42 || got[0].Path != "copy.txt" || got[0].Reason != "approved copy" {
		t.Fatalf("text allows %+v, want only sal 1 (the note's own store, live, rule internal-note)", got)
	}
}

// TestTextAllowFixRunsWherePrinted (#129): the refusal's allow line names
// the file only for a commit or push, quotes a path a shell would split,
// and always carries the note, the passage hash and --reason.
func TestTextAllowFixRunsWherePrinted(t *testing.T) {
	t.Parallel()
	f := leakcheck.Finding{Rule: leakcheck.RuleText, RowID: "dec_1", Path: "copy.txt", Hash: 7}
	if got := textAllowFix(f, LeakWhereCommit); !strings.Contains(got, "lore leaks allow --note dec_1 --file copy.txt --match-hash 7 --reason ") {
		t.Errorf("commit: %q", got)
	}
	f.Path = "my docs/copy.txt"
	if got := textAllowFix(f, LeakWherePush); !strings.Contains(got, `--file "my docs/copy.txt" --match-hash 7`) {
		t.Errorf("push, path with a space: %q", got)
	}
	f.Path = LeakWherePRBody
	if got := textAllowFix(f, LeakWherePRBody); strings.Contains(got, "--file") || !strings.Contains(got, "--note dec_1 --match-hash 7") {
		t.Errorf("PR body: %q", got)
	}
}
