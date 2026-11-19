package engine

// Bulk copies and the secret scan (devco D-717). `lore store adopt` wrote
// a repo's rows straight into a store folder (one rowfs batch, no engine
// write per row), so it ran no secret scan at all: --secret-unstored,
// --not-a-secret and --secret-ref did nothing and key-shaped values went
// into the store. Every op that copies rows in bulk now runs the scan of
// a normal write (detect + resolve, the same flags), in two steps:
//
//	PlanSecrets(rows)  every row scanned before anything is written
//	   no findings ─────────────────────────▶ nothing to do
//	   an answer flag given ────────────────▶ each row gets its slice of
//	                                           the answer (ForRow)
//	   on_detect redact ────────────────────▶ the per-row write redacts
//	   on_detect ask / refuse, no flag ─────▶ refused: every row id, its
//	                                           masked findings and the
//	                                           printed reruns; nothing written
//	per row: the op's own write path (a move's MoveWith screens; a rowfs
//	   batch calls ScreenCopy) answers the row as a write does.

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// SecretPlan is the outcome of a bulk copy's secret pre-scan: the rows
// that hold findings and each one's part of the caller's answer.
type SecretPlan struct {
	// Rows are the ids of the rows that hold a finding, in batch order.
	Rows []ids.AnyID
	// choice is each such row's slice of the caller's answer: the same
	// flags, and with one --secret-ref per finding, that row's refs.
	choice map[ids.AnyID]op.SecretChoice
}

// rowFindings is one row of a bulk copy and its unique findings.
type rowFindings struct {
	r    row.Row
	name string
	uniq int
	desc string
}

// PlanSecrets scans every row a bulk copy is about to write into target
// before any of it is written (D-717), with the scanner, rules, allow
// entries and policy of a normal write. It refuses the whole batch when
// a row holds a finding no flag answers and secrets.on_detect is ask or
// refuse, naming every such row (table/id, field, rule, line, masked
// preview, never the value) and the commands that resolve them: rerun
// is the command to repeat (with its --yes when it needs one); empty
// prints "re-run your own command with". With several --secret-ref
// values the batch takes one per finding, in the order the refusal
// lists them.
func (e *Engine) PlanSecrets(ctx context.Context, verb, rerun string, target store.Source, rows []row.Row) (SecretPlan, error) {
	return e.PlanSecretsNamed(ctx, verb, rerun, target, rows, nil)
}

// PlanSecretsNamed is PlanSecrets for rows that do not exist yet under
// the ids they are planned with (task import: a record of a file gets its
// task id at the write): names[id] is how the refusal names that row (the
// record's file line or external id), so the person can find it; a row
// without a name reads "table/id" as in PlanSecrets.
func (e *Engine) PlanSecretsNamed(ctx context.Context, verb, rerun string, target store.Source, rows []row.Row, names map[ids.AnyID]string) (SecretPlan, error) {
	cl := e.callOf(ctx)
	var (
		hit   []rowFindings
		total int
		mode  policy.OnDetect
		fall  = map[ids.AnyID][]int{} // finding indexes of each row, for the refs slice
	)
	var all []scanFindingRow
	for _, r := range rows {
		if len(r.Fields) == 0 {
			continue
		}
		name := names[r.ID]
		if name == "" {
			name = r.Table.String() + "/" + r.ID.String()
		}
		fnames := slices.Sorted(maps.Keys(r.Fields))
		sc, err := e.detect(ctx, cl, target, r.ID, r.Fields, fnames)
		if err != nil {
			return SecretPlan{}, errs.Prefix(verb+": "+name+"; nothing written", err)
		}
		mode = sc.pol.OnDetect()
		uniq, _ := uniqueFindings(sc.fieldFindings())
		if len(uniq) == 0 {
			continue
		}
		for range uniq {
			fall[r.ID] = append(fall[r.ID], total)
			total++
		}
		for _, f := range uniq {
			all = append(all, scanFindingRow{id: r.ID, f: f})
		}
		hit = append(hit, rowFindings{r: r, name: name, uniq: len(uniq), desc: describe(uniq)})
	}
	if len(hit) == 0 {
		return SecretPlan{}, nil
	}
	plan := SecretPlan{choice: map[ids.AnyID]op.SecretChoice{}}
	for _, h := range hit {
		plan.Rows = append(plan.Rows, h.r.ID)
	}
	ch := cl.caller.Secret
	if ch.Any() {
		if err := checkChoice(ch, total); err != nil {
			return SecretPlan{}, errs.Prefix(verb+": "+strconv.Itoa(total)+" finding(s) in "+strconv.Itoa(len(hit))+" row(s)", err)
		}
		for _, h := range hit {
			c := ch
			if len(ch.Refs) > 1 {
				idx := fall[h.r.ID]
				c.Refs = slices.Clone(ch.Refs[idx[0] : idx[len(idx)-1]+1])
			}
			plan.choice[h.r.ID] = c
		}
		return plan, nil
	}
	if mode == policy.OnDetectRedact {
		return plan, nil // each row's write redacts it, as one write does
	}
	choice := choiceRefused
	if mode == policy.OnDetectAsk {
		choice = choiceCancelled
	}
	for _, h := range hit {
		var fs = findingsOf(all, h.r.ID)
		if err := e.auditFindings(ctx, cl, h.r.ID, string(mode), choice, fs); err != nil {
			return SecretPlan{}, err
		}
	}
	return SecretPlan{}, batchSecretErr(verb, rerun, hit, total, all, mode)
}

