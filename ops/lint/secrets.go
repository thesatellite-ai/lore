package lint

import (
	"context"
	"maps"
	"math"
	"slices"
	"strconv"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
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

// secret_allows field names (schema.Builtin).
const (
	fAllowRowID = "row_id"
	fAllowField = "field"
	fAllowRule  = "rule_id"
	fAllowHash  = "match_hash"
)

// secretFindings re-scans every text field (text, json, each text item of
// a list) of every live row with sc, drops what a secret_allows row
// accepts, and reports row id, field, rule and the masked preview, never
// the value. secret_rules rows are skipped: they hold patterns, not values.
// Flow: allows from the secret_allows rows → per row × field: sc.Scan →
// scan.Filter(row id) → one finding per match.
func secretFindings(sc scan.Scanner, all map[row.Table][]sourced) []Finding {
	allows := allowsOf(all[schema.TableSecretAllows])
	var out []Finding
	for _, t := range slices.Sorted(maps.Keys(all)) {
		if t == schema.TableSecretRules {
			continue
		}
		for _, s := range all[t] {
			for _, f := range slices.Sorted(maps.Keys(s.r.Fields)) {
				for _, txt := range texts(s.r.Fields[f]) {
					for _, hit := range scan.Filter(s.r.ID, sc.Scan(f, txt), allows) {
						id := s.r.ID.String()
						out = append(out, Finding{Code: SecretFound, ID: id, Store: string(s.src),
							Message: f + " looks like a secret (" + hit.RuleID + ") at line " + strconv.Itoa(hit.Line) + ": " + hit.Preview,
							Fix:     "move the value to your vault and write ${secret:…}: lore edit " + id + ", or if it is not a secret: lore secrets allow " + id + " --field " + f + " --rule " + hit.RuleID + " --match-hash " + strconv.FormatUint(uint64(hit.MatchHash), 10) + ` --reason "<why>"`})
					}
				}
			}
		}
	}
	return out
}

// texts is the scannable text of one field value.
func texts(v value.Value) []string {
	switch x := v.(type) {
	case value.Text:
		return []string{x.String()}
	case value.JSON:
		return []string{x.String()}
	case value.List:
		var out []string
		for _, it := range x.Items() {
			out = append(out, texts(it)...)
		}
		return out
	}
	return nil
}

// allowsOf reads the secret_allows rows; an incomplete or malformed one
// allows nothing (fail closed: the finding stays).
func allowsOf(rs []sourced) []scan.Allow {
	var out []scan.Allow
	for _, s := range rs {
		target, err := ids.ParseAny(text(s.r, fAllowRowID))
		if err != nil {
			continue
		}
		n, ok := s.r.Fields[fAllowHash].(value.Number)
		if !ok {
			continue
		}
		h, err := n.Int64()
		if err != nil || h < 0 || h > math.MaxUint32 {
			continue
		}
		out = append(out, scan.Allow{RowID: target, Field: text(s.r, fAllowField), RuleID: text(s.r, fAllowRule), MatchHash: uint32(h)})
	}
	return out
}

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
