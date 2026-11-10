package wire

import (
	"context"
	"strings"

	opssettings "github.com/thesatellite-ai/lore/ops/settings"
)

// MetaStatusLines are the metadata lines of `lore sync status` (METADATA
// 3.13 "counted", FA-META-11): one line per row whose metadata is
// quarantined, and one per registry past meta.max_keys_per_registry (rows
// that arrived by sync are never refused, so status and `lore meta lint`
// report them). Display only: a cache or store that cannot be read gives
// no lines (lint names the error).
func (a *App) MetaStatusLines(ctx context.Context) (quarantined, overCap []string) {
	if qs, err := a.MetaQuarantined(ctx); err == nil {
		for _, q := range qs {
			quarantined = append(quarantined, q.Table+" "+q.ID+": "+strings.Join(q.Problems, "; ")+" (fix with lore meta set "+q.ID+" …, or lore meta unset)")
		}
	}
	if over, err := opssettings.RegistriesOverCap(ctx, a); err == nil {
		for _, r := range over {
			overCap = append(overCap, r.Line())
		}
	}
	return quarantined, overCap
}
