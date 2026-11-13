package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// v1Rule is a lore 0.1.17 rule file (flat columns, nulls written out).
func v1Rule(archived, updated string) []byte {
	a := "null"
	if archived != "" {
		a = `"` + archived + `"`
	}
	return []byte(`{"_table":"rules","_v":1,"id":"rul_01a147bb852171d080e52ed0d434f505","body":"keep secrets out","severity":"must","archived_at":` + a +
		`,"project_id":"prj_01a144f172517b4e9c5b5e5fe6c5ce1d","created_at":"2026-10-17T02:40:34.081116000Z","updated_at":"` + updated + `"}`)
}

// TestUpcastForMergeKeepsPreMigrationEdit (E-4): a branch that archived a
// rule on 0.1.17 merges across the migration (which wrote the rule as
// _v: 2, null keys dropped, a later stamp): the archive survives.
func TestUpcastForMergeKeepsPreMigrationEdit(t *testing.T) {
	reg, err := builtinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	base := v1Rule("", "2026-10-17T02:40:34.081117000Z")
	ours := v1Rule("2026-11-13T22:08:06Z", "2026-11-13T22:08:06.000000000Z")
	r, err := row.Decode(base)
	if err != nil {
		t.Fatal(err)
	}
	// the migrated side as the old migration wrote it: a newer stamp
	mig := upcastV1(reg, r)
	mig.UpdatedAt = mig.UpdatedAt.AddDate(0, 1, 0)
	mig.HLC.Wall = mig.UpdatedAt
	mig.Fields = withoutNulls(mig.Fields) // the engine's write drops null fields
	theirs, err := row.Encode(mig)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(theirs), "archived_at") {
		t.Fatalf("migrated side still writes archived_at: %s", theirs)
	}
	sides := UpcastForMerge(base, ours, theirs)
	out, conflict, err := merge.Driver{Registry: reg, OursFirst: true}.Merge(sides[0], sides[1], sides[2])
	if err != nil || conflict {
		t.Fatalf("merge: conflict %v, err %v", conflict, err)
	}
	got, err := row.Decode(out)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got.Fields["archived_at"]; !ok || value.Equal(v, value.Null{}) {
		t.Fatalf("the branch's archive was dropped: %s", out)
	}
	if got.SourceVersion != row.FormatVersion {
		t.Fatalf("merged row is _v %d, want %d", got.SourceVersion, row.FormatVersion)
	}
	// all v1 sides: left alone (a repo not migrated yet never gets _v: 2)
	same := UpcastForMerge(base, ours, ours)
	if string(same[1]) != string(ours) {
		t.Fatal("an all-v1 merge was upcast")
	}
}

// TestMigratedBaseNotAHandEdit (E-9): the tags step's tag_ids and the
// actor_projection trim are the migration's, never a hand edit.
func TestMigratedBaseNotAHandEdit(t *testing.T) {
	reg, err := builtinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	base, err := row.Decode(v1Rule("", "2026-10-17T02:40:34.081117000Z"))
	if err != nil {
		t.Fatal(err)
	}
	hand := upcastV1(reg, base)
	list, err := tagIDList([]string{"tag_a443cf586dd57c6ebb87252995efee75"})
	if err != nil {
		t.Fatal(err)
	}
	f := map[string]value.Value{}
	for k, v := range hand.Fields {
		f[k] = v
	}
	f[schema.FieldTagIDs] = list
	hand.Fields = f
	if got := migratedBase(reg, base, hand); len(engine.PickUpChanged(got, hand)) != 0 {
		t.Fatalf("tag_ids taken for a hand edit: %v", engine.PickUpChanged(got, hand))
	}
	actor, err := row.Decode([]byte(`{"_table":"actors","_v":1,"id":"act_596bcaa243e17d28b4dbc5c959064ac5","display_name":"Ann Example","kind":"human","stable_key":"ann@example.test","created_at":"2026-10-17T02:40:34Z","updated_at":"2026-10-17T02:40:34Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	up := upcastV1(reg, actor)
	trimmed := trimActorCopy(up, []string{"stable_key"})
	if got := migratedBase(reg, actor, trimmed); len(engine.PickUpChanged(got, trimmed)) != 0 {
		t.Fatalf("actor trim taken for a hand edit: %v", engine.PickUpChanged(got, trimmed))
	}
	// a real hand edit still shows
	edited := trimmed
	ef := map[string]value.Value{}
	for k, v := range trimmed.Fields {
		ef[k] = v
	}
	name, err := value.TextOf("Ann Changed")
	if err != nil {
		t.Fatal(err)
	}
	ef["display_name"] = name
	edited.Fields = ef
	if got := migratedBase(reg, actor, edited); len(engine.PickUpChanged(got, edited)) == 0 {
		t.Fatal("a real hand edit of an actor copy was hidden")
	}
}
