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
//	            → a clone at an old address holding unpushed commits:
//	              carried to the new address, or refused before any
//	              write (storeseturl_clone.go)
//	            → for an org whose store moved: its lore-org.yaml in the
//	              store (at the new url), then the entry's source stamp;
//	              a store that cannot take the commit is a note, the move
//	              on this machine stands

import (
	"context"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
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
	if err = a.confirmOrgMove(ctx, in.Name, src.URL, raw); err != nil {
		return opsstores.SetURLOut{}, err // asked before anything is cloned or written
	}
	// D-514: every open of raw below (the marker check, the carried clone,
	// the org file commit) runs before or while orgs[].stores moves to raw;
	// the store stays its org's, so its clone gets the org identity.
	ctx = withStoreMove(ctx, src.URL, raw)
	// N-13: an empty clone of raw from an earlier check is opened again
	// without a fetch, so it would still say "empty" after the person
	// pushed; it holds nothing, so it goes before the check.
	cloneDir, hadClone := a.setURLClone(raw, src.Branch)
	if hadClone && dropEmptyClone(ctx, cloneDir) {
		hadClone = false
	}
	// D-541: every line names the store the person asked about (in.Name);
	// src.Name of an org's store is the org's alias, so a hint built on it
	// told the person to set-url the team store to this store's url
	asked := attach.Store{Name: store.Source(in.Name), URL: src.URL, Branch: src.Branch}
	newID, seeded, err := a.newStoreID(ctx, asked, raw, cloneDir)
	if err != nil {
		if !hadClone {
			dropNewClone(ctx, cloneDir) // a refused set-url leaves no clone that blocks the retry
		}
		return opsstores.SetURLOut{}, err
	}
	if newID.IsZero() {
		// D-541: the store was never written (no marker at either address,
		// no clone holding one): only its address moves
		return a.moveAddressOnly(ctx, out, src, raw, cloneDir, seeded)
	}
	out.ID = newID.String()
	out.Clone = seeded
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
				WithHint("attach it under its own name: lore store add --name <name> --url " + raw)
		}
	}
	// B3-15: a clone at an old address holding unpushed work is carried
	// to the new one (storeseturl_clone.go), or the move is refused here,
	// before anything is written.
	carry, carrying, err := a.planCloneCarry(ctx, out.ID, raw, src.Branch)
	if err != nil {
		return opsstores.SetURLOut{}, err
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
		return out, errs.Prefix(verb+": "+strings.Join(out.Places, ", ")+" moved, but "+lorehome.Shown()+"/"+ConfigFile+" was not", err)
	}
	out.Places = append(out.Places, moved.places...)
	out.Changed = len(out.Places)+len(out.Folders) > 0
	if rerr := recordStoreAddress(a.opts.Home, out.ID, raw, a.opts.Wall()); rerr != nil {
		return out, rerr
	}
	tail, at := "", ""
	if carrying {
		var cerr error
		if tail, at, cerr = a.applyCloneCarry(ctx, carry, raw); cerr != nil {
			return out, errs.Prefix(verb+": the places moved, but the clone "+carry.from+" holding unpushed work was not carried (run this command again)", cerr)
		}
		out.Changed = true
	}
	left := leftClonesLine(leftClones(ctx, a.opts.Home, out.ID, raw), old)
	out.OrgFiles = a.moveOrgFiles(ctx, moved.orgs, in.Name, old, raw)
	if carrying {
		// the org file commit above lands in the carried clone: its line
		// counts what waits after it (D-562)
		out.Clone = joinLines(out.Clone, carryLine(carry, at, raw, tail, pendingIn(ctx, at)))
	}
	out.Clone = joinLines(out.Clone, left)
	return out, nil
}

// confirmOrgMove asks before set-url changes a store's address for a whole
// org (D-562, owner use pass): run by an admin of an org listing the store,
// it commits the new address to that org's lore-org.yaml, which every
// teammate's lore follows, so like `lore org config set` it stops with
// E_NEEDS_CONFIRM naming what it would do (the CLI asks at a terminal) and
// writes only under --yes. A path on this machine is called out: no
// teammate can fetch from it. A member's set-url (only this machine moves)
// and a url that is already the store's ask nothing.
func (a *App) confirmOrgMove(ctx context.Context, name, from, raw string) error {
	if engine.Confirmed(ctx) || op.Confirmed(ctx) || storeaddr.Same(from, raw) { // --yes reaches a run op through the engine, a direct one through op
		return nil
	}
	orgs := adminOrgsNaming(a.opts.Home, from)
	if len(orgs) == 0 {
		return nil
	}
	who := strings.Join(orgs, ", ")
	plan := "store set-url would move store " + name + " to " + raw + " for everyone in " + who + ": it commits the new address to lore-org.yaml of " + who + ", and every teammate's lore follows it after their next sync"
	if repourl.IsLocalRemote(raw) {
		plan += "; " + raw + " is a path on this machine, which no teammate can reach, so their lore would lose the store until it moves back"
	}
	q := "Move store " + name + " to " + raw + " for everyone in " + who + "?"
	return errs.New(errs.NeedsConfirm, plan).WithQuestion(q).
		WithHint("nothing written; to move it: lore store set-url --name " + name + " --url " + raw + " --yes")
}

