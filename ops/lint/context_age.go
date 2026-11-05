package lint

// Age checks of `lore lint --context` (CONTEXT_BUDGET 6.12), on the env's
// clock so a test or LORE_NOW fixes "today":
//
//	ageFindings(rows, now)
//	  ├─ high_hotfix_old   high hotfix created more than 45 days ago → medium, or a rule
//	  └─ not_validated     pinned, last validated (else created) more than 90 days ago
//
// "Not used in 60 days on this machine" is not here: it needs the
// machine-local usage record, which 0.1.18 does not write.

import (
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Age finding codes (6.12).
const (
	HighHotfixOld Code = "high_hotfix_old"
	NotValidated  Code = "not_validated"
)

// Age thresholds (6.12 table): a high hotfix that is still loud after 45
// days is either a standing rule or no longer urgent; a pinned entry
// nobody re-checked in 90 days may no longer hold.
const (
	highHotfixMaxAge = 45 * 24 * time.Hour
	validatedMaxAge  = 90 * 24 * time.Hour
	fValidated       = "last_validated_at"
	day              = 24 * time.Hour
)

// ageFindings runs the two age checks over the pinned rows.
func ageFindings(all map[row.Table][]sourced, now time.Time) []Finding {
	var out []Finding
	for _, t := range []struct {
		table row.Table
		noun  string
	}{{schema.TableRules, "rule"}, {schema.TableHotfixes, "hotfix"}} {
		for _, s := range all[t.table] {
			if !isPinned(t.table, s.r) || text(s.r, fSuperseded) != "" {
				continue // superseded rows have their own finding
			}
			id := s.r.ID.String()
			if t.table == schema.TableHotfixes && text(s.r, fSeverity) == schema.HotfixSeverityHigh &&
				!s.r.CreatedAt.IsZero() && now.Sub(s.r.CreatedAt) > highHotfixMaxAge {
				out = append(out, Finding{Code: HighHotfixOld, ID: id, Store: string(s.src),
					Message: "high hotfix is " + days(now.Sub(s.r.CreatedAt)) + " old (over 45): lower it to medium, or turn it into a rule",
					Fix:     "lore hotfix edit " + id + " --severity medium"})
			}
			checked, from := s.r.CreatedAt, "never validated, created "
			if dt, ok := s.r.Fields[fValidated].(value.DateTime); ok && !dt.Time().IsZero() {
				checked, from = dt.Time(), "last validated "
			}
			if !checked.IsZero() && now.Sub(checked) > validatedMaxAge {
				out = append(out, Finding{Code: NotValidated, ID: id, Store: string(s.src),
					Message: "pinned " + t.noun + " " + from + days(now.Sub(checked)) + " ago (over 90): check it still holds",
					Fix:     "lore show " + id + "; if it no longer holds: lore archive " + id})
			}
		}
	}
	return out
}

// days is a duration in whole days, "N days".
func days(d time.Duration) string {
	return strconv.Itoa(int(d/day)) + " days"
}
