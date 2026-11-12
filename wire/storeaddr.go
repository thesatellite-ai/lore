package wire

// storeaddr.go — one address per store id on this machine
// (SOURCE_OF_TRUTH row 7). The store's id (lore-store.yaml `id:`) is its
// identity; every place naming it (the repo's .lore/stores.yaml, the org
// entries' stores, personal.store, setups[].personal_store, and the
// address record `lore store set-url` keeps) is one entry, its id read
// from the store's local copy (never the network).
//
//	addressEntries ─▶ logic/storeaddr.Splits: one id at two addresses
//	   ├─▶ markSplitAddresses (openAttached): a store opened at a second
//	   │   address refuses writes (engine.Attached.WriteRefused, E_REFUSED)
//	   ├─▶ DoctorStoreAddresses: "store sto_… is reached at two
//	   │   addresses: A, B" with the fix `lore store set-url <sto_> <A>`
//	   └─▶ AddressNote (store list ADDRESS column)

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// storeAddrRecordFile is the address record under <home>/state: the
// address `lore store set-url` last recorded for each store id.
const storeAddrRecordFile = "store-addresses.json"

// storeAddrRecord is the record file.
type storeAddrRecord struct {
	Stores map[string]storeAddrRecordEntry `json:"stores"`
}

// storeAddrRecordEntry is one store's recorded address.
type storeAddrRecordEntry struct {
	URL   string `json:"url"`
	SetAt string `json:"set_at"`
}

// storeAddrRecordPath is <home>/state/store-addresses.json.
func storeAddrRecordPath(home string) string {
	return filepath.Join(home, "state", storeAddrRecordFile)
}

// readStoreAddrRecord reads the record; a missing or unreadable file is an
// empty record (the record only picks the primary address; without it the
// machine config's address is primary).
func readStoreAddrRecord(home string) storeAddrRecord {
	r := storeAddrRecord{Stores: map[string]storeAddrRecordEntry{}}
	b, err := os.ReadFile(filepath.Clean(storeAddrRecordPath(home)))
	if err != nil {
		return r
	}
	if json.Unmarshal(b, &r) != nil || r.Stores == nil {
		return storeAddrRecord{Stores: map[string]storeAddrRecordEntry{}}
	}
	return r
}

// recordStoreAddress writes id's address into the record.
func recordStoreAddress(home, id, raw string, now time.Time) error {
	r := readStoreAddrRecord(home)
	r.Stores[id] = storeAddrRecordEntry{URL: raw, SetAt: now.UTC().Format(time.RFC3339)}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode the store address record", err)
	}
	p := storeAddrRecordPath(home)
	if err = os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return errs.FileIO("create", filepath.Dir(p), err)
	}
	tmp := p + ".tmp"
	if err = os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return errs.FileIO("write", tmp, err)
	}
	if err = os.Rename(tmp, p); err != nil {
		return errs.FileIO("write", p, err)
	}
	return nil
}

