package lookup_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// asksAlways reports whether e is a lookup op that always asks first.
func asksAlways(e op.Entry) bool { return isLookup(e) && e.Spec().Confirm.Always }

// TestConfirmAfterLookupMatrix (G-ENGINE-4, dcdbe767): the lookup matrices
// run with --yes; this one runs every always-ask lookup op WITHOUT it, so
// the order is pinned: a well-formed id of any table that names no row
// answers E_NOT_FOUND, and a trashed row of the op's own table E_IN_TRASH,
// before E_NEEDS_CONFIRM is ever answered: nobody is asked to confirm a
// write that cannot happen.
func TestConfirmAfterLookupMatrix(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	h := opstest.New(t)
	var unknown []string
	for _, def := range reg.Tables() {
		if def.Prefix == "" || def.Kind != (schema.TableDef{}).Kind {
			continue
		}
		unknown = append(unknown, string(def.Prefix)+"_01a1a5aca7f57659be7320c5f4b7f00d")
	}
	n := 0
	for _, e := range h.App.Registry().Entries() {
		if !asksAlways(e) {
			continue
		}
		n++
		name := e.Spec().Name
		for _, id := range unknown {
			_, err := h.Run(name, inputFor(e, id))
			if errs.Has(err, errs.NeedsConfirm) {
				t.Errorf("%s %s: asked before the lookup: %v", name, id, err)
			} else if !errs.Has(err, errs.NotFound) {
				t.Errorf("%s %s: %v, want E_NOT_FOUND", name, id, err)
			}
		}
	}
	if n < 15 {
		t.Fatalf("only %d always-ask lookup ops found: the registry walk is broken", n)
	}

	// a trashed row of the op's own table: E_IN_TRASH before asking
	ctx := context.Background()
	inTrash := 0
	for _, def := range reg.Tables() {
		if def.Prefix == "" || def.Kind != (schema.TableDef{}).Kind || def.Name == schema.TableActors || def.Name == schema.TableProjects || def.Name == schema.TableRepos || def.Name == schema.TableSecretRules {
			continue
		}
		if !seedable(reg, def, 0) {
			continue
		}
		th := opstest.New(t)
		id := th.Seed(def.Name, nil)
		aid, perr := ids.ParseAny(id)
		if perr != nil {
			t.Fatal(perr)
		}
		if _, derr := th.App.Delete(ctx, engine.Ref{Store: store.SourceRepo, Table: def.Name, ID: aid}); derr != nil {
			continue
		}
		for _, e := range th.App.Registry().Entries() {
			name := e.Spec().Name
			if !asksAlways(e) || !strings.HasPrefix(name, strings.TrimSuffix(string(def.Name), "s")+".") || !strings.HasSuffix(name, ".delete") {
				continue
			}
			_, err := th.Run(name, inputFor(e, id))
			if !errs.Has(err, errs.InTrash) {
				t.Errorf("%s of a trashed %s: %v, want E_IN_TRASH before any question", name, def.Name, err)
				continue
			}
			inTrash++
		}
	}
	if inTrash < 5 {
		t.Fatalf("only %d delete ops checked on a trashed row: the walk is broken", inTrash)
	}
	t.Logf("%d always-ask lookup ops x %d unknown ids; %d trashed-row deletes", n, len(unknown), inTrash)
}
