package wire

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// prepareSource attaches the source an input names through op.SourceNamer
// (`task done|show --from <org>[/<folder>]`, v0.1.17 fixed rule 5) when no
// open store answers to it yet: another org entry's store (a staging
// entry reads its own store branch) at the named folder, or at this repo's
// link folder for that entry. It is attached for this process only, under
// the --from value itself, so the operation finds it by name; nothing is
// written to stores.yaml (an org store's url never goes into the repo,
// CFG7). A name already attached, recorded, brought by the org or covered
// by an existing attachment is left alone, and an unknown entry is left to
// the operation's own refusal ("no org entry named …").
func (a *App) prepareSource(ctx context.Context, e op.Entry, d op.Decoder) error {
	if a.opts.OrgEntryStore == nil {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	from, ok := in.NamedSource()
	if !ok {
		return nil
	}
	name := store.Source(from)
	base, folder, _ := strings.Cut(from, targetSep)
	a.mu.Lock()
	for _, x := range a.extra {
		if x.Name == name {
			a.mu.Unlock()
			return nil
		}
	}
	a.mu.Unlock()
	if recorded, rerr := a.recordedTarget(ctx, name); rerr != nil || recorded {
		return rerr
	}
	for _, s := range a.orgStoresHere(ctx) {
		if s.Name == name || (folder == "" && string(s.Name) == base) {
			return nil // the org in use brings it under that name
		}
	}
	src, found, err := a.opts.OrgEntryStore(ctx, base, folder)
	if err != nil || !found {
		return err
	}
	if _, covered, cerr := a.coveringAttachment(ctx, src, src.Folder); cerr != nil || covered {
		return cerr // the same folder is open already (its label answers --from)
	}
	src.Name = name
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extra = append(a.extra, attach.Store{Name: src.Name, URL: src.URL, Folder: src.Folder, Branch: src.Branch, ReadOnly: src.ReadOnly})
	if a.st != nil { // already open (a long-lived `lore api`): reopen with the source
		cerr := a.st.close()
		a.st, a.opened, a.err = nil, false, nil
		return cerr
	}
	if a.opened {
		a.opened, a.err = false, nil
	}
	return nil
}
