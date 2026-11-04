package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/store"
)

// StoreClass places an attached store in its F15 class (logic/direction):
// the repo store is client-class only in a client repo (Options.RepoIsClient
// said so, failed, or is unset: fail closed), `me` and `me/<folder>`
// personal, any other store internal unless the engine flags it a client
// repo. An empty name is the default write target.
func (a *App) StoreClass(ctx context.Context, name store.Source) (direction.Class, error) {
	st, err := a.state(ctx)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = st.defaultTarget
	}
	return direction.ClassOf(string(name), st.eng.IsClient(name)), nil
}

// RepoClassNote is why the repo store is client-class without a positive
// answer from Options.RepoIsClient (no hook, or the check failed: fail
// closed); empty when the hook answered. For a doctor-style report.
func (a *App) RepoClassNote(ctx context.Context) (string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return "", err
	}
	return st.repoClassNote, nil
}
