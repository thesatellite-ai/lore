package wire

// doctor_storereadme.go — the store_readme check of `lore doctor` (plain-
// files round, D-f): a store lore reads without its README.md gets a note.
// lore writes the README once, with its next commit in the store (or when
// it makes the store); a person may rewrite or remove it, so a missing one
// is a note, never a warning, and doctor writes nothing. A folder store
// (no commits) gets its README only when lore makes it.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// DoctorCheckStoreReadme is the check's JSON name.
const DoctorCheckStoreReadme DoctorCheck = "store_readme"

// DoctorStoreReadmes notes each attached store checkout (a git store's
// clone, a folder store) whose top has no README.md. It only reads.
func (a *App) DoctorStoreReadmes(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []DoctorFinding
	for _, src := range a.Stores() {
		dir, fix := "", ""
		switch b := concrete(st.byName[src]).(type) {
		case *git.Backend:
			if b.CloneMode() {
				dir, fix = b.Dir(), "lore adds it with its next commit in this store; or commit your own "+rowfs.ReadmeFile+" there"
			}
		case *folder.Backend:
			dir, fix = b.Dir(), "write your own "+rowfs.ReadmeFile+" there (lore writes one only when it makes a folder store)"
		}
		if dir == "" || seen[dir] || rowfs.HasReadme(dir) {
			continue
		}
		seen[dir] = true
		out = append(out, DoctorFinding{Check: DoctorCheckStoreReadme, Level: DoctorNote, Store: src,
			Message: "store " + string(src) + ": " + dir + " has no " + rowfs.ReadmeFile + " (the page that tells a person what the store's folders are and how to edit them by hand)",
			Fix:     fix})
	}
	return out, nil
}
