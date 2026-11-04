package main

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestCoreDocOfAuthor pins where a coordination document's author comes
// from: a v0.1.17 row's own created_by_actor_id column wins; a row written
// through the core (no such column) gets the envelope's created_by, so the
// inbox and mission owner see the same author for both writers (F07 poison
// task: the owner's inbox was empty because the core row had no column).
func TestCoreDocOfAuthor(t *testing.T) {
	env, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	col, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	colRef, err := value.RefOf(col.Any())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := value.TextOf("")
	if err != nil {
		t.Fatal(err)
	}
	task, err := ids.ParseAny("tsk_01a1a69973047a1f8526a3ee26fdba38")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		envBy  ids.ID[ids.Actor]
		column value.Value // nil = no column
		want   string
	}{
		{"core row: envelope only", env, nil, env.String()},
		{"v1 row: column only", ids.ID[ids.Actor]{}, colRef, col.String()},
		{"both: the column wins", env, colRef, col.String()},
		{"empty column: the envelope fills it", env, empty, env.String()},
		{"neither: no author", ids.ID[ids.Actor]{}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := row.Row{Table: row.Table(tableTasks), ID: task, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0), CreatedBy: tc.envBy, Fields: map[string]value.Value{}}
			if tc.column != nil {
				r.Fields[fieldCreatedByActor] = tc.column
			}
			d, err := coreDocOf(r)
			if err != nil {
				t.Fatal(err)
			}
			if got := d.Str(fieldCreatedByActor); got != tc.want {
				t.Fatalf("created_by_actor_id = %q, want %q", got, tc.want)
			}
			if d.Str(docKeyID) != task.String() {
				t.Fatalf("id = %q", d.Str(docKeyID))
			}
		})
	}
}
