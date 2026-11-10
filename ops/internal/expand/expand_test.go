package expand

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// revealDummy is the value the fake env manager returns (not key-shaped).
const revealDummy = "fake-sunflower-tractor-42"

// vars resolves ${var:<name>} from a map.
type vars map[string]string

func (v vars) Resolve(r refs.Ref) (refs.Resolved, error) {
	s, ok := v[r.Target]
	if !ok {
		return refs.Resolved{}, refs.ErrUnresolved
	}
	return refs.Resolved{Text: s}, nil
}

// fakeEnv is an Env over one row; audited collects AuditSecret calls when
// the env is wrapped as an auditor.
type fakeEnv struct {
	r       row.Row
	def     schema.TableDef
	scanner scan.Scanner
}

func (f *fakeEnv) Get(_ context.Context, ref engine.Ref) (store.Source, row.Row, error) {
	if ref.ID != f.r.ID {
		return "", row.Row{}, errs.New(errs.NotFound, "no row")
	}
	return store.SourceRepo, f.r, nil
}
func (f *fakeEnv) RefResolver(context.Context) (refs.Resolver, error) {
	return vars{"host": "db.example"}, nil
}
func (f *fakeEnv) OutputScanner(context.Context) (scan.Scanner, error) { return f.scanner, nil }
func (f *fakeEnv) TableSchema(_ context.Context, t row.Table) (schema.TableDef, bool) {
	return f.def, t == f.def.Name
}

// auditingEnv adds reveal.Auditor and reveal.ManagerSource (env manager).
type auditingEnv struct {
	*fakeEnv
	audits []engine.SecretAudit
}

func (a *auditingEnv) AuditSecret(_ context.Context, _ engine.Action, s engine.SecretAudit) error {
	a.audits = append(a.audits, s)
	return nil
}
func (a *auditingEnv) SecretManagers(context.Context) (secretmgr.Set, error) {
	return secretmgr.Builtin(secretmgr.Options{Getenv: func(k string) string {
		if k == "LORE_TEST_PW" {
			return revealDummy
		}
		return ""
	}}), nil
}

func newEnv(t *testing.T, fields map[string]string) *fakeEnv {
	t.Helper()
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	fs := map[string]value.Value{}
	for k, s := range fields {
		v, terr := value.TextOf(s)
		if terr != nil {
			t.Fatal(terr)
		}
		fs[k] = v
	}
	sc, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	def := schema.Table(string(schema.TableMemories), ids.PrefixMemory,
		schema.Field("body", value.KindText), schema.Field("title", value.KindText), schema.Field("pin", value.KindText, schema.Sensitive()))
	return &fakeEnv{r: row.Row{Table: schema.TableMemories, ID: id.Any(), Fields: fs}, def: def, scanner: sc}
}

const envRefText = "${secret:env://LORE_TEST_PW}"

// TestShowExpand (SECRETS 2.7, 12, OV37): --expand fills var references,
// masks every secret, leaves a sensitive field untouched (RQ1) and lists
// only changed fields; each reference appears once in refs even when two
// fields use it.
func TestShowExpand(t *testing.T) {
	env := newEnv(t, map[string]string{
		"body":  "connect to ${var:host} with " + envRefText + " and ${var:nope}",
		"title": "on ${var:host}",
		"pin":   "${var:host} " + envRefText,
	})
	b, err := Show(context.Background(), env, env.r.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Expanded["body"]; got != "connect to db.example with "+refpoint.Mask+" and ${var:nope}" {
		t.Fatalf("body = %q", got)
	}
	if got := b.Expanded["title"]; got != "on db.example" {
		t.Fatalf("title = %q", got)
	}
	if _, ok := b.Expanded["pin"]; ok {
		t.Fatal("a sensitive field was expanded")
	}
	seen := map[string]int{}
	for _, u := range b.Refs {
		seen[u.Ref]++
	}
	if seen["${var:host}"] != 1 || seen[envRefText] != 1 || seen["${var:nope}"] != 1 {
		t.Fatalf("refs = %+v", b.Refs)
	}
	if b.Revealed != 0 || strings.Contains(Text(b.Expanded, b.Refs), revealDummy) {
		t.Fatal("plain --expand revealed something")
	}
}

// TestShowWithRefusals: --force without --reveal, a nil env and a bad id
// are refused before anything is read.
func TestShowWithRefusals(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t, map[string]string{"body": "x"})
	if _, err := ShowWith(ctx, env, env.r.ID.String(), RevealFlag{Force: true}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("--force alone: %v", err)
	}
	if _, err := ShowWith(ctx, nil, env.r.ID.String(), RevealFlag{}); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("nil env: %v", err)
	}
	if _, err := ShowWith(ctx, env, "not an id", RevealFlag{}); !errs.Has(err, errs.InvalidID) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := Reveal(ctx, nil, env.r, true); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("nil env reveal: %v", err)
	}
}

