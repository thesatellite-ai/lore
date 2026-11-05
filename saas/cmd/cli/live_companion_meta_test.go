package main

// PASS3-A-1 (METADATA 3.10): a metadata key row registered by a live write
// rides live as a companion of the content row that uses it, like an actor
// row (FLAG6); one no content row carried goes to the branch data.

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"

	"saas/pkg/aicoder/live"
)

// ltMkyID is a fixed meta_keys row id (valid, never a real row).
const ltMkyID = "mky_01a1a6841fc0784d92a62b0acf148b29"

func TestLiveBackend_MetaKeyRidesLiveAsCompanion(t *testing.T) {
	ctx := context.Background()
	b, inner, s := newLiveFixture(t)
	// engine/meta.go registers the key in its own write before the row.
	k := ltRow(t, schema.TableMetaKeys, ltMkyID, 1, "newkey", false)
	if err := b.Write(ctx, []store.Change{{Row: k}}); err != nil {
		t.Fatal(err)
	}
	if liveHas(t, b, s, schema.TableMetaKeys, ltMkyID) {
		t.Fatal("a held key row is not written before a content row carries it")
	}
	if got, err := b.Get(ctx, k.Table, k.ID); err != nil || got.HLC.Compare(k.HLC) != 0 {
		t.Fatalf("a held key row reads back within the command: %v", err)
	}
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, tableTasks, ltTaskA, 2, "uses newkey", false)}}); err != nil {
		t.Fatal(err)
	}
	_, files, err := b.liveFiles(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := files[live.RowKey{Table: schema.TableMetaKeys, ID: ltMkyID}]
	if !ok || !f.Block.Companion {
		t.Fatalf("the key row rides live with the task, marked companion: present=%v companion=%v", ok, f.Block.Companion)
	}
	if _, err = inner.Get(ctx, k.Table, k.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the key row is not in the branch data: %v", err)
	}
	if len(s.actorCompanions) != 0 {
		t.Fatal("a carried companion is no longer held")
	}
}

func TestLiveBackend_LoneMetaKeyFlushesToBranch(t *testing.T) {
	ctx := context.Background()
	b, inner, s := newLiveFixture(t)
	k := ltRow(t, schema.TableMetaKeys, ltMkyID, 1, "newkey", false)
	if err := b.Write(ctx, []store.Change{{Row: k}}); err != nil {
		t.Fatal(err)
	}
	if err := s.flushActorCompanions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Get(ctx, k.Table, k.ID); err != nil {
		t.Fatalf("a key row no content row carried lands in the branch data: %v", err)
	}
	if liveHas(t, b, s, schema.TableMetaKeys, ltMkyID) {
		t.Fatal("a lone key row never goes live")
	}
}
