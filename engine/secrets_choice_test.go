package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// onDetect is a fixed policy with secrets.on_detect set to mode.
func onDetect(t *testing.T, mode policy.OnDetect) Option {
	t.Helper()
	set, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyOnDetect, Value: txt(string(mode))}})
	if err != nil {
		t.Fatal(err)
	}
	return WithSettings(fixedSettings{set: set})
}

// TestSecretChoiceMatrix (SECRETS 4, D12, D21, SQ2): an up-front answer to
// a secret finding (--not-a-secret --reason, --secret-ref,
// --secret-unstored) works in every on_detect mode, refuses a bad answer
// with E_INVALID_INPUT before anything is stored, and never puts the
// secret into an error. Two findings in one body cover one-for-all and
// per-finding --secret-ref.
func TestSecretChoiceMatrix(t *testing.T) {
	second := "AKIA" + strings.Repeat("Z", 16)
	two := "a " + fakeKey + " b " + second + " c"
	one := "key " + fakeKey + " end"
	type want struct {
		code      errs.Code // non-empty: refused with this code
		body      string    // stored body
		allow     string    // non-empty: one allow row whose reason starts with this
		redacted  int       // audited redacted findings
		hookCalls int
	}
	cases := []struct {
		name   string
		mode   policy.OnDetect
		body   string
		caller op.Caller
		want   want
	}{
		{name: "not-a-secret without reason", mode: policy.OnDetectAsk, body: one,
			caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true}}, want: want{code: errs.InvalidInput}},
		{name: "not-a-secret blank reason", mode: policy.OnDetectAsk, body: one,
			caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true, Reason: "  \t"}}, want: want{code: errs.InvalidInput}},
		{name: "not-a-secret no terminal no yes", mode: policy.OnDetectAsk, body: one,
			caller: op.Caller{Secret: op.SecretChoice{NotASecret: true, Reason: "sample key from the aws docs"}}, want: want{code: errs.NeedsConfirm}},
		{name: "not-a-secret with yes (ask)", mode: policy.OnDetectAsk, body: one,
			caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true, Reason: "sample key from the aws docs"}},
			want:   want{body: one, allow: "sample key from the aws docs (" + allowReasonPrefix}},
		{name: "not-a-secret in refuse mode", mode: policy.OnDetectRefuse, body: one,
			caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true, Reason: "fixture"}},
			want:   want{body: one, allow: "fixture (" + allowReasonPrefix}},
		{name: "not-a-secret in redact mode keeps text", mode: policy.OnDetectRedact, body: one,
			caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true, Reason: "fixture ü"}},
			want:   want{body: one, allow: "fixture ü (" + allowReasonPrefix}},
		{name: "unstored (ask)", mode: policy.OnDetectAsk, body: one,
			caller: op.Caller{Secret: op.SecretChoice{Unstored: true}},
			want:   want{body: "key " + refs.Unstored + " end", redacted: 1, hookCalls: 1}},
		{name: "unstored in refuse mode", mode: policy.OnDetectRefuse, body: two,
			caller: op.Caller{Secret: op.SecretChoice{Unstored: true}},
			want:   want{body: "a " + refs.Unstored + " b " + refs.Unstored + " c", redacted: 2, hookCalls: 1}},
		{name: "one ref for all", mode: policy.OnDetectRefuse, body: two,
			caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"gopass://acme/aws"}}},
			want:   want{body: "a ${secret:gopass://acme/aws} b ${secret:gopass://acme/aws} c"}},
		{name: "one ref per finding, in order", mode: policy.OnDetectAsk, body: two,
			caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"gopass://acme/one", "env://TWO"}}},
			want:   want{body: "a ${secret:gopass://acme/one} b ${secret:env://TWO} c"}},
		{name: "ref count mismatch", mode: policy.OnDetectAsk, body: two,
			caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"gopass://a", "gopass://b", "gopass://c"}}}, want: want{code: errs.InvalidInput}},
		{name: "ref without scheme", mode: policy.OnDetectAsk, body: one,
			caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"acme/aws"}}}, want: want{code: errs.InvalidInput}},
		{name: "ref unstored literal", mode: policy.OnDetectAsk, body: one,
			caller: op.Caller{Secret: op.SecretChoice{Refs: []string{refs.TargetUnstored}}}, want: want{code: errs.InvalidInput}},
		{name: "mixed not-a-secret + unstored", mode: policy.OnDetectAsk, body: one,
			caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true, Reason: "x", Unstored: true}}, want: want{code: errs.InvalidInput}},
		{name: "mixed ref + unstored", mode: policy.OnDetectAsk, body: one,
			caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"gopass://a"}, Unstored: true}}, want: want{code: errs.InvalidInput}},
		{name: "no finding: choice changes nothing", mode: policy.OnDetectRefuse, body: "plain text",
			caller: op.Caller{Secret: op.SecretChoice{Unstored: true}}, want: want{body: "plain text"}},
		{name: "no choice in refuse mode", mode: policy.OnDetectRefuse, body: one,
			caller: op.Caller{Yes: true}, want: want{code: errs.SecretDetected}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hooks := 0
			f := setup(t, onDetect(t, c.mode), WithRedactHook(func(context.Context, RedactedWrite) error { hooks++; return nil }))
			ctx := context.Background()
			o := memAddOp(f, op.ConfirmRule{})
			res, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{c.body}}, c.caller)
			mems, lerr := f.eng.List(ctx, store.SourceRepo, schema.TableMemories)
			if lerr != nil {
				t.Fatal(lerr)
			}
			allows, lerr := f.eng.List(ctx, store.SourceRepo, schema.TableSecretAllows)
			if lerr != nil {
				t.Fatal(lerr)
			}
			if c.want.code != "" {
				if !errs.Has(err, c.want.code) {
					t.Fatalf("err = %v, want %s", err, c.want.code)
				}
				if strings.Contains(err.Error(), fakeKey) || strings.Contains(err.Error(), second) {
					t.Fatalf("error leaks the secret: %v", err)
				}
				if len(mems) != 0 || len(allows) != 0 {
					t.Fatalf("a refused write stored %d rows, %d allows", len(mems), len(allows))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := res.JSON(); err != nil {
				t.Fatal(err)
			}
			if len(mems) != 1 || mems[0].Fields["body"].String() != c.want.body {
				t.Fatalf("stored %v, want body %q", mems, c.want.body)
			}
			if c.want.allow == "" {
				if len(allows) != 0 {
					t.Fatalf("unexpected allow rows: %v", allows)
				}
			} else {
				if len(allows) != 1 || !strings.HasPrefix(textOf(allows[0], allowReason), c.want.allow) || textOf(allows[0], allowRowID) != mems[0].ID.String() {
					t.Fatalf("allow rows = %v, want reason prefix %q", allows, c.want.allow)
				}
			}
			evs := f.audit.events()
			if got := len(evs[len(evs)-1].SecretsRedacted); got != c.want.redacted {
				t.Fatalf("audited redacted = %d, want %d", got, c.want.redacted)
			}
			if hooks != c.want.hookCalls {
				t.Fatalf("D21 hook calls = %d, want %d", hooks, c.want.hookCalls)
			}
		})
	}
}

