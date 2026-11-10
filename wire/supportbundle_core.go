package wire

// supportbundle_core.go — the core's own `lore support-bundle` host
// (ops/supportbundle.Host) for a binary that supplies none (cmd/lore;
// the old binary keeps its lore.db one, saas/cmd/cli/support_bundle.go,
// whose behaviour this ports). App.SupportHost falls back to it.
//
//	Version   ─ Options.Version
//	Identity  ─ resolveIdentity (the chain every core write uses)
//	Doctor    ─ doctor.run over this App (never --fix) ─▶ maskBundleJSON
//	Counts    ─ live rows of the v0.1.17 tables in every attached store,
//	            audit chain entries, this machine's render records
//	Sessions  ─ the newest machine-store sessions, stripped (AGENT_IDENTITY 9)
//
// Invariant: no secret value leaves this file. The doctor report is the
// only part that carries free text (warning lines can name a remote or a
// config value), so every string in it is masked: URL credentials are
// cut (logic/repourl) and every gitleaks default-rule match is replaced
// by its safe preview (scan.Mask), the same preview a leak refusal prints.
// Every part degrades to empty, never an error, so a bundle is produced
// outside a project too, where it is needed most.

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/row"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opssupportbundle "github.com/thesatellite-ai/lore/ops/supportbundle"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// supportSessionLimit is how many sessions a bundle describes (v0.1.17).
const supportSessionLimit = 10

// Count keys of the bundle that are not a table name (v0.1.17's).
const (
	countAuditRows  = "audit_log_rows"
	countRenderRows = "render_history_rows"
)

// supportCoreTables are the tables whose live rows a bundle counts (the
// v0.1.17 bundle's set).
var supportCoreTables = []string{schema.TableMemories, schema.TableRules, schema.TableHotfixes, schema.TableDecisions, schema.TableSnapshots}

// maskField is the field name the scanner reports (never printed).
const maskField = "doctor"

// maskedSecret replaces a doctor string the scanner cannot be built for:
// failing closed, the string is dropped rather than printed unscanned.
const maskedSecret = "<masked>"

