package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Joined-scan test rules: a password pair (keyword "password", a value of
// 8+ characters after ':' or '='), a hex key that only counts near the
// word "apikey" (the keyword prefilter, so a field without the word is
// clean on its own), and a session-id rule whose match needs a newline
// (only the joined text can hold one between two fields).
const (
	rulePWPair = "pw-pair"
	ruleHexKey = "hex-key"
)

// joinedScanner is the test fixture scanner plus the joined-scan rules.
func joinedScanner(t *testing.T) Option {
	t.Helper()
	sc, err := scan.NewRegexScanner([]scan.Rule{
		{ID: awsRule, Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: scan.SourceBuiltin},
		{ID: rulePWPair, Regex: `(?i)password\s*[:=]\s*[A-Za-z0-9!]{8,}`, Keywords: []string{"password"}, Source: scan.SourceBuiltin},
		{ID: ruleHexKey, Regex: `[0-9a-f]{32}`, Keywords: []string{"apikey"}, Source: scan.SourceBuiltin},
		{ID: scan.RuleSessionID, Regex: `sess-[a-z]+\n[a-z]+`, Keywords: []string{"sess-"}, Source: scan.SourceBuiltin},
	})
	if err != nil {
		t.Fatal(err)
	}
	return WithScanner(sc)
}

// TestJoinedFieldsScan (SECRETS 19.2): the written text fields are joined
// in name order and scanned once more. A secret split across two adjacent
// fields (source_kind ends "…password", source_ref starts ": <value>") is
// refused in every mode and with every answer, naming both fields and never
// the value; a finding the joined text reveals inside ONE field is that
// field's finding, redacted at its own offset; a finding the per-field scan
// already had is not counted twice; the session-id rule is never joined;
// ordinary multi-field rows give no finding.
func TestJoinedFieldsScan(t *testing.T) {
	t.Parallel()
	const pw = "Hunter2Hunter2"
	hex := strings.Repeat("0123456789abcdef", 2)
	type want struct {
		code     errs.Code         // non-empty: refused with this code
		fields   map[string]string // stored field values
		detected int               // secret.detected events
	}
	split := map[string]string{"source_kind": "notes password", "source_ref": ": " + pw}
	cases := []struct {
		name   string
		mode   policy.OnDetect
		caller op.Caller
		fields map[string]string
		want   want
	}{
		{name: "split pair, ask", mode: policy.OnDetectAsk, fields: split, want: want{code: errs.SecretDetected, detected: 1}},
		{name: "split pair, redact", mode: policy.OnDetectRedact, fields: split, want: want{code: errs.SecretDetected, detected: 1}},
		{name: "split pair, refuse", mode: policy.OnDetectRefuse, fields: split, want: want{code: errs.SecretDetected, detected: 1}},
		{name: "split pair, --secret-unstored", mode: policy.OnDetectAsk, fields: split,
			caller: op.Caller{Secret: op.SecretChoice{Unstored: true}}, want: want{code: errs.SecretDetected, detected: 1}},
		{name: "split pair, --not-a-secret --yes", mode: policy.OnDetectAsk, fields: split,
			caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true, Reason: "doc"}}, want: want{code: errs.SecretDetected, detected: 1}},
		{name: "found only when joined, redacted in its field", mode: policy.OnDetectRedact,
			fields: map[string]string{"source_kind": "apikey", "source_ref": "x " + hex + " y"},
			want:   want{fields: map[string]string{"source_kind": "apikey", "source_ref": "x " + refs.Unstored + " y"}, detected: 1}},
		{name: "per-field finding not doubled", mode: policy.OnDetectRedact,
			fields: map[string]string{"body": "key " + fakeKey + " end", "source_ref": "docs/readme.md"},
			want:   want{fields: map[string]string{"body": "key " + refs.Unstored + " end", "source_ref": "docs/readme.md"}, detected: 1}},
		{name: "session id never joined", mode: policy.OnDetectRefuse,
			fields: map[string]string{"source_kind": "sess-abc", "source_ref": "def"},
			want:   want{fields: map[string]string{"source_kind": "sess-abc", "source_ref": "def"}}},
		{name: "ordinary row", mode: policy.OnDetectRefuse,
			fields: map[string]string{"body": "the password policy lives in the wiki", "source_kind": "manual", "source_ref": "docs/security.md"},
			want:   want{fields: map[string]string{"body": "the password policy lives in the wiki", "source_kind": "manual", "source_ref": "docs/security.md"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := setup(t, onDetect(t, c.mode), joinedScanner(t), WithConfirmer(&yesConfirmer{answer: true}),
				WithRedactHook(func(context.Context, RedactedWrite) error { return nil }))
			ctx := context.Background()
			fields := f.memFields("plain body")
			for k, v := range c.fields {
				fields[k] = txt(v)
			}
			o := op.Define(op.Spec{Name: "memory.add", Summary: "add", Permission: op.PermWrite, Access: op.AccessWrite,
				Errors: []errs.Code{errs.SecretDetected, errs.NeedsConfirm}},
				func(ctx context.Context, _ memAddIn) (memAddOut, error) {
					w, err := f.eng.Create(ctx, "", schema.TableMemories, fields)
					if err != nil {
						return memAddOut{}, err
					}
					return memAddOut{ID: w.Row.ID.String()}, nil
				})
			caller := c.caller
			caller.Actor = testActor
			_, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{"x"}}, caller)
			mems, lerr := f.eng.List(ctx, store.SourceRepo, schema.TableMemories)
			if lerr != nil {
				t.Fatal(lerr)
			}
			evs := secretEvents(f, ActionSecretDetected)
			noSecretIn(t, evs, pw, hex, fakeKey)
			if len(evs) != c.want.detected {
				t.Fatalf("secret.detected = %d (%+v), want %d", len(evs), evs, c.want.detected)
			}
			if c.want.code != "" {
				if !errs.Has(err, c.want.code) {
					t.Fatalf("err = %v, want %s", err, c.want.code)
				}
				msg := err.Error()
				if strings.Contains(msg, pw) || !strings.Contains(msg, "source_kind and source_ref") {
					t.Fatalf("error must name both fields, never the value: %v", err)
				}
				if len(mems) != 0 {
					t.Fatalf("a refused write stored %d rows", len(mems))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(mems) != 1 {
				t.Fatalf("stored %d rows", len(mems))
			}
			for k, v := range c.want.fields {
				got, ok := mems[0].Fields[k].(value.Text)
				if !ok || got.String() != v {
					t.Fatalf("%s = %v, want %q", k, mems[0].Fields[k], v)
				}
			}
		})
	}
}
