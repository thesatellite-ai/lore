package mission_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/mission"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// internalRepo marks the repo internal: time data lives in internal
// stores only (PC-LEAK-1).
func internalRepo(o *wire.Options) {
	o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
}

// showTasks is mission show's task table titles, in order.
func showTasks(t *testing.T, h *opstest.H, in string) string {
	t.Helper()
	var out mission.ShowOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(mission.OpShow, in))), &out); err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, b := range out.Data.Tasks {
		titles = append(titles, b.Title)
	}
	return strings.Join(titles, ",")
}

// TestMissionShowTimeFlags (FA-TASK-5, TIME 10, later 43): mission show's
// task table filters by --worked-by / --worked-since / --worked-until /
// --min-time / --max-time and orders by --sort time_spent|elapsed (asc
// default, :desc); any other --sort key is E_INVALID_INPUT naming it; a
// trashed task drops out once a flag is given; no flag keeps the table.
func TestMissionShowTimeFlags(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := opstest.New(t, internalRepo)
	// the harness actor's row, so --worked-by can name it
	aid, err := ids.ParseAny(opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	name, err := value.TextOf("Ann")
	if err != nil {
		t.Fatal(err)
	}
	kind, err := value.TextOf(schema.ActorKindHuman)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.App.CreateWithID(context.Background(), store.SourceRepo, schema.TableActors, aid, map[string]value.Value{"kind": kind, "display_name": name}); err != nil {
		t.Fatal(err)
	}
	m := addMission(t, h, `{"title":"Ship"}`)
	tid := map[string]string{}
	for _, n := range []string{"short", "long", "none", "trashed"} {
		tid[n] = h.ID(h.Must(opstask.OpAdd, `{"title":"`+n+`","mission":"`+m+`"}`))
	}
	h.Must(opstask.OpTimeLog, `{"id":"`+tid["short"]+`","duration":"30m","at":"2026-11-10"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+tid["long"]+`","duration":"3h","at":"2026-11-17"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+tid["trashed"]+`","duration":"1h","at":"2026-11-17"}`)
	h.MustYes(opstask.OpDelete, `{"id":"`+tid["trashed"]+`"}`)

	base := `{"id":"` + m + `"`
	cases := []struct{ in, want string }{
		{base + `}`, "short,long,none"},
		{base + `,"sort":["time_spent"]}`, "none,short,long"},
		{base + `,"sort":["time_spent:desc"]}`, "long,short,none"},
		{base + `,"worked_by":"` + opstest.Actor + `"}`, "short,long"},
		{base + `,"worked_since":"2026-11-15"}`, "long"},
		{base + `,"worked_until":"2026-11-15"}`, "short"},
		{base + `,"min_time":"1h"}`, "long"},
		{base + `,"max_time":"1h"}`, "short,none"},
		{base + `,"min_time":"10m","sort":["time_spent:desc"]}`, "long,short"},
	}
	for _, c := range cases {
		if got := showTasks(t, h, c.in); got != c.want {
			t.Errorf("%s: %s, want %s", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"priority", "title:desc", "time_spent,priority"} {
		_, err := h.Run(mission.OpShow, base+`,"sort":["`+strings.ReplaceAll(bad, ",", `","`)+`"]}`)
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "time_spent or elapsed only") {
			t.Errorf("--sort %s: %v, want E_INVALID_INPUT (time keys only)", bad, err)
		}
	}
}
