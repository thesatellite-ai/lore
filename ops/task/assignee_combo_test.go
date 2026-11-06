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

// TestListAssigneeCombos is the rest of saas TestUC_Tasks_ListAssignee
// (later 192) read through the core: --assignee with @me, an id, a
// display name padded with spaces, combined with --status, --all,
// --include-proposed and --commitment; an actor with no tasks lists
// nothing and says so.
func TestListAssigneeCombos(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	me := opstest.Actor
	// the harness identity's actor row (a real first write creates it)
	meID, err := ids.ParseAny(me)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.App.CreateWithID(context.Background(), store.SourceRepo, schema.TableActors, meID, map[string]value.Value{
		"kind": text(t, schema.ActorKindHuman), "display_name": text(t, "Me"), "stable_key": text(t, "human:me"),
	}); err != nil {
		t.Fatal(err)
	}
	bot := newActor(h, "Review-Bot")
	idle := newActor(h, "Idle-Bot")
	add(h, "unassigned", "")
	add(h, "mine todo", `"assigned_to":"`+me+`"`)
	started := add(h, "mine started", `"assigned_to":"`+me+`"`)
	h.Must(opstask.OpStart, `{"id":"`+started+`"}`)
	fin := add(h, "mine finished", `"assigned_to":"`+me+`"`)
	h.Must(opstask.OpDone, `{"id":"`+fin+`","no_lesson":true}`)
	add(h, "theirs", `"assigned_to":"`+bot+`"`)
	add(h, "their idea", `"assigned_to":"`+bot+`","commitment":"proposed"`)

	titles := func(in string) string {
		t.Helper()
		var out struct {
			Data []struct {
				Title string `json:"title"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpList, in))), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range out.Data {
			got = append(got, d.Title)
		}
		slices.Sort(got)
		return strings.Join(got, ",")
	}
	cases := []struct{ in, want string }{
		{`{"assignee":"` + me + `"}`, "mine started,mine todo"},
		{`{"assignee":"@me"}`, "mine started,mine todo"},
		{`{"assignee":"` + bot + `"}`, "theirs"},
		{`{"assignee":"  review-bot  "}`, "theirs"},
		{`{"assignee":"` + idle + `"}`, ""},
		{`{"assignee":"idle-bot","all":true}`, ""},
		{`{"assignee":"` + me + `","status":"in_progress"}`, "mine started"},
		{`{"assignee":"` + me + `","status":"done"}`, "mine finished"},
		{`{"assignee":"` + bot + `","status":"done"}`, ""},
		{`{"assignee":"` + me + `","all":true}`, "mine finished,mine started,mine todo"},
		{`{"assignee":"` + bot + `","include_proposed":true}`, "their idea,theirs"},
		{`{"assignee":"` + bot + `","commitment":"proposed"}`, "their idea"},
	}
	for _, c := range cases {
		if got := titles(c.in); got != c.want {
			t.Errorf("task list %s = %q, want %q", c.in, got, c.want)
		}
	}
	if out := opstest.Text(h.Must(opstask.OpList, `{"assignee":"`+idle+`"}`)); !strings.Contains(out, "(no tasks)") {
		t.Errorf("an assignee with no tasks must say so: %q", out)
	}
}
