// core_from.go — wire.Options.OrgEntryStore for the old binary: the store
// of any configured org entry, not only the one in use, so `lore task
// done|show --from <org>[/<folder>]` reaches it (v0.1.17 fixed rule 5,
// openCoordTargetFrom: "<org>/<folder>" is a project of that org entry's
// stores; a staging entry reads its staging store branch).
package main

import (
	"context"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"

	"saas/pkg/aicoder/lorecfg"
	oldstores "saas/pkg/aicoder/stores"
)

// coreOrgEntryStore finds org entry name's store at folder. With no
// folder it is this repo's link folder for that entry (its default link
// first), else the repo's default link folder (v0.1.17 defaultLink); with
// neither it is v0.1.17's "name the folder" refusal. An unknown entry is
// not found (the operation answers "no org entry named …").
func coreOrgEntryStore(ctx context.Context, name, folder string) (attach.Store, bool, error) {
	s, err := coreSituationOnce(ctx)
	if err != nil {
		return attach.Store{}, false, err
	}
	if s.Config == nil {
		return attach.Store{}, false, nil
	}
	org := s.Config.FindOrg(name)
	if org == nil || len(org.Stores) == 0 {
		return attach.Store{}, false, nil
	}
	sc := &org.Stores[0]
	if folder == "" {
		var link, fallback *string
		for i := range s.Result.Links {
			l := &s.Result.Links[i]
			switch {
			case l.Entry == name && (link == nil || l.Default):
				link = &l.Folder
				if st := oldstores.StoreFor(org, l.StoreURL); st != nil {
					sc = st
				}
			case l.Default && fallback == nil:
				fallback = &l.Folder
			}
		}
		switch {
		case link != nil:
			folder = *link
		case fallback != nil:
			folder = *fallback
		default:
			return attach.Store{}, false, errs.New(errs.InvalidInput, "--from "+name+": name the folder (<org>/<folder>); this repo has no default link")
		}
	}
	branch := ""
	if b := sc.EffectiveBranch(); b != lorecfg.DefaultStoreBranch {
		branch = b
	}
	return attach.Store{Name: store.Source(name), URL: sc.URL, Folder: folder, Branch: branch}, true, nil
}
