package wire_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// deleteSuffix marks a noun's delete operation.
const deleteSuffix = ".delete"

// deletePath says how the command tree deletes one row of a table: the
// operation and its input for the row's id. make, when set, writes the
// row through the commands and returns its id and the delete input (rows
// whose delete addresses them by name, or that only a command writes in a
// deletable shape); set, when set, fills fields opstest.Seed cannot sample
// (a polymorphic target). A table no command deletes row by row carries
// none, the reason why.
type deletePath struct {
	op    string
	input func(id string) string
	make  func(h *opstest.H) (id, input string)
	set   func(h *opstest.H) map[string]string
	none  string
}

// byID is the input of a plain `<noun> delete <id>`.
func byID(id string) string { return `{"id":"` + id + `"}` }

// byName is the input of a delete that takes a name or the row's id.
func byName(id string) string { return `{"name":"` + id + `"}` }

// noRowDelete is why a table has no row delete command: the row is not
// content a person deletes (identity, machine, catalogue or link rows the
// engine writes and keeps), so it leaves only with what owns it or as a
// tombstone through a purge of its owner.
const noRowDelete = "no row delete: "

// deleteExceptions lists the tables whose delete is not `<noun>.delete`
// with an id: another operation, or no row delete at all (with the
// reason). Every other table must have a `<noun>.delete` in the registry,
// so a new table without delete coverage fails the test.
var deleteExceptions = map[row.Table]deletePath{
	schema.TableComments:      {op: "comment.delete", input: byID, set: onMemory},
	schema.TableCommitLinks:   {op: "link.remove", input: byID, set: onMemory},
	schema.TableMetaKeys:      {op: "meta.delete", input: byID},
	schema.TableSecretAllows:  {op: "secrets.allow.rm", input: byID, set: allowOnMemory},
	schema.TableTaskTemplates: {op: "task.template.delete", input: byName},
	schema.TableTaskViews:     {op: "task.view.delete", input: byName},
	schema.TableVars:          {op: "vars.rm", input: byID},
	schema.TablePolicies: {op: "policy.unset", make: func(h *opstest.H) (string, string) {
		id := h.ID(h.MustYes("policy.set", `{"key":"trash.retention_days","value":"30"}`))
		return id, `{"key":"trash.retention_days"}`
	}},
	schema.TableKindFields: {op: "resource.field.remove", make: func(h *opstest.H) (string, string) {
		h.Must("resource.kind.add", `{"kind":"tree_box"}`)
		id := h.ID(h.Must("resource.field.add", `{"kind":"tree_box","field":"size","type":"text"}`))
		return id, `{"kind":"tree_box","field":"size"}`
	}},
	schema.TableSecretRules: {op: "secrets.rule.on", make: func(h *opstest.H) (string, string) {
		id := h.ID(h.MustYes("secrets.rule.off", `{"id":"generic-api-key","reason":"tree test"}`))
		return id, `{"id":"generic-api-key"}`
	}},
	// Identity, machine and catalogue rows: v0.1.17 had no delete for
	// them either (archive_commands.go lists none), and the 0.1.18 design
	// adds none (DELETION 3.12).
	schema.TableActors:          {none: noRowDelete + "an actor is identity: its id stays on every row it wrote (roster edits change it)"},
	schema.TableMachines:        {none: noRowDelete + "a machine profile is written by the machine itself (machine.label edits it)"},
	schema.TableTeams:           {none: noRowDelete + "a roster team is edited through the roster, not deleted row by row"},
	schema.TableScopes:          {none: noRowDelete + "a scope is moved (scope.move), never deleted"},
	schema.TableKinds:           {none: noRowDelete + "a resource kind is renamed or edited; its fields are removed with resource.field.remove"},
	schema.TableTags:            {none: noRowDelete + "v0.1.17 had no tag delete; tag.detach removes an attachment"},
	schema.TableExternalSources: {none: noRowDelete + "a learn-from source is switched off (external_source.disable), not deleted"},
	// Rows that leave only with what owns them (DELETION 3.4 group rule).
	schema.TableMetaValues:       {none: noRowDelete + "allowed values go to the trash with their key (meta.delete)"},
	schema.TableKnowledgeRefs:    {none: noRowDelete + "a link goes to the trash with either end (DELETION 3.3, RESOURCES group)"},
	schema.TableRuleVerifierRefs: {none: noRowDelete + "a verifier ref goes with its rule"},
}

// allowOnMemory points a secret allow entry at a freshly added memory.
func allowOnMemory(h *opstest.H) map[string]string {
	id := h.ID(h.Must("memory.add", `{"body":"target"}`))
	return map[string]string{"row_id": opstest.Quote(id), "field": opstest.Quote("body")}
}

// onMemory points a comment or commit link at a freshly added memory.
func onMemory(h *opstest.H) map[string]string {
	id := h.ID(h.Must("memory.add", `{"body":"target"}`))
	return map[string]string{schema.FieldEntityTable: opstest.Quote(string(schema.TableMemories)), schema.FieldEntityID: opstest.Quote(id)}
}