// adminOrgsNaming is every org whose entry lists a store at url and that
// this machine administers (an admin setup of it): the orgs whose
// lore-org.yaml a set-url of that store rewrites (moveOrgFiles).
func adminOrgsNaming(home, url string) []string {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return nil
	}
	var out []string
	for _, o := range cfg.Orgs {
		if !isOrgAdmin(home, o.Name) {
			continue
		}
		for _, sc := range o.Stores {
			if storeaddr.Same(sc.URL, url) {
				out = append(out, o.Name)
				break
			}
		}
	}
	return out
}

// moveOrgFiles commits the move to each org's lore-org.yaml this machine
// administers; for another org it says who must, naming the store asked
// about (name), never the org's own store (D-541).
func (a *App) moveOrgFiles(ctx context.Context, orgs []string, name string, old []string, raw string) []string {
	var lines []string
	for _, org := range orgs {
		if !isOrgAdmin(a.opts.Home, org) {
			// H-12: the org's lore-org.yaml is every member's address of
			// the store; only an admin of the org changes it (as `lore store
			// set` does). This machine follows the move all the same.
			lines = append(lines, "lore-org.yaml of "+org+" left as it is: only an admin of "+org+" changes the store's address for the whole org (this machine now uses "+raw+"); ask an admin to run lore store set-url --name "+name+" --url "+raw)
			continue
		}
		lines = append(lines, a.moveOrgFile(ctx, org, name, old, raw))
	}
	return lines
}

// moveAddressOnly is set-url of a store that holds nothing yet (D-541: an
// admins-only store `org store add` added and nobody wrote to, moved to
// another empty repo): no id to compare or record and no clone to carry,
// so the places naming the old address move and the empty clone of the
// new one goes (opened again it would not fetch the first write).
func (a *App) moveAddressOnly(ctx context.Context, out opsstores.SetURLOut, src attach.Store, raw, cloneDir, seeded string) (opsstores.SetURLOut, error) {
	const verb = "store set-url"
	old := []string{src.URL}
	if storeaddr.Same(src.URL, raw) {
		old = nil
	}
	top, terr := a.repoTop(ctx)
	if terr != nil {
		top = ""
	}
	if top != "" && len(old) > 0 {
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
		return out, errs.Prefix(verb+": "+strings.Join(out.Places, ", ")+" moved, but "+lorehome.Shown()+"/"+ConfigFile+" was not", err)
	}
	out.Places = append(out.Places, moved.places...)
	out.Changed = len(out.Places)+len(out.Folders) > 0
	dropEmptyClone(ctx, cloneDir)
	out.Clone = joinLines(seeded, "the store holds nothing yet at either address, so only its address moved; its first write goes to "+raw)
	out.OrgFiles = a.moveOrgFiles(ctx, moved.orgs, out.Name, old, raw)
	return out, nil
}

// newStoreID is the store id at raw: its marker's, or, for an empty repo
// and a folder store being moved, the folder's after set-url copied it
// there (seeded is then the line saying so). A url holding no store is
// E_NOT_A_STORE with the step that fits.
func (a *App) newStoreID(ctx context.Context, src attach.Store, raw, cloneDir string) (ids.ID[ids.Store], string, error) {
	newID, err := a.storeIDAt(ctx, attach.Store{Name: src.Name, URL: raw, Branch: src.Branch})
	if err != nil || !newID.IsZero() {
		return newID, "", err
	}
	id, seeded, ok, err := a.seedFromFolder(ctx, src, raw, cloneDir)
	if err != nil {
		return ids.ID[ids.Store]{}, "", err
	}
	if ok && !id.IsZero() {
		return id, seeded, nil
	}
	if emptyClone(ctx, cloneDir) {
		// D-541: an empty new address of a git store: the store's id is
		// its clone's at the old address (carried there below, and the
		// next push sends its history), and a store nobody wrote to has
		// none (zero: its address alone moves)
		if oldID, has := oldCloneID(ctx, a.opts.Home, src.URL); has {
			return oldID, "", nil
		}
		// no clone holds it: the old address decides; one still holding a
		// store keeps the refusal (its history must reach raw first)
		oldID, oerr := a.storeIDAt(ctx, src)
		switch {
		case oerr == nil && oldID.IsZero():
			return ids.ID[ids.Store]{}, "", nil
		case oerr != nil:
			return ids.ID[ids.Store]{}, "the old address could not be read (" + string(errs.CodeOf(oerr)) + ") and no clone on this machine holds the store", nil
		}
	}
	return ids.ID[ids.Store]{}, "", notAStoreAt(string(src.Name), raw, emptyClone(ctx, cloneDir))
}

