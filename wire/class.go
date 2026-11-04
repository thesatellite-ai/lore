package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/store"
)

// StoreClass places an attached store in its F15 class (logic/direction):
// the repo store is client-class (fail closed: wire cannot yet tell an
// org's own repo from a customer's), `me` and `me/<folder>` personal, any
// other store internal unless the engine flags it a client repo. An empty
// name is the default write target.
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
