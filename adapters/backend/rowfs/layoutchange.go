package rowfs

import (
	"strconv"
	"strings"
)

// ReadmeChange says what a layout change did to the store's README.md.
type ReadmeChange int

// The README outcomes of a layout change.
const (
	// ReadmeKept: README.md was current; untouched.
	ReadmeKept ReadmeChange = iota
	// ReadmeAdded: the store had no README.md; lore wrote one.
	ReadmeAdded
	// ReadmeRefreshed: README.md was lore's older text; lore rewrote it.
	ReadmeRefreshed
)

// LayoutChange is what one store-layout step changed (the git layout
// commit, or a folder store's move into _store/). Summary turns it into
// the words after the em dash of the line lore prints, naming only what
// changed: never a zero count.
type LayoutChange struct {
	// Moved counts the files moved.
	Moved int
	// FromOld is true when the files moved from <folder>/.lore/data/ (the
	// layout before plain files); false when they moved from the store's
	// top into _store/.
	FromOld bool
	// Folders are the folders whose rows moved (old layout only).
	Folders []string
	// Untracked counts lore's own files an earlier build committed that
	// are no longer tracked.
	Untracked int
	// Readme is what happened to README.md.
	Readme ReadmeChange
	// Marker is the lore_store version lore-store.yaml was rewritten to;
	// 0 when the marker did not change.
	Marker int
}

// Empty reports whether the change touched nothing.
func (c LayoutChange) Empty() bool {
	return c.Moved == 0 && c.Untracked == 0 && c.Readme == ReadmeKept && c.Marker == 0
}

// Summary is the change in a few words, for example
// "12 rows moved to _store/, README refreshed, marker lore_store: 4".
// An Empty change is "nothing changed".
func (c LayoutChange) Summary() string {
	var parts []string
	if c.Moved > 0 {
		n := plural(c.Moved, "row", "rows")
		if c.FromOld {
			p := n + " moved from <folder>/.lore/data/ to <folder>/, the store's own rows to " + StoreDataDir + "/"
			if len(c.Folders) > 0 {
				p += " (folders: " + strings.Join(c.Folders, ", ") + ")"
			}
			parts = append(parts, p)
		} else {
			parts = append(parts, n+" moved to "+StoreDataDir+"/")
		}
	}
	if c.Untracked > 0 {
		parts = append(parts, plural(c.Untracked, "lore state file", "lore state files")+" no longer tracked")
	}
	switch c.Readme {
	case ReadmeAdded:
		parts = append(parts, "README added")
	case ReadmeRefreshed:
		parts = append(parts, "README refreshed")
	case ReadmeKept:
	}
	if c.Marker > 0 {
		parts = append(parts, "marker lore_store: "+strconv.Itoa(c.Marker))
	}
	if len(parts) == 0 {
		return "nothing changed"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
