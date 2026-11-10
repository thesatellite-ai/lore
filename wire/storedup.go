package wire

// storedup.go — `lore store init|add` refuse a second stores.yaml entry
// for a place an entry already attaches (the same store, branch and
// folder under another name). Two names for one place read every row
// twice and label it with whichever name the walk meets first (issue #1);
// reads already dedupe such files (engine.ReadStores, wire/open.go
// location), so a stores.yaml that has duplicates still loads, but no
// command writes a new one.

import (
	"context"
	"net/url"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// entryPlace is the place an entry attaches: the store (storeKey: any
// spelling git accepts, .git and a trailing slash ignored), its effective
// branch (the entry's branch, else the url's ?branch=:
// opsstores.PinnedBranch; "" is the store's default branch, which is not
// compared to an explicit name since its value lives on the remote) and
// its folder (the entry's, else the url's ?folder=; the root as "").
type entryPlace struct{ store, branch, folder string }

// entryPlaceOf is s's place; false for a url that does not read (the open or
// the scheme check reports it).
func entryPlaceOf(s attach.Store) (entryPlace, bool) {
	key, ok := storeKey(s.URL)
	if !ok {
		return entryPlace{}, false
	}
	branch, err := opsstores.PinnedBranch(s.URL, s.Branch)
	if err != nil {
		return entryPlace{}, false
	}
	f := s.Folder
	if f == "" {
		if u, perr := url.Parse(s.URL); perr == nil {
			f = u.Query().Get(rowfs.FolderParam)
		}
	}
	return entryPlace{store: key, branch: branch, folder: rootFolder(f)}, true
}

// checkNewPlace refuses place when a stores.yaml entry already attaches
// it, naming that entry and the two ways on.
func (a *App) checkNewPlace(ctx context.Context, place attach.Store) error {
	want, ok := entryPlaceOf(place)
	if !ok {
		return nil
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		return err
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return err
	}
	for _, s := range cfg.Stores {
		if got, ok := entryPlaceOf(s); !ok || got != want {
			continue
		}
		name := string(s.Name)
		where := s.URL
		if want.branch != "" {
			where += "@" + want.branch
		}
		if want.folder != "" {
			where += " (folder " + want.folder + ")"
		}
		return errs.New(errs.InvalidInput, "store "+name+" already attaches "+where).
			WithHint("use --to " + name + ", or remove it first: lore store remove " + name)
	}
	return nil
}
