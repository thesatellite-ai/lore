package wire

// rowcites.go — keep a note citing ${row:<id>} expanded the same way
// whether its cache was built write by write or from nothing (bug hunt
// H-283, devco:D-1021):
//
//	cache Apply ─▶ expandField ─▶ cacheExpander.Expand
//	                                │ every ${row:<id>} asked, resolved or not,
//	                                ▼ becomes a row_refs line of the field
//	later Apply of <id> (restored, arrived, edited) ─▶ ReexpandIDs finds it
//	Apply in a personal folder ─▶ another folder's cache file cites an
//	                              applied id ─▶ bumpRefsEpoch: it expands
//	                              again on its next open
//
// A dangling ${row:<id>} kept no row_refs line, so a restored row (lore
// trash restore) never filled in the notes citing it until `lore search
// rebuild`; and a row edited in me/work left the me/archive note citing
// it with the old text, since the write opened me/work alone.

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// cacheExpander is the expander the cache files fill row_expanded with:
// refs.Expander plus the id of every ${row:<id>} the text (or a text it
// pulls in) asks for, resolved or not.
type cacheExpander struct {
	rv refs.Resolver
}

// Expand implements sqlite.Expander.
func (e cacheExpander) Expand(text string) (string, []string, error) {
	rec := &rowAsked{next: e.rv}
	res, err := refs.Expander{Resolver: rec}.ExpandFull(text)
	if err != nil {
		return "", nil, err
	}
	out := append(slices.Clone(res.IDs), rec.ids...)
	return res.Text, slices.Compact(slices.Sorted(slices.Values(out))), nil
}

var _ sqlite.Expander = cacheExpander{}

// rowAsked is a Resolver that notes the id of every row reference it is
// asked for, then asks next.
type rowAsked struct {
	next refs.Resolver
	mu   sync.Mutex
	ids  []string
}

// Resolve implements refs.Resolver.
func (r *rowAsked) Resolve(ref refs.Ref) (refs.Resolved, error) {
	if ref.Scheme == refs.SchemeRow {
		idText, _, _ := strings.Cut(ref.Target, "/")
		if id, err := ids.ParseAny(idText); err == nil {
			r.mu.Lock()
			r.ids = append(r.ids, id.String())
			r.mu.Unlock()
		}
	}
	return r.next.Resolve(ref)
}

// siblingsCite reports whether a cache file of the personal store whose
// folder source reads through this process's file, other than the files
// this process has open (reexpand refreshed those), has a row_refs line
// for one of changed. A file that cannot be read counts as citing: the
// cost is one more expansion on its next open, never a stale answer.
func (c *caches) siblingsCite(ctx context.Context, source store.Source, changed []string) bool {
	c.mu.Lock()
	p := c.paths[source]
	c.mu.Unlock()
	if p == "" || len(changed) == 0 {
		return false
	}
	open := map[string]bool{}
	for _, f := range c.files() {
		open[f.path] = true
	}
	var others []string
	_ = filepath.WalkDir(filepath.Dir(p), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, cacheExt) && !open[path] {
			others = append(others, path)
		}
		return nil
	})
	for _, o := range others {
		if cites, err := sqlite.FileCites(ctx, o, changed); err != nil || cites {
			return true
		}
	}
	return false
}
