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
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
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
	for _, def := range reg.Tables() {
		if def.Noun != "" && def.Noun == noun { // schema.Noun: "link" → commit_links
			return def.Name, true
		}
	}
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

// fieldProjectID is the project column a content table may carry.
const fieldProjectID = "project_id"

// walkKind is the resource kind the walk creates for resource.list.
const walkKind = "walk_box"

// walkListSetups prepares the lists whose input has a required field:
// it returns the fields to seed the row with (JSON literals, as
// opstest.H.Seed takes them) and the base input members (no braces) every
// run of the list carries. A list needing input without an entry here
// fails the walk, so a new one cannot slip past it.
var walkListSetups = map[string]func(h *opstest.H) (map[string]string, string){
	// resource.list lists one kind (lore resource list --kind <kind>).
	"resource.list": func(h *opstest.H) (map[string]string, string) {
		kind := h.ID(h.Must("resource.kind.add", `{"kind":"`+walkKind+`"}`))
		return map[string]string{"kind": opstest.Quote(kind)}, `"kind":"` + walkKind + `"`
	},
}

// walkInput joins the base members and extra into one JSON object.
func walkInput(base, extra string) string {
	switch {
	case base == "":
		return "{" + extra + "}"
	case extra == "":
		return "{" + base + "}"
	}
	return "{" + base + "," + extra + "}"
}

// hasField reports whether table defines a field named name.
func hasField(reg *schema.Registry, table row.Table, name string) bool {
	def, ok := reg.Table(table)
	if !ok {
		return false
	}
	for _, f := range def.Fields {
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
		setup, hasSetup := walkListSetups[name]
		if needs := requiredInputs(e); len(needs) > 0 && !hasSetup {
			t.Errorf("%s needs %v: give it an entry in walkListSetups so the walk can run it", name, needs)
			continue
		}
		t.Run(name, func(t *testing.T) {
			set, base := map[string]string{}, ""
			if hasSetup {
				set, base = setup(h)
			}
			// A table with a project column gets the repo's project: a
			// list without --project shows the repo project's rows only
			// (playbook.list, as v0.1.17's list did), so a row with no
			// project would never be listed and the walk could not judge it.
			if _, given := set[fieldProjectID]; !given && hasField(reg, table, fieldProjectID) {
				set[fieldProjectID] = opstest.Quote(h.Project)
			}
			id := h.Seed(table, set)
			input := func(extra string) string { return walkInput(base, extra) }
			if live, lerr := h.Run(name, input(``)); lerr != nil || !strings.Contains(h.JSON(live), id) {
				t.Fatalf("%s does not list a freshly seeded %s row (%v): %s", name, table, lerr, h.JSON(live))
			}
			x, perr := ids.ParseAny(id)
			if perr != nil {
				t.Fatal(perr)
			}
			if _, derr := h.App.Delete(context.Background(), engine.Ref{Store: store.SourceRepo, Table: table, ID: x}); derr != nil {
				t.Fatalf("delete %s: %v", id, derr)
			}
			plain, rerr := h.Run(name, input(``))
			if rerr != nil {
				t.Fatalf("%s: %v", name, rerr)
			}
			if strings.Contains(h.JSON(plain), id) {
				t.Fatalf("%s shows the trashed row %s", name, id)
			}
			for _, in := range []string{`"include_deleted":true`, `"deleted_only":true`} {
				if in == `"deleted_only":true` && !hasInput(e, "deleted_only") {
					continue
				}
				res, ierr := h.Run(name, input(in))
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
		case name == opsstores.OpFolderRemove:
			// `lore store folder remove --purge` asks for the literal
			// --confirm once it has counted the folder's rows and links,
			// so it needs a store with that folder, which this harness has
			// none of: wire TestFolderRemoveWithRows walks --purge --yes
			// (E_NEEDS_CONFIRM naming --confirm, never --yes) and
			// --confirm.
			continue
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
			// `lore live purge`, the leak-cleanup tool of the live ref, asks
			// like trash purge (only the literal --confirm purges), but only
			// once a live ref exists, which this harness has none of:
			// ops/live TestPurgeConfirmAndDryRun and tests/userscen
			// TestCampaignSync_LivePurgeDryRunAndConfirm walk it.
			continue
		case name == opssetup.OpTrashPurge:
			// lore setup trash purge --name (owner decision 2026-11-06): a trashed
			// setup of config.yaml, refused before the lookup, so any name
			// serves and nothing is read or removed.
			modes = []string{`{"name":"any-setup"}`, `{"name":"any-setup","clear_identity":true}`}
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
