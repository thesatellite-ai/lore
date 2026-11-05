package engine

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// screened is the outcome of the secret scan of one write.
type screened struct {
	fields   map[string]value.Value // redacted where the policy said so
	accepted []scan.Finding
	redacted []scan.Finding
	warnings []Warning
	// pol is the effective policy the scan read, kept for the POLICY step
	// that follows it (enforceRow), so one write reads the settings once.
	pol policy.Set
}

// screen scans the named fields of a write (text, every text item of a
// list, and the raw text of json) and applies secrets.on_detect: refuse →
// E_SECRET_DETECTED; ask → the caller confirms (E_NEEDS_CONFIRM otherwise)
// and the findings are audited as accepted; redact → each match is replaced
// with ${secret:unstored}. Rules turned off at any level and allow entries
// for rowID are honoured first (D2/D9, secret_allows).
func (e *Engine) screen(ctx context.Context, cl call, target store.Source, rowID ids.AnyID, fields map[string]value.Value, names []string) (screened, error) {
	if rs, ok := e.scanner.(RefreshingScanner); ok {
		// rules are settings rows: a rule added since the last write
		// (a long-lived `lore api`) applies to this one
		if err := rs.Refresh(ctx); err != nil {
			return screened{}, err
		}
	}
	pol, rules, err := e.settings.Policy(target)
	if err != nil {
		return screened{}, err
	}
	fields, urlWarns, err := stripURLs(fields)
	if err != nil {
		return screened{}, err
	}
	out := screened{fields: fields, pol: pol, warnings: urlWarns}
	// BATCH 8: a rule the personal store turned off covers only that
	// person's own captures, i.e. a write stamped as the engine's own actor
	// (the person whose personal store is attached). A write a surface
	// stamps as someone else (a shared `lore api`) keeps the rule.
	own := e.actorOf(cl) == e.actor
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
			if !rules.IsOff(f.RuleID, own) {
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
		return out, e.refuseClientSecretRefs(target, rowID, out.fields)
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
		return out, e.refuseClientSecretRefs(target, rowID, out.fields)
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
		// a redacted ${secret:unstored} is a secret reference too
		return out, e.refuseClientSecretRefs(target, rowID, out.fields)
	case policy.OnDetectRefuse:
	}
	return screened{}, secretErr(all)
}

// RefreshingScanner is a scanner whose rules come from settings rows: the
// engine calls Refresh before each write's scan, so rules added while a
// long-lived process runs apply to the next write. Refresh must be cheap
// when nothing changed and safe for concurrent use.
type RefreshingScanner interface {
	scan.Scanner
	Refresh(ctx context.Context) error
}

// allowReasonPrefix starts the reason of a secret_allows row a confirmed
// write records. The confirm carries no typed reason (op.Caller has none),
// so the reason is this fixed text plus the actor and the command, which
// is enough for lore doctor and the commit hook to say who accepted it.
const allowReasonPrefix = "confirmed not a secret by "

// allowRows turns the findings a caller accepted as not secrets (on_detect
// ask, then --yes or a terminal yes) into secret_allows rows for rowID
// (SQ2): one per distinct (field, rule, match hash), so scan.Filter drops
// exactly that match on every later write and the commit hook can read
// them, while a different match in the same row still asks. A zero rowID
// makes rows to be pointed at the final id later (pointAllows).
func (e *Engine) allowRows(cl call, accepted []scan.Finding, rowID ids.AnyID, actor ids.ID[ids.Actor], stamp hlc.HLC) ([]row.Row, error) {
	if len(accepted) == 0 {
		return nil, nil
	}
	reasonText := allowReasonPrefix + actor.String()
	if cl.op != "" {
		reasonText += " (" + strings.ReplaceAll(cl.op, ".", " ") + ")"
	}
	reason, err := value.TextOf(reasonText)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []row.Row
	for _, f := range accepted {
		k := f.Field + "\x00" + f.RuleID + "\x00" + strconv.FormatUint(uint64(f.MatchHash), 10)
		if seen[k] {
			continue
		}
		seen[k] = true
		id, err := e.ids.NewID(ids.PrefixSecretAllow)
		if err != nil {
			return nil, err
		}
		field, err := value.TextOf(f.Field)
		if err != nil {
			return nil, err
		}
		rule, err := value.TextOf(f.RuleID)
		if err != nil {
			return nil, err
		}
		out = append(out, row.Row{
			Table: schema.TableSecretAllows, ID: id, HLC: stamp,
			Fields: map[string]value.Value{
				allowField: field, allowRule: rule, allowHash: value.Int(int64(f.MatchHash)), allowReason: reason,
			},
			CreatedAt: stamp.Wall, CreatedBy: actor, UpdatedAt: stamp.Wall, UpdatedBy: actor,
		})
	}
	return pointAllows(out, rowID)
}

