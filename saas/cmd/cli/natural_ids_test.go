package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dbent"
	"dbent/gen/ent"
	"dbent/pkg/dbtemplate"

	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lsync"
)

// openTestClient returns an ent client over a freshly migrated temp DB.
func openTestClient(t *testing.T) *ent.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lore.db")
	if err := dbtemplate.Copy(templateDB, path); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(path)
	if err := dbent.ApplyPragmas(db); err != nil {
		t.Fatal(err)
	}
	client := dbent.New(db).Client()
	client.Use(naturalIDHook())
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestNaturalIDSpecs_MatchRegistry fails when a synced table gains (or
// changes) a natural unique key that the id hook does not know about, so
// the two lists can never drift apart silently.
func TestNaturalIDSpecs_MatchRegistry(t *testing.T) {
	t.Parallel()
	reg, _ := lsync.NewRegistry()
	byTable := map[string]naturalIDSpec{}
	for _, s := range naturalIDSpecs {
		byTable[s.table] = s
	}
	for _, name := range reg.Names() {
		tb, _ := reg.Table(name)
		for _, key := range tb.NaturalKeys {
			spec, ok := byTable[name]
			if !ok {
				t.Errorf("synced table %s has natural key %v but no naturalIDSpecs entry", name, key)
				continue
			}
			if reflect.DeepEqual(spec.fields, key) {
				continue
			}
			// A table may carry several unique keys; one must match.
			matched := false
			for _, k := range tb.NaturalKeys {
				matched = matched || reflect.DeepEqual(spec.fields, k)
			}
			if !matched {
				t.Errorf("%s: spec fields %v match none of %v", name, spec.fields, tb.NaturalKeys)
			}
		}
	}
	for typ, s := range naturalIDSpecs {
		if err := func() error { _, err := ids.Deterministic(s.prefix, "x"); return err }(); err != nil {
			t.Errorf("%s: bad prefix %q: %v", typ, s.prefix, err)
		}
	}
}

func TestNaturalIDHook_SameKeySameID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, b := openTestClient(t), openTestClient(t)
	const project = "prj_01a10237c9a279ee94a32b8450dfb6f9"
	ta, err := a.Tag.Create().SetProjectID(project).SetName("auth").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tb, err := b.Tag.Create().SetProjectID(project).SetName("auth").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ta.ID != tb.ID {
		t.Fatalf("two clones minted different ids for the same tag: %s vs %s", ta.ID, tb.ID)
	}
	other, err := a.Tag.Create().SetProjectID(project).SetName("db").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == ta.ID || !strings.HasPrefix(other.ID, ids.PrefixTag+"_") {
		t.Fatalf("bad id %s", other.ID)
	}
	act, err := a.Actor.Create().SetKind("agent").SetDisplayName("bot").SetStableKey("agent:bot").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := ids.Deterministic(ids.PrefixActor, "agent:bot")
	if act.ID != want {
		t.Fatalf("actor id = %s want %s", act.ID, want)
	}
	// Entities without a natural key keep random UUIDv7 ids.
	p1, err := a.Project.Create().SetName("same").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := b.Project.Create().SetName("same").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p1.ID == p2.ID {
		t.Fatal("projects must not get deterministic ids")
	}
	// Updates never touch ids.
	upd, err := a.Tag.UpdateOneID(ta.ID).SetName("authn").Save(ctx)
	if err != nil || upd.ID != ta.ID {
		t.Fatalf("update changed id: %v %v", upd, err)
	}
}

// Review finding: deterministic ids come from editable fields. Creating a
// row under the OLD key of a renamed row must still succeed.
func TestNaturalIDHook_RenamedRowDoesNotBlockRecreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := openTestClient(t)
	const project = "prj_01a10237c9a279ee94a32b8450dfb6f9"
	first, err := c.Prompt.Create().SetProjectID(project).SetName("deploy").SetDescription("d").SetBody("b").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Prompt.UpdateOneID(first.ID).SetName("release").Save(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := c.Prompt.Create().SetProjectID(project).SetName("deploy").SetDescription("d").SetBody("b2").Save(ctx)
	if err != nil {
		t.Fatalf("re-create under the old name failed: %v", err)
	}
	if again.ID == first.ID {
		t.Fatal("ids must differ")
	}
	// A real natural-key duplicate still fails.
	if _, err := c.Prompt.Create().SetProjectID(project).SetName("deploy").SetDescription("d").SetBody("b3").Save(ctx); err == nil {
		t.Fatal("duplicate (project, name) must still be refused")
	}
}
