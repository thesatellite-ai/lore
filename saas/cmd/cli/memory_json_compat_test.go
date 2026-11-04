package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"dbent"
	"dbent/gen/ent"
)

// --json byte compatibility of the memory commands (memparity): for the
// same row, `lore memory show|list --json` (the core's ShowOut/ListOut)
// must print exactly what v0.1.17 printed, i.e. printJSON's jsonEnvelope
// (json.MarshalIndent) over the ent Memory. The reference row is lore.db's
// bridge copy of the core's file (imported by an old command's sync pass),
// read through ent the way v0.1.17's show did, so nothing in the
// comparison is hand-built. Matrix: plain, source ref, repo-scoped,
// archived, superseded (old and new row), HTML/unicode/quote body, a long
// multi-line body, and the list of all of them.
func TestMemoryJSONByteCompat(t *testing.T) {
	_, c := initProject(t)
	c.lore("repo", "add", "api", "--origin", "git@example.com:acme/api.git")
	add := func(args ...string) string {
		t.Helper()
		id := idPattern.FindString(c.lore(append([]string{"memory", "add"}, args...)...).stdout)
		if id == "" {
			t.Fatalf("memory add %v printed no id", args)
		}
		return id
	}
	ids := map[string]string{
		"plain":      add("--body", "the printer is downstairs"),
		"source ref": add("--body", "from the PR", "--source", "pr", "--source-ref", "pr#42"),
		"repo":       add("--body", "api only", "--repo", "api"),
		"kind":       add("--body", "how to deploy", "--kind", "procedural"),
		"html":       add("--body", `<b>bold</b> & "quoted" 'single' — é 漢字 😀 \ backslash`),
		"long":       add("--body", strings.Repeat("line of text\n", 200)+"end"),
	}
	ids["archived"] = add("--body", "old news")
	c.lore("memory", "archive", ids["archived"])
	ids["superseded old"] = add("--body", "v1 of the fact")
	ids["superseded new"] = add("--body", "v2 of the fact", "--supersedes", ids["superseded old"])
	c.lore("sync") // import the core's files into lore.db's bridge copy

	db := dbent.InitDB(filepath.Join(c.dir, ".lore", "lore.db"))
	client := dbent.New(db).Client()
	t.Cleanup(func() { _ = client.Close() }) // read-only probe of a temp fixture
	ctx := context.Background()
	v017 := func(kind string, data any, count int) string {
		t.Helper()
		b, err := json.MarshalIndent(jsonEnvelope{SchemaVersion: 1, Kind: kind, Count: count, Data: data}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var all []*ent.Memory
	for name, id := range ids {
		m, err := client.Memory.Get(ctx, id)
		if err != nil {
			t.Fatalf("%s: lore.db bridge copy of %s: %v", name, id, err)
		}
		got := strings.TrimSuffix(c.lore("memory", "show", id, "--json").stdout, "\n")
		if want := v017("memory.show", m, 0); got != want {
			t.Errorf("%s: memory show --json differs from v0.1.17:\n got %s\nwant %s", name, got, want)
		}
		all = append(all, m)
	}
	// list --all-repos --archived: every row, in v0.1.17's order (oldest
	// first by created_at, checked against the v0.1.17 binary).
	got := strings.TrimSuffix(c.lore("memory", "list", "--all-repos", "--archived", "--json").stdout, "\n")
	var listed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &listed); err != nil {
		t.Fatal(err)
	}
	byID := map[string]*ent.Memory{}
	for _, m := range all {
		byID[m.ID] = m
	}
	ordered := make([]*ent.Memory, 0, len(listed.Data))
	for _, d := range listed.Data {
		ordered = append(ordered, byID[d.ID])
	}
	if len(ordered) != len(all) {
		t.Fatalf("list --all-repos --archived printed %d rows, want %d", len(ordered), len(all))
	}
	for i := 1; i < len(ordered); i++ {
		if ordered[i].CreatedAt.Before(ordered[i-1].CreatedAt) {
			t.Fatalf("list order is not oldest first at %d: %v before %v", i, ordered[i].CreatedAt, ordered[i-1].CreatedAt)
		}
	}
	if want := v017("memory.list", ordered, len(ordered)); got != want {
		t.Errorf("memory list --json differs from v0.1.17:\n got %s\nwant %s", got, want)
	}
}
