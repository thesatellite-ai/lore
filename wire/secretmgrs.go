package wire

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/store"
)

// SecretManagers implements reveal.ManagerSource (settings.SecretManagerSource):
// the built-in managers with the effective secrets.manager_timeout of the
// repo's view (SECRETS_AND_REFERENCES 6: default 5 s, max 30 s; secretmgr
// caps it again, so a bad value can never mean "wait forever").
func (a *App) SecretManagers(ctx context.Context) (secretmgr.Set, error) {
	set, err := a.Policy(ctx, store.SourceRepo)
	if err != nil {
		return secretmgr.Set{}, err
	}
	var opt secretmgr.Options
	if n, isSet := set.Limit(policy.KeySecretsManagerTimeout); isSet && n > 0 {
		opt.Timeout = time.Duration(n) * time.Second
	}
	return secretmgr.Builtin(opt), nil
}

// redactTask is the engine RedactHook (D21, SECRETS 7): a write that
// on_detect: redact stored with ${secret:unstored} gets its "store, then
// rotate" task in the same command, beside the row (idempotent by row).
func (a *App) redactTask(ctx context.Context, w engine.RedactedWrite) error {
	_, _, err := settings.AddUnstoredTask(ctx, a, w.Store, w.ID.String(), w.Table.String(), w.Fields)
	return err
}
