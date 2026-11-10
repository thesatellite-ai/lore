package task_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestSplitPerChildFlags (TASK 6.3, P3-TASK-1): a sub-task's own
// priority / commitment / assignee / due win over the shared values, an
// item without its own takes the shared ones, and the --from YAML piece
// takes assign and due too. An empty positional title is refused (its
// flags would vanish).
func TestSplitPerChildFlags(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	ann := newActor(h, "Ann")
	bob := newActor(h, "Bob")
	ft := &fakeTeam{people: map[string]string{"@ann": ann, "@bob": bob}, board: &fakeBoard{}}
	ctx := opstask.WithTeam(context.Background(), ft)
	parent := add(h, "parent", "")

	res := mustCtx(h, ctx, opstask.OpSplit, `{"id":"`+parent+`","priority":"low","assign":["@bob"],"due":"2030-01-02",
		"children":[{"title":"a","priority":"high","assign":["@ann"],"due":"2030-01-01","commitment":"proposed"},{"title":"b"}],
		"titles":["c"]}`)
	var out struct {
		Added []struct {
			ID string `json:"id"`
		} `json:"added"`
	}
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil || len(out.Added) != 3 {
		t.Fatalf("split out %s (%v)", h.JSON(res), err)
	}
	want := []struct{ prio, commit, actor, due string }{
		{"high", schema.TaskCommitmentProposed, ann, "2030-01-01"},
		{"low", "", bob, "2030-01-02"},
		{"low", "", bob, "2030-01-02"},
	}
	for i, w := range want {
		_, r := get(h, schema.TableTasks, out.Added[i].ID)
		if field(r, "priority") != w.prio || field(r, "assigned_to_actor_id") != w.actor {
			t.Errorf("child %d: priority %q actor %q, want %q %q", i+1, field(r, "priority"), field(r, "assigned_to_actor_id"), w.prio, w.actor)
		}
		if w.commit != "" && field(r, "commitment") != w.commit {
			t.Errorf("child %d: commitment %q want %q", i+1, field(r, "commitment"), w.commit)
		}
		if d := field(r, "due_at"); len(d) < len(w.due) || d[:len(w.due)] != w.due {
			t.Errorf("child %d: due %q want %s", i+1, d, w.due)
		}
	}
	if _, err := runCtx(h, ctx, opstask.OpSplit, `{"id":"`+parent+`","children":[{"title":"  ","priority":"high"}]}`); err == nil {
		t.Error("empty child title: want E_INVALID_INPUT")
	}
	text := opstest.Text(mustCtx(h, ctx, opstask.OpSplit, `{"id":"`+parent+`","dry_run":true,"children":[{"title":"d","priority":"urgent","assign":["@ann"]}]}`))
	if want := "d  (priority urgent, assign @ann)"; !strings.Contains(text, want) {
		t.Errorf("dry run %q lacks %q", text, want)
	}
}
