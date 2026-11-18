package profilestore

// companyrows.go — which rows of a company place only an admin may take
// out of what every repo is told, and the one check every write path of
// them runs: the one-row forms (rule/hotfix edit, archive, delete), the
// bulk --where forms and the generic row paths (lore edit, row move)
// (devco D-637, D-659: the bulk forms were not role-gated, so a member's
// `rule delete --where 'severity=must'` removed the company's must rules
// the one-row form refuses).
//
//	rows ─► CompanyPinned(row) && CompanyPlace(env, src)? ─none─► pass
//	                    │ some
//	                    ▼
//	        roster admins ─► caller admin, or no admin yet ─► pass
//	                      └─► E_REFUSED naming the rows and the admins;
//	                          nothing was written

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fieldSeverity is the severity field of rules and hotfixes.
const fieldSeverity = "severity"

// namedRows caps how many row ids a refusal names before "and N more".
const namedRows = 5

// CompanyPinned reports whether r is a row every repo reading its place
// gets in full: a must rule, a critical or high hotfix. In a company place
// (CompanyPlace) lowering, archiving, deleting or moving one is an admin's
// change; other rows, and these rows in a project folder, stay every
// member's.
func CompanyPinned(r row.Row) bool {
	sev := fieldText(r, fieldSeverity)
	switch r.Table {
	case schema.TableRules:
		return sev == schema.RuleSeverityMust
	case schema.TableHotfixes:
		return sev == schema.HotfixSeverityCritical || sev == schema.HotfixSeverityHigh
	}
	return false
}

// Placed is a row with the store it is in.
type Placed struct {
	Source store.Source
	Row    row.Row
}

// RequireAdminForRows refuses a member's write that would change any
// company pinned row of rows (CompanyPinned in a CompanyPlace): one
// E_REFUSED naming those rows and the admins, before anything is written.
// verb is what the write does ("delete", "archive"); line is the command
// an admin runs; narrow is the hint for the member's own way on ("narrow
// --where to leave them out"), "" for none. With no such row, no roster,
// or a caller who may (Admins.May) it passes, as RequireAdminAt does.
func RequireAdminForRows(ctx context.Context, env PlaceEnv, rows []Placed, verb, line, narrow string) error {
	var hit []string
	var at store.Source
	for _, p := range rows {
		if !CompanyPinned(p.Row) || !CompanyPlace(env, p.Source) {
			continue
		}
		hit = append(hit, p.Row.ID.String())
		if at == "" {
			at = p.Source
		}
	}
	if len(hit) == 0 {
		return nil
	}
	s, ok := upgrade.As[Store](env)
	if !ok {
		return nil
	}
	r, done, err := s.Roster(ctx, false)
	defer done()
	if err != nil || r == nil || r.Source() == store.SourceRepo {
		return nil
	}
	a, err := ReadAdmins(ctx, r)
	if err != nil {
		return err
	}
	if a.May() {
		return nil
	}
	hint := "ask " + a.Who() + " to run: " + line
	if narrow != "" {
		hint = narrow + "; or " + hint
	}
	return errs.New(errs.Refused, "only an admin of the org can "+verb+" "+rowList(hit)+" (a must rule or a critical/high hotfix of the company place "+string(at)+
		", which every repo of the org reads), and you are not one on "+r.Where()+"'s roster; nothing was written").WithHint(hint)
}

// rowList is "rul_a" / "rul_a, rul_b" / "rul_a, … and 3 more".
func rowList(ids []string) string {
	if len(ids) <= namedRows {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:namedRows], ", ") + " and " + strconv.Itoa(len(ids)-namedRows) + " more"
}

// fieldArchivedAt is the field an archive stamps.
const fieldArchivedAt = "archived_at"

// TakesOut reports whether patch takes a CompanyPinned row r out of what
// every repo is told: it lowers the severity below pinned or stamps
// archived_at (the generic `lore edit` write-back, devco D-659). A
// rewording or a tag passes.
func TakesOut(r row.Row, patch map[string]value.Value) bool {
	if !CompanyPinned(r) {
		return false
	}
	if v, ok := patch[fieldArchivedAt]; ok && v != nil && v.Kind() != value.KindNull {
		return true
	}
	v, ok := patch[fieldSeverity]
	if !ok {
		return false
	}
	after := row.Row{Table: r.Table, Fields: map[string]value.Value{fieldSeverity: v}}
	return !CompanyPinned(after)
}