// joinLines joins two set-url lines with "; ", skipping an empty one.
func joinLines(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// setURLSource is the store set-url names: a sto_ id (any place naming
// it; the doctor's fix uses the id), `me`, a stores.yaml name or a store
// the org brings (baseStore), or an org entry's name (its org folder's
// store); outside a repo every name outsideStoreAt takes (an org store's
// own name, <org>/<store>, a setup). Not found is E_NOT_FOUND naming what
// was tried.
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
	if _, err := a.repoTop(ctx); err != nil {
		// H-12: outside a repo no .lore/stores.yaml names anything; D-540:
		// every store an org lists is named as push, show and forget name
		// it (storename_outside.go), an org's second store too
		s, ok, oerr := a.outsideStoreAt(ctx, name)
		if oerr != nil {
			return attach.Store{}, oerr
		}
		if ok {
			return s, nil
		}
		return attach.Store{}, a.outsideNameErr(verbSetURL, name)
	}
	if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil {
		if o := cfg.FindOrg(name); o != nil && len(o.Stores) > 0 {
			return orgEntryStore(name, o), nil
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
func (a *App) moveOrgFile(ctx context.Context, org, name string, old []string, raw string) string {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return "lore-org.yaml of " + org + " not updated: " + lorehome.Shown() + "/" + ConfigFile + " could not be read"
	}
	o := cfg.FindOrg(org)
	if o == nil {
		return "lore-org.yaml of " + org + " not updated: the entry is gone"
	}
	sc := o.OrgFolderStore()
	if sc == nil {
		return "" // no store holds the org's lore-org.yaml
	}
	// D-540: lore-org.yaml lives in the org folder's store (sc.URL, raw
	// when that store moved) and lists every store of the org, so a second
	// store's move is committed there too (it was skipped: teammates and
	// the org's repos kept the old address)
	g := &orgcfg.GitLinks{Held: orgStoreHeld, Home: a.opts.Home, Config: cfg, Now: a.opts.Wall, Route: storeRouter(a.opts.Home, a.projectDir())}
	res, err := g.MoveOrgFileStore(ctx, orgcfg.OrgFileMove{URL: sc.URL, Branch: sc.Branch, Old: old, New: raw, Org: org, Signature: storeSignature(o)})
	if err != nil {
		return "lore-org.yaml of " + org + " still names the old address (" + orgFileError(err, sc.URL).Error() + "); run `lore store set-url --name " + name + " --url " + raw + "` again when the store takes the commit, so teammates importing the org get the new address"
	}
	mine, _ := mineOf(ctx, cfg, o, res.Suggestion)
	if werr := writeOrgSource(a.opts.Home, configLockWait, org, mine, res.Suggestion.Source()); werr != nil {
		return "lore-org.yaml of " + org + " updated, but the entry's source stamp was not written: " + werr.Error() + "; `lore org refresh --org " + org + "` writes it"
	}
	where := "pushed"
	switch {
	case res.Undone:
		// D-565: back where it was before an unpushed move: no commit left
		return "lore-org.yaml of " + org + " names " + raw + " again: the earlier move to another address was never pushed, so both move commits were dropped and nothing waits to be sent; orgs[" + org + "] stamped with version " + short(res.Suggestion.Version)
	case !res.Changed:
		return "lore-org.yaml of " + org + " already names " + raw + " (version " + short(res.Suggestion.Version) + ")"
	case res.Offline:
		where = "committed in the local clone; offline, pushed by the next command"
	case !res.Pushed:
		where = "committed in the local clone; `lore sync` pushes it"
	}
	line := "lore-org.yaml of " + org + " now names " + raw + " (" + where + "); orgs[" + org + "] stamped with version " + short(res.Suggestion.Version)
	if repourl.IsLocalRemote(raw) {
		line += "; " + raw + " is a path on this machine: teammates cannot fetch from it" // D-562
	}
	return line
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
