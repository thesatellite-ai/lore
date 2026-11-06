package task_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestWhereMe (OV47, TASK 16.1/16.2): @me in --where is the caller's
// actor id, resolved for whoever runs the list (so a saved view with
// assignee:@me follows its runner); it works in lead and watcher terms
// and in bulk edit's --where too.
func TestWhereMe(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	// The harness identity's actor row (a real first write would create
	// it; the harness writes rows without one).
	me, err := ids.ParseAny(opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.App.CreateWithID(context.Background(), store.SourceRepo, schema.TableActors, me, map[string]value.Value{
		"kind": text(t, schema.ActorKindHuman), "display_name": text(t, "Me"), "stable_key": text(t, "human:me"),
	}); err != nil {
		t.Fatal(err)
	}
	first := add(h, "unassigned", "")
	ann := newActor(h, "Ann")
	mine := add(h, "mine", `"assigned_to":"`+opstest.Actor+`"`)
	led := add(h, "led by me", `"assign":["`+ann+`"],"lead":"`+opstest.Actor+`"`)
	annOnly := add(h, "ann only", `"assigned_to":"`+ann+`"`)

	ids := func(in string) []string {
		t.Helper()
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpList, in))), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range out.Data {
			got = append(got, d.ID)
		}
		slices.Sort(got)
		return got
	}
	set := func(s ...string) []string { slices.Sort(s); return s }
	cases := []struct {
		in   string
		want []string
	}{
		{`{"where":["assignee=@me"]}`, set(mine, led)},
		{`{"where":["lead=@me"]}`, set(mine, led)}, // a lone assignee is the lead (14.2)
		{`{"where":["assignee!=@me"]}`, set(first, annOnly)},
		{`{"where":["assignee=@me","title~=led"]}`, set(led)},
	}
	for _, c := range cases {
		if got := ids(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.in, got, c.want)
		}
	}
	// A saved view keeps the text @me and resolves it when run.
	h.Must(opstask.OpViewAdd, `{"name":"my_work","where":["assignee=@me"]}`)
	if got := ids(`{"view":"my_work"}`); !slices.Equal(got, set(mine, led)) {
		t.Errorf("view @me: %v", got)
	}
	if show := h.JSON(h.Must(opstask.OpViewShow, `{"name":"my_work"}`)); !strings.Contains(show, "@me") {
		t.Errorf("view stored a resolved id instead of @me: %s", show)
	}
}
