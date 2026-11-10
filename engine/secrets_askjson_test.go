package engine

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestSecretAskJSON (SECRETS 2.5, FB-SEC-3/4): the E_NEEDS_CONFIRM answer
// of on_detect ask carries findings[] (n, rule, field, line, masked) and
// next.add_args_one_of with the three flag sets, keeps every older key,
// never holds the value, and counts one secret once: the same value twice
// in one write is one finding, and one --secret-ref then redacts both.
func TestSecretAskJSON(t *testing.T) {
	t.Parallel()
	second := "AKIA" + strings.Repeat("Z", 16)
	cases := []struct {
		name  string
		body  string
		rules []string
	}{
		{name: "one", body: "key " + fakeKey + " end", rules: []string{"aws-access-token"}},
		{name: "two values", body: "a " + fakeKey + " b " + second, rules: []string{"aws-access-token", "aws-access-token"}},
		{name: "same value twice", body: "a " + fakeKey + " b " + fakeKey, rules: []string{"aws-access-token"}},
	}
	wantSets := [][]string{{"--secret-ref", "<manager>://<entry>"}, {"--secret-unstored"}, {"--not-a-secret", "--reason", "<why>", "--yes"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := setup(t, onDetect(t, policy.OnDetectAsk))
			_, err := f.eng.Run(context.Background(), memAddOp(f, op.ConfirmRule{}), op.ArgsDecoder{Args: []string{c.body}}, op.Caller{Yes: true})
			if !errs.Has(err, errs.NeedsConfirm) {
				t.Fatalf("err = %v", err)
			}
			raw, jerr := json.Marshal(errs.EnvelopeOf(err))
			if jerr != nil {
				t.Fatal(jerr)
			}
			if strings.Contains(string(raw), fakeKey) || strings.Contains(string(raw), second) {
				t.Fatalf("the answer holds the value: %s", raw)
			}
			var env struct {
				Error map[string]json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"code", "message", "hint", "exit", "class", "retryable", "needs_person", "question", "next", "findings"} {
				if _, ok := env.Error[k]; !ok {
					t.Fatalf("key %q missing: %s", k, raw)
				}
			}
			var next map[string]json.RawMessage
			if err := json.Unmarshal(env.Error["next"], &next); err != nil {
				t.Fatal(err)
			}
			if _, ok := next["command"]; ok {
				t.Fatalf("an add-args next names no command: %s", env.Error["next"])
			}
			var sets [][]string
			if err := json.Unmarshal(next["add_args_one_of"], &sets); err != nil || !reflect.DeepEqual(sets, wantSets) {
				t.Fatalf("add_args_one_of = %v (%v)", sets, err)
			}
			var fs []errs.Finding
			if err := json.Unmarshal(env.Error["findings"], &fs); err != nil {
				t.Fatal(err)
			}
			if len(fs) != len(c.rules) {
				t.Fatalf("findings = %+v, want %d", fs, len(c.rules))
			}
			for i, fd := range fs {
				if fd.N != i+1 || fd.Rule != c.rules[i] || fd.Field != "body" || fd.Line != 1 || fd.Masked == "" {
					t.Fatalf("finding %d = %+v", i, fd)
				}
			}
		})
	}
	// one reference per unique finding: the duplicated value takes one
	f := setup(t, onDetect(t, policy.OnDetectAsk))
	ctx := context.Background()
	dup := "a " + fakeKey + " b " + fakeKey
	if _, err := f.eng.Run(ctx, memAddOp(f, op.ConfirmRule{}), op.ArgsDecoder{Args: []string{dup}},
		op.Caller{Secret: op.SecretChoice{Refs: []string{"gopass://a", "gopass://b"}}}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("two refs for one secret: %v", err)
	}
	if _, err := f.eng.Run(ctx, memAddOp(f, op.ConfirmRule{}), op.ArgsDecoder{Args: []string{dup}},
		op.Caller{Secret: op.SecretChoice{Refs: []string{"gopass://a"}}}); err != nil {
		t.Fatal(err)
	}
	mems, err := f.eng.List(ctx, store.SourceRepo, schema.TableMemories)
	if err != nil || len(mems) != 1 || mems[0].Fields["body"].String() != "a ${secret:gopass://a} b ${secret:gopass://a}" {
		t.Fatalf("stored %v (%v)", mems, err)
	}
}
