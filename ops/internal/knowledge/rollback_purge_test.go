package knowledge_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/rule"
	"github.com/thesatellite-ai/lore/wire"
)

// markFails is the App with every Update of row failOn refused, so a
// --supersedes add writes its new row and then cannot mark the old one.
type markFails struct {
	*wire.App
	failOn  string
	deleted []string
}

func (f *markFails) Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error) {
	if ref.ID.String() == f.failOn {
		return engine.Written{}, errs.New(errs.StoreLocked, "injected: store locked")
	}
	return f.App.Update(ctx, ref, patch)
}

func (f *markFails) Delete(ctx context.Context, ref engine.Ref) (engine.Written, error) {
	f.deleted = append(f.deleted, ref.ID.String())
	return f.App.Delete(ctx, ref)
}

// TestSupersedesRollbackPurges (later 498): a rule add whose --supersedes
// mark fails removes the new rule by purging it, so "nothing was kept" is
// true on the real engine: the new id is not live, not in the trash, and
// the old rule stays unmarked.
func TestSupersedesRollbackPurges(t *testing.T) {
	h := opstest.New(t)
	old := h.ID(h.Must("rule.add", `{"body":"first"}`))
	f := &markFails{App: h.App, failOn: old}
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	var add op.Entry
	for _, e := range rule.Ops(f) {
		if e.Spec().Name == "rule.add" {
			add = e
		}
	}
	if add == nil {
		t.Fatal("no rule.add")
	}
	_, err = h.App.Run(context.Background(), add, op.JSONDecoder{Body: []byte(`{"body":"second","supersedes":"` + old + `"}`)}, op.Caller{Actor: actor})
	if !errs.Has(err, errs.StoreLocked) || !strings.Contains(err.Error(), "nothing was kept") {
		t.Fatalf("failed mark: %v", err)
	}
	if len(f.deleted) != 1 {
		t.Fatalf("rollback deleted %v, want the new rule", f.deleted)
	}
	if _, err := h.Run("rule.show", `{"id":"`+f.deleted[0]+`"}`); !errs.Has(err, errs.NotFound) {
		t.Errorf("the rolled-back rule is still live or in the trash: %v", err)
	}
	if strings.Contains(h.JSON(h.Must("trash.list", `{}`)), f.deleted[0]) {
		t.Errorf("the rolled-back rule is in the trash")
	}
	if strings.Contains(h.JSON(h.Must("rule.show", `{"id":"`+old+`"}`)), f.deleted[0]) {
		t.Errorf("old rule marked although the mark failed")
	}
}
