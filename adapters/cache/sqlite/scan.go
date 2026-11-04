package sqlite

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// scanAll reads every row of rs as typed values.
func scanAll(rs *sql.Rows) (Result, error) {
	cols, err := rs.Columns()
	if err != nil {
		return Result{}, err
	}
	res := Result{Columns: cols}
	// Forced stdlib boundary: (*sql.Rows).Scan takes ...any and a query's
	// column count is known only at run time. Every cell is narrowed to a
	// value.Value by cellValue before it leaves this function.
	cells, dest := make([]any, len(cols)), make([]any, len(cols))
	for i := range cells {
		dest[i] = &cells[i]
	}
	for rs.Next() {
		if err = rs.Scan(dest...); err != nil {
			return Result{}, err
		}
		out := make([]value.Value, len(cells))
		for i, c := range cells {
			if out[i], err = cellValue(c); err != nil {
				return Result{}, errs.Wrap(errs.CodeOf(err), "sqlite lens: column "+cols[i], err)
			}
		}
		res.Rows = append(res.Rows, out)
	}
	if err := rs.Err(); err != nil {
		return Result{}, err
	}
	return res, nil
}

// cellValue narrows one driver value (the closed set database/sql hands
// back for SQLite: int64, float64, string, []byte, time.Time, nil).
func cellValue(c driver.Value) (value.Value, error) {
	switch v := c.(type) {
	case nil:
		return value.Null{}, nil
	case int64:
		return value.Int(v), nil
	case float64:
		return value.Float(v)
	case string:
		return value.TextOf(v)
	case []byte:
		return value.TextOf(string(v))
	case time.Time:
		return value.DateTimeFrom(v), nil
	case bool:
		return value.BoolOf(v), nil
	default:
		return nil, errs.New(errs.Internal, "sqlite lens: unexpected driver value of type "+strconv.Quote(fmt.Sprintf("%T", v)))
	}
}