// TestSecretChoiceInteractive: --not-a-secret on a terminal asks once and
// a "no" cancels without storing.
func TestSecretChoiceInteractive(t *testing.T) {
	c := &yesConfirmer{answer: false}
	f := setup(t, WithConfirmer(c))
	ctx := context.Background()
	o := memAddOp(f, op.ConfirmRule{})
	caller := op.Caller{Interactive: true, Secret: op.SecretChoice{NotASecret: true, Reason: "doc sample"}}
	if _, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{"key " + fakeKey}}, caller); !errs.Has(err, errs.Cancelled) {
		t.Fatalf("a no must cancel: %v", err)
	}
	if len(c.asked) != 1 || !strings.Contains(c.asked[0], "doc sample") || strings.Contains(c.asked[0], fakeKey) {
		t.Fatalf("asked %q", c.asked)
	}
	c.answer = true
	if _, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{"key " + fakeKey}}, caller); err != nil {
		t.Fatal(err)
	}
	if allows, _ := f.eng.List(ctx, store.SourceRepo, schema.TableSecretAllows); len(allows) != 1 {
		t.Fatalf("allow rows = %d", len(allows))
	}
}

// TestSecretAskNeedsAChoice (SECRETS 2.1, 2.5, 4; SQ2, D12; FB-SEC-1): the
// decision table of a write with one finding over on_detect × terminal ×
// answer flags. --yes only confirms a choice given by flags: a bare --yes
// (terminal or not) stores nothing and records no allow entry under ask;
// "not a secret" needs a typed reason, then --yes or a terminal yes, in
// every mode; only that path writes an allow entry.
func TestSecretAskNeedsAChoice(t *testing.T) {
	body := "key " + fakeKey + " end"
	reason := "sample key from the aws docs"
	type outcome int
	const (
		kept     outcome = iota // stored as written, one allow entry
		redacted                // stored as ${secret:unstored}, no allow entry
	)
	flagSets := []struct {
		name   string
		yes    bool
		choice op.SecretChoice
	}{
		{name: "no flags"},
		{name: "--yes", yes: true},
		{name: "--not-a-secret no reason", choice: op.SecretChoice{NotASecret: true}},
		{name: "--not-a-secret no reason --yes", yes: true, choice: op.SecretChoice{NotASecret: true}},
		{name: "--not-a-secret --reason", choice: op.SecretChoice{NotASecret: true, Reason: reason}},
		{name: "--not-a-secret --reason --yes", yes: true, choice: op.SecretChoice{NotASecret: true, Reason: reason}},
	}
	// want returns the refusal code ("" = stored) and, when stored, how.
	want := func(mode policy.OnDetect, terminal bool, fs int) (errs.Code, outcome) {
		switch fs {
		case 0, 1: // no choice: the mode decides; --yes changes nothing
			switch mode {
			case policy.OnDetectAsk:
				return errs.NeedsConfirm, 0
			case policy.OnDetectRedact:
				return "", redacted
			default:
				return errs.SecretDetected, 0
			}
		case 2, 3:
			return errs.InvalidInput, 0
		case 4:
			if !terminal {
				return errs.NeedsConfirm, 0
			}
		}
		return "", kept
	}
	for _, mode := range []policy.OnDetect{policy.OnDetectAsk, policy.OnDetectRedact, policy.OnDetectRefuse} {
		for _, terminal := range []bool{false, true} {
			for i, fl := range flagSets {
				name := string(mode) + "/terminal=" + strconv.FormatBool(terminal) + "/" + fl.name
				t.Run(name, func(t *testing.T) {
					conf := &yesConfirmer{answer: true}
					f := setup(t, onDetect(t, mode), WithConfirmer(conf), WithRedactHook(func(context.Context, RedactedWrite) error { return nil }))
					ctx := context.Background()
					caller := op.Caller{Actor: testActor, Yes: fl.yes, Interactive: terminal, Secret: fl.choice}
					_, err := f.eng.Run(ctx, memAddOp(f, op.ConfirmRule{}), op.ArgsDecoder{Args: []string{body}}, caller)
					mems, lerr := f.eng.List(ctx, store.SourceRepo, schema.TableMemories)
					if lerr != nil {
						t.Fatal(lerr)
					}
					allows, lerr := f.eng.List(ctx, store.SourceRepo, schema.TableSecretAllows)
					if lerr != nil {
						t.Fatal(lerr)
					}
					code, how := want(mode, terminal, i)
					if code != "" {
						if !errs.Has(err, code) || strings.Contains(err.Error(), fakeKey) {
							t.Fatalf("err = %v, want %s without the secret", err, code)
						}
						if len(mems) != 0 || len(allows) != 0 {
							t.Fatalf("a refused write stored %d rows, %d allow entries", len(mems), len(allows))
						}
						if code == errs.NeedsConfirm && i < 2 && !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "--not-a-secret --reason") {
							t.Fatalf("the answer must name the flag sets: %+v", errs.EnvelopeOf(err).Error)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(mems) != 1 {
						t.Fatalf("stored %d rows", len(mems))
					}
					switch how {
					case kept:
						if mems[0].Fields["body"].String() != body || len(allows) != 1 || !strings.HasPrefix(textOf(allows[0], allowReason), reason+" (") {
							t.Fatalf("kept: body %q, allows %v", mems[0].Fields["body"].String(), allows)
						}
					case redacted:
						if mems[0].Fields["body"].String() != "key "+refs.Unstored+" end" || len(allows) != 0 {
							t.Fatalf("redacted: body %q, allows %v", mems[0].Fields["body"].String(), allows)
						}
					}
				})
			}
		}
	}
}
