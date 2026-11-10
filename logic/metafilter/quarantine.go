package metafilter

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Import caps (3.13): compiled in, the same on every machine, looser than
// the write-time policy defaults so a teammate's larger configured limit
// still imports; only a broken or hostile file is quarantined.
const (
	ImportMaxBytes   = 64 << 10
	ImportMaxDepth   = 6
	ImportMaxNameLen = 128
	ImportMaxEntries = 2000
)

// Problem is one reason a row's meta is quarantined; Key is the entry id
// ("" for a whole-row reason). Text names the key and the rule, never a
// value (a sensitive value must not leak through a quarantine line).
type Problem struct {
	Key  string
	Text string
}

// Quarantine checks a row's meta as it arrives through git (3.13, R10)
// and returns every problem, sorted by key id. Empty: the meta is usable.
// A conflict entry ({"conflict": …}, 3.12) is not a quarantine: it is an
// unsettled conflict, reported by merge.MetaConflicts.
func Quarantine(m map[ids.ID[ids.MetaKey]]value.JSON) []Problem {
	var out []Problem
	if len(m) > ImportMaxEntries {
		out = append(out, Problem{Text: strconv.Itoa(len(m)) + " entries; the import cap is " + strconv.Itoa(ImportMaxEntries)})
	}
	var entries []meta.Entry
	for id, j := range m {
		k := id.String()
		ms, err := canon.Members(j.Raw())
		if err != nil {
			out = append(out, Problem{Key: k, Text: "the entry is not an object"})
			continue
		}
		if hasMember(ms, entryConflict) {
			continue
		}
		e, err := meta.DecodeEntry(j)
		if err != nil {
			out = append(out, Problem{Key: k, Text: "the entry has no v"})
			continue
		}
		if p := checkImportName(e.Name); p != "" {
			out = append(out, Problem{Key: k, Text: p})
		}
		if p := checkImportValue(e.V); p != "" {
			out = append(out, Problem{Key: k, Text: e.Name + ": " + p})
		}
		entries = append(entries, e)
	}
	if n, err := meta.Size(entries); err == nil && n > ImportMaxBytes {
		out = append(out, Problem{Text: "metadata is " + strconv.Itoa(n) + " bytes; the import cap is " + strconv.Itoa(ImportMaxBytes)})
	}
	slices.SortFunc(out, func(a, b Problem) int {
		if c := strings.Compare(a.Key, b.Key); c != 0 {
			return c
		}
		return strings.Compare(a.Text, b.Text)
	})
	return out
}

func hasMember(ms []canon.Member, k string) bool {
	for _, m := range ms {
		if m.Key == k {
			return true
		}
	}
	return false
}

// checkImportName applies 3.5 with the import caps.
func checkImportName(name string) string {
	q := strconv.Quote(name)
	segs := meta.Segments(name)
	switch {
	case name == "":
		return "the entry has no name"
	case len(name) > ImportMaxNameLen:
		return q + " is longer than " + strconv.Itoa(ImportMaxNameLen) + " characters"
	case len(segs) > ImportMaxDepth:
		return q + " has " + strconv.Itoa(len(segs)) + " dot segments; the import cap is " + strconv.Itoa(ImportMaxDepth)
	}
	for _, s := range segs {
		if s == "" || len(s) > meta.MaxKeyLen || meta.CheckName(s, 1) != nil {
			return q + " is not a snake_case key name"
		}
	}
	return ""
}

// checkImportValue refuses null, empty values and control characters.
func checkImportValue(raw []byte) string {
	switch string(raw) {
	case "null", `""`, "[]", "{}":
		return "an empty value (" + string(raw) + ")"
	}
	switch canon.KindOf(raw) {
	case canon.KindInvalid:
		return "not a JSON value"
	case canon.KindString:
		var e meta.Entry
		e.V = raw
		if strings.ContainsFunc(meta.Show(e), func(r rune) bool { return unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' }) {
			return "text holds control characters"
		}
	case canon.KindArray:
		els, err := canon.Elements(raw)
		if err != nil {
			return "not a JSON list"
		}
		for _, el := range els {
			if p := checkImportValue(el); p != "" {
				return "a list item is " + p
			}
		}
	case canon.KindObject:
		ms, err := canon.Members(raw)
		if err != nil {
			return "not a JSON object"
		}
		for _, m := range ms {
			if p := checkImportValue(m.Raw); p != "" {
				return "field " + strconv.Quote(m.Key) + ": " + p
			}
		}
	case canon.KindNull, canon.KindNumber, canon.KindBool:
	}
	return ""
}
