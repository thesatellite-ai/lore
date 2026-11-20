package wire

import (
	"context"
	"fmt"
	"time"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/ids"
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

// PlaceProject implements task.PlaceProjecter: the project of the store
// folder src names (its _meta.json id), so a secret auto-task written
// into that folder belongs to it from any place, a store folder's own
// engine included (devco:D-762). The folder's project row is made when it
// has none (as a `--to <store>/<folder>` write makes it), so the task's
// project_id never names a row no store holds. ok is false for the repo
// store and a store root, which keep the repo's project.
func (a *App) PlaceProject(ctx context.Context, src store.Source) (ids.ID[ids.Project], bool, error) {
	st, err := a.state(ctx)
	if err != nil {
		return ids.ID[ids.Project]{}, false, err
	}
	pid, err := folderProjectIDOf(ctx, st, src)
	if err != nil {
		return ids.ID[ids.Project]{}, false, fmt.Errorf("project of %s: %w", src, err)
	}
	if pid.IsZero() {
		return ids.ID[ids.Project]{}, false, nil
	}
	if _, err := a.rosterProject(ctx, st, src, rosterFolderName(src)); err != nil {
		return ids.ID[ids.Project]{}, false, fmt.Errorf("project of %s: %w", src, err)
	}
	return pid, true, nil
}

// redactTask is the engine RedactHook (D21, SECRETS 7): a write that
// on_detect: redact stored with ${secret:unstored} gets its "store, then
// rotate" task in the same command, beside the row (idempotent by row).
func (a *App) redactTask(ctx context.Context, w engine.RedactedWrite) error {
	_, _, err := settings.AddUnstoredTask(ctx, a, w.Store, w.ID.String(), w.Table.String(), w.Fields)
	return err
}
