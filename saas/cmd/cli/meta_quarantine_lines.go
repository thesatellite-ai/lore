package main

import (
	"context"
	"strings"
)

// metaQuarantineLines are the rows whose metadata is quarantined (METADATA
// 3.13 "counted": lore sync status and lore doctor list them), one line per
// row: "<table> <id>: <problem>; <problem> (lore meta set <id> …)". Problems
// name keys and rules, never values. No core app: none.
func metaQuarantineLines(ctx context.Context) []string {
	if coreApp == nil {
		return nil
	}
	qs, err := coreApp.MetaQuarantined(ctx)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(qs))
	for _, q := range qs {
		out = append(out, q.Table+" "+q.ID+": "+strings.Join(q.Problems, "; ")+" (fix with lore meta set "+q.ID+" …, or lore meta unset)")
	}
	return out
}
