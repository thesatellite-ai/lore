package pin

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	kn "github.com/thesatellite-ai/lore/ops/internal/knowledge"
	"github.com/thesatellite-ai/lore/schema"
)

// Operation names.
const (
	Noun    = "pin"
	OpSet   = "pin.set"
	OpUnset = "pin.unset"
)

// Pin forms (6.4): the same words as logic/contextfit's forms.
const (
	FormFull = "full"
	FormLine = "line"
)

// Fields the pin writes (schema.FieldContextPin / FieldContextPinnedAt).
const (
	FieldPin      = "context_pin"
	FieldPinnedAt = "context_pinned_at"
	fieldSeverity = "severity"
)

// Env is the knowledge env (Show + Update).
type Env = kn.Env

var nouns = []kn.Noun{
	{Name: "rule", Table: schema.TableRules, Prefix: ids.PrefixRule},
	{Name: "hotfix", Table: schema.TableHotfixes, Prefix: ids.PrefixHotfix},
}

var writeErrors = []errs.Code{
	errs.InvalidInput, errs.NotFound, errs.PolicyDenied, errs.Stale,
	errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
}

// SetIn is the input of pin.set.
type SetIn struct {
	ID      string `json:"id" arg:"0" desc:"the must rule (rul_…) or critical/high hotfix (hfx_…) to pin"`
	Context string `json:"context" flag:"context" desc:"full (always in full, ahead of other must rules) or line (always one line)"`
}

// IDIn is the input of pin.unset.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the pinned rule or hotfix id"`
}

// Out is the result of pin.set and pin.unset.
type Out struct {
	ID      string `json:"id"`
	Store   string `json:"store"`
	Context string `json:"context"` // "" after unpin
}

// Text is the human line.
func (o Out) Text() string {
	if o.Context == "" {
		return "✓ " + o.ID + " unpinned (render ranks it again)\n"
	}
	return "✓ " + o.ID + " pinned: context " + o.Context + " (lore render to apply)\n"
}

// Ops returns the pin operations bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{Name: OpSet, Summary: "Pin how a must rule or critical/high hotfix appears in LORE.md: --context full or line",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Examples: []op.Example{{Title: "always in full", CLI: "lore pin set rul_01b2c3d4e5f60718293a4b5c6d7e8f90 --context full", Want: "pinned"}}}, o.set),
		op.Define(op.Spec{Name: OpUnset, Summary: "Remove a pin; render ranks the entry again",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors, Examples: []op.Example{{Title: "let render rank a pinned rule again", CLI: "lore pin unset rul_1f3a"}}}, o.unset),
	}
}

type ops struct{ env Env }

// set is pin.set (flow in doc.go).
func (o ops) set(ctx context.Context, in SetIn) (Out, error) {
	form := strings.TrimSpace(in.Context)
	if form != FormFull && form != FormLine {
		return Out{}, errs.New(errs.InvalidInput, "pin: --context must be full or line").WithHint("lore pin set <id> --context full")
	}
	v, err := value.TextOf(form)
	if err != nil {
		return Out{}, errs.New(errs.InvalidInput, "pin: --context: "+err.Error())
	}
	return o.write(ctx, in.ID, v, kn.Now(o.env), form)
}

// unset is pin.unset.
func (o ops) unset(ctx context.Context, in IDIn) (Out, error) {
	return o.write(ctx, in.ID, value.Null{}, value.Null{}, "")
}

// write checks the row is in the core and sets both fields.
func (o ops) write(ctx context.Context, raw string, pinV, atV value.Value, form string) (Out, error) {
	n, err := nounOf(raw)
	if err != nil {
		return Out{}, err
	}
	it, err := kn.Show(ctx, o.env, n, raw)
	if err != nil {
		return Out{}, err
	}
	if form != "" && !inCore(n, it) {
		return Out{}, errs.New(errs.InvalidInput, "pin: only must rules and critical/high hotfixes are in LORE.md, so only they take a pin").
			WithHint("raise its severity first, or fetch it with lore context / lore search")
	}
	w, err := o.env.Update(ctx, engine.Ref{Store: it.Source, Table: n.Table, ID: it.Row.ID},
		map[string]value.Value{FieldPin: pinV, FieldPinnedAt: atV})
	if err != nil {
		return Out{}, err
	}
	return Out{ID: w.Row.ID.String(), Store: string(it.Source), Context: form}, nil
}

// nounOf picks the knowledge noun from the id prefix.
func nounOf(raw string) (kn.Noun, error) {
	for _, n := range nouns {
		if _, err := n.ID(raw); err == nil {
			return n, nil
		}
	}
	return kn.Noun{}, errs.New(errs.NotFound, "pin: "+strings.TrimSpace(raw)+" is not a rule (rul_) or hotfix (hfx_) id").
		WithHint("pin takes the id of a must rule or a critical/high hotfix: lore rule list, lore hotfix list")
}

// inCore reports whether the row is a must rule or a critical/high hotfix.
func inCore(n kn.Noun, it kn.Item) bool {
	sev := ""
	if v, ok := it.Row.Fields[fieldSeverity]; ok && v != nil {
		sev = v.String()
	}
	if n.Table == schema.TableRules {
		return sev == schema.RuleSeverityMust
	}
	return sev == schema.HotfixSeverityCritical || sev == schema.HotfixSeverityHigh
}
