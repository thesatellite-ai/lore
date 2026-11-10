package workflow_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// otherActor is a second writer (arbitrary hex), distinct from opstest.Actor.
const otherActor = "act_0e45506e64357c94a87e70ef1c6bbbb0"

// rowFile reads the stored row file of workflow id as raw JSON members.
func rowFile(t *testing.T, repo, id string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, ".lore", "data", schema.TableWorkflows, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// str decodes one JSON string member ("" when absent).
func str(t *testing.T, m map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return s
}

// shown is one string field of `workflow show --json` (its data object).
func shown(t *testing.T, h *opstest.H, id, key string) string {
	t.Helper()
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must("workflow.show", `{"id":"`+id+`"}`))), &env); err != nil {
		t.Fatal(err)
	}
	return str(t, env.Data, key)
}

// TestWorkflowCreatedBy (INTEG-WF-1): a workflow records its creator like
// the other knowledge rows. The writer is created_by (row key) and
// created_by_actor_id in --json; an edit by another actor keeps the
// creator and moves updated_by to the editor.
func TestWorkflowCreatedBy(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := h.ID(h.Must("workflow.add", `{"name":"release","body":"tag, build, publish"}`))
	f := rowFile(t, h.Repo, id)
	if got := str(t, f, "created_by"); got != opstest.Actor {
		t.Fatalf("new workflow created_by = %q, want the writer %s", got, opstest.Actor)
	}
	if got := str(t, f, "updated_by"); got != opstest.Actor {
		t.Fatalf("new workflow updated_by = %q, want %s", got, opstest.Actor)
	}
	if got := shown(t, h, id, "created_by_actor_id"); got != opstest.Actor {
		t.Fatalf("workflow show created_by_actor_id = %q, want %s", got, opstest.Actor)
	}

	other, err := ids.Parse[ids.Actor](otherActor)
	if err != nil {
		t.Fatal(err)
	}
	// opstest.H.Run always calls as opstest.Actor, so the other writer's
	// edit goes through App.Run with its own caller.
	b := h.Reopen(func(o *wire.Options) { o.Actor = other })
	e, ok := b.App.Registry().Lookup("workflow.edit")
	if !ok {
		t.Fatal("no op workflow.edit")
	}
	in := `{"id":"` + id + `","body":"tag, build, publish, announce"}`
	if _, err := b.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(in)}, op.Caller{Actor: other}); err != nil {
		t.Fatal(err)
	}
	f = rowFile(t, h.Repo, id)
	if got := str(t, f, "created_by"); got != opstest.Actor {
		t.Fatalf("after another actor's edit created_by = %q, want the creator %s", got, opstest.Actor)
	}
	if got := str(t, f, "updated_by"); got != otherActor {
		t.Fatalf("after edit updated_by = %q, want the editor %s", got, otherActor)
	}
	if got := shown(t, b, id, "created_by_actor_id"); got != opstest.Actor {
		t.Fatalf("after edit created_by_actor_id = %q, want %s", got, opstest.Actor)
	}
}

// TestWorkflowOldRowWithoutCreator (INTEG-WF-1): a v0.1.17 workflow file
// (`_v: 1`, no created_by, no created_by_actor_id) still reads, and
// `lore migrate run` fills its creator with the reserved "unknown" actor,
// never the actor running the migration.
func TestWorkflowOldRowWithoutCreator(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := "wfl_01a1a59b891c70b789144cd3e711f001"
	dir := filepath.Join(h.Repo, ".lore", "data", schema.TableWorkflows)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	doc := `{"_table":"workflows","_v":1,"body":"old steps","created_at":"2026-11-04T08:09:56.252002659Z","id":"` + id +
		`","name":"old","project_id":"` + h.Project + `","updated_at":"2026-11-04T08:09:57.252002894Z"}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	c := h.ReopenCold()
	if got := shown(t, c, id, "name"); got != "old" {
		t.Fatalf("old workflow name = %q, want old", got)
	}
	c.MustYes("migrate.run", `{}`)
	f := rowFile(t, h.Repo, id)
	unknown := auditfill.UnknownActor().String()
	if got := str(t, f, "created_by"); got != unknown {
		t.Fatalf("migrated old workflow created_by = %q, want unknown %s", got, unknown)
	}
	if got := shown(t, c, id, "created_by_actor_id"); got != unknown {
		t.Fatalf("migrated old workflow created_by_actor_id = %q, want %s", got, unknown)
	}
}
