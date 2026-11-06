package wire_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opslive "github.com/thesatellite-ai/lore/ops/live"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// listSuffix marks a noun's list operation.
const listSuffix = ".list"

// tableOfNoun maps a noun ("memory", "tasklist", "techdoc") to its
// built-in table (memories, task_lists, tech_docs: underscores dropped,
// plural forms -s, -es, -ies); ok is false for a noun with no table.
func tableOfNoun(reg *schema.Registry, noun string) (row.Table, bool) {
	noun = strings.ReplaceAll(noun, "_", "")
	want := map[string]bool{noun + "s": true, noun + "es": true}
	if stem, ok := strings.CutSuffix(noun, "y"); ok {
		want[stem+"ies"] = true
	}
	for _, def := range reg.Tables() {
		if want[strings.ReplaceAll(string(def.Name), "_", "")] {
			return def.Name, true
		}
	}
	return "", false
}

// requiredInputs lists e's required input fields (a list that needs a
// kind or an id cannot be run with no input).
func requiredInputs(e op.Entry) []string {
	var out []string
	for _, f := range e.InputFields() {
		if f.Required {
			out = append(out, f.Name)
		}
	}
	return out
}

// hasInput reports whether e takes an input field named name.
func hasInput(e op.Entry, name string) bool {
	for _, f := range e.InputFields() {
		if f.Name == name {
			return true
		}
	}
	return false
}

// TestDeletedRowHiddenOnEveryList is DELETION 3.2's command-tree walk over
// wire's real registry: for every noun list that offers --include-deleted,
// one row of the noun's table is written and moved to the trash; the
// plain list must not show it, --include-deleted and --deleted-only must.
// A new noun list therefore cannot forget the rule. The walk must reach a
// minimum number of lists, so it can never pass by finding none.
func TestDeletedRowHiddenOnEveryList(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range h.App.Registry().Entries() {
		name := e.Spec().Name
		noun, isList := strings.CutSuffix(name, listSuffix)
		if !isList || !hasInput(e, "include_deleted") {
			continue
		}
		table, ok := tableOfNoun(reg, noun)
		if !ok {
			t.Errorf("%s offers --include-deleted but no table matches noun %q", name, noun)
			continue
		}
		if needs := requiredInputs(e); len(needs) > 0 {
			t.Logf("%s skipped: needs %v", name, needs)
			continue
		}
		t.Run(name, func(t *testing.T) {
			id := h.Seed(table, nil)
			if live, lerr := h.Run(name, ``); lerr != nil || !strings.Contains(h.JSON(live), id) {
				t.Skipf("%s does not list a freshly seeded %s row (%v): the walk cannot judge it", name, table, lerr)
			}
			x, perr := ids.ParseAny(id)
			if perr != nil {
				t.Fatal(perr)
			}
			if _, derr := h.App.Delete(context.Background(), engine.Ref{Store: store.SourceRepo, Table: table, ID: x}); derr != nil {
				t.Fatalf("delete %s: %v", id, derr)
			}
			plain, rerr := h.Run(name, ``)
			if rerr != nil {
				t.Fatalf("%s: %v", name, rerr)
			}
			if strings.Contains(h.JSON(plain), id) {
				t.Fatalf("%s shows the trashed row %s", name, id)
			}
			for _, in := range []string{`{"include_deleted":true}`, `{"deleted_only":true}`} {
				if in == `{"deleted_only":true}` && !hasInput(e, "deleted_only") {
					continue
				}
				res, ierr := h.Run(name, in)
				if ierr != nil {
					t.Fatalf("%s %s: %v", name, in, ierr)
				}
				if !strings.Contains(h.JSON(res), id) {
					t.Fatalf("%s %s does not show the trashed row %s: %s", name, in, id, h.JSON(res))
				}
			}
		})
		checked++
	}
	if checked < 20 {
		t.Fatalf("the walk reached %d noun lists; the registry has more", checked)
	}
}

// purgeModes are the inputs of every way trash.purge removes content
// (DELETION 3.5, 3.12): by target, --older-than, --expired,
// --archived-before. The target is filled in per run.
var purgeModes = []string{`{"target":"%s"}`, `{"older_than":"0d"}`, `{"expired":true}`, `{"archived_before":"0d"}`}

// TestEveryPurgePathRefusesYes (DQ2, DELETION 709): every operation in
// wire's registry that can purge — trash.purge in each mode, every delete
// with --purge (op.PurgeFlags), and any operation whose name or input
// names "purge" — answers E_NEEDS_CONFIRM
// for a caller that passed --yes but not --confirm, names --confirm in
// next, and removes nothing. An unknown purge-like operation fails the
// test until its modes are listed here.
func TestEveryPurgePathRefusesYes(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := h.ID(h.Must("memory.add", `{"body":"stays in the trash"}`))
	h.MustYes("memory.delete", `{"id":"`+id+`"}`)
	h.MustYes("memory.archive", `{"id":"`+h.ID(h.Must("memory.add", `{"body":"archived"}`))+`"}`)
	trashCount := func() int {
		rows, err := h.App.Trashed(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	before := trashCount()

	purgers := 0
	for _, e := range h.App.Registry().Entries() {
		name := e.Spec().Name
		purgeLike := strings.Contains(name, "purge")
		for _, f := range e.InputFields() {
			purgeLike = purgeLike || strings.Contains(f.Name, "purge")
		}
		if !purgeLike {
			continue
		}
		purgers++
		modes := purgeModes
		switch {
		case hasInput(e, "purge") && hasInput(e, "confirm"):
			// A delete's op.PurgeFlags (P3-DEL-1): the engine refuses
			// before the id is looked up, so the memory's id serves every
			// noun and nothing is removed either way.
			modes = []string{`{"id":"%s","purge":true}`}
		case name == opssync.OpPurge:
			// v0.1.17's `lore sync purge` spelling (FA-DEL-4) runs in the
			// binary's sync host (Options.Sync, none here); that host
			// refuses without the literal --confirm, never on --yes.
			continue
		case name == opslive.OpPurge:
			// DELETION_DESIGN (command table): `lore live purge` is unchanged,
			// the leak-cleanup tool of the live ref; it removes no trashed row
			// and asks as v0.1.17 did, so it is not a trash purge path.
			continue
		case name != "trash.purge":
			t.Errorf("%s can purge but is not walked: add its modes to this test", name)
			continue
		}
		for _, mode := range modes {
			in := strings.Replace(mode, "%s", id, 1)
			_, err := h.RunYes(name, in)
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != errs.NeedsConfirm {
				t.Fatalf("%s %s with --yes: %v, want E_NEEDS_CONFIRM", name, in, err)
			}
			if e.Next == nil || !strings.Contains(strings.Join(e.Next.Args, " "), "--confirm") || strings.Contains(strings.Join(e.Next.Args, " "), "--yes") {
				t.Fatalf("%s %s: next %+v must name --confirm, never --yes", name, in, e.Next)
			}
		}
	}
	if purgers == 0 {
		t.Fatal("no purge operation found in the registry")
	}
	if after := trashCount(); after != before {
		t.Fatalf("a refused purge changed the trash: %d → %d rows", before, after)
	}
	archived, err := h.App.List(context.Background(), store.SourceRepo, schema.TableMemories)
	if err != nil || len(archived) == 0 {
		t.Fatalf("a refused --archived-before purge removed the archived memory: %d %v", len(archived), err)
	}
}
