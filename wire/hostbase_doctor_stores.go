package wire

// hostbase_doctor_stores.go — the "unpushed store work" row of `lore doctor`
// on cmd/lore (LORE_TEAM_SPEC §9.4; port of the unpushed half of
// saas/cmd/cli/doctor_stores.go doctorStores). Read-only and offline: each
// store of ~/.lore/config.yaml, once per normalised url, through the same
// clone inspection `lore store show` uses (orgcfg.Clones.Inspect: local
// refs the tracking refs do not hold). A store not cloned here, or a config
// that does not load, is no finding (`lore config check` reports the
// config; `lore store doctor` the clones).

import (
	"context"
	"strings"
)

// doctorUnpushedStores warns once per store clone holding work its remote
// has not confirmed; the url is redacted when output is quiet (no TTY or
// CI, §6.5).
func (a *App) doctorUnpushedStores(ctx context.Context, warn func(string)) {
	c := &standaloneClones{home: a.opts.Home}
	list, err := c.configuredStores()
	if err != nil {
		return
	}
	cl, quiet := c.clonesFor(), quietOutput()
	for _, cs := range list {
		info := cl.Inspect(ctx, cs.norm)
		if !info.Cloned || len(info.Unpushed) == 0 {
			continue
		}
		name := cs.norm
		if quiet {
			name = redactedText
		}
		warn(name + ": unpushed store work (" + strings.Join(info.Unpushed, ", ") + "); `lore sync` pushes it")
	}
}
