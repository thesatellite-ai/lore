// infra_core_seed.go — the old binary's writes of the repo's own project
// row, routed through the 0.1.18 core (one writer of
// .lore/data/projects/, phase 5 group 9).
//
// `lore init` still creates lore.db and its project row (projresolve and
// the old readers need it at once), then calls coreSeedProject so the
// row FILE is written by the core's engine (`_v: 2`, audited, stamped)
// under the same id; the first lsync pass then imports that file into
// lore.db instead of exporting a `_v: 1` copy. Store folders' project rows
// are not written here: the old store code stays their only writer until
// the core attaches org stores.
package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// Field names of a projects row the seed writes (schema coreDefs).
const (
	seedFieldName   = "name"
	seedFieldOrigin = "origin_url"
)

// coreSeedProject writes the project row file of the repository at root
// through the core, with the lore.db row's id (projectID), name and
// origin URL ("" = none). A row the core already holds under that id is
// left as is (a re-run, or a teammate's file).
func coreSeedProject(ctx context.Context, root, projectID, name, originURL string) error {
	id, err := ids.ParseAny(projectID)
	if err != nil {
		return errs.Wrap(errs.InvalidID, "init: project id "+projectID, err)
	}
	home, err := loreHome()
	if err != nil {
		home = filepath.Join(os.TempDir(), "lore-home-unresolved")
	}
	// FLAG6: the seed App must be built from the binary's one options builder
	// (live wrapper included), so it sees the actor row the init's first core
	// write holds on the live session; a hand-built App without WrapBackend
	// re-created that actor (a second actors.write) and the held row then
	// failed with E_STALE when flushed.
	app, err := wire.New(coreOptions(root, home))
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()
	if _, _, gerr := app.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableProjects, ID: id}); gerr == nil {
		return nil
	} else if !errs.Has(gerr, errs.NotFound) {
		return gerr
	}
	fields := map[string]value.Value{}
	for k, v := range map[string]string{seedFieldName: name, seedFieldOrigin: originURL} {
		if v == "" {
			continue
		}
		t, terr := value.TextOf(v)
		if terr != nil {
			return errs.Wrap(errs.InvalidInput, "init: project "+k, terr)
		}
		fields[k] = t
	}
	_, err = app.CreateWithID(ctx, store.SourceRepo, schema.TableProjects, id, fields)
	return err
}
