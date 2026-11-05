package mission_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/wire"
)

// TestMissionEstimateSkipsTrash (OV12, TASK 9.2/9.4): a mission's estimate
// rollup counts its live tasks only; a task moved to the trash takes its
// estimate and its tracked time out of the rollup, and restoring it (not
// tested here: trash restore is p2-del's) is the way back.
func TestMissionEstimateSkipsTrash(t *testing.T) {
	// time data lives in internal stores only (PC-LEAK-1): an own repo
	h := opstest.New(t, func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
	})
	m := addMission(t, h, `{"title":"estimated"}`)
	taskID := func(in string) string {
		t.Helper()
		var w struct{ Data struct{ ID string } }
		if err := json.Unmarshal([]byte(h.JSON(h.Must("task.add", in))), &w); err != nil {
			t.Fatal(err)
		}
		return w.Data.ID
	}
	a := taskID(`{"title":"a","mission":"` + m + `","estimate":"3"}`)
	b := taskID(`{"title":"b","mission":"` + m + `","estimate":"2"}`)
	taskID(`{"title":"c","mission":"` + m + `"}`)
	h.Must("task.time.log", `{"id":"`+a+`","duration":"1h","at":"2026-11-02"}`)
	h.Must("task.time.log", `{"id":"`+b+`","duration":"2h","at":"2026-11-02"}`)

	rollup := func() (string, int64, int) {
		t.Helper()
		var out struct {
			Data struct {
				Estimate struct {
					Total       string `json:"estimate"`
					Tracked     int64  `json:"tracked_seconds"`
					Unestimated int    `json:"unestimated"`
				} `json:"estimate_rollup"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must("mission.show", `{"id":"`+m+`"}`))), &out); err != nil {
			t.Fatal(err)
		}
		e := out.Data.Estimate
		return e.Total, e.Tracked, e.Unestimated
	}
	if total, tracked, un := rollup(); total != "5" || tracked != 3*3600 || un != 1 {
		t.Fatalf("before delete: estimate %q tracked %d unestimated %d", total, tracked, un)
	}
	h.MustYes("task.delete", `{"id":"`+b+`"}`)
	if total, tracked, un := rollup(); total != "3" || tracked != 3600 || un != 1 {
		t.Errorf("after delete: estimate %q tracked %d unestimated %d, want 3, 3600, 1", total, tracked, un)
	}
}
