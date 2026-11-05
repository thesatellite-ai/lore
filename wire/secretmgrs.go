package wire

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
	"github.com/thesatellite-ai/lore/logic/policy"
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