// storeIDOnDisk is the sto_ id in the marker of the store raw names, read
// from this machine's copy (the file:// folder, or the git clone for
// branch, else its default-branch clone); "" when there is
// no local copy or no 0.1.18 marker. Never the network.
func storeIDOnDisk(home, raw, branch string) string {
	canon, err := canonical(attach.Store{URL: raw})
	if err != nil {
		return ""
	}
	u, err := url.Parse(canon)
	if err != nil {
		return ""
	}
	var dirs []string
	if u.Scheme == folder.Scheme {
		dirs = []string{fileuri.OSPath(u.Path)}
	} else {
		r, rerr := git.RemoteOf(u)
		if rerr != nil {
			return ""
		}
		if branch != "" {
			if d, _, derr := git.CloneDirFor(home, r, branch); derr == nil {
				dirs = append(dirs, d)
			}
		}
		if d, _, derr := git.CloneDirFor(home, r, ""); derr == nil {
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		if m, merr := rowfs.ReadMarker(d); merr == nil && !m.ID.IsZero() {
			return m.ID.String()
		}
	}
	return ""
}

// addressEntries is every place on this machine naming a store, as seen
// from the repo at top ("" = no repo): the address record first, then the
// repo's stores.yaml (store entries, not folder entries: those follow
// their store), then the org entries' stores, personal.store and each
// setup's own personal_store. Each carries the id of the store its url
// reaches on this machine (storeIDOnDisk).
func addressEntries(home, top string) []storeaddr.Entry {
	ids := map[string]string{}
	idOf := func(raw, branch string) string {
		k := storeaddr.Key(raw) + "@" + branch
		if v, ok := ids[k]; ok {
			return v
		}
		v := storeIDOnDisk(home, raw, branch)
		ids[k] = v
		return v
	}
	var out []storeaddr.Entry
	for id, e := range readStoreAddrRecord(home).Stores {
		out = append(out, storeaddr.Entry{Place: storeaddr.PlaceRecord, URL: e.URL, StoreID: id})
	}
	if top != "" {
		if c, err := readStoresFile(top); err == nil {
			for _, s := range c.Stores {
				if strings.Contains(string(s.Name), targetSep) {
					continue
				}
				out = append(out, storeaddr.Entry{Place: storeaddr.PlaceStoresFile, Name: string(s.Name), URL: s.URL, StoreID: idOf(s.URL, s.Branch)})
			}
		}
	}
	if cfg, _, err := orgcfg.LoadConfig(home); err == nil {
		for _, o := range cfg.Orgs {
			for _, sc := range o.Stores {
				out = append(out, storeaddr.Entry{Place: storeaddr.PlaceOrg, Name: o.Name, URL: sc.URL, StoreID: idOf(sc.URL, sc.Branch)})
			}
		}
	}
	if mc, err := readMachineConfig(home); err == nil {
		if p := mc.personalURL(); p != "" {
			out = append(out, storeaddr.Entry{Place: storeaddr.PlacePersonal, URL: p, StoreID: idOf(p, "")})
		}
	}
	if sc, err := ReadSetupsConfig(home); err == nil {
		for _, s := range sc.Setups {
			switch p := strings.TrimSpace(s.PersonalStore); p {
			case "", onboard.PersonalStoreInherit, onboard.PersonalStoreNone:
			default:
				out = append(out, storeaddr.Entry{Place: storeaddr.PlaceSetup, Name: s.Name, URL: p, StoreID: idOf(p, "")})
			}
		}
	}
	return out
}

// markSplitAddresses sets WriteRefused on every opened store reached at a
// second address of its store id (storeaddr.Split.Refusal): reads keep
// working, writes go only to the primary address. urls are the opened
// stores' canonical urls by name.
func markSplitAddresses(home, top string, opened []attach.Opened, urls map[store.Source]string) {
	if len(opened) == 0 {
		return
	}
	entries := addressEntries(home, top)
	splits := storeaddr.Splits(entries)
	if len(splits) == 0 {
		return
	}
	for i := range opened {
		m, ok := opened[i].Backend.(markerOf)
		if !ok {
			continue
		}
		sp, ok := storeaddr.For(splits, m.Marker().ID.String())
		if !ok {
			continue
		}
		shown := urls[opened[i].Name]
		for _, e := range entries { // the address as the person wrote it
			if storeaddr.Same(e.URL, shown) {
				shown = e.URL
				break
			}
		}
		opened[i].WriteRefused = sp.Refusal(string(opened[i].Name), shown)
	}
}

// DoctorCheckStoreAddress is the "one store at two addresses" check of
// `lore doctor` (SOURCE_OF_TRUTH row 7).
const DoctorCheckStoreAddress DoctorCheck = "store_address"

// DoctorStoreAddresses warns once per store id this machine reaches at
// more than one address, naming each place and the fix.
func (a *App) DoctorStoreAddresses(ctx context.Context) ([]DoctorFinding, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		top = "" // outside a repo: the machine config's places only
	}
	var out []DoctorFinding
	for _, sp := range storeaddr.Splits(addressEntries(a.opts.Home, top)) {
		out = append(out, DoctorFinding{Check: DoctorCheckStoreAddress, Level: DoctorWarning,
			Message: sp.Line() + " (" + placesText(sp.Entries) + "); writes go only to " + sp.Primary,
			Fix:     sp.Fix()})
	}
	return out, nil
}

// placesText names each entry: "acme in .lore/stores.yaml: B".
func placesText(es []storeaddr.Entry) string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		who := string(e.Place)
		if e.Name != "" {
			who = e.Name + " in " + who
		}
		parts = append(parts, who+": "+e.URL)
	}
	return strings.Join(parts, "; ")
}

// AddressNote is store list's ADDRESS column for an entry at raw: "" when
// the store has one address on this machine, else "second address (writes
// go to <primary>)" or "primary of N addresses".
func (a *App) AddressNote(ctx context.Context, raw, branch string) string {
	id := storeIDOnDisk(a.opts.Home, raw, branch)
	if id == "" {
		return ""
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		top = ""
	}
	sp, ok := storeaddr.For(storeaddr.Splits(addressEntries(a.opts.Home, top)), id)
	if !ok {
		return ""
	}
	if storeaddr.Same(raw, sp.Primary) {
		return "primary of " + strconv.Itoa(len(sp.Others)+1) + " addresses"
	}
	return "second address; writes go to " + sp.Primary
}