// urlUserinfo matches the user part of a URL inside free text
// (a scheme, a user part, an at sign, then the host); repourl.StripCredentials cleans each hit.
var urlUserinfo = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s/@"]+@[^\s"]+`)

// coreSupportBundle implements opssupportbundle.Host over an App.
type coreSupportBundle struct {
	a *App
}

var _ opssupportbundle.Host = coreSupportBundle{}

// Version implements opssupportbundle.Host.
func (s coreSupportBundle) Version() string { return s.a.opts.Version }

// Identity implements opssupportbundle.Host: the identity a write would
// use (empty when it cannot resolve, as v0.1.17 printed an empty key).
func (s coreSupportBundle) Identity(ctx context.Context) opssupportbundle.Identity {
	r, err := resolveIdentity(ctx, s.a.opts)
	if err != nil {
		return opssupportbundle.Identity{}
	}
	return opssupportbundle.Identity{Resolved: r.StableKey, Source: string(r.Step), Stable: r.Stable()}
}

// Doctor implements opssupportbundle.Host: `lore doctor --json`'s report
// (never --fix: a bundle changes nothing), masked; null when it cannot run.
func (s coreSupportBundle) Doctor(ctx context.Context) opssupportbundle.RawJSON {
	rep, err := opsdoctor.Run(ctx, s.a, opsdoctor.RunIn{})
	if err != nil {
		return nil
	}
	data, err := json.Marshal(rep)
	if err != nil {
		return nil
	}
	return maskBundleJSON(data)
}

// Counts implements opssupportbundle.Host.
func (s coreSupportBundle) Counts(ctx context.Context) opssupportbundle.Counts {
	counts := opssupportbundle.Counts{}
	if _, err := s.a.Engine(ctx); err != nil {
		return counts // not a lore project: nothing to count
	}
	for _, table := range supportCoreTables {
		if n, ok := s.liveRows(ctx, s.a.Stores(), table); ok {
			counts[table] = n
		}
	}
	if recs, err := s.a.AuditLog(ctx, 0); err == nil {
		counts[countAuditRows] = len(recs)
	}
	if n, ok := s.liveRows(ctx, []store.Source{store.SourceMachine}, schema.TableRenderHistories); ok {
		counts[countRenderRows] = n
	}
	return counts
}

// liveRows counts table's rows that are not deleted in sources; ok is
// false when any source cannot be read (a partial count would mislead).
func (s coreSupportBundle) liveRows(ctx context.Context, sources []store.Source, table string) (int, bool) {
	n := 0
	for _, src := range sources {
		rows, err := s.a.List(ctx, src, row.Table(table))
		if err != nil {
			return 0, false
		}
		for _, r := range rows {
			if !r.Deleted {
				n++
			}
		}
	}
	return n, true
}

// Sessions implements opssupportbundle.Host: the newest sessions with the
// tool session id, model, cwd and CI stripped to "present or not".
func (s coreSupportBundle) Sessions(ctx context.Context) []opssupportbundle.Session {
	out := []opssupportbundle.Session{}
	rows, err := s.a.Sessions(ctx, supportSessionLimit)
	if err != nil {
		return out
	}
	for _, r := range rows {
		out = append(out, opssupportbundle.Session{Tool: r.Tool, ToolVersion: r.ToolVersion, Signal: r.Signal, Flags: slices.Clone(r.Flags),
			DeclaredHuman: r.DeclaredHuman, HasToolSessionID: r.ToolSessionID != "", HasModel: r.Model != "",
			HasCwd: r.Cwd != "", HasCI: r.CI != ""})
	}
	return out
}

// textMasker masks free text: URL credentials cut, every default-rule
// secret match replaced by its safe preview. sc is nil when the scanner
// could not be built: every string is then replaced whole (fail closed).
type textMasker struct {
	sc *gitleaks.Scanner
}

// newTextMasker builds the masker over the gitleaks default rules.
func newTextMasker() textMasker {
	rules, err := gitleaks.Defaults()
	if err != nil {
		return textMasker{}
	}
	sc, err := gitleaks.New(rules)
	if err != nil {
		return textMasker{}
	}
	return textMasker{sc: sc}
}

// maskBundleJSON masks every string of a JSON document (object keys
// included), keeping its shape. A document that is not JSON is dropped
// (null): it cannot be masked string by string.
func maskBundleJSON(data []byte) opssupportbundle.RawJSON {
	out, ok := newTextMasker().value(data)
	if !ok {
		return nil
	}
	return opssupportbundle.RawJSON(out)
}

// value masks one JSON value; ok is false when it does not parse.
func (m textMasker) value(data []byte) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, false
	}
	switch trimmed[0] {
	case '{':
		var obj map[string]json.RawMessage
		if json.Unmarshal(data, &obj) != nil {
			return nil, false
		}
		masked := make(map[string]json.RawMessage, len(obj))
		for k, v := range obj {
			mv, ok := m.value(v)
			if !ok {
				return nil, false
			}
			masked[m.text(k)] = mv
		}
		return marshalRaw(masked)
	case '[':
		var arr []json.RawMessage
		if json.Unmarshal(data, &arr) != nil {
			return nil, false
		}
		for i, v := range arr {
			mv, ok := m.value(v)
			if !ok {
				return nil, false
			}
			arr[i] = mv
		}
		return marshalRaw(arr)
	case '"':
		var str string
		if json.Unmarshal(data, &str) != nil {
			return nil, false
		}
		return marshalRaw(m.text(str))
	}
	// a number, true, false or null carries no text
	if !json.Valid([]byte(trimmed)) {
		return nil, false
	}
	return json.RawMessage(trimmed), true
}

// marshalRaw encodes v (a masked map, slice or string) as a raw message.
func marshalRaw[T map[string]json.RawMessage | []json.RawMessage | string](v T) (json.RawMessage, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return b, true
}

// span is a byte range [lo, hi) of a text.
type span struct{ lo, hi int }

// text masks one string.
func (m textMasker) text(text string) string {
	text = urlUserinfo.ReplaceAllStringFunc(text, func(u string) string {
		if clean, _, err := repourl.StripCredentials(u); err == nil {
			return clean
		}
		return maskedSecret
	})
	if m.sc == nil {
		return maskedSecret
	}
	found := m.sc.Scan(maskField, text)
	if len(found) == 0 {
		return text
	}
	// merge overlapping matches, so no part of any match survives
	spans := make([]span, 0, len(found))
	for _, f := range found {
		lo, hi := max(f.Offset, 0), min(f.Offset+f.Length, len(text))
		if lo < hi {
			spans = append(spans, span{lo, hi})
		}
	}
	slices.SortFunc(spans, func(x, y span) int { return x.lo - y.lo })
	var b strings.Builder
	pos := 0
	for i := 0; i < len(spans); {
		cur := spans[i]
		for i++; i < len(spans) && spans[i].lo < cur.hi; i++ {
			cur.hi = max(cur.hi, spans[i].hi)
		}
		lo := max(cur.lo, pos)
		b.WriteString(text[pos:lo])
		if lo < cur.hi {
			b.WriteString(scan.Mask(text[lo:cur.hi]))
		}
		pos = max(pos, cur.hi)
	}
	b.WriteString(text[pos:])
	return b.String()
}
