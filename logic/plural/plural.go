// Package plural writes a count with its noun: "1 row", "2 rows",
// "0 entries". Every human line that prints a number before a noun uses
// it, so none says "1 rows" again (ops/trash count, engine/trash rowCount
// and wire's plural are private twins from before this package; they print
// the same text and can move to it).
//
// Layer L1: pure (the standard library only), so ops and wire share it.
package plural

import "strconv"

// Count is n and word, with an "s" unless n is 1.
func Count(n int, word string) string {
	return Of(n, word, word+"s")
}

// Of is n and one or many: one when n is 1, many otherwise (an irregular
// plural: Of(n, "entry", "entries")).
func Of(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
