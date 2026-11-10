package lint

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

func id(t *testing.T, p ids.Prefix, n byte) ids.AnyID {
	t.Helper()
	hex := "019a00000000700080000000000000" + "0" + string("0123456789abcdef"[n]) // UUIDv7 shape
	got, err := ids.ParseAny(string(p) + "_" + hex)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func tx(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func codes(fs []Finding) map[Code]int {
	out := map[Code]int{}
	for _, f := range fs {
		out[f.Code]++
	}
	return out
}

// TestCheckText: unknown schemes outside code only; dangling row and var
// references; an alias names the current variable.
func TestCheckText(t *testing.T) {
	live := id(t, ids.PrefixMemory, 1)
	gone := id(t, ids.PrefixMemory, 2)
	s := sourced{src: store.SourceRepo, r: row.Row{ID: live}}
	vars := map[string]bool{"db_host": true}
	aliases := map[string]string{"old_host": "db_host"}
	txt := "${foo:bar} `${baz:x}` ```\n${qux:y}\n``` ${row:" + live.String() + "} ${row:" + gone.String() + ".body} ${var:db_host} ${var:old_host} ${var:nope}"
	got := codes(checkText(s, "body", txt, map[string]bool{live.String(): true}, vars, aliases))
	want := map[Code]int{UnknownScheme: 1, DanglingRef: 2, VarOldName: 1}
	for c, n := range want {
		if got[c] != n {
			t.Errorf("%s: got %d want %d (all %v)", c, got[c], n, got)
		}
	}
}

// TestVarFindings: a store winner shadows another store (SQ22); a repo
// winner is normal; shareable values that differ are reported (SQ20).
func TestVarFindings(t *testing.T) {
	v := func(n byte, src store.Source, name, val string, share bool) sourced {
		return sourced{src: src, r: row.Row{Table: schema.TableVars, ID: id(t, ids.PrefixVar, n),
			Fields: map[string]value.Value{fVarName: tx(t, name), fVarValue: tx(t, val), fVarShareable: value.BoolOf(share)}}}
	}
	got := codes(varFindings([]sourced{
		v(1, "agency", "a", "1", false), v(2, "me", "a", "1", false),
		v(3, store.SourceRepo, "b", "1", true), v(4, "agency", "b", "2", true),
	}))
	if got[VarShadowed] != 1 || got[VarShareableDiffers] != 1 {
		t.Fatalf("got %v", got)
	}
}

// TestTaskFindings: a two-task parent cycle is one finding.
func TestTaskFindings(t *testing.T) {
	a, b := id(t, ids.PrefixTask, 1), id(t, ids.PrefixTask, 2)
	task := func(self, parent ids.AnyID) sourced {
		return sourced{src: store.SourceRepo, r: row.Row{Table: schema.TableTasks, ID: self,
			Fields: map[string]value.Value{schema.FieldTaskParent: tx(t, parent.String())}}}
	}
	if got := codes(taskFindings([]sourced{task(a, b), task(b, a)})); got[ParentCycle] != 1 {
		t.Fatalf("got %v", got)
	}
}
