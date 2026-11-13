package lint

import (
	"context"
	"strconv"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/logic/rowscan"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// SecretFound is a stored row whose text the current secret rules match
// (SQ12: rows are not re-scanned on show; lint and check leaks re-scan
// with the rules of today, so a rule added later finds old rows).
const SecretFound Code = "secret_found"

// HookScanning is the optional Env upgrade lint re-scans with: the team
// rule set of the hooks (*wire.App, BATCH 8: a personal "off" never
// hides a finding in shared rows).
type HookScanning interface {
	HookScanner(ctx context.Context) (scan.Scanner, error)
}

// secret_allows field names (schema.Builtin), for this package's tests.
const (
	fAllowRowID = "row_id"
	fAllowField = "field"
	fAllowRule  = "rule_id"
	fAllowHash  = "match_hash"
)

// secretFindings re-scans every live row with sc through rowscan.Scan
// (the one every-row scan, shared with `lore check leaks --rows`) and
// reports row id, field, rule and the masked preview, never the value.
func secretFindings(sc scan.Scanner, all map[row.Table][]sourced) []Finding {
	in := make(map[row.Table][]rowscan.Row, len(all))
	for t, rs := range all {
		for _, s := range rs {
			in[t] = append(in[t], rowscan.Row{Source: string(s.src), Row: s.r})
		}
	}
	var out []Finding
	for _, h := range rowscan.Scan(sc, in) {
		id, f, hit := h.ID.String(), h.Finding.Field, h.Finding
		out = append(out, Finding{Code: SecretFound, ID: id, Store: h.Source,
			Message: f + " looks like a secret (" + hit.RuleID + ") at line " + strconv.Itoa(hit.Line) + ": " + hit.Preview,
			Fix:     "move the value to your vault and write ${secret:…}: lore edit " + id + ", or if it is not a secret: " + leakcheck.AllowRowCommand(id, f, hit.RuleID, strconv.FormatUint(uint64(hit.MatchHash), 10))})
	}
	return out
}

// texts is the scannable text of one field value (rowscan.Texts).
func texts(v value.Value) []string { return rowscan.Texts(v) }

// secretScan is the SQ12 step of lint.run; an Env without HookScanning is
// an internal error, never a silent "no findings" (skeleton honesty).
func (o ops) secretScan(ctx context.Context, all map[row.Table][]sourced) ([]Finding, error) {
	hs, ok := o.env.(HookScanning)
	if !ok {
		return nil, errs.New(errs.Internal, "lint: this host has no secret scanner; the secret re-scan cannot run")
	}
	sc, err := hs.HookScanner(ctx)
	if err != nil {
		return nil, err
	}
	return secretFindings(sc, all), nil
}
