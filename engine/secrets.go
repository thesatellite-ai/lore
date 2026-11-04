package engine

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// screened is the outcome of the secret scan of one write.
type screened struct {
	fields   map[string]value.Value // redacted where the policy said so
	accepted []scan.Finding
	redacted []scan.Finding
	warnings []Warning
}

// screen scans the named fields of a write (text, every text item of a
// list, and the raw text of json) and applies secrets.on_detect: refuse →
// E_SECRET_DETECTED; ask → the caller confirms (E_NEEDS_CONFIRM otherwise)
// and the findings are audited as accepted; redact → each match is replaced
// with ${secret:unstored}. Rules turned off at any level and allow entries
// for rowID are honoured first (D2/D9, secret_allows).
func (e *Engine) screen(ctx context.Context, cl call, target store.Source, rowID ids.AnyID, fields map[string]value.Value, names []string) (screened, error) {
	out := screened{fields: fields}
	pol, rules, err := e.settings.Policy(target)
	if err != nil {
		return screened{}, err
	}
	allows, err := e.settings.Allows(target)
	if err != nil {
		return screened{}, err
	}
	type hit struct {
		field    string
		item     int // list item index; -1 for the whole field
		findings []scan.Finding
	}
	var hits []hit
	look := func(field string, item int, text string) {
		found, warn := e.scanText(field, text)
		if warn != nil {
			out.warnings = append(out.warnings, *warn)
		}
		var kept []scan.Finding
		for _, f := range found {
			if !rules.IsOff(f.RuleID, true) {
				kept = append(kept, f)
			}
		}
		if kept = scan.Filter(rowID, kept, allows); len(kept) > 0 {
			hits = append(hits, hit{field: field, item: item, findings: kept})
		}
	}
	for _, name := range names {
		switch v := fields[name].(type) {
		case value.Text:
			look(name, -1, v.String())
		case value.JSON:
			look(name, -1, v.String())
		case value.List:
			// A list read back from a file may hold json objects as items
			// (value.Infer): scan those too. Redacting one is refused
			// (redactValue), never skipped.
			for i, it := range v.Items() {
				switch t := it.(type) {
				case value.Text:
					look(name, i, t.String())
				case value.JSON:
					look(name, i, t.String())
				}
			}
		}
	}
	if len(hits) == 0 {
		return out, nil
	}
	var all []scan.Finding
	for _, h := range hits {
		all = append(all, h.findings...)
	}
	switch pol.OnDetect() {
	case policy.OnDetectAsk:
		if err := e.confirm(ctx, cl, describe(all)+": store it anyway as not a secret?"); err != nil {
			var ee *errs.Error
			if errors.As(err, &ee) && ee.Code == errs.NeedsConfirm {
				return screened{}, ee.WithHint("store it in your vault and write ${secret:…} instead, or rerun with --yes to mark it not a secret")
			}
			return screened{}, err
		}
		out.accepted = all
		return out, nil
	case policy.OnDetectRedact:
		red := make(map[string]value.Value, len(fields))
		for k, v := range fields {
			red[k] = v
		}
		for _, h := range hits {
			v, err := redactValue(red[h.field], h.item, h.findings)
			if err != nil {
				return screened{}, err
			}
			red[h.field] = v
		}
		out.fields, out.redacted = red, all
		return out, nil
	case policy.OnDetectRefuse:
	}
	return screened{}, secretErr(all)
}

// scanText runs the scanner, turning a degraded scan into a warning.
func (e *Engine) scanText(field, text string) ([]scan.Finding, *Warning) {
	if r, ok := e.scanner.(scan.Reporter); ok {
		rep := r.ScanReport(field, text)
		if rep.Degraded {
			return rep.Findings, &Warning{Code: WarnScanDegraded, Field: field, Message: rep.Note}
		}
		return rep.Findings, nil
	}
	return e.scanner.Scan(field, text), nil
}

// redactValue replaces the matches in one text (or one list item) with the
// unstored-secret reference. A json field cannot be redacted safely in
// place (a match may straddle its syntax), so it is refused instead.
func redactValue(v value.Value, item int, fs []scan.Finding) (value.Value, error) {
	switch x := v.(type) {
	case value.Text:
		return value.TextOf(redactText(x.String(), fs))
	case value.List:
		items := x.Items()
		t, ok := items[item].(value.Text)
		if !ok {
			return nil, secretErr(fs)
		}
		nt, err := value.TextOf(redactText(t.String(), fs))
		if err != nil {
			return nil, err
		}
		items[item] = nt
		return value.ListOf(x.Elem(), items...)
	}
	return nil, secretErr(fs).WithHint("a json value cannot be redacted in place; store the secret in your vault and write ${secret:…}")
}

// redactText replaces each finding's span, last first so earlier offsets
// stay valid; an overlapping span is covered by the one already replaced.
func redactText(s string, fs []scan.Finding) string {
	sorted := slices.Clone(fs)
	slices.SortFunc(sorted, func(a, b scan.Finding) int { return b.Offset - a.Offset })
	end := len(s) + 1
	for _, f := range sorted {
		lo, hi := f.Offset, f.Offset+f.Length
		if lo < 0 || hi > len(s) || hi > end {
			continue
		}
		s = s[:lo] + refs.Unstored + s[hi:]
		end = lo
	}
	return s
}

// describe names findings for a message: field, rule, line, masked preview.
func describe(fs []scan.Finding) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		parts = append(parts, f.Field+" looks like a secret ("+f.RuleID+") at line "+strconv.Itoa(f.Line)+": "+f.Preview)
	}
	return strings.Join(parts, "; ")
}

// secretErr is the refusal (policy refuse, or a value that cannot be
// redacted).
func secretErr(fs []scan.Finding) *errs.Error {
	return errs.New(errs.SecretDetected, "engine: "+describe(fs)).
		WithHint("store it in your vault and write ${secret:…} instead, or mark it not a secret: lore secrets allow …")
}
