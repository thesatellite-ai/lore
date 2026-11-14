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

	"github.com/thesatellite-ai/lore/logic/repourl"
)

// doctorUnpushedStores warns once per store clone holding work its remote
// has not confirmed, named by its url (only a credential in it masked).
// Only a push-now store's waiting work is news (a push failed): a held
// store (manual, the default, or batch; C5) keeps work waiting for
// `lore sync` by design, and doctorPushModes notes it and warns only once
// nothing has pushed it for over a day.
func (a *App) doctorUnpushedStores(ctx context.Context, warn func(string)) {
	c := &standaloneClones{home: a.opts.Home, now: a.opts.Wall}
	list, err := c.configuredStores()
	if err != nil {
		return
	}
	cl := c.clonesFor()
	for _, cs := range list {
		info := cl.Inspect(ctx, cs.norm)
		if !info.Cloned || len(info.Unpushed) == 0 || storeHeld(info.Dir) {
			continue
		}
		warn(repourl.Mask(cs.norm) + ": unpushed store work (" + strings.Join(info.Unpushed, ", ") + "); `lore sync` pushes it")
	}
}
