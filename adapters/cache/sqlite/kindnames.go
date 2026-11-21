package sqlite

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// KindNames maps every resource kind id this cache holds to its name.
// Why (devco:D-808): `lore search` names the kind of each resource hit;
// it read the names through the SQL lens (`SELECT id, name FROM
// kinds_all`), which opened a second connection and created every lens
// view on each call, about a third of a warm search. One indexed read of
// the cache's own kinds table is all the naming needs. A cache with no
// kinds table (an old or empty one) is an empty map.
func (c *Cache) KindNames(ctx context.Context) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return nil, err
	}
	if err := c.ensureInit(ctx); err != nil {
		return nil, err
	}
	out := map[string]string{}
	if _, ok := c.st.plans[row.Table(schema.TableKinds)]; !ok {
		return out, nil
	}
	rs, err := c.db.QueryContext(ctx, `SELECT "`+colID+`", "name" FROM "`+schema.TableKinds+`"`)
	if err != nil {
		return nil, dbErr("kind names", err)
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var id, name string
		if err := rs.Scan(&id, &name); err != nil {
			return nil, dbErr("kind names", err)
		}
		out[id] = name
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("kind names", err)
	}
	return out, nil
}
