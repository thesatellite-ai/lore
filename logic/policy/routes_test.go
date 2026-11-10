package policy

import (
	"maps"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestClientRoutes: the routing.client_repos set joins across levels, then
// per kind a store target beats "repo" whatever the level or row order,
// two store targets settle on the first in sorted order, and malformed
// items (no "=", an empty side) are skipped.
func TestClientRoutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		entries []Entry
		want    map[string]string
	}{
		{"no rows", nil, map[string]string{}},
		{"one item", []Entry{{Level: LevelFolder, Key: KeyClientRoutes, Value: list("memory=agency"), Source: src(1)}},
			map[string]string{"memory": "agency"}},
		{"store beats repo, same row", []Entry{{Level: LevelFolder, Key: KeyClientRoutes, Value: list("memory=repo", "memory=agency"), Source: src(1)}},
			map[string]string{"memory": "agency"}},
		{"store beats repo across levels (repo nearer)", []Entry{
			{Level: LevelRepo, Key: KeyClientRoutes, Value: list("task=repo"), Source: src(1)},
			{Level: LevelOrg, Key: KeyClientRoutes, Value: list("task=agency/org"), Source: src(2)},
		}, map[string]string{"task": "agency/org"}},
		{"repo kept when no store names the kind", []Entry{
			{Level: LevelRepo, Key: KeyClientRoutes, Value: list("memory=repo"), Source: src(1)},
			{Level: LevelOrg, Key: KeyClientRoutes, Value: list("task=agency"), Source: src(2)},
		}, map[string]string{"memory": "repo", "task": "agency"}},
		{"two stores: first sorted", []Entry{
			{Level: LevelFolder, Key: KeyClientRoutes, Value: list("rule=zeta", "rule=agency/org"), Source: src(1)},
			{Level: LevelOrg, Key: KeyClientRoutes, Value: list("rule=agency"), Source: src(2)},
		}, map[string]string{"rule": "agency"}},
		{"two stores, repo sorted between them", []Entry{
			{Level: LevelFolder, Key: KeyClientRoutes, Value: list("rule=zeta", "rule=repo"), Source: src(1)},
		}, map[string]string{"rule": "zeta"}},
		{"bad items skipped", []Entry{
			{Level: LevelFolder, Key: KeyClientRoutes, Value: list("noequals", "=agency", "memory=", " = ", "decision=agency"), Source: src(1)},
		}, map[string]string{"decision": "agency"}},
		{"spaces trimmed, unicode kept", []Entry{
			{Level: LevelFolder, Key: KeyClientRoutes, Value: list(" memory = agency ", "notiz=büro/org"), Source: src(1)},
		}, map[string]string{"memory": "agency", "notiz": "büro/org"}},
	}
	for _, c := range cases {
		set, err := Resolve(c.entries)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := set.ClientRoutes(); !maps.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		// Row order never changes the answer.
		rev := make([]Entry, len(c.entries))
		for i, e := range c.entries {
			rev[len(rev)-1-i] = e
		}
		set2, err := Resolve(rev)
		if err != nil {
			t.Fatal(err)
		}
		if got := set2.ClientRoutes(); !maps.Equal(got, c.want) {
			t.Errorf("%s reversed: got %v", c.name, got)
		}
	}
	if ClientRouteItem("memory", "agency") != "memory=agency" {
		t.Error("ClientRouteItem")
	}
	if got := Defaults().ClientRoutes(); len(got) != 0 {
		t.Errorf("defaults route something: %v", got)
	}
}

// TestEntryFromRowBothForms: a policies row reads the same whether its
// value was built in memory (a json field) or decoded from the row file
// (inferred: a list, text, bool or number), and ClientRoutes then gives
// the same answer.
func TestEntryFromRowBothForms(t *testing.T) {
	t.Parallel()
	id := src(1)
	for _, raw := range []string{`["memory=agency","task=repo"]`, `"refuse"`, `true`, `4000`, `[]`} {
		j, err := value.JSONOf([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		mem := row.Row{Table: schema.TablePolicies, ID: id, Fields: map[string]value.Value{"key": txt(string(KeyClientRoutes)), "value": j}}
		inferred, err := value.Infer([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		file := row.Row{Table: schema.TablePolicies, ID: id, Fields: map[string]value.Value{"key": txt(string(KeyClientRoutes)), "value": inferred}}
		a, err := EntryFromRow(LevelFolder, mem)
		if err != nil {
			t.Fatal(err)
		}
		b, err := EntryFromRow(LevelFolder, file)
		if err != nil {
			t.Fatal(err)
		}
		ja, _ := value.JSONForm(a.Value)
		jb, _ := value.JSONForm(b.Value)
		if a.Value.Kind() != b.Value.Kind() || string(ja) != string(jb) {
			t.Errorf("%s: memory %s %s, file %s %s", raw, a.Value.Kind(), ja, b.Value.Kind(), jb)
		}
	}
	j, err := value.JSONOf([]byte(`["memory=agency"]`))
	if err != nil {
		t.Fatal(err)
	}
	e, err := EntryFromRow(LevelFolder, row.Row{Table: schema.TablePolicies, ID: id, Fields: map[string]value.Value{"key": txt(string(KeyClientRoutes)), "value": j}})
	if err != nil {
		t.Fatal(err)
	}
	set, err := Resolve([]Entry{e})
	if err != nil {
		t.Fatal(err)
	}
	if got := set.ClientRoutes(); got["memory"] != "agency" {
		t.Errorf("json-form routes %v", got)
	}
}
