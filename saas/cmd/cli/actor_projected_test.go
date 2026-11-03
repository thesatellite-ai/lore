package main

import (
	"context"
	"path/filepath"
	"testing"

	"dbent"
	"dbent/gen/ent"
	entActor "dbent/gen/ent/actor"
	"dbent/pkg/dbtemplate"

	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lsync"
)

// projectedTestKey is the person whose row arrives as a client projection.
const projectedTestKey = "human:dev1@agency.test"

// openRawTestClient is openTestClient with the raw *sql.DB registered the
// way resolveContext registers it, so actorByKey can adopt a projection.
func openRawTestClient(t *testing.T) *ent.Client {
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
	clientToRawDB[client] = db
	t.Cleanup(func() { delete(clientToRawDB, client); _ = client.Close() })
	return client
}

// seedProjectedActor leaves lore.db the way a fresh clone of a client repo
// leaves it: the person's row under its derived id, holding only the
// projection's placeholder key (lsync upsertRow, D8).
func seedProjectedActor(t *testing.T, ctx context.Context, client *ent.Client, key string) string {
	t.Helper()
	a, err := client.Actor.Create().SetKind(entActor.KindHuman).SetDisplayName("Dev One").SetStableKey(key).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDBFromClient(client).ExecContext(ctx, `UPDATE actors SET stable_key = ? WHERE id = ?`, lsync.ProjectedKeyPrefix+a.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// TestUpsertActor_ReusesProjectedRowOnSecondMachine pins N1: the same
// person on a second machine (fresh clone, the repo holds only their
// projection) gets their existing row back, never a second actor.
func TestUpsertActor_ReusesProjectedRowOnSecondMachine(t *testing.T) {

	ctx := context.Background()
	want, err := ids.Deterministic(ids.PrefixActor, projectedTestKey)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		seed func(t *testing.T, client *ent.Client)
	}{
		{"row imported as a projection", func(t *testing.T, c *ent.Client) { seedProjectedActor(t, ctx, c, projectedTestKey) }},
		{"row with the real key", func(t *testing.T, c *ent.Client) {
			if _, err := c.Actor.Create().SetKind(entActor.KindHuman).SetDisplayName("Dev One").SetStableKey(projectedTestKey).Save(ctx); err != nil {
				t.Fatal(err)
			}
		}},
		{"no row yet", func(*testing.T, *ent.Client) {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := openRawTestClient(t)
			tc.seed(t, client)
			r := identity.Resolved{StableKey: projectedTestKey, DisplayName: "Dev One", Kind: "human"}
			for run := 1; run <= 2; run++ {
				a, err := upsertActor(ctx, client, r)
				if err != nil {
					t.Fatalf("run %d: %v", run, err)
				}
				if a.ID != want {
					t.Fatalf("run %d: actor %s, want the id derived from the key %s", run, a.ID, want)
				}
				if a.StableKey != projectedTestKey {
					t.Fatalf("run %d: stable_key %q, want the real key in lore.db", run, a.StableKey)
				}
			}
			if n := client.Actor.Query().CountX(ctx); n != 1 {
				t.Fatalf("%d actor rows for one person, want 1", n)
			}
		})
	}
}

// TestActorByKey_ReadOnlyNeverAdopts: a read-side lookup (@me filters)
// finds the projected row but leaves its placeholder key alone.
func TestActorByKey_ReadOnlyNeverAdopts(t *testing.T) {

	ctx := context.Background()
	client := openRawTestClient(t)
	id := seedProjectedActor(t, ctx, client, projectedTestKey)
	a, err := actorByKey(ctx, client, projectedTestKey, false)
	if err != nil || a.ID != id {
		t.Fatalf("got %v, %v; want %s", a, err, id)
	}
	if a.StableKey != lsync.ProjectedKeyPrefix+id {
		t.Fatalf("a read-only lookup rewrote the key: %q", a.StableKey)
	}
	if _, err := actorByKey(ctx, client, "human:someone-else@agency.test", true); !ent.IsNotFound(err) {
		t.Fatalf("another person's key must not match the projection: %v", err)
	}
}

// TestNaturalIDHook_ActorNeverFallsBackToRandomID: the random-id fallback
// is for editable keys; for actors it would mint a duplicate person (N1).
func TestNaturalIDHook_ActorNeverFallsBackToRandomID(t *testing.T) {

	ctx := context.Background()
	client := openRawTestClient(t)
	seedProjectedActor(t, ctx, client, projectedTestKey)
	_, err := client.Actor.Create().SetKind(entActor.KindHuman).SetDisplayName("Dev One").SetStableKey(projectedTestKey).Save(ctx)
	if err == nil {
		t.Fatal("a create colliding with the person's own projected row must fail, not mint a random id")
	}
	if n := client.Actor.Query().CountX(ctx); n != 1 {
		t.Fatalf("%d actor rows, want 1", n)
	}
}
