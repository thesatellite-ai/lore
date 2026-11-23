package sqlite

import (
	"context"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Held is the version of one row a cache holds: its hlc and whether it is
// a tombstone.
type Held struct {
	HLC     hlc.HLC
	Deleted bool
}

// HeldVersions lists the version of every row source's cache holds, by
// key (tombstones included). Why (devco D-920, hunt H-178): `lore doctor`
// compares it with the row files to find an index that shows text no
// file has any more (a cache filled before that fix kept a newer copy
// over a file that went back to an older one, and its cursor had moved
// past the file, so no refresh ever read it again). A cell that does not
// parse (not lore's) is left out.
func (c *Cache) HeldVersions(ctx context.Context, source store.Source) (map[store.Key]Held, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return nil, err
	}
	rs, err := c.db.QueryContext(ctx, `SELECT tbl, id, hlc, deleted FROM "`+tblRows+`" WHERE source = ?`, string(source))
	if err != nil {
		return nil, dbErr("read the held rows", err)
	}
	defer rs.Close()
	out := map[store.Key]Held{}
	for rs.Next() {
		var tbl, id, stamp string
		var deleted int
		if err := rs.Scan(&tbl, &id, &stamp, &deleted); err != nil {
			return nil, dbErr("read the held rows", err)
		}
		rid, err := ids.ParseAny(id)
		if err != nil {
			continue
		}
		h, err := hlc.Parse(stamp)
		if err != nil {
			continue
		}
		out[store.Key{Table: row.Table(tbl), ID: rid}] = Held{HLC: h, Deleted: deleted != 0}
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("read the held rows", err)
	}
	return out, nil
}

// Sources lists every source that has rows or a cursor in this file. Why
// (devco D-920, public issue #142): a folder's cache keys rows by the name
// the folder is attached under, so after the folder was attached under
// another name the rows under the earlier name stayed in the file, were
// searched with the rest, and a rebuild of the current name left them; the
// caller clears the names no attachment uses any more.
func (c *Cache) Sources(ctx context.Context) ([]store.Source, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return nil, err
	}
	rs, err := c.db.QueryContext(ctx, `SELECT source FROM "`+tblRows+`" UNION SELECT source FROM "`+tblCursors+`"`)
	if err != nil {
		return nil, dbErr("list the sources", err)
	}
	defer rs.Close()
	var out []store.Source
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			return nil, dbErr("list the sources", err)
		}
		out = append(out, store.Source(s))
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("list the sources", err)
	}
	return out, nil
}
