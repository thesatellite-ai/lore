package auditfill

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

func TestUnknownActor(t *testing.T) {
	if !IsUnknown(UnknownActor()) {
		t.Fatal("UnknownActor is not IsUnknown")
	}
}

func text(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestDefaults is the backfill table of FORMAT_EVOLUTION section 3: creator
// column kept, unknown otherwise, updated_by follows created_by only for a
// never-edited row, deleted_by only when deleted_at is set, tombstones
// untouched, a second pass changes nothing.
func TestDefaults(t *testing.T) {
	creator, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := map[string]struct {
		in                  row.Row
		wantCreated, wantUp string
		wantDeletedBy       string
	}{
		"creator column, never edited": {
			in:          row.Row{CreatedAt: t0, UpdatedAt: t0, Fields: map[string]value.Value{FieldCreatorActor: text(t, creator.String())}},
			wantCreated: creator.String(), wantUp: creator.String(),
		},
		"creator column, edited": {
			in:          row.Row{CreatedAt: t0, UpdatedAt: t0.Add(time.Hour), Fields: map[string]value.Value{FieldCreatorActor: text(t, creator.String())}},
			wantCreated: creator.String(), wantUp: unknownActorText,
		},
		"no creator column": {
			in:          row.Row{CreatedAt: t0, UpdatedAt: t0, Fields: map[string]value.Value{}},
			wantCreated: unknownActorText, wantUp: unknownActorText,
		},
		"bad creator column": {
			in:          row.Row{CreatedAt: t0, UpdatedAt: t0, Fields: map[string]value.Value{FieldCreatorActor: text(t, "nobody")}},
			wantCreated: unknownActorText, wantUp: unknownActorText,
		},
		"soft deleted": {
			in:          row.Row{CreatedAt: t0, UpdatedAt: t0, Fields: map[string]value.Value{FieldDeletedAt: text(t, "2026-01-03T00:00:00Z")}},
			wantCreated: unknownActorText, wantUp: unknownActorText, wantDeletedBy: unknownActorText,
		},
		"already filled": {
			in:          row.Row{CreatedAt: t0, UpdatedAt: t0.Add(time.Hour), CreatedBy: creator, UpdatedBy: creator, Fields: map[string]value.Value{}},
			wantCreated: creator.String(), wantUp: creator.String(),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, _ := Defaults(c.in)
			if got.CreatedBy.String() != c.wantCreated || got.UpdatedBy.String() != c.wantUp {
				t.Fatalf("created_by %s updated_by %s, want %s %s", got.CreatedBy, got.UpdatedBy, c.wantCreated, c.wantUp)
			}
			db := ""
			if v, ok := got.Fields[FieldDeletedBy]; ok {
				db = v.String()
			}
			if db != c.wantDeletedBy {
				t.Fatalf("deleted_by %q, want %q", db, c.wantDeletedBy)
			}
			if _, again := Defaults(got); again {
				t.Fatal("second pass changed the row")
			}
		})
	}
	tomb := row.Row{Deleted: true}
	if got, changed := Defaults(tomb); changed || !got.CreatedBy.IsZero() {
		t.Fatal("a tombstone was filled")
	}
	in := row.Row{Fields: map[string]value.Value{FieldDeletedAt: text(t, "x")}}
	_, _ = Defaults(in)
	if _, ok := in.Fields[FieldDeletedBy]; ok {
		t.Fatal("Defaults wrote into the caller's map")
	}
}

// TestNameOf: only the reserved id is named "unknown" (devco K-2).
func TestNameOf(t *testing.T) {
	if n, ok := NameOf(UnknownActor().String()); !ok || n != UnknownName {
		t.Fatalf("NameOf(unknown) = %q, %v", n, ok)
	}
	if n, ok := NameOf("act_0123456789abcdef0123456789abcdef"); ok || n != "" {
		t.Fatalf("NameOf(real id) = %q, %v", n, ok)
	}
}
