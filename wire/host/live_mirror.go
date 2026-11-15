package host

// live_mirror.go — a live row's backend form kept on disk by its blob id
// (devco T-20 follow-up: task list / show at ~5,000 open tasks).
//
// A full read turned every live file into its backend row (liveStoreRow:
// clone, encode, decode, encode) in every process: 1.5 s of each task list
// at 5,600 open tasks, though the files had not changed. The conversion is
// a function of the file's bytes, so its result is kept in the store's row
// mirror (the SQLite cache's _mirror table, store.RowMirror) under the
// live path and the blob id: a later process with the same blob reads the
// row back instead of converting it. Live paths are kept under livePathKey
// so they never meet the branch files' entries of the same store.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// livePathKey prefixes a live file's path in the row mirror.
const livePathKey = "live:"

// unwrapper is a backend wrapper that names the backend it wraps.
type unwrapper interface{ Unwrap() store.Backend }

// liveMirrorOf is the row mirror ctx carries for the backend under b (the
// engine binds it to the store's MirroredReader), nil when none.
func liveMirrorOf(ctx context.Context, b store.Backend) store.RowMirror {
	for b != nil {
		if r, ok := b.(store.MirroredReader); ok {
			return store.RowMirrorFor(ctx, r)
		}
		u, ok := b.(unwrapper)
		if !ok {
			return nil
		}
		b = u.Unwrap()
	}
	return nil
}

// liveConverted returns the backend row of each live file in keys (all of
// them of files), read from the row mirror when it holds the same blob,
// converted (and then kept) otherwise. Without a mirror it is storeRow per
// file, as before.
func (b *liveBackend) liveConverted(ctx context.Context, s *liveSession, oid string, files map[live.RowKey]live.File, keys []live.RowKey) (map[live.RowKey]store.Row, error) {
	out := make(map[live.RowKey]store.Row, len(keys))
	m := liveMirrorOf(ctx, b.inner)
	var blobs map[live.RowKey][2]string // key -> mirror path, blob id
	if m != nil && len(keys) > 0 {
		blobs = b.liveBlobs(ctx, s, oid)
	}
	if blobs == nil {
		for _, k := range keys {
			r, err := s.decoded.storeRow(oid, k, files[k])
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	}
	byTable := map[row.Table]map[string]string{}
	for _, k := range keys {
		pb, ok := blobs[k]
		if !ok {
			continue
		}
		t := row.Table(k.Table)
		if byTable[t] == nil {
			byTable[t] = map[string]string{}
		}
		byTable[t][pb[0]] = pb[1]
	}
	held := map[string]store.Row{}
	for t, want := range byTable {
		got, err := m.Lookup(ctx, t, want)
		if err != nil {
			break // best effort: the files are converted
		}
		for p, r := range got {
			held[p] = r
		}
	}
	var keep []store.MirrorEntry
	for _, k := range keys {
		pb, known := blobs[k]
		if r, ok := held[pb[0]]; known && ok {
			out[k] = r
			s.decoded.keepStored(oid, k, r)
			continue
		}
		r, err := s.decoded.storeRow(oid, k, files[k])
		if err != nil {
			return nil, err
		}
		out[k] = r
		if known {
			keep = append(keep, store.MirrorEntry{Path: pb[0], Blob: pb[1], Row: r})
		}
	}
	if len(keep) > 0 {
		_ = m.Keep(ctx, keep) // best effort: the next read converts again
	}
	return out, nil
}

// liveBlobs maps each live row of this backend's folder at commit oid to
// its mirror path and blob id (one git ls-tree); nil when it cannot be read.
func (b *liveBackend) liveBlobs(ctx context.Context, s *liveSession, oid string) map[live.RowKey][2]string {
	if oid == "" {
		return nil
	}
	raw, err := s.ref.Repo.Run(ctx, gitref.Invocation{Args: []string{"ls-tree", "-r", "-z", "--full-tree", oid}})
	if err != nil {
		return nil
	}
	out := map[live.RowKey][2]string{}
	for _, entry := range strings.Split(string(raw), "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta) // mode type oid
		if len(f) != 3 || f[1] != "blob" {
			continue
		}
		folder, k, ok := live.ParseRowPath(path)
		if !ok || folder != b.folder {
			continue
		}
		out[k] = [2]string{livePathKey + path, f[2]}
	}
	return out
}