// pointAllows sets the row_id of allow rows to rowID (zero: left unset).
func pointAllows(rows []row.Row, rowID ids.AnyID) ([]row.Row, error) {
	if rowID.IsZero() {
		return rows, nil
	}
	ref, err := value.RefOf(rowID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Fields[allowRowID] = ref
	}
	return rows, nil
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

// WarnURLUserinfo is W_URL_CREDENTIALS: a URL value lost a credential before it was stored
// (D24/D25). The message names the field and the clean URL, never the
// removed text.
const WarnURLUserinfo WarnCode = "W_URL_CREDENTIALS"

// stripURLs is the write-time URL credential strip (URL CREDENTIALS FIX,
// D24, 14.2): every url-typed value (already stripped by value.URLOf)
// and every text field whose name marks a URL (repourl.URLFieldName:
// origin_url, base_url, *_url, ...) is stored through repourl.Strip, so
// no write path can store https://user:token@host. A credential warns
// (W_URL_CREDENTIALS, field + clean URL only); a plain user name is
// dropped silently (14.4); a value lore cannot read is refused
// (E_URL_CREDENTIALS). fields is copied only when something changes.
func stripURLs(fields map[string]value.Value) (map[string]value.Value, []Warning, error) {
	var warns []Warning
	var out map[string]value.Value
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	slices.Sort(names) // warnings in a stable order
	for _, name := range names {
		switch v := fields[name].(type) {
		case value.URL:
			if v.Stripped() {
				warns = append(warns, urlWarning(name, v.String()))
			}
		case value.Text:
			if !repourl.URLFieldName(name) || v.String() == "" {
				continue
			}
			clean, removed, err := repourl.Strip(v.String())
			if errors.Is(err, repourl.ErrUnreadable) {
				return nil, nil, errs.New(errs.URLUserinfo, "engine: field "+name+" cannot be read as a URL, and lore cannot tell whether it holds a credential").
					WithHint("give the address without any user name or token, e.g. https://host/path")
			}
			if err != nil || clean == v.String() {
				continue // not a URL at all: kept as written
			}
			t, terr := value.TextOf(clean)
			if terr != nil {
				return nil, nil, terr
			}
			if out == nil {
				out = make(map[string]value.Value, len(fields))
				for k, fv := range fields {
					out[k] = fv
				}
			}
			out[name] = t
			if removed.Warn() {
				warns = append(warns, urlWarning(name, clean))
			}
		}
	}
	if out == nil {
		out = fields
	}
	return out, warns, nil
}

// urlWarning is the one-line warning of 14.4 (D25: clean URL only).
func urlWarning(field, clean string) Warning {
	return Warning{Code: WarnURLUserinfo, Field: field,
		Message: "a credential was in " + field + "; lore stored the URL without it: " + clean}
}

// refuseClientSecretRefs is SQ17 / BATCH 5 / FLAG5 on a write: a row whose
// home is a client repo may hold no ${secret:…} reference (E_LEAK_BLOCKED),
// the same refusal lore move gives (refuseSecretRefs).
func (e *Engine) refuseClientSecretRefs(target store.Source, rowID ids.AnyID, fields map[string]value.Value) error {
	a, err := e.attached(target)
	if err != nil || !a.Client {
		return nil // an unknown target is refused by the write itself
	}
	return refuseSecretRefs(row.Row{ID: rowID, Fields: fields})
}

// RedactedWrite is a stored write whose scan replaced secrets with
// ${secret:unstored} (secrets.on_detect: redact): the row and the fields
// and rules that fired, never a value or a preview.
type RedactedWrite struct {
	Store  store.Source
	Table  row.Table
	ID     ids.AnyID
	Fields []string // sorted, unique
	Rules  []string // sorted, unique
}

// RedactHook runs after a redacted write is stored, cached and audited
// (D21: the "store, then rotate" task appears at write time). wire sets it
// to task.AddSecretTask; the engine cannot import ops. An error fails the
// command after the row is stored (like a failed audit record); lore
// secrets check adds the same task idempotently later.
type RedactHook func(ctx context.Context, w RedactedWrite) error

// WithRedactHook sets the hook (nil: no hook).
func WithRedactHook(h RedactHook) Option { return func(e *Engine) error { e.redact = h; return nil } }

// SetRedactHook binds the hook after New, for a composition root whose
// hook needs the object that owns the engine (wire.App). Call it before
// the engine serves any write.
func (e *Engine) SetRedactHook(h RedactHook) {
	e.redact = h
	e.audit = redactSink(e.audit, h)
}

// redactKey marks a context already inside a RedactHook, so the hook's own
// writes never call it again.
type redactKey struct{}

// redactSink wraps the audit sink: Record is the last step of every write
// (finish, after the backend and the cache), so the hook runs there for
// each create, update or move event that carries redacted findings.
//
//	finish ─▶ Record(ev) ─▶ inner.Record ─▶ ev.SecretsRedacted? ─▶ hook (once per row write)
func redactSink(inner AuditSink, h RedactHook) AuditSink {
	if r, ok := inner.(redacting); ok {
		inner = r.inner // rebinding replaces the hook, never stacks it
	}
	if h == nil {
		return inner
	}
	return redacting{inner: inner, hook: h}
}

type redacting struct {
	inner AuditSink
	hook  RedactHook
}

// Record implements AuditSink: the inner record first, then the hook.
func (r redacting) Record(ctx context.Context, ev Event) error {
	if err := r.inner.Record(ctx, ev); err != nil {
		return err
	}
	if len(ev.SecretsRedacted) == 0 || ctx.Value(redactKey{}) != nil {
		return nil
	}
	if ev.Action != ActionCreate && ev.Action != ActionUpdate {
		return nil
	}
	w := RedactedWrite{Store: ev.Store, Table: ev.Table, ID: ev.ID}
	for _, f := range ev.SecretsRedacted {
		if !slices.Contains(w.Fields, f.Field) {
			w.Fields = append(w.Fields, f.Field)
		}
		if !slices.Contains(w.Rules, f.RuleID) {
			w.Rules = append(w.Rules, f.RuleID)
		}
	}
	slices.Sort(w.Fields)
	slices.Sort(w.Rules)
	if err := r.hook(context.WithValue(ctx, redactKey{}, true), w); err != nil {
		return errs.Wrap(errs.CodeOf(err), "engine: the row is stored but its store-then-rotate task failed (lore secrets check adds it)", err)
	}
	return nil
}
