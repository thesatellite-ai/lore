package tasktree

import "strings"

// digits is the rank alphabet in byte order, so a lexical compare of two
// ranks is their order (6.2: a fractional index, ties broken by id).
const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// FirstRank is the rank of the first child of a parent with none. It does
// not end in the zero digit, so a rank below it always exists.
const FirstRank = "a1"

// After is a rank just above last ("" = FirstRank): the last digit moves
// up one, or a middle digit is appended at the top of the alphabet, so a
// run of appends grows slowly.
func After(last string) string {
	if last == "" {
		return FirstRank
	}
	i := strings.IndexByte(digits, last[len(last)-1])
	if i >= 0 && i < len(digits)-1 {
		return last[:len(last)-1] + string(digits[i+1])
	}
	return last + string(digits[len(digits)/2])
}

// Between is a rank strictly between a and b (either may be "": no bound).
// Ranks that do not order (a merge gave two siblings the same rank, or a
// hand edit) fall back to After(a): the tie is then broken by id (6.13).
func Between(a, b string) string {
	if b == "" {
		return After(a)
	}
	if a >= b || !valid(a) || !valid(b) {
		return After(a)
	}
	return midpoint(a, b)
}

// valid reports a rank of alphabet digits not ending in the zero digit.
func valid(s string) bool {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(digits, s[i]) < 0 {
			return false
		}
	}
	return s == "" || s[len(s)-1] != digits[0]
}

// midpoint is the shortest key strictly between a and b (a < b, b != "",
// neither ending in the zero digit): the fractional-indexing midpoint.
func midpoint(a, b string) string {
	n := 0
	for n < len(b) && digitAt(a, n) == b[n] {
		n++
	}
	if n > 0 {
		return b[:n] + midpoint(suffix(a, n), b[n:])
	}
	da := 0
	if a != "" {
		da = strings.IndexByte(digits, a[0])
	}
	db := len(digits)
	if b != "" {
		db = strings.IndexByte(digits, b[0])
	}
	if db-da > 1 {
		return string(digits[(da+db)/2])
	}
	if len(b) > 1 {
		return b[:1]
	}
	return string(digits[da]) + midpoint(suffix(a, 1), "")
}

// digitAt is a's digit at i, the zero digit past its end.
func digitAt(a string, i int) byte {
	if i < len(a) {
		return a[i]
	}
	return digits[0]
}

// suffix is a from i ("" past its end).
func suffix(a string, i int) string {
	if i >= len(a) {
		return ""
	}
	return a[i:]
}
