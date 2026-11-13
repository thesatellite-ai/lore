package lint

// `lore lint --context` (CONTEXT_BUDGET 6.12): read-only clean-up
// suggestions for the pinned core of LORE.md, each with its command.
//
//	contextFindings(rows)
//	  ├─ pinned = must rules + critical/high hotfixes, not archived
//	  ├─ superseded_pinned   superseded_by_id set, still live → archive it
//	  ├─ no_summary          pinned, summary empty (CQ4: line form = first sentence)
//	  ├─ entry_too_long      body over context.entry_max default (1,500 bytes)
//	  ├─ path_no_globs       body names a code path, rule has no globs
//	  ├─ untagged            pinned, no tag (not in any topic of the index)
//	  ├─ near_duplicate      three-word shingle overlap over 0.5 between pinned entries
//	  ├─ high_hotfix_old, not_validated (context_age.go: need the clock)
//	  └─ scope_missing, scope_conflict (context_scope.go: need the Env)

import (
	"regexp"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// Context finding codes (6.12).
const (
	SupersededPinned Code = "superseded_pinned"
	NoSummary        Code = "no_summary"
	EntryTooLong     Code = "entry_too_long"
	PathNoGlobs      Code = "path_no_globs"
	Untagged         Code = "untagged"
	NearDuplicate    Code = "near_duplicate"
)

// ContextCodes lists the --context finding codes.
func ContextCodes() []Code {
	return []Code{SupersededPinned, NoSummary, EntryTooLong, PathNoGlobs, Untagged, NearDuplicate, ScopeMissing, ScopeConflict, HighHotfixOld, NotValidated, NotUsed}
}

// Context check thresholds (6.12). entryMax is the context.entry_max
// default; the lint Env has no policy reader, so a tighter policy value is
// not seen here (render applies the real one).
const (
	entryMax       = 1500
	shingleSize    = 3
	nearDupOverlap = 0.5
)

// Fields the context checks read.
const (
	fBody       = "body"
	fSeverity   = "severity"
	fArchivedAt = "archived_at"
	fSuperseded = "superseded_by_id"
	fGlobs      = "globs"
)

// codePath matches a path-like token in prose: a/b with a file extension
// or a trailing slash ("services/billing/invoice.go", "db/migrations/").
var codePath = regexp.MustCompile(`(?:^|[\s(` + "`" + `'"])((?:[A-Za-z0-9_.-]+/)+(?:[A-Za-z0-9_-]+\.[A-Za-z0-9]+)?)`)

// pinnedRow is one row the core pins.
type pinnedRow struct {
	s    sourced
	noun string
}

// isPinned reports whether r would be pinned (before the superseded filter).
func isPinned(table row.Table, r row.Row) bool {
	if text(r, fArchivedAt) != "" {
		return false
	}
	sev := text(r, fSeverity)
	switch table {
	case schema.TableRules:
		return sev == schema.RuleSeverityMust
	case schema.TableHotfixes:
		return sev == schema.HotfixSeverityCritical || sev == schema.HotfixSeverityHigh
	}
	return false
}

// contextFindings runs the 6.12 checks over the knowledge rows.
func contextFindings(all map[row.Table][]sourced) []Finding {
	var pinned []pinnedRow
	var out []Finding
	for _, t := range []struct {
		table row.Table
		noun  string
	}{{schema.TableRules, "rule"}, {schema.TableHotfixes, "hotfix"}} {
		for _, s := range all[t.table] {
			if !isPinned(t.table, s.r) {
				continue
			}
			id := s.r.ID.String()
			if text(s.r, fSuperseded) != "" {
				out = append(out, Finding{Code: SupersededPinned, ID: id, Store: string(s.src),
					Message: "superseded by " + text(s.r, fSuperseded) + " but still pinned (render leaves it out of LORE.md)",
					Fix:     "lore archive " + id})
				continue
			}
			pinned = append(pinned, pinnedRow{s: s, noun: t.noun})
		}
	}
	for _, p := range pinned {
		id, body := p.s.r.ID.String(), text(p.s.r, fBody)
		if text(p.s.r, schema.FieldSummary) == "" && p.noun == "rule" {
			out = append(out, Finding{Code: NoSummary, ID: id, Store: string(p.s.src),
				Message: "pinned rule has no summary; its line form is the first sentence (CQ4)",
				Fix:     "lore " + p.noun + " edit --id " + id + " --summary \"...\""})
		}
		if len(body) > entryMax {
			out = append(out, Finding{Code: EntryTooLong, ID: id, Store: string(p.s.src),
				Message: "body is longer than context.entry_max (1,500 bytes), so LORE.md shows only its line",
				Fix:     "split it, or add a summary: lore " + p.noun + " edit --id " + id + " --summary \"...\""})
		}
		if p.noun == "rule" && text(p.s.r, fGlobs) == "" {
			if m := codePath.FindStringSubmatch(stripCode(body)); m != nil {
				dir := m[1]
				if i := strings.LastIndexByte(strings.TrimSuffix(dir, "/"), '/'); i >= 0 && strings.Contains(dir[i:], ".") {
					dir = dir[:i+1]
				}
				out = append(out, Finding{Code: PathNoGlobs, ID: id, Store: string(p.s.src),
					Message: "names the code path " + m[1] + " but has no globs, so it loads everywhere",
					Fix:     "lore rule edit --id " + id + " --activation glob --globs '[\"" + strings.TrimSuffix(dir, "/") + "/**\"]'"})
			}
		}
		if len(listItems(p.s.r, schema.FieldTagIDs)) == 0 {
			out = append(out, Finding{Code: Untagged, ID: id, Store: string(p.s.src),
				Message: "pinned but untagged, so it is under (no topic) in the index",
				// `lore tag add` only creates a tag (--name); the edit's --tag
				// attaches an existing one.
				Fix: "lore " + p.noun + " edit --id " + id + " --tag <tag> (create a new tag first: lore tag add --name <tag>)"})
		}
	}
	return append(out, nearDuplicates(pinned)...)
}

// nearDuplicates pairs pinned entries whose word-shingle sets overlap by
// more than nearDupOverlap (Jaccard), once per pair, in id order.
func nearDuplicates(ps []pinnedRow) []Finding {
	sort.Slice(ps, func(i, j int) bool { return ps[i].s.r.ID.String() < ps[j].s.r.ID.String() })
	sh := make([]map[string]bool, len(ps))
	for i, p := range ps {
		sh[i] = shingles(text(p.s.r, fBody))
	}
	var out []Finding
	for i := range ps {
		for j := i + 1; j < len(ps); j++ {
			if jaccard(sh[i], sh[j]) > nearDupOverlap {
				a, b := ps[i].s.r.ID.String(), ps[j].s.r.ID.String()
				out = append(out, Finding{Code: NearDuplicate, ID: a, Store: string(ps[i].s.src),
					Message: "near duplicate of " + b + " (most of the wording is shared)",
					Fix:     "keep one: lore archive " + b + " (or merge the text first)"})
			}
		}
	}
	return out
}

// shingles is the set of three-word windows of s (lower case).
func shingles(s string) map[string]bool {
	w := strings.Fields(strings.ToLower(s))
	out := map[string]bool{}
	for i := 0; i+shingleSize <= len(w); i++ {
		out[strings.Join(w[i:i+shingleSize], " ")] = true
	}
	return out
}

// jaccard is |a∩b| / |a∪b| (0 when both are empty).
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}
