package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestPlanLinkedRows (devco:D-736): in a linked repo whose notes all
// moved, doctor --fix removes every row the linked folder holds (an
// actor, a tombstone, a comment, a policy of the same value), moves a
// policy the folder lacks, keeps the repo's identity rows (_meta.json's
// project and repo) without a word, and keeps, naming why and the command
// that moves it, a row the folder does not hold and a policy the folder
// sets to another value.
func TestPlanLinkedRows(t *testing.T) {
	const (
		prj       = "prj_01a1f7820f317ea88e77193a2f5a98df"
		otherPrj  = "prj_3bad73a087f37496880b48b17dc1990d"
		rep       = "rep_14e9dabe88917eaa8a89e329c48a117d"
		actHeld   = "act_cb98f8b36b637507a4cfe91eca849f00"
		cmtHeld   = "cmt_01a1af108f8670868b2d8157445678e1"
		memTomb   = "mem_01a1af108f8670868b2d8157445678e2"
		memLost   = "mem_01a1af108f8670868b2d8157445678e3"
		polMove   = "pol_01a1af108f8670868b2d8157445678e4"
		polSame   = "pol_01a1af108f8670868b2d8157445678e5"
		polOther  = "pol_01a1af108f8670868b2d8157445678e6"
		target    = "acme/client_billing"
		keyTarget = "context.targets"
		keyMode   = "branches.default_mode"
		keyIso    = "branches.isolate"
	)
	key := func(table row.Table, id string) store.Key {
		t.Helper()
		aid, err := ids.ParseAny(id)
		if err != nil {
			t.Fatalf("id %s: %v", id, err)
		}
		return store.Key{Table: table, ID: aid}
	}
	text := func(s string) value.Value {
		t.Helper()
		v, err := value.TextOf(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	files := []linkedRowFile{
		{Key: key(schema.TableProjects, prj)},
		{Key: key(schema.TableRepos, rep)},
		{Key: key(schema.TableProjects, otherPrj)},
		{Key: key(schema.TableActors, actHeld)},
		{Key: key(schema.TableComments, cmtHeld)},
		{Key: key(schema.TableMemories, memTomb), Deleted: true},
		{Key: key(schema.TableMemories, memLost), Deleted: true},
		{Key: key(schema.TablePolicies, polMove), PolicyKey: keyTarget, PolicyScope: "repo", PolicyValue: text("claude")},
		{Key: key(schema.TablePolicies, polSame), PolicyKey: keyMode, PolicyScope: "repo", PolicyValue: text("shared")},
		{Key: key(schema.TablePolicies, polOther), PolicyKey: keyIso, PolicyScope: "repo", PolicyValue: text("feature/*")},
	}
	held := map[string]bool{actHeld: true, cmtHeld: true, memTomb: true, otherPrj: true}
	folder := map[string]value.Value{keyMode + "|repo": text("shared"), keyIso + "|repo": text("spike/*")}
	facts := linkedRowFacts{
		Holds:        func(k store.Key, _ bool) bool { return held[k.ID.String()] },
		FolderPolicy: func(k, s string) (value.Value, bool) { v, ok := folder[k+"|"+s]; return v, ok },
	}
	p := planLinkedRows(files, prj, rep, target, facts)

	idsOf := func(keys []store.Key) string {
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			out = append(out, k.ID.String())
		}
		return strings.Join(out, " ")
	}
	if got, want := idsOf(p.Remove), strings.Join([]string{otherPrj, actHeld, cmtHeld, memTomb, polSame}, " "); got != want {
		t.Errorf("Remove = %s\nwant      %s", got, want)
	}
	if got := idsOf(p.MovePolicies); got != polMove {
		t.Errorf("MovePolicies = %q, want %s (the folder sets no %s)", got, polMove, keyTarget)
	}
	kept := strings.Join(p.Kept, "\n")
	for _, want := range []string{
		"memories/" + memLost + " stays: a deletion record " + target + " has no copy of",
		"policies/" + polOther + " (" + keyIso + ") stays: " + target + " sets " + keyIso + " to another value",
	} {
		if !strings.Contains(kept, want) {
			t.Errorf("Kept lacks %q:\n%s", want, kept)
		}
	}
	for _, never := range []string{prj, rep} {
		if strings.Contains(kept+idsOf(p.Remove)+idsOf(p.MovePolicies), never) {
			t.Errorf("the repo's identity row %s is planned or named (it must stay silently):\n%s", never, kept)
		}
	}
	if p.count() != 6 {
		t.Errorf("count = %d, want 6", p.count())
	}

	// a live row the folder lacks is kept with the command that moves it
	lost := planLinkedRows([]linkedRowFile{{Key: key(schema.TableActors, "act_cb98f8b36b637507a4cfe91eca849f01")}}, prj, rep, target, facts)
	if len(lost.Remove) != 0 || len(lost.Kept) != 1 || !strings.Contains(lost.Kept[0], "lore move --id act_cb98f8b36b637507a4cfe91eca849f01 --to "+target+" --yes") {
		t.Errorf("a row the folder lacks: Remove %v, Kept %q (want kept, named with lore move --id … --to %s --yes)", lost.Remove, lost.Kept, target)
	}
}
