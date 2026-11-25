package ask

// order.go — ask answers the same for the same store, whatever order the
// cache hands its lines in (devco:D-979, bug hunt H-238). The lens views
// (<table>_all) are a UNION ALL of each attached store's table in its scan
// order, and that order is the order the rows went into the cache file: an
// edit or a sync re-inserts a row at the end, a rebuilt cache writes them
// back in file order. Every later step breaks its ties by that order (the
// BM25 and meaning pools of hybrid.Fuse, the nearest rows by meaning, the
// copy pickLines keeps, the tasks of one day, a resource's refs), so the
// same question on the same rows printed a note on one run and left it
// out minutes later, after the cache was rewritten. canonicalLines puts
// the lines in one order fixed by the rows themselves before anything
// reads them.

import (
	"cmp"
	"slices"

	"github.com/thesatellite-ai/lore/model/value"
)

// canonicalLines is lines in the order of their key (keyOf: the cells that
// name the line, never its text), stably: two lines with one key (the
// copies of one row in two stores) keep their order, nearest store first,
// which pickLines and loadTyped rely on. It sorts a copy: the read server
// keeps lines across calls and other calls read them at the same time.
func canonicalLines(lines [][]value.Value, keyOf func([]value.Value) []string) [][]value.Value {
	keys := make([][]string, len(lines))
	idx := make([]int, len(lines))
	for i, l := range lines {
		keys[i], idx[i] = keyOf(l), i
	}
	slices.SortStableFunc(idx, func(a, b int) int { return slices.Compare(keys[a], keys[b]) })
	out := make([][]value.Value, len(lines))
	for i, j := range idx {
		out[i] = lines[j]
	}
	return out
}

// textLineKey names a line of a text statement: a row line by its id, a
// ref line by its entity and the resource it references.
func textLineKey(l []value.Value) []string {
	if len(l) <= colRefID {
		return nil
	}
	if cell(l[colKind]) == "ref" {
		return []string{"ref", cell(l[colTable]), cell(l[colID]), cell(l[colRefID])}
	}
	return []string{cell(l[colKind]), cell(l[colID])}
}

// typedLineKey names a line of typedSQL: a resource, field or task by its
// id; a task's metadata by its key (its value is what two stores may
// disagree on, so the nearer store's line keeps its place); a ref by its
// relation and resource.
func typedLineKey(l []value.Value) []string {
	if len(l) < 5 {
		return nil
	}
	switch kind := cell(l[0]); kind {
	case "met":
		return []string{kind, cell(l[1]), cell(l[3])}
	case "ref":
		return []string{kind, cell(l[1]), cell(l[3]), cell(l[4])}
	default:
		return []string{kind, cell(l[1])}
	}
}

// byIDLast is c, or the order of two ids when c is 0: the last tie-break
// of every ranking, so equal rows never fall back to the order they came
// in.
func byIDLast(c int, a, b string) int { return cmp.Or(c, cmp.Compare(a, b)) }
