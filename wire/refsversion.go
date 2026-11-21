package wire

// refsversion.go — keep every cache file's references expanded by this
// binary's rules and against the personal store's current resources
// (devco D-793, owner P-12). Each cache file keeps a refs stamp (sqlite
// Cache.RefreshRefs); on open a file whose stamp is not this one expands
// every text holding a reference again, once:
//
//	stamp = v<refsVersion>                 every file
//	      + "/" + <refs-epoch file>        a file of the personal store
//
// refsVersion moves when the reference rules change (an older binary's
// file then expands again instead of keeping its row_refs until a manual
// `lore search rebuild`). The refs-epoch file, beside the personal store's
// cache files, moves when a resource or kind is written in one of its
// folders: a journal note naming a person added later in me/work then
// fills in on the journal cache's next open, though that write opened
// me/work alone.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// refsVersion is the version of the reference rules this binary expands
// ${…} references by (resolver.go). Bump it whenever what a reference
// resolves to changes.
//
//	(none) a file never stamped (every binary before the stamp)
//	1      ${res:<kind>/<name>} and ${res:<place>:<kind>/<name>} (D-764),
//	       and a reference from one folder of the personal store to a
//	       resource or kind in another folder of it (D-793)
const refsVersion = "1"

// refsEpochFile is the file beside a personal store's cache files whose
// content moves on every resource or kind write in one of its folders.
const refsEpochFile = "refs-epoch"

// cacheFile is one distinct cache file and whether a personal store's
// folder reads through it (its stamp then holds the refs epoch).
type cacheFile struct {
	f        *sqlite.Cache
	path     string
	personal bool
}

// files lists the distinct cache files (a file opened under several names
// once).
func (c *caches) files() []cacheFile {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []cacheFile
	at := map[*sqlite.Cache]int{}
	for _, s := range c.order {
		f := c.by[s]
		if f == nil {
			continue
		}
		_, personal := personalBase(s)
		if i, ok := at[f]; ok {
			out[i].personal = out[i].personal || personal
			continue
		}
		at[f] = len(out)
		out = append(out, cacheFile{f: f, path: c.paths[s], personal: personal})
	}
	return out
}

// stamp is the refs stamp file f must carry to be current.
func (f cacheFile) stamp() string {
	s := "v" + refsVersion
	if f.personal && f.path != "" {
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(f.path), refsEpochFile)); err == nil {
			s += "/" + strings.TrimSpace(string(b))
		}
	}
	return s
}

// refreshRefs runs RefreshRefs on every distinct cache file. A current
// file costs one one-row read (and, for the personal store, one small file
// read); the engine must be set (the resolver reads row files through it).
func (c *caches) refreshRefs(ctx context.Context) error {
	for _, f := range c.files() {
		if _, err := f.f.RefreshRefs(ctx, f.stamp()); err != nil {
			return err
		}
	}
	return nil
}

// bumpRefsEpoch moves the refs epoch of the personal store whose folder
// source just took a resource or kind write, then stamps every open file
// of that store with it: the Apply that wrote it re-expanded them already
// (reexpand), so only the folders this process did not open expand again.
func (c *caches) bumpRefsEpoch(ctx context.Context, source store.Source) error {
	c.mu.Lock()
	p := c.paths[source]
	c.mu.Unlock()
	if p == "" {
		return nil
	}
	dir := filepath.Dir(p)
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return errs.Wrap(errs.Internal, "refs epoch", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, refsEpochFile), []byte(hex.EncodeToString(b)+"\n"), 0o600); err != nil {
		return err
	}
	for _, f := range c.files() {
		if f.personal && f.path != "" && filepath.Dir(f.path) == dir {
			if err := f.f.StampRefs(ctx, f.stamp()); err != nil {
				return err
			}
		}
	}
	return nil
}
