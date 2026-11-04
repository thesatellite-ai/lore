package sessions

import (
	"context"
	"path/filepath"
	"testing"

	"dbent"
	"dbent/pkg/dbtemplate"
)

// TestFindOrCreate pins 6.2: one row per (actor, tool, tool session id);
// a later call fills fields still empty (the hook's model) and never
// clears a filled one; KnownToolSessionIDs skips synthetic keys.
func TestFindOrCreate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lore.db")
	if err := dbtemplate.Build(ctx, path); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(path)
	defer func() { _ = db.Close() }()
	if ok, err := Ready(ctx, db); err != nil || !ok {
		t.Fatalf("Ready: %v %v", ok, err)
	}
	c := dbent.New(db).Client()
	d := Details{ActorID: "act_1", Tool: "codex", ToolSessionID: "019a7c2e-aaaa-bbbb", ToolVersion: "0.58.0", Sources: map[string]string{"tool_version": "package_metadata"}}
	id1, err := FindOrCreate(ctx, c, "prj_1", d)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := FindOrCreate(ctx, c, "prj_1", Details{ActorID: "act_1", Tool: "codex", ToolSessionID: "019a7c2e-aaaa-bbbb",
		Model: "m1", ToolVersion: "9.9", Sources: map[string]string{"model": SourceSessionHook}})
	if err != nil || id1 != id2 {
		t.Fatalf("same session must be one row: %s %s %v", id1, id2, err)
	}
	row, err := c.Session.Get(ctx, id1)
	if err != nil {
		t.Fatal(err)
	}
	if row.Model != "m1" || row.ToolVersion != "0.58.0" || row.Sources["model"] != SourceSessionHook || row.Sources["tool_version"] != "package_metadata" {
		t.Fatalf("fill rule broken: %+v", row)
	}
	if _, serr := FindOrCreate(ctx, c, "prj_1", Details{ActorID: "act_1", Tool: "codex", ToolSessionID: SyntheticKey("codex", 42)}); serr != nil {
		t.Fatal(serr)
	}
	known, err := KnownToolSessionIDs(ctx, db)
	if err != nil || len(known) != 1 || known[0] != "019a7c2e-aaaa-bbbb" {
		t.Fatalf("known ids: %v %v", known, err)
	}
}
