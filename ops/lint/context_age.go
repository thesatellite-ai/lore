package lint

// Age checks of `lore lint --context` (CONTEXT_BUDGET 6.12), on the env's
// clock so a test or LORE_NOW fixes "today":
//
//	ageFindings(rows, now)
//	  ├─ high_hotfix_old   high hotfix created more than 45 days ago → medium, or a rule
//	  ├─ not_validated     pinned, last validated (else created) more than 90 days ago
//	  └─ not_used          pinned, last printed by `lore context` here over 60 days ago
//	                       (machine-local row_usage; LocalStater upgrade)

import (
	"context"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store/localstate"
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

// NotUsed is the "not used in 60 days on this machine" finding (6.12).
const NotUsed Code = "not_used"

// usedMaxAge: a pinned entry no `lore context` call printed for 60 days.
const usedMaxAge = 60 * 24 * time.Hour

// LocalStater is the optional Env upgrade that opens this machine's local
// state (wire.App.LocalState); without it the not_used check is skipped.
type LocalStater interface {
	LocalState(ctx context.Context) (localstate.Store, error)
}

// rowUsage mirrors the doc `lore context` writes per row id
// (saas/cmd/cli/context_usage.go); only last_used is read here.
type rowUsage struct {
	LastUsed time.Time `json:"last_used"`
}

// usageFindings flags pinned rows whose last recorded use on this machine
// is older than 60 days. DECISION FA-CTX-4: a row with no record is never
// flagged (a fresh machine, or usage recording newer than the row, would
// otherwise flag every entry).
func (o ops) usageFindings(ctx context.Context, all map[row.Table][]sourced, now time.Time) []Finding {
	ls, ok := upgrade.As[LocalStater](o.env)
	if !ok {
		return nil
	}
	s, err := ls.LocalState(ctx)
	if err != nil {
		return nil // read-only suggestions: no local state, no finding
	}
	defer func() { _ = s.Close() }() // read-only handle; a close error changes no finding
	var out []Finding
	for _, table := range []row.Table{schema.TableRules, schema.TableHotfixes} {
		for _, sr := range all[table] {
			if !isPinned(table, sr.r) || text(sr.r, fSuperseded) != "" {
				continue
			}
			id := sr.r.ID.String()
			u, found, gerr := localstate.GetJSON[rowUsage](ctx, s, localstate.Usage, id)
			if gerr != nil || !found || u.LastUsed.IsZero() || now.Sub(u.LastUsed) <= usedMaxAge {
				continue
			}
			out = append(out, Finding{Code: NotUsed, ID: id, Store: string(sr.src),
				Message: "pinned, but no `lore context` on this machine printed it in " + days(now.Sub(u.LastUsed)) + " (over 60)",
				Fix:     "lore show " + id + "; if nobody needs it pinned, lower its severity or archive it"})
		}
	}
	return out
}
