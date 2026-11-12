package expand

import (
	"context"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/resmask"
	"github.com/thesatellite-ai/lore/ops/internal/reveal"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Env is the env upgrade --expand needs (wire.App: the cache's resolver,
// the write scanner for the output (13.2) and the table definitions for
// the sensitive flag). A noun env without it answers E_UNSUPPORTED.
type Env interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	RefResolver(ctx context.Context) (refs.Resolver, error)
	OutputScanner(ctx context.Context) (scan.Scanner, error)
	TableSchema(ctx context.Context, table row.Table) (schema.TableDef, bool)
}

// Flag is the --expand flag, embedded in each show input.
type Flag struct {
	Expand bool `json:"expand,omitempty" flag:"expand" desc:"fill in ${var:…}, ${res:…}, ${row:…} and ${env:…} references; secrets stay masked"`
}

// RevealFlag is --reveal / --force of a show that can reveal (2.7, SQ10):
// embedded only in the shows that call Reveal, so no show offers a flag it
// ignores.
type RevealFlag struct {
	Reveal bool `json:"reveal,omitempty" flag:"reveal" desc:"--expand and also read each ${secret:…} from its manager and print it in clear (asks every time; audited)"`
	Force  bool `json:"force,omitempty" flag:"force" desc:"with --reveal: reveal without a person's answer (agents; audited as FORCED)"`
}

// Block is what --expand adds to a show output (SECRETS 12: "--json adds
// expanded and refs"): each text field it changed, filled in, and every
// reference met with its value or why it stayed as written.
type Block struct {
	Expanded map[string]string
	Refs     []refpoint.Used
	RevealOut
}

// RevealOut is what --reveal adds to a show (2.7: "revealed": n,
// "forced", "warning" W_REVEAL_FORCED). Op outputs cannot embed a struct
// (op/fields.go), so each show output repeats the three fields.
type RevealOut struct {
	Revealed int    `json:"revealed,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// WarningText is the W_REVEAL_FORCED line of a show's human form ("" when
// the reveal was not forced).
func WarningText(warning string) string {
	if warning == "" {
		return ""
	}
	return warning + "\n"
}

// RevealConfirm is the confirm rule of a show with --reveal: nothing is
// asked up front, but the reveal answers E_NEEDS_CONFIRM (SQ10), so
// surfaces offer --yes.
var RevealConfirm = op.ConfirmRule{MayAsk: true}

// RevealErrors adds the codes --reveal can return to a show's codes.
func RevealErrors(base []errs.Code) []errs.Code {
	out := slices.Clone(base)
	for _, c := range []errs.Code{errs.InvalidInput, errs.NeedsConfirm, errs.RefScheme, errs.VaultEntryUnavailable, errs.Internal} {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// Unsupported is the refusal for an env that cannot resolve references.
func Unsupported() error {
	return errs.New(errs.Unsupported, "show --expand: this build's store wiring cannot resolve references")
}

// Show reads the row id names (a noun show already answered for it) and
// expands it; env is nil when the caller's env does not implement Env.
func Show(ctx context.Context, env Env, id string) (Block, error) {
	return ShowWith(ctx, env, id, RevealFlag{})
}

// ShowWith is Show with --reveal / --force: a noun show that embeds
// RevealFlag calls it when --expand or --reveal is set (--force alone is
// refused).
func ShowWith(ctx context.Context, env Env, id string, rf RevealFlag) (Block, error) {
	if rf.Force && !rf.Reveal {
		return Block{}, errs.New(errs.InvalidInput, "--force goes with --reveal")
	}
	if env == nil {
		return Block{}, Unsupported()
	}
	aid, err := ids.ParseAny(strings.TrimSpace(id))
	if err != nil {
		return Block{}, errs.Wrap(errs.InvalidID, id+" is not a row id", err)
	}
	_, r, err := env.Get(ctx, engine.Ref{ID: aid})
	if err != nil {
		return Block{}, err
	}
	if rf.Reveal {
		return Reveal(ctx, env, r, rf.Force)
	}
	return Row(ctx, env, r)
}

// Row expands r's non-sensitive text fields at the show point. env is
// nil when the caller's env does not implement Env.
func Row(ctx context.Context, env Env, r row.Row) (Block, error) {
	return expandRow(ctx, env, r, nil, resourceKeyPrefix)
}

// Resource is --expand of `lore resource show`: only resource row r's
// text values, keyed by the kind field's name (the generic Row keys them
// resourceKeyPrefix + name beside the row's own fields).
func Resource(ctx context.Context, env Env, r row.Row) (Block, error) {
	var only []textField
	for _, tf := range texts(ctx, env, r, "") {
		if tf.resource {
			only = append(only, tf)
		}
	}
	return expandTexts(ctx, env, r, only, nil)
}

// resourceKeyPrefix keys a resource's own text value in the generic
// show's Expanded ("fields.<name>"): its values live inside the row's
// "fields" JSON, so a bare name could collide with a row field.
const resourceKeyPrefix = resmask.FieldsField + "."

// textField is one text --expand may fill in: a row field, or a resource
// value (inside the "fields" JSON; RES 3.1), by the key Expanded uses.
type textField struct {
	key, text string
	resource  bool
}

// texts lists r's expandable texts in key order: its non-sensitive text
// fields and, for a resource row, its non-sensitive text values keyed
// prefix + field name (resmask.Texts).
func texts(ctx context.Context, env Env, r row.Row, prefix string) []textField {
	def, _ := env.TableSchema(ctx, r.Table)
	var out []textField
	for _, k := range slices.Sorted(maps.Keys(r.Fields)) {
		if t, ok := expandable(def, k, r.Fields[k]); ok {
			out = append(out, textField{key: k, text: t})
		}
	}
	if vals, ok := resmask.Texts(ctx, env, r); ok {
		var res []textField
		for _, v := range vals {
			res = append(res, textField{key: prefix + v.Name, text: v.Text, resource: true})
		}
		slices.SortFunc(res, func(a, b textField) int { return strings.Compare(a.key, b.key) })
		out = append(out, res...)
	}
	return out
}

// Reveal is show --reveal (2.7, D18, SQ10): --expand, and each manager
// secret reference in r's non-sensitive text fields read through
// ops/internal/reveal and printed in clear.
//
//	collect ${secret:…} targets ─▶ none: plain --expand
//	reveal.Ask (--yes | --force | agent_reveal allow; else E_NEEDS_CONFIRM)
//	env is a reveal.Auditor? no ─▶ refused (never unaudited)
//	reveal.Group per reference (alternates in order, each audited)
//	expand with each revealed reference held by a placeholder, then filled
//
// Fixed targets (unstored, redacted) stay masked. A secret brought in by a
// var/res value stays masked (only references written in the row reveal).
func Reveal(ctx context.Context, env Env, r row.Row, force bool) (Block, error) {
	if env == nil {
		return Block{}, Unsupported()
	}
	var raws []string
	var groups [][]reveal.Target
	for _, tf := range texts(ctx, env, r, resourceKeyPrefix) {
		for _, p := range refs.Parse(tf.text) {
			if p.Kind != refs.PartRef || p.Ref.Scheme != refs.SchemeSecret || slices.Contains(raws, p.Raw) {
				continue
			}
			var grp []reveal.Target
			for _, at := range p.Ref.Alternates {
				if tg, ok := reveal.Of(at); ok {
					grp = append(grp, tg)
				}
			}
			if len(grp) > 0 {
				raws, groups = append(raws, p.Raw), append(groups, grp)
			}
		}
	}
	if len(groups) == 0 {
		return expandRow(ctx, env, r, nil, resourceKeyPrefix)
	}
	pol, _ := env.(bulk.Policier)
	g, err := reveal.Ask(ctx, pol, groups, force)
	if err != nil {
		return Block{}, err
	}
	auditor, ok := env.(reveal.Auditor)
	if !ok {
		return Block{}, errs.New(errs.Internal, "this surface cannot audit a reveal, so lore does not reveal here")
	}
	src, _ := env.(reveal.ManagerSource)
	set, err := reveal.Managers(ctx, src)
	if err != nil {
		return Block{}, err
	}
	got := map[string]reveal.One{}
	for i, grp := range groups {
		one, rerr := reveal.Group(ctx, set, auditor, grp, g, cmdShow, r.ID.String())
		if rerr != nil {
			return Block{}, rerr
		}
		got[raws[i]] = one
	}
	b, err := expandRow(ctx, env, r, got, resourceKeyPrefix)
	if err != nil {
		return Block{}, err
	}
	b.Revealed, b.Forced = len(got), g.Forced
	if g.Forced {
		b.Warning = reveal.Warning
	}
	return b, nil
}

// cmdShow names this surface in the secret.revealed audit record.
const cmdShow = "show"

// expandable is a field's text when --expand may touch it: text, and not
// sensitive (RQ1: a sensitive value is hidden, never expanded).
func expandable(def schema.TableDef, k string, v value.Value) (string, bool) {
	t, isText := v.(value.Text)
	if f, ok := def.Field(k); !isText || (ok && f.Sensitive) {
		return "", false
	}
	return t.String(), true
}

// placeholder holds revealed reference i's place through refpoint (which
// masks every ${secret:…}); NUL bytes never occur in stored text.
func placeholder(i int) string { return "\x00lore-reveal-" + strconv.Itoa(i) + "\x00" }

// keyFlags is the key registry's sensitive flag by key id, for ${meta:…}
// (METADATA 3.20 "one masker": entry label OR registry). A key row that
// does not exist leaves the entry label to decide; any other lookup error
// masks (fail closed: a value that cannot be cleared is not printed).
func keyFlags(ctx context.Context, env Env) refpoint.KeyFlags {
	return refpoint.KeyFlagsFunc(func(id string) bool {
		kid, err := ids.Parse[ids.MetaKey](id)
		if err != nil {
			return true
		}
		_, k, err := env.Get(ctx, engine.Ref{Table: schema.TableMetaKeys, ID: kid.Any()})
		if errs.Has(err, errs.NotFound) {
			return false
		}
		if err != nil {
			return true
		}
		b, ok := k.Fields["sensitive"].(value.Bool)
		return ok && b.Bool()
	})
}

// expandRow is --expand of r; revealed maps a secret reference's raw text
// to its revealed value (nil: nothing revealed, every secret masked).
func expandRow(ctx context.Context, env Env, r row.Row, revealed map[string]reveal.One, prefix string) (Block, error) {
	if env == nil {
		return Block{}, Unsupported()
	}
	return expandTexts(ctx, env, r, texts(ctx, env, r, prefix), revealed)
}

// expandTexts fills in each of tfs at the show point.
func expandTexts(ctx context.Context, env Env, r row.Row, tfs []textField, revealed map[string]reveal.One) (Block, error) {
	var b Block
	if env == nil {
		return b, Unsupported()
	}
	rv, err := env.RefResolver(ctx)
	if err != nil {
		return b, err
	}
	sc, err := env.OutputScanner(ctx)
	if err != nil {
		return b, err
	}
	opt := refpoint.Options{Point: refpoint.Show, Resolver: rv, Scanner: sc, Env: os.LookupEnv,
		Meta: refpoint.MetaOfWith(r, keyFlags(ctx, env))} // ${meta:…}: the row's own metadata (OV13a), registry-masked (3.20)
	order := slices.Sorted(maps.Keys(revealed))
	for _, tf := range tfs {
		k, stored := tf.key, tf.text
		in := stored
		if len(revealed) > 0 {
			in = hold(stored, order)
		}
		res, err := refpoint.Expand(opt, k, in)
		if err != nil {
			return b, err
		}
		for i, raw := range order {
			if strings.Contains(res.Text, placeholder(i)) {
				res.Text = strings.ReplaceAll(res.Text, placeholder(i), revealed[raw].Value)
				res.Refs = append(res.Refs, refpoint.Used{Ref: raw, Source: revealed[raw].Answered, State: refpoint.StateExpanded, Note: "revealed (audited)"})
			}
		}
		for _, u := range res.Refs {
			if !slices.ContainsFunc(b.Refs, func(v refpoint.Used) bool { return v.Ref == u.Ref }) {
				b.Refs = append(b.Refs, u)
			}
		}
		if res.Text != stored {
			if b.Expanded == nil {
				b.Expanded = map[string]string{}
			}
			b.Expanded[k] = res.Text
		}
	}
	return b, nil
}

// hold replaces each revealed secret reference of s (by raw text, order
// giving its index) with its placeholder.
func hold(s string, order []string) string {
	var out strings.Builder
	for _, p := range refs.Parse(s) {
		if i := slices.Index(order, p.Raw); p.Kind == refs.PartRef && i >= 0 {
			out.WriteString(placeholder(i))
			continue
		}
		out.WriteString(p.Raw)
	}
	return out.String()
}

// Text is the human block a noun show appends under --expand: the
// changed fields filled in, then the references ("" when nothing).
func Text(expanded map[string]string, used []refpoint.Used) string {
	var b strings.Builder
	if len(expanded) > 0 {
		b.WriteString("expanded:\n")
		for _, k := range slices.Sorted(maps.Keys(expanded)) {
			b.WriteString("  " + k + ": " + expanded[k] + "\n")
		}
	}
	b.WriteString(RefsText(used))
	return b.String()
}

// RefsText is the "references:" block ("" when none).
func RefsText(used []refpoint.Used) string {
	if len(used) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("references:\n")
	for _, u := range used {
		b.WriteString("  " + u.Ref + "  " + RefLine(u) + "\n")
	}
	return b.String()
}

// RefLine is one reference's state as show --expand prints it.
func RefLine(u refpoint.Used) string {
	parts := []string{}
	if u.State == refpoint.StateExpanded {
		parts = append(parts, u.Value)
	} else {
		parts = append(parts, string(u.State))
	}
	if u.Source != "" {
		parts = append(parts, "("+u.Source+")")
	}
	if u.Note != "" {
		parts = append(parts, u.Note)
	}
	return strings.Join(parts, "  ")
}
