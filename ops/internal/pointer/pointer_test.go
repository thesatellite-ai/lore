package pointer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// target is the row every case points at; where it lives is per case.
const target = "mem_01a1a5aca7f57659be7320c5f4b70011"

// nameOnly is a Getter without a Classifier: stores are placed by name.
type nameOnly struct {
	at  store.Source
	err error
}

func (g nameOnly) Get(context.Context, engine.Ref) (store.Source, row.Row, error) {
	return g.at, row.Row{}, g.err
}

// classified adds wire's StoreClass answer (per store name).
type classified struct {
	nameOnly
	class map[store.Source]direction.Class
	err   error
}

func (c classified) StoreClass(_ context.Context, name store.Source) (direction.Class, error) {
	return c.class[name], c.err
}

func ref(t *testing.T, table row.Table) engine.Ref {
	t.Helper()
	id, err := ids.ParseAny(target)
	if err != nil {
		t.Fatal(err)
	}
	return engine.Ref{Table: table, ID: id}
}

// TestCheckNameOnlyFallback is the F15 table with no Classifier: the repo
// store and the default target ("") are client-class (fail closed), "me"
// and "me/<folder>" personal, every other attached name internal.
func TestCheckNameOnlyFallback(t *testing.T) {
	cases := []struct {
		from, at store.Source
		refused  bool
	}{
		{store.SourceRepo, store.SourceRepo, false},
		{store.SourceRepo, "agency", true},
		{store.SourceRepo, "agency/org", true},
		{store.SourceRepo, "me", true},
		{store.SourceRepo, "me/notes", true},
		{"", "agency", true}, // default target counts as the repo
		{"", store.SourceRepo, false},
		{"agency", store.SourceRepo, false},
		{"agency", "agency/org", false},
		{"agency", "me", true},
		{"agency", "me/notes", true},
		{"me", "agency", false},
		{"me", store.SourceRepo, false},
		{"me", "me/notes", false},
		{"me/notes", "me", false},
		{"ünïcode", "me", true},
	}
	for _, c := range cases {
		err := Check(context.Background(), nameOnly{at: c.at}, c.from, ref(t, schema.TableMemories))
		if got := errs.Has(err, errs.LeakBlocked); got != c.refused || (!c.refused && err != nil) {
			t.Errorf("from %q to row in %q: err %v, refused want %v", c.from, c.at, err, c.refused)
		}
		if c.refused && err != nil && !strings.Contains(err.Error(), "pointer to "+target) {
			t.Errorf("from %q to %q: message does not name the target: %v", c.from, c.at, err)
		}
	}
}

// TestCheckClassifier: wire's StoreClass decides, not the name. An internal
// repo (an org's own repo) may point into the org store; a store wire
// calls client may not, whatever its name; an unknown class is placed
// strictly (from: client, to: personal).
func TestCheckClassifier(t *testing.T) {
	cls := map[store.Source]direction.Class{
		store.SourceRepo: direction.Internal,
		"agency":         direction.Internal,
		"clientstore":    direction.Client,
		"me":             direction.Personal,
		"odd":            direction.Class("weird"),
	}
	cases := []struct {
		from, at store.Source
		refused  bool
	}{
		{store.SourceRepo, "agency", false}, // internal repo -> internal store
		{"clientstore", "agency", true},
		{"clientstore", store.SourceRepo, true},
		{"agency", "clientstore", false},
		{"agency", "me", true},
		{"odd", "agency", true}, // unknown from = client
		{"agency", "odd", true}, // unknown to = personal
		{"me", "odd", false},    // personal may point at personal
		{"clientstore", "clientstore", false},
	}
	for _, c := range cases {
		env := classified{nameOnly: nameOnly{at: c.at}, class: cls}
		err := Check(context.Background(), env, c.from, ref(t, schema.TableMemories))
		if got := errs.Has(err, errs.LeakBlocked); got != c.refused || (!c.refused && err != nil) {
			t.Errorf("from %q to %q: err %v, refused want %v", c.from, c.at, err, c.refused)
		}
	}
}

// TestCheckRosterNotFoundAndErrors: a person or team may be named from
// anywhere (the roster is exempt, without even a read); a target not
// loaded here is allowed (its readers say "not available here"); any other
// read error and a Classifier error are returned (fail closed).
func TestCheckRosterNotFoundAndErrors(t *testing.T) {
	ctx := context.Background()
	readErr := errs.New(errs.StoreUnreachable, "boom")
	for _, tbl := range []row.Table{schema.TableActors, schema.TableTeams} {
		// the getter would fail and the class would refuse: never asked
		env := classified{nameOnly: nameOnly{at: "me", err: readErr}, class: map[store.Source]direction.Class{"me": direction.Personal}}
		if err := Check(ctx, env, store.SourceRepo, ref(t, tbl)); err != nil {
			t.Errorf("roster %s: %v", tbl, err)
		}
	}
	if err := Check(ctx, nameOnly{err: errs.New(errs.NotFound, "no row")}, store.SourceRepo, ref(t, schema.TableMemories)); err != nil {
		t.Errorf("not found: %v, want allowed", err)
	}
	if err := Check(ctx, nameOnly{err: readErr}, store.SourceRepo, ref(t, schema.TableMemories)); !errs.Has(err, errs.StoreUnreachable) {
		t.Errorf("read error: %v, want it returned", err)
	}
	classErr := errs.New(errs.Internal, "links unreadable")
	env := classified{nameOnly: nameOnly{at: "agency"}, err: classErr}
	if err := Check(ctx, env, "agency", ref(t, schema.TableMemories)); !errs.Has(err, errs.Internal) {
		t.Errorf("classifier error: %v, want it returned", err)
	}
}

// TestCheckKeepsHint: the refusal keeps direction's code and hint while
// naming the target.
func TestCheckKeepsHint(t *testing.T) {
	err := Check(context.Background(), nameOnly{at: "me"}, "agency", ref(t, schema.TableMemories))
	var e *errs.Error
	if !errors.As(err, &e) || e.Hint == "" || !strings.Contains(e.Hint, "--to me") {
		t.Fatalf("hint lost: %#v", err)
	}
}
