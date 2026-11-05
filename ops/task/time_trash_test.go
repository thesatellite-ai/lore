package task_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestListTimeFiltersWithTrash (OV16, DELETION 3.2, TIME 10): the time
// filters of task list (--min-time, --worked-since) apply to trashed tasks
// only when --include-deleted / --deleted-only bring them in; a trashed
// task is marked, and its time still counts.
func TestListTimeFiltersWithTrash(t *testing.T) {
	h := opstest.New(t)
	live := add(h, "live", "")
	gone := add(h, "gone", "")
	small := add(h, "small", "")
	h.Must(opstask.OpTimeLog, `{"id":"`+live+`","duration":"2h","at":"2026-11-02"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+gone+`","duration":"3h","at":"2026-11-03"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+small+`","duration":"10m","at":"2026-11-03"}`)
	h.MustYes(opstask.OpDelete, `{"id":"`+gone+`"}`)

	type brief struct {
		ID      string `json:"id"`
		Trashed bool   `json:"trashed"`
	}
	list := func(in string) ([]string, map[string]bool) {
		t.Helper()
		var out struct {
			Data []brief `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpList, in))), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		trashed := map[string]bool{}
		for _, d := range out.Data {
			got = append(got, d.ID)
			trashed[d.ID] = d.Trashed
		}
		slices.Sort(got)
		return got, trashed
	}
	set := func(s ...string) []string { slices.Sort(s); return s }
	cases := []struct {
		in   string
		want []string
	}{
		{`{"min_time":"1h"}`, set(live)},
		{`{"min_time":"1h","include_deleted":true}`, set(live, gone)},
		{`{"min_time":"1h","deleted_only":true}`, set(gone)},
		{`{"worked_since":"2026-11-03"}`, set(small)},
		{`{"worked_since":"2026-11-03","include_deleted":true}`, set(gone, small)},
		{`{"max_time":"30m","include_deleted":true}`, set(small)},
	}
	for _, c := range cases {
		got, trashed := list(c.in)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.in, got, c.want)
		}
		for id, tr := range trashed {
			if tr != (id == gone) {
				t.Errorf("%s: %s trashed=%v", c.in, id, tr)
			}
		}
	}
}
