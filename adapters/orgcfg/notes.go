package orgcfg

// notes.go — whether a repo still keeps notes of its own in .lore/data
// (devco:D-723). A repo whose notes moved to a store folder (`lore store
// adopt --remove-source`, `lore move`) and that is linked to that folder
// writes there with no --to (orgresolve.TargetInputs.RepoHoldsNotes); a
// repo that still holds live rows keeps writing next to them.

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/row"
)

// holdsNotesScanMax caps the row files HoldsNotes reads. Past it the repo
// counts as holding notes: the old default (write the repo) is kept rather
// than reading thousands of tombstones on every write.
const holdsNotesScanMax = 2000

// HoldsNotes reports whether the .lore/data folder under dir holds a live
// note row: a row file, not a tombstone, in the folder of a table for
// which note answers true (the kinds F14 routes: memories, rules, tasks,
// …). The rows `lore init` writes (projects, repos), actor rows,
// _meta.json and tombstones are no notes. Why: in a linked repo whose
// notes moved out, a write with no --to went back into the code repo's
// .lore/data (D-723); this is the evidence that the repo's notes live in
// its linked folder.
func HoldsNotes(dir string, note func(table string) bool) bool {
	data := filepath.Join(dir, loreDir, dataDir)
	seen, holds := 0, false
	_ = filepath.WalkDir(data, func(p string, d fs.DirEntry, err error) error {
		if err != nil || holds {
			return filepath.SkipAll
		}
		rel, rerr := filepath.Rel(data, p)
		if rerr != nil || rel == "." {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if d.IsDir() {
			// only the folders of note kinds are read
			if len(parts) == 1 && !note(parts[0]) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(parts) < 2 || filepath.Ext(p) != ".json" {
			return nil // _meta.json and other top-level files
		}
		seen++
		if seen > holdsNotesScanMax || !tombstone(p) {
			holds = true
			return filepath.SkipAll
		}
		return nil
	})
	return holds
}

// tombstone reports whether the row file at p is a deleted row's marker
// (what `lore move` leaves behind). A file that cannot be read or parsed
// counts as a live row.
func tombstone(p string) bool {
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	raw, ok := m[row.KeyDeleted]
	return ok && strings.TrimSpace(string(raw)) == "true"
}
