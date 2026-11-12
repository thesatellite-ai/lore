package wire

// store.set_url (PROV 11.9, SOURCE_OF_TRUTH row 7): the composition-root
// half. The store's id (lore-store.yaml `id:`) is its identity; set-url
// moves every place on this machine that names that id, in one step, so
// roster writes and row writes reach one address. Flow:
//
//	SetStoreURL → setURLSource: a sto_ id, me, a stores.yaml name, a store
//	            the org brings, or an org entry name → its current url
//	            → clean the new url → open it: a 0.1.18 marker with an id,
//	              else E_NOT_A_STORE
//	            → read the old address's marker when it still opens: a
//	              different sto_ id is another store (E_INVALID_INPUT)
//	            → the old addresses: the source's url plus every place
//	              whose url reaches the same id here (addressEntries)
//	            → rewrite each place naming an old address:
//	                .lore/stores.yaml entries (folder entries too)
//	                orgs[].stores[].url, personal.store,
//	                setups[].personal_store (one validated config write)
//	                the address record (state/store-addresses.json)
//	            → for an org whose store moved: its lore-org.yaml in the
//	              store (at the new url), then the entry's source stamp;
//	              a store that cannot take the commit is a note, the move
//	              on this machine stands

import (
	"context"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

// Config keys set-url rewrites.
const (
	configKeyPersonalStore = "personal_store"
	orgStoreKeyURL         = "url"
)

// SetStoreURL implements opsstores.URLSetter.
func (a *App) SetStoreURL(ctx context.Context, in opsstores.SetURLIn) (opsstores.SetURLOut, error) {
	const verb = "store set-url"
	src, err := a.setURLSource(ctx, in.Name)
	if err != nil {
		return opsstores.SetURLOut{}, err
	}
	raw, err := cleanFileURL(in.URL)
	if err != nil {
		return opsstores.SetURLOut{}, err
	}
	out := opsstores.SetURLOut{Name: in.Name, URL: raw, Previous: src.URL}
	newID, err := a.storeIDAt(ctx, attach.Store{Name: src.Name, URL: raw, Branch: src.Branch})
	if err != nil {
		return opsstores.SetURLOut{}, err
	}
	if newID.IsZero() {
		return opsstores.SetURLOut{}, errs.New(errs.NotAStore, verb+": "+raw+" holds no lore store with an id ("+rowfs.MarkerFile+")").
			WithHint("check the url; a v0.1.17 store needs lore fix first")
	}
	out.ID = newID.String()
	if !storeaddr.Same(raw, src.URL) {
		// PROV 11.9 "same rules as 10.4": the store keeps its permanent id,
		// so an old address that still opens must name the same store (a
		// typo or a different repo is refused, never silently re-pointed).
		oldID, oerr := a.storeIDAt(ctx, src)
		switch {
		case oerr != nil:
			out.Note = "the old address could not be read (" + string(errs.CodeOf(oerr)) + "), so its store id was not compared"
		case !oldID.IsZero() && oldID != newID:
			return opsstores.SetURLOut{}, errs.New(errs.InvalidInput, verb+": "+raw+" is another store ("+newID.String()+"; "+in.Name+" is "+oldID.String()+")").
				WithHint("attach it under its own name: lore store add <name> " + raw)
		}
	}
	top, terr := a.repoTop(ctx)
	if terr != nil {
		top = "" // outside a repo: the machine's places only
	}
	old := oldAddresses(addressEntries(a.opts.Home, top), out.ID, src.URL, raw)
	if storeaddr.Same(src.URL, raw) && len(old) > 0 {
		out.Previous = old[0] // the name already named raw; the places that move had this address
	}
	if top != "" {
		names, serr := moveStoresFileURLs(top, string(src.Name), old, raw)
		if serr != nil {
			return opsstores.SetURLOut{}, serr
		}
		for _, n := range names {
			if strings.Contains(n, targetSep) {
				out.Folders = append(out.Folders, n)
			} else {
				out.Places = append(out.Places, attach.FileName+" "+n)
			}
		}
	}
	moved, err := moveConfigURLs(a.opts.Home, old, raw)
	if err != nil {
		return out, errs.Prefix(verb+": "+strings.Join(out.Places, ", ")+" moved, but ~/.lore/"+ConfigFile+" was not", err)
	}
	out.Places = append(out.Places, moved.places...)
	out.Changed = len(out.Places)+len(out.Folders) > 0
	if rerr := recordStoreAddress(a.opts.Home, out.ID, raw, a.opts.Wall()); rerr != nil {
		return out, rerr
	}
	for _, org := range moved.orgs {
		out.OrgFiles = append(out.OrgFiles, a.moveOrgFile(ctx, org, old, raw))
	}
	return out, nil
}

// setURLSource is the store set-url names: a sto_ id (any place naming
// it; the doctor's fix uses the id), `me`, a stores.yaml name or a store
// the org brings (baseStore), or an org entry's name (its org folder's
// store). Not found is E_NOT_FOUND naming what was tried.
func (a *App) setURLSource(ctx context.Context, name string) (attach.Store, error) {
	if _, err := ids.Parse[ids.Store](name); err == nil {
		top, terr := a.repoTop(ctx)
		if terr != nil {
			top = ""
		}
		entries := addressEntries(a.opts.Home, top)
		if sp, ok := storeaddr.For(storeaddr.Splits(entries), name); ok {
			return attach.Store{Name: store.Source(name), URL: sp.Primary}, nil
		}
		for _, e := range entries {
			if e.StoreID == name {
				return attach.Store{Name: store.Source(name), URL: e.URL}, nil
			}
		}
		return attach.Store{}, errs.New(errs.NotFound, "store set-url: no place on this machine names store "+name).
			WithHint("run `lore store list`; a store with no local copy is named by its stores.yaml or org entry name")
	}
	if _, err := a.repoTop(ctx); err == nil || name == attach.ReservedMe {
		src, found, _, berr := a.baseStore(ctx, name)
		if berr != nil {
			return attach.Store{}, berr
		}
		if found {
			return src, nil
		}
	}
	if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil {
		if o := cfg.FindOrg(name); o != nil && len(o.Stores) > 0 {
			sc := o.OrgFolderStore()
			if sc == nil {
				sc = &o.Stores[0]
			}
			return attach.Store{Name: store.Source(name), URL: sc.URL, Branch: sc.Branch}, nil
		}
	}
	return attach.Store{}, errs.New(errs.NotFound, "store set-url: no store named "+name+" is attached, and no org entry has that name").
		WithHint("run `lore store list` (or pass the store's sto_ id)")
}

// oldAddresses is every address to move: the source's url and each
// place's url that reaches store id on this machine, except the new url
// as written. Another spelling of the same repo (https for an ssh url)
// is one address to the split check, but set-url records the spelling
// the person gave, so it moves too.
func oldAddresses(entries []storeaddr.Entry, id, srcURL, raw string) []string {
	var out []string
	add := func(u string) {
		if u == "" || u == raw || slices.Contains(out, u) {
			return
		}
		out = append(out, u)
	}
	add(srcURL)
	for _, e := range entries {
		if e.StoreID == id {
			add(e.URL)
		}
	}
	return out
}

// namesOld reports whether u is one of the old addresses.
func namesOld(old []string, u string) bool {
	return slices.ContainsFunc(old, func(o string) bool { return storeaddr.Same(o, u) })
}

// moveStoresFileURLs points every stores.yaml entry (folder entries too)
// at an old address to raw; it returns the names it changed and writes
// nothing when none matched. A folder entry "<name>/<folder>" of the named
// store follows it whatever url it carries: it is the same store by name
// (targets.go records it from the store's entry), so one left stale by an
// older lore, or whose old address no longer opens to show its id, is
// repaired here too (B2-sync).
func moveStoresFileURLs(top, name string, old []string, raw string) ([]string, error) {
	c, err := readStoresFile(top)
	if err != nil {
		return nil, err
	}
	moves := func(entryName, u string) bool {
		if u == raw {
			return false
		}
		return namesOld(old, u) || (name != "" && strings.HasPrefix(entryName, name+targetSep))
	}
	if !slices.ContainsFunc(c.Stores, func(s attach.Store) bool { return moves(string(s.Name), s.URL) }) {
		return nil, nil
	}
	var names []string
	err = editStoresFile(top, func(list *yaml.Node) error {
		for _, n := range list.Content {
			u, nm := mapGet(n, keyURL), mapGet(n, keyName)
			if u == nil || nm == nil || !moves(nm.Value, u.Value) {
				continue
			}
			mapSet(n, keyURL, scalar(raw))
			names = append(names, nm.Value)
		}
		return nil
	})
	return names, err
}

// configMove is what moveConfigURLs changed.
type configMove struct {
	places []string // "orgs[acme].stores", "personal.store", "setups[work].personal_store"
	orgs   []string // org entries whose stores moved
}

// moveConfigURLs points every config.yaml place at an old address to raw
// in one validated write (none when nothing matched).
func moveConfigURLs(home string, old []string, raw string) (configMove, error) {
	var mv configMove
	edit := func(root *yaml.Node) error {
		mv = configMove{}
		if orgs := mapGet(root, configKeyOrgs); orgs != nil && orgs.Kind == yaml.SequenceNode {
			for _, item := range orgs.Content {
				name := mapGet(item, configKeyName)
				stores := mapGet(item, orgKeyStores)
				if name == nil || stores == nil || stores.Kind != yaml.SequenceNode {
					continue
				}
				hit := false
				for _, sc := range stores.Content {
					if u := mapGet(sc, orgStoreKeyURL); u != nil && u.Value != raw && namesOld(old, u.Value) {
						mapSet(sc, orgStoreKeyURL, scalar(raw))
						hit = true
					}
				}
				if hit {
					mv.places = append(mv.places, "orgs["+name.Value+"].stores")
					mv.orgs = append(mv.orgs, name.Value)
				}
			}
		}
		if p := mapGet(root, configKeyPersonal); p != nil {
			if s := mapGet(p, configKeyStore); s != nil && s.Value != raw && namesOld(old, s.Value) {
				mapSet(p, configKeyStore, scalar(raw))
				mv.places = append(mv.places, "personal.store")
			}
		}
		if setups := mapGet(root, configKeySetups); setups != nil && setups.Kind == yaml.SequenceNode {
			for _, item := range setups.Content {
				ps, name := mapGet(item, configKeyPersonalStore), mapGet(item, configKeyName)
				if ps == nil || name == nil || ps.Value == raw || !namesOld(old, ps.Value) {
					continue
				}
				mapSet(item, configKeyPersonalStore, scalar(raw))
				mv.places = append(mv.places, "setups["+name.Value+"].personal_store")
			}
		}
		return nil
	}
	// Check first: no write (and no lock wait) when nothing names an old
	// address.
	if _, err := orgConfigWith(home, edit); err != nil {
		return configMove{}, err
	}
	if len(mv.places) == 0 {
		return mv, nil
	}
	return mv, writeOrgConfig(home, edit)
}

// moveOrgFile records the move in org's lore-org.yaml (at the store's new
// address) and stamps the entry with the file's new version. It returns
// the line store set-url prints; a failure is a line too (the move on this
// machine stands; teammates still import the old address until it lands).
func (a *App) moveOrgFile(ctx context.Context, org string, old []string, raw string) string {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return "lore-org.yaml of " + org + " not updated: ~/.lore/" + ConfigFile + " could not be read"
	}
	o := cfg.FindOrg(org)
	if o == nil {
		return "lore-org.yaml of " + org + " not updated: the entry is gone"
	}
	sc := o.OrgFolderStore()
	if sc == nil || !storeaddr.Same(sc.URL, raw) {
		return "" // the moved store is not the org's org-folder store: lore-org.yaml lives elsewhere
	}
	g := &orgcfg.GitLinks{Home: a.opts.Home, Config: cfg, Now: a.opts.Wall, Route: storeRouter(a.opts.Home, a.projectDir())}
	res, err := g.MoveOrgFileStore(ctx, orgcfg.OrgFileMove{URL: raw, Branch: sc.Branch, Old: old, New: raw, Org: org, Signature: storeSignature(o)})
	if err != nil {
		return "lore-org.yaml of " + org + " still names the old address (" + orgFileError(err, raw).Error() + "); run `lore store set-url " + org + " " + raw + "` again when the store takes the commit, so teammates importing the org get the new address"
	}
	if werr := writeOrgSource(a.opts.Home, configLockWait, org, res.Suggestion.Shared(), res.Suggestion.Source()); werr != nil {
		return "lore-org.yaml of " + org + " updated, but the entry's source stamp was not written: " + werr.Error() + "; `lore org refresh " + org + "` writes it"
	}
	where := "pushed"
	switch {
	case !res.Changed:
		return "lore-org.yaml of " + org + " already names " + raw + " (version " + short(res.Suggestion.Version) + ")"
	case res.Offline:
		where = "committed in the local clone; offline, pushed by the next command"
	case !res.Pushed:
		where = "committed in the local clone; `lore sync` pushes it"
	}
	return "lore-org.yaml of " + org + " now names " + raw + " (" + where + "); orgs[" + org + "] stamped with version " + short(res.Suggestion.Version)
}

// storeIDAt opens the store src names and returns its marker's sto_ id
// (zero for a folder without a 0.1.18 marker).
func (a *App) storeIDAt(ctx context.Context, src attach.Store) (ids.ID[ids.Store], error) {
	canon, err := canonical(attach.Store{Name: src.Name, URL: src.URL, Branch: src.Branch})
	if err != nil {
		return ids.ID[ids.Store]{}, err
	}
	b, err := a.openStore(ctx, canon, src.URL)
	if err != nil {
		return ids.ID[ids.Store]{}, err
	}
	m, _ := b.(markerOf)
	_ = b.Close() // opened only to read the marker
	if m == nil {
		return ids.ID[ids.Store]{}, nil
	}
	return m.Marker().ID, nil
}

// compile-time check: App offers store.set_url.
var _ opsstores.URLSetter = (*App)(nil)
