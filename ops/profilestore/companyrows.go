package profilestore

// companyrows.go — which rows of a company place only an admin may take
// out of what every repo is told, and the one check every write path of
// them runs: the one-row forms (rule/hotfix edit, archive, delete), the
// bulk --where forms and the generic row paths (lore edit, row move)
// (devco D-637, D-659: the bulk forms were not role-gated, so a member's
// `rule delete --where 'severity=must'` removed the company's must rules
// the one-row form refuses).
//
//	rows ─► CompanyPinned(row) && CompanyGround(src)? ─none─► pass
//	                    │ some
//	                    ▼
//	        roster admins ─► caller admin, or no admin yet ─► pass
//	                      └─► E_REFUSED naming the rows and the admins;
//	                          nothing was written

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fieldSeverity is the severity field of rules and hotfixes.
const fieldSeverity = "severity"

// namedRows caps how many row ids a refusal names before "and N more".
const namedRows = 5

// CompanyPinned reports whether r is a row every repo reading its place
// gets in full: a must rule, a critical or high hotfix. On company ground
// (CompanyGround: the org folder, a store root, every project folder of
// the company store, devco D-681) lowering, archiving, deleting, pinning
// or moving one is an admin's change; other rows stay every member's.
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
// company pinned row of rows (CompanyPinned on CompanyGround): one
// E_REFUSED naming those rows and the admins, before anything is written.
// verb is what the write does ("delete", "archive"); line is the command
// an admin runs; narrow is the hint for the member's own way on ("narrow
// --where to leave them out"), "" for none. With no such row, no roster,
// or a caller who may (Admins.May) it passes, as RequireAdminAt does.
func RequireAdminForRows(ctx context.Context, env PlaceEnv, rows []Placed, verb, line, narrow string) error {
	type cand struct {
		id  string
		src store.Source
		lv  policy.Level
	}
	var cands []cand
	for _, p := range rows {
		if !CompanyPinned(p.Row) {
			continue
		}
		src, lv := placeOf(ctx, env, p.Source)
		if mayBeGround(src, lv) {
			cands = append(cands, cand{id: p.Row.ID.String(), src: src, lv: lv})
		}
	}
	// each row is judged by its own store's roster (openRoster, devco
	// D-681): a batch can span the company store and the ops store, whose
	// admins differ; the first roster that refuses answers
	byOrg := map[string][]cand{}
	var orgs []string
	for _, c := range cands {
		o := orgOf(c.src)
		if _, seen := byOrg[o]; !seen {
			orgs = append(orgs, o)
		}
		byOrg[o] = append(byOrg[o], c)
	}
	for _, o := range orgs {
		group := byOrg[o]
		if err := requireAdminForGroup(ctx, env, group[0].src, verb, line, narrow, func(org string) ([]string, store.Source) {
			var hit []string
			var at store.Source
			for _, c := range group {
				if !CompanyGround(c.lv, c.src, org) {
					continue
				}
				hit = append(hit, c.id)
				if at == "" {
					at = c.src
				}
			}
			return hit, at
		}); err != nil {
			return err
		}
	}
	return nil
}

// requireAdminForGroup is RequireAdminForRows for rows of one store: the
// roster src is judged by (openRoster), the rows that are company ground
// of its org (ground), and the refusal when its caller may not.
func requireAdminForGroup(ctx context.Context, env PlaceEnv, src store.Source, verb, line, narrow string, ground func(org string) ([]string, store.Source)) error {
	r, done, ok := openRoster(ctx, env, src)
	defer done()
	if !ok {
		return nil
	}
	hit, at := ground(orgOf(r.Source()))
	if len(hit) == 0 {
		return nil
	}
	a, err := ReadAdmins(ctx, r)
	if err != nil {
		return err
	}
	if a.May() {
		return nil
	}
	err = refuseMember(r, a, verb+" "+rowList(hit)+" (a must rule or a critical/high hotfix of the company folder "+string(at)+", which every repo reading it is told)", line)
	if narrow != "" {
		var e *errs.Error
		if errors.As(err, &e) {
			return e.WithHint(narrow + "; or " + e.Hint)
		}
	}
	return err
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
