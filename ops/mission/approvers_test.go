package mission_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// rosterTeam resolves @handles from a fixed roster; it has no claims board
// (the approvers path never reads one).
type rosterTeam map[string]string

func (r rosterTeam) ResolveHandle(_ context.Context, handle string, _ bool) (opstask.Assignee, error) {
	if id, ok := r[handle]; ok {
		return opstask.Assignee{ActorID: id}, nil
	}
	return opstask.Assignee{}, errs.New(errs.NotFound, "no person "+handle).WithHint("lore team list")
}

func (rosterTeam) Claims(context.Context) (opstask.ClaimBoard, error) {
	return nil, errs.New(errs.Unsupported, "no claims board in this test")
}

// approversOf is the approvers list of mission.show --json.
func approversOf(t *testing.T, h *opstest.H, id string) []string {
	t.Helper()
	var out struct {
		Data struct {
			Approvers []string `json:"approvers"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must("mission.show", `{"id":"`+id+`"}`))), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data.Approvers
}

// TestApproverHandlesResolve (later 216.2): with the host's roster, mission
// add --approvers @h and mission apply's `approvers:` store the act_ ids
// the handles resolve to (mixed with act_ ids, comma separated or
// repeated); a handle the roster lacks is E_NOT_FOUND with its hint and
// writes no mission.
func TestApproverHandlesResolve(t *testing.T) {
	const ann, bob = "act_01936f0000007a1b8c2d3e4f5a6b7c8d", "act_01936f0000007a1b8c2d3e4f5a6b7c8e"
	h := opstest.New(t)
	ctx := opstask.WithTeam(context.Background(), rosterTeam{"@ann": ann, "@bob": bob})
	run := func(name, in string) (op.Result, error) {
		t.Helper()
		e, ok := h.App.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		return h.App.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{})
	}
	idOf := func(res op.Result, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return h.ID(res)
	}

	m := idOf(run("mission.add", `{"title":"handles","approvers":["@ann,`+bob+`"]}`))
	if got := approversOf(t, h, m); !slices.Equal(got, []string{ann, bob}) {
		t.Fatalf("add: approvers %v", got)
	}
	m2 := idOf(run("mission.add", `{"title":"repeated","approvers":["@bob","@ann"]}`))
	if got := approversOf(t, h, m2); !slices.Equal(got, []string{bob, ann}) {
		t.Fatalf("repeated: approvers %v", got)
	}
	before := h.JSON(h.Must("mission.list", `{}`))
	if _, err := run("mission.add", `{"title":"bad","approvers":["@nobody"]}`); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown handle: %v", err)
	}
	if h.JSON(h.Must("mission.list", `{}`)) != before {
		t.Fatal("a refused add wrote a mission")
	}

	plan := writePlan(t, "mission:\n  title: Applied\napprovers: [\"@ann\", \"@bob\"]\ntasks: []\n")
	if _, err := run("mission.apply", `{"file":`+opstest.Quote(plan)+`}`); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Data []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must("mission.list", `{}`))), &list); err != nil {
		t.Fatal(err)
	}
	applied := ""
	for _, r := range list.Data {
		if r.Title == "Applied" {
			applied = r.ID
		}
	}
	if applied == "" {
		t.Fatal("mission apply wrote no mission")
	}
	if got := approversOf(t, h, applied); !slices.Equal(got, []string{ann, bob}) {
		t.Fatalf("apply: approvers %v", got)
	}
}
