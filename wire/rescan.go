package wire

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/rowscan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// `lore check leaks --rows` re-scans every stored row with the rules of
// today (SQ12, SECRETS 3.6): a rule added after a row was written finds
// that row here, before it reaches a commit or a client. It is the same
// scan as `lore lint`'s secret_found (logic/rowscan, the one every-row
// scan) with the same rule set (HookScanner: the team rules, BATCH 8 and
// OV43-2, so a personal "off" never hides a finding in shared rows).
// Unlike lint it is a check: any finding no allow entry accepts is a
// refusal (E_LEAK_BLOCKED, exit class guard), as every leak check is.
//
//	open the App (every attached store)
//	rows    = every row of every built-in table in every store
//	hits    = rowscan.Scan(HookScanner, rows)   (allow entries applied)
//	no hits → the clean line; hits → E_LEAK_BLOCKED with one line per hit

// rescanClean is the line a clean every-row re-scan prints.
const rescanClean = "✓ lore check leaks --rows: no stored row matches a secret rule"

// RescanRows re-scans every stored row of every attached store with the
// hook rule set; it returns the clean line, or E_LEAK_BLOCKED whose
// message names each finding (store, row id, field, rule, line and the
// masked preview, never the matched text) and whose hint gives the fix.
func (a *App) RescanRows(ctx context.Context) (string, error) {
	st, err := a.state(ctx) // a Local check op has not opened the App yet
	if err != nil {
		return "", err
	}
	sc, err := a.HookScanner(ctx)
	if err != nil {
		return "", err
	}
	rows := map[row.Table][]rowscan.Row{}
	for _, def := range schema.Builtin() {
		for _, src := range st.eng.Stores() {
			rs, err := st.eng.List(ctx, src, def.Name)
			if err != nil {
				return "", err
			}
			for _, r := range rs {
				rows[def.Name] = append(rows[def.Name], rowscan.Row{Source: string(src), Row: r})
			}
		}
	}
	hits := rowscan.Scan(sc, rows)
	if len(hits) == 0 {
		return rescanClean, nil
	}
	return "", errs.New(errs.LeakBlocked, rescanReport(hits)).
		WithHint("move each value to your vault and write ${secret:…} (lore edit <id>), or if it is not a secret: lore secrets allow <id> --field <field> --rule <rule> --match-hash <hash> --reason \"<why>\"")
}

// rescanReport is the block report: a count line, then one line per hit
// with the exact allow command for it (OV40-3: --match-hash, never the
// text).
func rescanReport(hits []rowscan.Hit) string {
	var b strings.Builder
	b.WriteString("lore check leaks --rows: " + strconv.Itoa(len(hits)) + " stored value(s) match a secret rule")
	for _, h := range hits {
		f := h.Finding
		id := h.ID.String()
		b.WriteString("\n  " + h.Source + " " + string(h.Table) + " " + id + " " + f.Field + ": " + f.RuleID +
			" at line " + strconv.Itoa(f.Line) + ": " + f.Preview +
			"\n    allow: lore secrets allow " + id + " --field " + f.Field + " --rule " + f.RuleID +
			" --match-hash " + strconv.FormatUint(uint64(f.MatchHash), 10) + ` --reason "<why>"`)
	}
	return b.String()
}
