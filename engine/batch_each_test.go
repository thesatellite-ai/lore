package engine

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestCommitEachWritesEveryStore (devco D-925, `lore batch apply`): a
// stage holding rows of two stores, which Commit refuses, is written by
// CommitEach as one backend write per store, in first-written order, with
// every row stored and audited once; the stage is closed afterwards, so a
// second CommitEach writes nothing.
func TestCommitEachWritesEveryStore(t *testing.T) {
	t.Parallel()
	f, sink, _, _ := journalEngine(t)
	ctx := Staging(context.Background())
	m1, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("repo one"))
	if err != nil {
		t.Fatal(err)
	}
	k, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, metaKeyFields("kb-one"))
	if err != nil {
		t.Fatal(err)
	}
	m2, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("repo two"))
	if err != nil {
		t.Fatal(err)
	}
	if got := StagedStores(ctx); !slices.Equal(got, []store.Source{store.SourceRepo, kbStore}) {
		t.Fatalf("StagedStores = %v, want [repo %s]", got, kbStore)
	}
	if storedIDs(t, f.eng, store.SourceRepo, schema.TableMemories)[m1.Row.ID] {
		t.Fatal("a staged row reached the store before CommitEach")
	}
	written, _, err := CommitEach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(written, []store.Source{store.SourceRepo, kbStore}) {
		t.Fatalf("CommitEach wrote %v, want [repo %s]", written, kbStore)
	}
	repo := storedIDs(t, f.eng, store.SourceRepo, schema.TableMemories)
	if !repo[m1.Row.ID] || !repo[m2.Row.ID] || !storedIDs(t, f.eng, kbStore, schema.TableMetaKeys)[k.Row.ID] {
		t.Fatal("CommitEach left a staged row unwritten")
	}
	if sink.recorded(m1.Row.ID) != 1 || sink.recorded(m2.Row.ID) != 1 || sink.recorded(k.Row.ID) != 1 {
		t.Fatal("CommitEach did not audit every row once")
	}
	again, _, err := CommitEach(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("second CommitEach = %v, %v; want nothing written", again, err)
	}
	if len(StagedStores(ctx)) != 0 {
		t.Fatal("a committed stage still reports staged stores")
	}
}
