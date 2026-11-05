package task

// columns.go — task list --columns as a column table (TASK 16.2, 9.x
// example `--columns id,title,estimate,tracked`) and a saved view's
// columns.
//
//	--columns a,b,c (or a view's columns when --columns is not given)
//	  └ parseColumns: bare names → table mode; +time / +elapsed alone keep
//	      v0.1.17's list line with extra columns (time_list.go)
//	list ─► deco fills Brief.Cells per column (cellOf) ─► ListOut.Columns
//	ListOut.Text ─► header + aligned rows when Columns is set
//
// A column is a task --where key (taskKeys: every visible field, the
// envelope keys, sla, assignee/lead/watcher) or one computed on read:
// time (= tracked, the effort), elapsed, progress, store.

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/filter"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// Computed column names (read time, not task fields).
const (
	colTracked  = "tracked"  // the effort, the same number as time
	colProgress = "progress" // sub-task rollup, done/total leaves
	colStore    = "store"    // the store label of a task outside the repo
	// columnSep separates the cells of a column table.
	columnSep = "  "
	// columnPlus marks v0.1.17's extra-column form (+time, +elapsed).
	columnPlus = "+"
	// emptyCell prints a column a task has no value for.
	emptyCell = "-"
)

// computedColumns are the columns that are not --where keys.
var computedColumns = []string{colTime, colTracked, colElapsed, colProgress, colStore}

// columnSpec is a parsed --columns.
type columnSpec struct {
	// named are the table columns in order (empty: line mode).
	named []string
	// time / elapsed: the time columns are needed (either mode).
	time, elapsed bool
}

// parseColumns reads --columns values (comma-separated or repeated). Any
// bare name switches to table mode; a +name in table mode is the same
// column. An unknown name is refused with the column list.
func parseColumns(raw []string, keys map[string]filter.Field) (columnSpec, error) {
	var c columnSpec
	plus := []string{}
	for _, v := range raw {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name := strings.TrimPrefix(part, columnPlus)
			if _, ok := keys[name]; !ok && !slices.Contains(computedColumns, name) {
				return columnSpec{}, errs.New(errs.InvalidInput, "--columns "+work.Quote(part)+" is not a column").
					WithHint("task list columns: " + strings.Join(columnNames(keys), ", ") + " (or +time, +elapsed)")
			}
			switch name {
			case colTime, colTracked:
				c.time = true
			case colElapsed:
				c.elapsed = true
			}
			if strings.HasPrefix(part, columnPlus) {
				plus = append(plus, name)
				continue
			}
			if !slices.Contains(c.named, name) {
				c.named = append(c.named, name)
			}
		}
	}
	if len(c.named) > 0 {
		for _, p := range plus {
			if !slices.Contains(c.named, p) {
				c.named = append(c.named, p)
			}
		}
	} else if len(plus) > 0 {
		for _, p := range plus {
			if p != colTime && p != colElapsed && p != colTracked {
				return columnSpec{}, errs.New(errs.InvalidInput, "--columns +"+p+": only +time and +elapsed extend the list line").
					WithHint("name the columns without + for a column table: --columns id,title," + p)
			}
		}
	}
	return c, nil
}

// columnList is --columns values split at commas, trimmed, empties
// dropped (a view stores the list, 16.2).
func columnList(raw []string) []string {
	var out []string
	for _, v := range raw {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// columnNames lists every column, for hints.
func columnNames(keys map[string]filter.Field) []string {
	out := append(sortedKeys(keys), computedColumns...)
	slices.Sort(out)
	return out
}

// cellOf is one column of a task: get reads --where keys (SLA included),
// b carries the computed values the list decorated it with.
func cellOf(col string, get filter.Getter, b Brief) string {
	var s string
	switch col {
	case colTime, colTracked:
		s = timeentry.FormatSeconds(b.Time.EffortSeconds)
	case colElapsed:
		s = timeentry.FormatSeconds(b.Time.ElapsedSeconds)
	case colProgress:
		if b.Rollup.LeavesTotal > 0 {
			s = strconv.Itoa(b.Rollup.LeavesDone) + "/" + strconv.Itoa(b.Rollup.LeavesTotal)
		}
	case colStore:
		s = strings.Trim(b.Label, "[]")
	case fieldEstimate:
		s = b.Estimate
	default:
		vals := get(filter.Key{Name: col})
		for i, v := range vals {
			vals[i] = shortTime(v)
		}
		s = strings.Join(vals, ",")
	}
	if s == "" {
		return emptyCell
	}
	return s
}

// shortTime prints an RFC 3339 cell as a day (midnight UTC: a calendar
// day, TSQ11) or a UTC minute; any other text is returned as is.
func shortTime(v string) string {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return v
	}
	t = t.UTC()
	if t.Equal(t.Truncate(24 * time.Hour)) {
		return t.Format(time.DateOnly)
	}
	return t.Format(cellMinute)
}

// cellMinute is a datetime cell's layout (UTC).
const cellMinute = "2006-01-02 15:04Z"

// cellsOf fills b.Cells for the named columns.
func cellsOf(cols []string, get filter.Getter, b *Brief) {
	if len(cols) == 0 {
		return
	}
	b.Cells = make([]string, len(cols))
	for i, c := range cols {
		b.Cells[i] = cellOf(c, get, *b)
	}
}

// columnsText is the column table: an upper-case header, then one row
// per task, each column padded to its widest cell (runes, not bytes).
func (o ListOut) columnsText() string {
	if len(o.Data) == 0 {
		return "(no tasks)\n"
	}
	width := make([]int, len(o.Columns))
	head := make([]string, len(o.Columns))
	for i, c := range o.Columns {
		head[i] = strings.ToUpper(c)
		width[i] = len([]rune(head[i]))
	}
	for _, t := range o.Data {
		for i, c := range t.Cells {
			if i < len(width) {
				width[i] = max(width[i], len([]rune(c)))
			}
		}
	}
	var b strings.Builder
	line := func(cells []string) {
		for i, c := range cells {
			if i >= len(width) {
				break
			}
			if i == len(cells)-1 {
				b.WriteString(c)
				break
			}
			b.WriteString(c + strings.Repeat(" ", width[i]-len([]rune(c))) + columnSep)
		}
		b.WriteString("\n")
	}
	line(head)
	for _, t := range o.Data {
		line(t.Cells)
	}
	return b.String()
}

// rowCells is the decorate step of table mode (list's deco).
func rowCells(cols []string, get func(row.Row) filter.Getter) func(b *Brief, r row.Row) {
	return func(b *Brief, r row.Row) { cellsOf(cols, get(r), b) }
}
