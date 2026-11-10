package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
)

// MetaQuarantined lists every row whose metadata is quarantined in any
// attached cache file (METADATA 3.13 "counted": lore sync status, lore
// doctor and lore meta lint list them). Problems name keys and rules,
// never values.
func (a *App) MetaQuarantined(ctx context.Context) ([]sqlite.MetaQuarantine, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	var out []sqlite.MetaQuarantine
	for _, c := range cacheFiles(st.caches) {
		q, qerr := c.MetaQuarantined(ctx)
		if qerr != nil {
			return nil, qerr
		}
		out = append(out, q...)
	}
	return out, nil
}
