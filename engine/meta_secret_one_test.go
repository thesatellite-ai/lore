package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMetaSecretOneAnswer (SECRETS 2.5 and FB-SEC-3, P3-METASEC-2): a
// write whose field and metadata value both hold a secret gets ONE
// answer: findings[] lists both, --secret-ref counts across both (fields
// first, then metadata), --not-a-secret asks one question at a terminal
// and pins an allow row on each, and the same value in a field and a
// metadata value is one finding. A refused write stores no row and no
// allow row; no error echoes a secret.
func TestMetaSecretOneAnswer(t *testing.T) {
	other := "AKIA" + strings.Repeat("W", 16)
	reason := "sample keys from the aws docs"
	body := "key " + fakeKey
	// vault locators an answer names (references, never values)
	refA, refB, refC := "gopass://acme/a", "gopass://acme/b", "gopass://acme/c"
	two := []metaArg{{Text: "token=x " + other}}    // a different secret in meta
	same := []metaArg{{Text: "token=x " + fakeKey}} // the body's secret again
	type want struct {
		code     errs.Code // non-empty: refused with this code
		findings int       // E_NEEDS_CONFIRM: findings[] length
		body     string
		metaVal  string // the stored "token" metadata value (canonical JSON)
		allows   int
		asked    int // terminal questions
	}
	cases := []struct {
		name     string
		mode     policy.OnDetect
		terminal bool
		yes      bool
		choice   op.SecretChoice
		args     []metaArg
		want     want
	}{
		{name: "ask/no answer lists both", mode: policy.OnDetectAsk, args: two, want: want{code: errs.NeedsConfirm, findings: 2}},
		{name: "ask/same value is one finding", mode: policy.OnDetectAsk, args: same, want: want{code: errs.NeedsConfirm, findings: 1}},
		{name: "ask/two refs cover field and meta", mode: policy.OnDetectAsk,
			choice: op.SecretChoice{Refs: []string{refA, refB}}, args: two,
			want: want{body: "key " + secretRefText(refA), metaVal: quoted("x " + secretRefText(refB))}},
		{name: "ask/three refs for two findings", mode: policy.OnDetectAsk,
			choice: op.SecretChoice{Refs: []string{refA, refB, refC}}, args: two,
			want: want{code: errs.InvalidInput}},
		{name: "ask/one ref covers both", mode: policy.OnDetectAsk,
			choice: op.SecretChoice{Refs: []string{refA}}, args: two,
			want: want{body: "key " + secretRefText(refA), metaVal: quoted("x " + secretRefText(refA))}},
		{name: "ask/same value two refs is too many", mode: policy.OnDetectAsk,
			choice: op.SecretChoice{Refs: []string{refA, refB}}, args: same,
			want: want{code: errs.InvalidInput}},
		{name: "refuse/--secret-unstored both", mode: policy.OnDetectRefuse, choice: op.SecretChoice{Unstored: true}, args: two,
			want: want{body: "key " + refs.Unstored, metaVal: `"x ` + refs.Unstored + `"`}},
		{name: "redact/no answer both", mode: policy.OnDetectRedact, args: two,
			want: want{body: "key " + refs.Unstored, metaVal: `"x ` + refs.Unstored + `"`}},
		{name: "refuse/no answer", mode: policy.OnDetectRefuse, args: two, want: want{code: errs.SecretDetected}},
		{name: "ask/terminal --not-a-secret asks once", mode: policy.OnDetectAsk, terminal: true,
			choice: op.SecretChoice{NotASecret: true, Reason: reason}, args: two,
			want: want{body: body, metaVal: `"x ` + other + `"`, allows: 2, asked: 1}},
		{name: "ask/--not-a-secret --yes", mode: policy.OnDetectAsk, yes: true,
			choice: op.SecretChoice{NotASecret: true, Reason: reason}, args: two,
			want: want{body: body, metaVal: `"x ` + other + `"`, allows: 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conf := &yesConfirmer{answer: true}
			f := setup(t, onDetect(t, c.mode), WithConfirmer(conf),
				WithRedactHook(func(context.Context, RedactedWrite) error { return nil }))
			ctx := context.Background()
			caller := op.Caller{Actor: testActor, Yes: c.yes, Interactive: c.terminal, Secret: c.choice}
			_, err := f.eng.Run(ctx, memMetaOp(f, metaWrite(t, c.args)), op.ArgsDecoder{Args: []string{body}}, caller)
			mems, lerr := f.eng.List(ctx, store.SourceRepo, schema.TableMemories)
			if lerr != nil {
				t.Fatal(lerr)
			}
			allows, lerr := f.eng.List(ctx, store.SourceRepo, schema.TableSecretAllows)
			if lerr != nil {
				t.Fatal(lerr)
			}
			if len(conf.asked) != c.want.asked {
				t.Fatalf("terminal questions = %d (%q), want %d", len(conf.asked), conf.asked, c.want.asked)
			}
			if c.want.code != "" {
				if !errs.Has(err, c.want.code) {
					t.Fatalf("err = %v, want %s", err, c.want.code)
				}
				if strings.Contains(err.Error(), fakeKey) || strings.Contains(err.Error(), other) {
					t.Fatalf("error leaks a secret: %v", err)
				}
				if c.want.findings > 0 {
					var e *errs.Error
					if !errors.As(err, &e) || len(e.Findings) != c.want.findings {
						t.Fatalf("findings = %+v, want %d", e, c.want.findings)
					}
				}
				if len(mems) != 0 || len(allows) != 0 {
					t.Fatalf("a refused write stored %d rows, %d allow rows", len(mems), len(allows))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(mems) != 1 {
				t.Fatalf("stored %d rows", len(mems))
			}
			if got := mems[0].Fields["body"].String(); got != c.want.body {
				t.Fatalf("body = %q, want %q", got, c.want.body)
			}
			if got := metaValues(t, mems[0])["token"]; got != c.want.metaVal {
				t.Fatalf("meta token = %s, want %s", got, c.want.metaVal)
			}
			if len(allows) != c.want.allows {
				t.Fatalf("allow rows = %d, want %d", len(allows), c.want.allows)
			}
		})
	}
}