// scanFindingRow is one unique finding of a bulk copy and its row.
type scanFindingRow struct {
	id ids.AnyID
	f  scan.Finding
}

// findingsOf is the findings of row id, in batch order.
func findingsOf(all []scanFindingRow, id ids.AnyID) []scan.Finding {
	var out []scan.Finding
	for _, x := range all {
		if x.id == id {
			out = append(out, x.f)
		}
	}
	return out
}

// batchSecretErr is the refusal of a bulk copy whose rows hold findings
// no flag answers: E_NEEDS_CONFIRM under on_detect ask, E_SECRET_DETECTED
// under refuse; one line per row and the reruns that resolve every row.
func batchSecretErr(verb, rerun string, hit []rowFindings, total int, all []scanFindingRow, mode policy.OnDetect) *errs.Error {
	var b strings.Builder
	b.WriteString(verb + ": " + strconv.Itoa(len(hit)) + " row(s) hold what looks like a secret (" + strconv.Itoa(total) + " finding(s)); nothing written")
	for _, h := range hit {
		b.WriteString("\n  " + h.name + ": " + h.desc)
	}
	fs := make([]scan.Finding, 0, len(all))
	for _, x := range all {
		fs = append(fs, x.f)
	}
	code := errs.SecretDetected
	if mode == policy.OnDetectAsk {
		code = errs.NeedsConfirm
	}
	return errs.New(code, b.String()).WithFindings(maskedFindings(fs)).WithAddArgs(askAddArgs...).
		WithHint(batchReruns(rerun, total))
}

// batchReruns is the hint of a refused bulk copy: one command per answer,
// each covering every row the refusal lists.
func batchReruns(rerun string, total int) string {
	if rerun == "" {
		return askFlagSets + " (one answer covers every row listed; --secret-ref takes one value, or one per finding in the order listed: " + strconv.Itoa(total) + ")"
	}
	refs := `--secret-ref "<manager>://<entry>"`
	if total > 1 {
		refs += " (one for all, or " + strconv.Itoa(total) + " in the order listed)"
	}
	return "store each value in your vault, then rerun with one answer for every row listed: " +
		rerun + " --secret-unstored | " + rerun + " " + refs + " | " + rerun + ` --not-a-secret --reason "<why>"`
}

// ForRow is ctx with the caller's answer narrowed to row id's part of a
// SecretPlan (its refs when the batch gave one per finding); ctx as it is
// for a row without findings.
func (e *Engine) ForRow(ctx context.Context, p SecretPlan, id ids.AnyID) context.Context {
	ch, ok := p.choice[id]
	if !ok {
		return ctx
	}
	c := e.callOf(ctx)
	c.caller.Secret = ch
	return context.WithValue(ctx, callKey{}, c)
}

// Screened is one row of a bulk copy after the secret scan: the row to
// write (fields redacted as the answer said), the secret_allows rows a
// --not-a-secret answer records beside it, and the redaction the
// store-then-rotate task needs (zero when nothing was redacted).
type Screened struct {
	Row      row.Row
	Allows   []row.Row
	Redacted RedactedWrite
}

// ScreenCopy answers the findings of one row a rowfs batch copies into
// target exactly as a write does (detect + resolve: the flags, the
// policy, the audit records, the client-repo reference check). Call it
// with ForRow's ctx after PlanSecrets passed. A row without findings
// comes back as it is.
func (e *Engine) ScreenCopy(ctx context.Context, target store.Source, r row.Row) (Screened, error) {
	if len(r.Fields) == 0 {
		return Screened{Row: r}, nil
	}
	cl := e.callOf(ctx)
	names := slices.Sorted(maps.Keys(r.Fields))
	sc, err := e.screen(ctx, cl, target, r.ID, r.Fields, names)
	if err != nil {
		return Screened{}, errs.Prefix(r.Table.String()+"/"+r.ID.String(), err)
	}
	r.Fields = sc.fields
	out := Screened{Row: r}
	if out.Allows, err = e.allowRows(cl, sc.accepted, r.ID, e.actorOf(cl), e.clock.Now(r.HLC)); err != nil {
		return Screened{}, err
	}
	if len(sc.redacted) > 0 {
		w := RedactedWrite{Store: target, Table: r.Table, ID: r.ID}
		for _, f := range sc.redacted {
			if !slices.Contains(w.Fields, f.Field) {
				w.Fields = append(w.Fields, f.Field)
			}
			if !slices.Contains(w.Rules, f.RuleID) {
				w.Rules = append(w.Rules, f.RuleID)
			}
		}
		slices.Sort(w.Fields)
		slices.Sort(w.Rules)
		out.Redacted = w
	}
	return out, nil
}

// RedactTasks runs the store-then-rotate hook (D21) for rows a bulk copy
// redacted and wrote outside the engine's write path, as the audit sink
// does after a normal redacted write. No hook, or a ctx that asked for
// none (WithoutRedactTask): nothing.
func (e *Engine) RedactTasks(ctx context.Context, ws []RedactedWrite) error {
	if e.redact == nil || ctx.Value(redactKey{}) != nil {
		return nil
	}
	hctx := context.WithValue(ctx, redactKey{}, true)
	for _, w := range ws {
		if w.ID.IsZero() {
			continue
		}
		if err := e.redact(hctx, w); err != nil {
			return errs.Prefix("the rows are stored but a store-then-rotate task failed (lore secrets check adds it)", err)
		}
	}
	return nil
}