// TestReveal (2.7, D18, SQ10): --reveal asks first (E_NEEDS_CONFIRM, the
// question names the manager, never the value); a surface that cannot
// audit is refused even when forced; with an auditor, --force reveals,
// marks the block forced with W_REVEAL_FORCED and audits the attempt
// without the value; a sensitive field's reference is never revealed; an
// ${secret:unstored} stays masked.
func TestReveal(t *testing.T) {
	ctx := context.Background()
	base := newEnv(t, map[string]string{"body": "pw " + envRefText + " u ${secret:unstored}", "pin": "${secret:env://LORE_OTHER}"})
	_, err := Reveal(ctx, &auditingEnv{fakeEnv: base}, base.r, false)
	if !errs.Has(err, errs.NeedsConfirm) || strings.Contains(err.Error(), revealDummy) || !strings.Contains(err.Error(), "env (LORE_TEST_PW)") {
		t.Fatalf("no answer: %v", err)
	}
	if strings.Contains(err.Error(), "LORE_OTHER") {
		t.Fatal("a sensitive field's reference reached the question")
	}
	if _, err = Reveal(ctx, base, base.r, true); !errs.Has(err, errs.Internal) {
		t.Fatalf("an env without an auditor must refuse: %v", err)
	}
	ae := &auditingEnv{fakeEnv: base}
	b, err := Reveal(ctx, ae, base.r, true)
	if err != nil {
		t.Fatal(err)
	}
	if b.Expanded["body"] != "pw "+revealDummy+" u "+refpoint.Mask {
		t.Fatalf("body = %q", b.Expanded["body"])
	}
	if b.Revealed != 1 || !b.Forced || b.Warning != WarningTextValue() {
		t.Fatalf("reveal out = %+v", b.RevealOut)
	}
	if len(ae.audits) != 1 || !ae.audits[0].Forced || ae.audits[0].Row != base.r.ID {
		t.Fatalf("audits = %+v", ae.audits)
	}
	// no secret reference at all: plain --expand, nothing asked
	plain := newEnv(t, map[string]string{"body": "${var:host}"})
	if b, err = Reveal(ctx, plain, plain.r, false); err != nil || b.Expanded["body"] != "db.example" || b.Revealed != 0 {
		t.Fatalf("no refs: %+v %v", b, err)
	}
}

// WarningTextValue is the warning a forced reveal carries (reveal.Warning).
func WarningTextValue() string {
	return strings.TrimSuffix(WarningText("W_REVEAL_FORCED: revealed without a person's answer; recorded in the audit log as forced"), "\n")
}

// TestRefLine: an expanded reference prints its value; any other state
// prints the state; source and note follow.
func TestRefLine(t *testing.T) {
	cases := []struct {
		u    refpoint.Used
		want string
	}{
		{refpoint.Used{Ref: "${var:h}", Value: "db", Source: "var_x", State: refpoint.StateExpanded}, "db  (var_x)"},
		{refpoint.Used{Ref: envRefText, State: refpoint.StateMasked, Note: "not revealed (add --reveal)"}, "masked  not revealed (add --reveal)"},
		{refpoint.Used{Ref: "${var:x}", State: refpoint.StateDangling}, "dangling"},
	}
	for _, c := range cases {
		if got := RefLine(c.u); got != c.want {
			t.Errorf("RefLine = %q, want %q", got, c.want)
		}
	}
	if Text(nil, nil) != "" || RefsText(nil) != "" {
		t.Error("nothing to show must print nothing")
	}
}
