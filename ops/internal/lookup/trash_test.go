package lookup_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLookupInTrashMatrix (DELETION 3.2): one trashed row per built-in
// table, then every lookup op × every trashed id. An op given another
// table's id answers E_NOT_FOUND as before; an op given an id of its own
// table answers E_IN_TRASH with `lore trash restore <id>` in the hint.
// Every lookup op must answer E_IN_TRASH for at least one id.
func TestLookupInTrashMatrix(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Each table gets its own harness, so the tables run as parallel
	// subtests; hits and names are shared and guarded by mu. LORE_HUMAN is
	// set on this (serial) parent, so the env is the same for every
	// subtest and no other top-level test runs meanwhile.
	var mu sync.Mutex
	hits := map[string]bool{}
	var names []string
	t.Run("tables", func(t *testing.T) {
		for _, def := range reg.Tables() {
			if def.Prefix == "" || def.Kind != (schema.TableDef{}).Kind || def.Name == schema.TableActors || def.Name == schema.TableProjects || def.Name == schema.TableRepos || def.Name == schema.TableSecretRules {
				continue
			}
			if def.NeverClient {
				// team profile and invite rows live only in the org roster
				// store (TEAM_PROFILES TS-1, TS-3): a repo harness holds none,
				// so it cannot trash one (see rosterLookups)
				continue
			}
			if !seedable(reg, def, 0) {
				t.Logf("%s: a required polymorphic ref (skipped)", def.Name)
				continue
			}
			t.Run(def.Name.String(), func(t *testing.T) {
				t.Parallel()
				h := opstest.New(t) // one harness per table: seeds of unique names never collide
				id := h.Seed(def.Name, nil)
				aid, perr := ids.ParseAny(id)
				if perr != nil {
					t.Fatal(perr)
				}
				if _, derr := h.App.Delete(ctx, engine.Ref{Store: store.SourceRepo, Table: def.Name, ID: aid}); derr != nil {
					t.Logf("delete %s: %v (skipped)", def.Name, derr)
					return
				}
				for _, e := range h.App.Registry().Entries() {
					if !isLookup(e) {
						continue
					}
					name := e.Spec().Name
					if strings.HasPrefix(name, "trash.") || name == "meta.show" {
						continue // the trash shows trashed rows; meta.show is ops/settings'
					}
					mu.Lock()
					if _, seen := hits[name]; !seen {
						hits[name] = false
						names = append(names, name)
					}
					mu.Unlock()
					_, err := h.RunYes(name, inputFor(e, id))
					switch {
					case errs.Has(err, errs.InTrash):
						mu.Lock()
						hits[name] = true
						mu.Unlock()
						if !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "lore trash restore --target "+id) {
							t.Errorf("%s %s: %v: no restore hint", name, id, err)
						}
					case errs.Has(err, errs.NotFound):
					case errs.Has(err, errs.InvalidID):
						// another table's id given to this op: B-ERRS-1 (the
						// own-prefix answers are pinned by TestLookupIDMatrix)
					default:
						t.Errorf("%s %s (%s): %v, want E_IN_TRASH, E_NOT_FOUND or E_INVALID_ID", name, id, def.Name, err)
					}
				}
			})
		}
	})
	slices.Sort(names) // subtests finish in any order
	var never []string
	for _, n := range names {
		if !hits[n] && !trashExempt[n] {
			never = append(never, n)
		}
	}
	if len(never) > 0 {
		t.Errorf("%d lookup ops never answer E_IN_TRASH: %s", len(never), strings.Join(never, " "))
	}
}

// trashExempt are lookup ops the matrix cannot reach with a trashed row of
// their own table: the table is never trashed here (actors are never
// deleted; projects and repos are refused while they hold rows; team
// profile rows live only in the org roster, which this no-org repo
// harness has none of: rosterLookups), the id is
// not a row id (batch, time entry, machine), the table has a required
// polymorphic ref Seed cannot fill (comments, links), the input's --to is
// the row's own store (moves: engine.Move answers E_IN_TRASH through
// deletedErr), or the op lives in ops/settings (its own owner).
var trashExempt = map[string]bool{
	"actor.show": true, "project.archive": true, "project.delete": true, "project.show": true, "project.unarchive": true,
	"repo.archive": true, "repo.delete": true, "repo.unarchive": true,
	"batch.show": true, "task.time.edit": true, "task.time.rm": true, "machine.show": true,
	"actor.contact.edit": true, "actor.contact.remove": true,
	"comment.delete": true, "link.remove": true, "memory.move": true, "row.move": true,
	"meta.delete": true, "meta.edit": true, "resource.archive": true, "resource.check": true, "resource.delete": true,
	"resource.edit": true, "resource.show": true, "secrets.allow.rm": true, "secrets.show": true, "vars.rm": true, "vars.show": true,
}

// seedable reports whether opstest's Seed can fill def's required fields
// (a required ref with no target table cannot be sampled).
func seedable(reg *schema.Registry, def schema.TableDef, depth int) bool {
	if depth > 5 {
		return false
	}
	for _, f := range def.Fields {
		if !f.Required || f.Kind != value.KindRef || f.RefTable == schema.TableProjects || f.RefTable == schema.TableActors {
			continue
		}
		next, ok := reg.Table(f.RefTable)
		if f.RefTable == "" || !ok || !seedable(reg, next, depth+1) {
			return false
		}
	}
	return true
}
