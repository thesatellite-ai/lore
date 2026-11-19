package task

// keyindex.go — the --key lookup of a task import (devco T-3).
//
// add's --key rule ("already added with --key") listed every task of
// every store for each row; a task import of 650 rows into a repo with
// 5,000 open tasks listed them 650 times (over 6 minutes). An import now
// lists them once into a keyIndex carried on its context; a key the same
// file repeats is answered by the import's own stagedKeys.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// keyIndex maps project, mission and key to the task that holds them.
type keyIndex struct{ byKey map[string]work.Sourced }

// keyIndexCtx is the context key of an import's keyIndex.
type keyIndexCtx struct{}

// taskKey is the index key of a task's project, mission and --key.
func taskKey(project, mission, key string) string {
	return project + "\x00" + strings.TrimSpace(mission) + "\x00" + key
}

// withKeyIndex lists every task once and returns ctx carrying their keys,
// so byKey answers from memory for the rest of the operation.
func (o ops) withKeyIndex(ctx context.Context) (context.Context, error) {
	all, err := work.ListAll(ctx, o.env, table, "")
	if err != nil {
		return nil, err
	}
	idx := &keyIndex{byKey: make(map[string]work.Sourced, len(all))}
	for _, s := range all {
		k := work.Field(s.Row, work.FieldKey)
		if k == "" {
			continue
		}
		ik := taskKey(work.Field(s.Row, work.FieldProject), work.Field(s.Row, work.FieldMission), k)
		if _, seen := idx.byKey[ik]; !seen {
			idx.byKey[ik] = s // the first, as byKey's scan answered
		}
	}
	return context.WithValue(ctx, keyIndexCtx{}, idx), nil
}

// keyIndexOf is ctx's keyIndex (nil when the operation built none).
func keyIndexOf(ctx context.Context) *keyIndex {
	idx, _ := ctx.Value(keyIndexCtx{}).(*keyIndex)
	return idx
}
