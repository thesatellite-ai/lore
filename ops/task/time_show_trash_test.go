package task_test

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestTimeShowTrashGroupByPerson (OV55, DQ10/D16, TQ1): time show of a
// task in the trash answers E_IN_TRASH without
// --include-deleted, and with it shows the time marked in_trash; grouping
// by person sums each person's entries (manual time logged --by another
// actor lands on that actor, not the writer).
func TestTimeShowTrashGroupByPerson(t *testing.T) {
	h := opstest.New(t)
	ann := newActor(h, "Ann")
	bob := newActor(h, "Bob")
	id := add(h, "worked then trashed", "")
	h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"1h","at":"2026-11-02","by":"`+ann+`"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"30m","at":"2026-11-02","by":"`+ann+`"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"2h","at":"2026-11-03","by":"`+bob+`"}`)
	h.MustYes(opstask.OpDelete, `{"id":"`+id+`"}`)

	_, err := h.Run(opstask.OpTimeShow, `{"id":"`+id+`"}`)
	if err == nil {
		t.Fatal("time show of a trashed task answered without --include-deleted")
	}
	if !errs.Has(err, errs.InTrash) {
		t.Errorf("trashed task: %v, want E_IN_TRASH (DELETION 3.2)", err)
	}

	var out struct {
		Data struct {
			Effort  int64 `json:"effort_seconds"`
			InTrash bool  `json:"in_trash"`
			GroupBy string
			Workers []struct {
				Actor string `json:"actor_id"`
				Total int64  `json:"total_seconds"`
			} `json:"workers"`
		} `json:"data"`
	}
	res := h.Must(opstask.OpTimeShow, `{"id":"`+id+`","include_deleted":true,"group_by":"person"}`)
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	d := out.Data
	if !d.InTrash || d.Effort != 3*3600+30*60 {
		t.Fatalf("include_deleted: in_trash %v effort %d", d.InTrash, d.Effort)
	}
	got := map[string]int64{}
	for _, w := range d.Workers {
		got[w.Actor] += w.Total
	}
	if got[ann] != 90*60 || got[bob] != 2*3600 || len(got) != 2 {
		t.Errorf("per person %v (ann %s bob %s)", got, ann, bob)
	}
}
