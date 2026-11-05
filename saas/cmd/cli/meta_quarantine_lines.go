package main

import (
	"context"
	"strings"

	opssettings "github.com/thesatellite-ai/lore/ops/settings"
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

// metaRegistryLines are the registries past meta.max_keys_per_registry
// (FA-META-11: rows that arrived by sync are never refused, so lore sync
// status and lore meta lint report them), one line each. No core app or an
// unreadable store: none (lint names the store error).
func metaRegistryLines(ctx context.Context) []string {
	if coreApp == nil {
		return nil
	}
	over, err := opssettings.RegistriesOverCap(ctx, coreApp)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(over))
	for _, r := range over {
		out = append(out, r.Line())
	}
	return out
}
