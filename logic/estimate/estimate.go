// Package estimate is the task estimate rule (TASK_SYSTEM_DESIGN 9, TSQ4):
// one decimal >= 0 with at most two decimals, in the project's unit
// (tasks.estimate_unit: points, hours or days).
//
// Layer L1: pure; imports model only.
//
//	--estimate raw ─► Parse(raw, unit) ─► literal text stored on the row
//	rollup ─► Hundredths(literal) summed ─► Format
//
// Values are summed as integer hundredths, so 0.1 + 0.2 is exactly 0.3.
package estimate

import (
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Unit is the closed set of tasks.estimate_unit values.
type Unit string

// Units (TSQ4); points is the default.
const (
	Points Unit = "points"
	Hours  Unit = "hours"
	Days   Unit = "days"
)

// Units lists every unit, in the policy's declared order.
func Units() []Unit { return []Unit{Points, Hours, Days} }

// UnitOf maps a policy value to a Unit; unknown or empty is Points.
func UnitOf(s string) Unit {
	for _, u := range Units() {
		if string(u) == s {
			return u
		}
	}
	return Points
}

// maxDecimals is the most decimals an estimate keeps (9.1).
const maxDecimals = 2

// hundred is hundredths per unit.
const hundred = 100

// daySuffix is the day form accepted with unit days ("2d").
const daySuffix = "d"

// Parse checks raw against unit and returns the literal to store (9.1):
// a plain decimal ("3", "0.5", "3.00" kept as typed) in every unit; with
// hours also a duration ("90m" → "1.5"); with days also "2d" → "2". A
// comma, a sign, an exponent, an hour duration with days and a duration
// with points are refused (E_INVALID_INPUT).
func Parse(raw string, unit Unit) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", bad(raw, unit, "empty")
	}
	if plainDecimal(s) {
		return s, nil
	}
	switch unit {
	case Hours:
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return "", bad(raw, unit, "not a number or a duration")
		}
		if d%(36*time.Second) != 0 {
			return "", bad(raw, unit, "more precise than 0.01 hours")
		}
		return Format(int64(d / (36 * time.Second))), nil
	case Days:
		if n, ok := strings.CutSuffix(s, daySuffix); ok && plainDecimal(n) {
			return n, nil
		}
		return "", bad(raw, unit, "days take a number or Nd (lore does not assume a working day's hours)")
	case Points:
	}
	return "", bad(raw, unit, "not a number")
}

// bad is the refusal of raw.
func bad(raw string, unit Unit, why string) error {
	return errs.New(errs.InvalidInput, "--estimate "+strconv.Quote(raw)+": "+why).
		WithHint("unit is " + string(unit) + " (tasks.estimate_unit): give a number >= 0 with at most 2 decimals, e.g. 3 or 0.5" + unitHint(unit))
}

// unitHint names the extra forms of unit.
func unitHint(u Unit) string {
	switch u {
	case Hours:
		return ", or a duration like 90m"
	case Days:
		return ", or 2d"
	case Points:
	}
	return ""
}

// plainDecimal reports digits with an optional "." and at most two decimals.
func plainDecimal(s string) bool {
	whole, frac, dot := strings.Cut(s, ".")
	if whole == "" || !digits(whole) || (dot && (frac == "" || len(frac) > maxDecimals || !digits(frac))) {
		return false
	}
	return true
}

// digits reports a non-empty run of ASCII digits.
func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Hundredths reads a stored literal; ok is false for "" or a bad value
// (a hand-edited row), which then counts as unestimated.
func Hundredths(lit string) (int64, bool) {
	s := strings.TrimSpace(lit)
	if !plainDecimal(s) {
		return 0, false
	}
	whole, frac, _ := strings.Cut(s, ".")
	frac = (frac + "00")[:maxDecimals]
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	return w*hundred + f, true
}

// Format renders hundredths without trailing zeros ("1.5", "3").
func Format(h int64) string {
	s := strconv.FormatInt(h/hundred, 10)
	if f := h % hundred; f != 0 {
		s += strings.TrimRight("."+strconv.FormatInt(hundred+f, 10)[1:], "0")
	}
	return s
}

// Label is an amount with its unit: "3 points", "1.5h", "2d".
func Label(h int64, u Unit) string {
	switch u {
	case Hours:
		return Format(h) + "h"
	case Days:
		return Format(h) + "d"
	case Points:
	}
	return Format(h) + " points"
}

// Total is a rollup over leaves (9.2): the sum of estimated leaves split
// by done / open, and how many leaves have none.
type Total struct {
	Done        int64
	Open        int64
	Unestimated int
}

// Leaf is one leaf of a rollup.
type Leaf struct {
	Literal string
	Done    bool
}

// Sum rolls leaves up; cancelled leaves are left out by the caller.
func Sum(leaves []Leaf) Total {
	var t Total
	for _, l := range leaves {
		h, ok := Hundredths(l.Literal)
		switch {
		case !ok:
			t.Unestimated++
		case l.Done:
			t.Done += h
		default:
			t.Open += h
		}
	}
	return t
}

// All is done + open.
func (t Total) All() int64 { return t.Done + t.Open }
