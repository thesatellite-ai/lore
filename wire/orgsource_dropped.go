package wire

// orgsource_dropped.go — the one note when a restricted store stops being
// read (devco D-727). A member taken off a store's team (or a store whose
// audience moved away from them) loses it at the refresh that copies
// lore-org.yaml into their org entry (refreshOrgEntries): the entry stops
// listing it and its clone stays on disk. That happened silently. The
// refresh runs at any command's open as well as after a sync's fetch, so
// the drop is recorded here at the moment it happens and the next
// `lore sync` prints it once and clears it:
//
//	refreshOrgEntries ─ store left out for the person ─▶ state/dropped-stores.json
//	lore sync ─▶ droppedStoreNotes: one line per store ─▶ record emptied

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
)

// droppedStoresFile is <home>/state/dropped-stores.json.
const droppedStoresFile = "dropped-stores.json"

// droppedStore is one store a refresh stopped reading for this person.
type droppedStore struct {
	Org      string `json:"org"`
	URL      string `json:"url"`
	Branch   string `json:"branch,omitempty"`
	Audience string `json:"audience"`
}

// droppedStoresPath is the record's file.
func droppedStoresPath(home string) string {
	return filepath.Join(home, "state", droppedStoresFile)
}

// droppedRestricted lists the restricted stores the entry's copy have
// lists that the person's copy mine leaves out while the current file
// full still lists them (for someone else: a team they left, or admins).
// A store taken out of the file altogether is not one: nobody reads it.
func droppedRestricted(org string, have, mine, full orgconf.Shared) []droppedStore {
	listed := func(sh orgconf.Shared, url string) (orgconf.Store, bool) {
		for _, s := range sh.Stores {
			if storeaddr.Same(s.URL, url) {
				return s, true
			}
		}
		return orgconf.Store{}, false
	}
	var out []droppedStore
	for _, h := range have.Stores {
		if _, kept := listed(mine, h.URL); kept {
			continue
		}
		if f, ok := listed(full, h.URL); ok && f.Restricted() {
			out = append(out, droppedStore{Org: org, URL: h.URL, Branch: h.Branch, Audience: f.Audience.String()})
		}
	}
	return out
}

// recordDroppedStores adds ds to the record. Best effort: a lost record
// only loses the note, never a row.
func recordDroppedStores(home string, ds []droppedStore) {
	if len(ds) == 0 || home == "" {
		return
	}
	all := append(readDroppedStores(home), ds...)
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return
	}
	p := droppedStoresPath(home)
	if os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, p) // best effort, see above
}

// readDroppedStores reads the record; missing or unreadable is empty.
func readDroppedStores(home string) []droppedStore {
	b, err := os.ReadFile(filepath.Clean(droppedStoresPath(home)))
	if err != nil {
		return nil
	}
	var out []droppedStore
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// droppedStoreNotes is the sync's one line per dropped store, after
// which the record is emptied so no later sync says it again.
func droppedStoreNotes(home string) []string {
	ds := readDroppedStores(home)
	if len(ds) == 0 {
		return nil
	}
	_ = os.Remove(droppedStoresPath(home)) // best effort: worst case the note shows once more
	notes := make([]string, 0, len(ds))
	for _, d := range ds {
		notes = append(notes, droppedStoreNote(home, d))
	}
	return notes
}

// droppedStoreNote says what changed, why, and what to do with the clone.
func droppedStoreNote(home string, d droppedStore) string {
	why := "you are not on it"
	if orgconf.Audience(d.Audience) == orgconf.AudienceAdmins {
		why = "you are not an admin of " + d.Org
	}
	where := ""
	if norm, err := repourl.NormalizeStore(d.URL); err == nil {
		if dir, _, derr := git.CloneDirFor(home, git.Remote{Norm: norm}, d.Branch); derr == nil {
			where = " at " + dir
		}
	}
	return orgconf.Store{URL: d.URL}.ShortName() + ": no longer read: audience " + d.Audience + ", " + why +
		"; the clone" + where + " is left as it is: lore store forget --url " + d.URL + " --yes removes it"
}