// deletePaths maps every built-in table to its delete path: the
// exceptions above, else the registry's `<noun>.delete` whose noun names
// the table. A table with neither is reported by the caller.
func deletePaths(t *testing.T, h *opstest.H, reg *schema.Registry) map[row.Table]deletePath {
	t.Helper()
	out := map[row.Table]deletePath{}
	for table, p := range deleteExceptions {
		out[table] = p
	}
	for _, e := range h.App.Registry().Entries() {
		name := e.Spec().Name
		noun, ok := strings.CutSuffix(name, deleteSuffix)
		if !ok || strings.Contains(noun, ".") {
			continue
		}
		table, ok := tableOfNoun(reg, noun)
		if !ok {
			continue
		}
		if _, set := out[table]; !set {
			out[table] = deletePath{op: name, input: byID}
		}
	}
	return out
}

// seedFor writes one row of table for p and returns its id and the
// delete input.
func seedFor(h *opstest.H, table row.Table, p deletePath) (string, string) {
	if p.make != nil {
		return p.make(h)
	}
	var set map[string]string
	if p.set != nil {
		set = p.set(h)
	}
	id := h.Seed(table, set)
	return id, p.input(id)
}

// trashItem finds id in `lore trash list --type <table>`.
func trashItem(t *testing.T, h *opstest.H, table row.Table, id string) (trash.Item, bool) {
	t.Helper()
	var out trash.ListOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(trash.OpList, `{"type":"`+string(table)+`"}`))), &out); err != nil {
		t.Fatal(err)
	}
	for _, it := range out.Data {
		if it.ID == id {
			return it, true
		}
	}
	return trash.Item{}, false
}

// tombstones counts the repo store's tombstones in `lore trash stats`.
func tombstones(t *testing.T, h *opstest.H) int {
	t.Helper()
	var st trash.StatsOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(trash.OpStats, ``))), &st); err != nil {
		t.Fatal(err)
	}
	for _, c := range st.Tombstones {
		if c.Name == string(store.SourceRepo) {
			return c.Count
		}
	}
	return 0
}

// alive reads id of table through the engine: nil for an alive row, an
// error naming why not otherwise (a trashed or purged row reads deleted).
func alive(h *opstest.H, table row.Table, id string) error {
	x, err := ids.ParseAny(id)
	if err != nil {
		return err
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{Store: store.SourceRepo, Table: table, ID: x})
	if err != nil {
		return err
	}
	if r.Deleted {
		return fmt.Errorf("%s reads deleted", id)
	}
	return nil
}

// TestEveryTableDeleteTrashRestorePurge is DELETION 3.2's command-tree
// test (FA-DEL-1, DQ8): for every built-in table, one row is seeded and
// deleted through the table's delete operation in wire's real registry
// with --yes; it must land in `lore trash` under its table, `lore trash
// restore` must bring it back alive with the same id, and after a second
// delete `lore trash purge --confirm` must remove it from the trash and
// leave a tombstone (the row reads E_PURGED, trash stats counts it). A
// table added without a delete operation, or without a reason in
// deleteExceptions, fails the test.
func TestEveryTableDeleteTrashRestorePurge(t *testing.T) {
	h := opstest.New(t)
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	paths := deletePaths(t, h, reg)
	for table, p := range deleteExceptions {
		if _, ok := reg.Table(table); !ok {
			t.Errorf("deleteExceptions names %s, which is not a table: drop the entry", table)
		}
		if p.op != "" {
			if _, ok := h.App.Registry().Lookup(p.op); !ok {
				t.Errorf("deleteExceptions: %s's delete %s is not in the registry", table, p.op)
			}
		}
		if (p.op == "") == (p.none == "") {
			t.Errorf("deleteExceptions: %s needs exactly one of op and none", table)
		}
	}
	covered := 0
	for _, def := range reg.Tables() {
		table := def.Name
		p, ok := paths[table]
		if !ok {
			t.Errorf("table %s: no delete operation in the registry and no entry in deleteExceptions", table)
			continue
		}
		if p.none != "" {
			continue
		}
		covered++
		t.Run(string(table), func(t *testing.T) {
			parent := h.T
			h.T = t
			defer func() { h.T = parent }()
			id, del := seedFor(h, table, p)
			h.MustYes(p.op, del)
			it, in := trashItem(t, h, table, id)
			if !in {
				t.Fatalf("%s %s: the row is not in the trash", p.op, id)
			}
			if it.Table != string(table) || it.Batch == "" {
				t.Fatalf("trash item %+v: want table %s and a batch", it, table)
			}
			if err := alive(h, table, id); err == nil {
				t.Fatalf("%s still reads alive after %s", id, p.op)
			}

			h.Must(trash.OpRestore, `{"target":"`+id+`"}`)
			if err := alive(h, table, id); err != nil {
				t.Fatalf("after restore: %v", err)
			}
			if _, in := trashItem(t, h, table, id); in {
				t.Fatal("a restored row is still in the trash")
			}

			before := tombstones(t, h)
			h.MustYes(p.op, del)
			h.Must(trash.OpPurge, `{"target":"`+id+`","confirm":true}`)
			if _, in := trashItem(t, h, table, id); in {
				t.Fatal("a purged row is still in the trash")
			}
			if err := alive(h, table, id); !errs.Has(err, errs.Purged) && err == nil {
				t.Fatalf("after purge: %v, want a tombstone", err)
			}
			if after := tombstones(t, h); after != before+1 {
				t.Fatalf("tombstones of %s: %d → %d, want one more", table, before, after)
			}
			h.Fails(trash.OpRestore, `{"target":"`+id+`"}`, errs.Purged)
		})
	}
	// The walk must reach most tables, so it can never pass by skipping.
	if covered < 30 {
		t.Fatalf("the walk deleted rows of %d tables; the schema has more", covered)
	}
}
