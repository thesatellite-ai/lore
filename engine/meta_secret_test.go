package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// metaArg is one --meta argument of a test write: Text is key=value as
// typed; JSON marks a --meta-json term (the value is JSON text).
type metaArg struct {
	Text string
	JSON bool
}

// metaWrite parses args into the MetaWrite a --meta flag set carries.
func metaWrite(t *testing.T, args []metaArg) MetaWrite {
	t.Helper()
	var mw MetaWrite
	for _, a := range args {
		term, err := meta.ParseWrite(a.Text)
		if err != nil {
			t.Fatal(err)
		}
		if a.JSON {
			mw.JSON = append(mw.JSON, term)
		} else {
			mw.Set = append(mw.Set, term)
		}
	}
	return mw
}

// memMetaIn is memAddIn whose create carries metadata (Run binds it the
// way the shared --meta flags do: the add's own row).
type memMetaIn struct {
	Body string `arg:"0" json:"body"`
}

// memMetaOp adds a memory whose create carries mw.
func memMetaOp(f *fixture, mw MetaWrite) *op.Op[memMetaIn, memAddOut] {
	return op.Define(op.Spec{
		Name: "memory.add", Summary: "add", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.SecretDetected, errs.NeedsConfirm},
	}, func(ctx context.Context, in memMetaIn) (memAddOut, error) {
		w, err := f.eng.Create(WithMeta(ctx, mw), "", schema.TableMemories, f.memFields(in.Body))
		if err != nil {
			return memAddOut{}, err
		}
		return memAddOut{ID: w.Row.ID.String()}, nil
	})
}

// memMetaEditIn names the memory an edit changes.
type memMetaEditIn struct {
	ID string `arg:"0" json:"id"`
}

// memMetaEditOp updates a memory's metadata only (memory edit --meta).
func memMetaEditOp(f *fixture, mw MetaWrite) *op.Op[memMetaEditIn, memAddOut] {
	return op.Define(op.Spec{
		Name: "memory.edit", Summary: "edit", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.SecretDetected, errs.NeedsConfirm},
	}, func(ctx context.Context, in memMetaEditIn) (memAddOut, error) {
		id, err := ids.ParseAny(in.ID)
		if err != nil {
			return memAddOut{}, err
		}
		bound := mw
		bound.ID = id
		w, err := f.eng.Update(WithMeta(ctx, bound), Ref{ID: id}, map[string]value.Value{})
		if err != nil {
			return memAddOut{}, err
		}
		return memAddOut{ID: w.Row.ID.String()}, nil
	})
}

// metaValues is a row's metadata as key name → canonical JSON value.
func metaValues(t *testing.T, r row.Row) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, j := range r.Meta {
		en, err := meta.DecodeEntry(j)
		if err != nil {
			t.Fatal(err)
		}
		out[en.Name] = string(en.V)
	}
	return out
}

// quoted is s as a JSON string (the test values hold no escapes).
func quoted(s string) string { return `"` + s + `"` }

// TestMetaSecretAskFlow (SECRETS 19.1, METADATA 3.21, PASS3-A-2,
// FB-SEC-1..4): a secret inside a metadata value gets the same answer
// flow as a field, leaf by leaf, over on_detect × terminal × answer
// flags: a bare --yes never stores it (FB-SEC-1), --secret-unstored and
// --secret-ref replace only the match inside its own leaf, --not-a-secret
// --reason keeps it and pins an allow row on that leaf, a finding in a
// JSON member name is refused in every mode whatever the answer, and a
// typed value (url) cannot be redacted. A refused write stores no row and
// no allow row, and no error echoes the secret.
func TestMetaSecretAskFlow(t *testing.T) {
	reason := "sample key from the aws docs"
	second := "AKIA" + strings.Repeat("Z", 16)
	text := []metaArg{{Text: "token=key " + fakeKey + " end"}}
	jsonLeaf := []metaArg{{Text: `cfg={"n":1,"smtp":{"host":"mail","url":"x ` + fakeKey + `"}}`, JSON: true}}
	jsonName := []metaArg{{Text: `cfg={"` + fakeKey + `":"plain"}`, JSON: true}}
	nestedName := []metaArg{{Text: `cfg={"` + fakeKey + `":{"` + second + `":"x ` + fakeKey + `"}}`, JSON: true}}
	typed := []metaArg{{Text: "site:url=https://example.com/" + fakeKey}}
	type want struct {
		code     errs.Code         // non-empty: refused with this code
		meta     map[string]string // stored metadata (key → canonical JSON)
		allow    string            // non-empty: one allow row on this field
		redacted int               // fields handed to the D21 hook
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
		// FB-SEC-1: no answer under ask refuses, with or without --yes or a terminal
		{name: "ask/no flags", mode: policy.OnDetectAsk, args: text, want: want{code: errs.NeedsConfirm}},
		{name: "ask/bare --yes", mode: policy.OnDetectAsk, yes: true, args: text, want: want{code: errs.NeedsConfirm}},
		{name: "ask/terminal bare --yes", mode: policy.OnDetectAsk, terminal: true, yes: true, args: text, want: want{code: errs.NeedsConfirm}},
		{name: "ask/terminal no flags", mode: policy.OnDetectAsk, terminal: true, args: text, want: want{code: errs.NeedsConfirm}},
		{name: "ask/json leaf bare --yes", mode: policy.OnDetectAsk, yes: true, args: jsonLeaf, want: want{code: errs.NeedsConfirm}},
		{name: "redact/no flags", mode: policy.OnDetectRedact, args: text,
			want: want{meta: map[string]string{"token": quoted("key " + refs.Unstored + " end")}, redacted: 1}},
		{name: "redact/--yes", mode: policy.OnDetectRedact, yes: true, args: text,
			want: want{meta: map[string]string{"token": quoted("key " + refs.Unstored + " end")}, redacted: 1}},
		{name: "refuse/--yes", mode: policy.OnDetectRefuse, yes: true, args: text, want: want{code: errs.SecretDetected}},
		{name: "refuse/terminal --yes", mode: policy.OnDetectRefuse, terminal: true, yes: true, args: text, want: want{code: errs.SecretDetected}},

		// SECRETS 4: an answer given up front applies in every mode
		{name: "ask/--secret-unstored", mode: policy.OnDetectAsk, choice: op.SecretChoice{Unstored: true}, args: text,
			want: want{meta: map[string]string{"token": quoted("key " + refs.Unstored + " end")}, redacted: 1}},
		{name: "refuse/--secret-ref", mode: policy.OnDetectRefuse, choice: op.SecretChoice{Refs: []string{"gopass://acme/aws"}}, args: text,
			want: want{meta: map[string]string{"token": quoted("key ${secret:gopass://acme/aws} end")}}},
		{name: "ask/--secret-ref bad scheme", mode: policy.OnDetectAsk, choice: op.SecretChoice{Refs: []string{"acme/aws"}}, args: text,
			want: want{code: errs.InvalidInput}},
		{name: "ask/--not-a-secret no reason --yes", mode: policy.OnDetectAsk, yes: true, choice: op.SecretChoice{NotASecret: true}, args: text,
			want: want{code: errs.InvalidInput}},
		{name: "ask/--not-a-secret --reason no yes", mode: policy.OnDetectAsk, choice: op.SecretChoice{NotASecret: true, Reason: reason}, args: text,
			want: want{code: errs.NeedsConfirm}},
		{name: "ask/--not-a-secret --reason --yes", mode: policy.OnDetectAsk, yes: true, choice: op.SecretChoice{NotASecret: true, Reason: reason}, args: text,
			want: want{meta: map[string]string{"token": quoted("key " + fakeKey + " end")}, allow: "meta.token"}},
		{name: "redact/--not-a-secret --reason terminal", mode: policy.OnDetectRedact, terminal: true, choice: op.SecretChoice{NotASecret: true, Reason: reason}, args: text,
			want: want{meta: map[string]string{"token": quoted("key " + fakeKey + " end")}, allow: "meta.token"}},

		// 19.1: leaf by leaf inside a json value
		{name: "ask/json leaf --secret-unstored", mode: policy.OnDetectAsk, choice: op.SecretChoice{Unstored: true}, args: jsonLeaf,
			want: want{meta: map[string]string{"cfg": `{"n":1,"smtp":{"host":"mail","url":"x ` + refs.Unstored + `"}}`}, redacted: 1}},
		{name: "ask/json leaf --not-a-secret --yes", mode: policy.OnDetectAsk, yes: true, choice: op.SecretChoice{NotASecret: true, Reason: reason}, args: jsonLeaf,
			want: want{meta: map[string]string{"cfg": `{"n":1,"smtp":{"host":"mail","url":"x ` + fakeKey + `"}}`}, allow: "meta.cfg.smtp.url"}},
		{name: "redact/json leaf", mode: policy.OnDetectRedact, args: jsonLeaf,
			want: want{meta: map[string]string{"cfg": `{"n":1,"smtp":{"host":"mail","url":"x ` + refs.Unstored + `"}}`}, redacted: 1}},

		// a member name is an identifier: refused in every mode, every answer
		{name: "ask/member name --secret-unstored", mode: policy.OnDetectAsk, choice: op.SecretChoice{Unstored: true}, args: jsonName, want: want{code: errs.SecretDetected}},
		{name: "redact/member name", mode: policy.OnDetectRedact, args: jsonName, want: want{code: errs.SecretDetected}},
		{name: "ask/member name --yes", mode: policy.OnDetectAsk, yes: true, args: jsonName, want: want{code: errs.SecretDetected}},
		{name: "ask/nested member names --secret-unstored", mode: policy.OnDetectAsk, choice: op.SecretChoice{Unstored: true}, args: nestedName, want: want{code: errs.SecretDetected}},

		// a typed value cannot hold a reference: refused, never half-redacted
		{name: "redact/url value", mode: policy.OnDetectRedact, args: typed, want: want{code: errs.SecretDetected}},
		{name: "ask/url value --secret-unstored", mode: policy.OnDetectAsk, choice: op.SecretChoice{Unstored: true}, args: typed, want: want{code: errs.SecretDetected}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conf := &yesConfirmer{answer: true}
			var hooked int
			f := setup(t, onDetect(t, c.mode), WithConfirmer(conf),
				WithRedactHook(func(_ context.Context, rw RedactedWrite) error { hooked += len(rw.Fields); return nil }))
			ctx := context.Background()
			caller := op.Caller{Actor: testActor, Yes: c.yes, Interactive: c.terminal, Secret: c.choice}
			_, err := f.eng.Run(ctx, memMetaOp(f, metaWrite(t, c.args)), op.ArgsDecoder{Args: []string{"plain body"}}, caller)
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
				for _, ev := range f.audit.events() {
					if r := ev.Secret.Result; strings.Contains(r, fakeKey) || strings.Contains(r, second) {
						t.Fatalf("audit leaks the secret: %s", r)
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
			if len(mems) != 1 || mems[0].Fields["body"].String() != "plain body" {
				t.Fatalf("stored %v", mems)
			}
			got := metaValues(t, mems[0])
			if len(got) != len(c.want.meta) {
				t.Fatalf("meta = %v, want %v", got, c.want.meta)
			}
			for k, v := range c.want.meta {
				if got[k] != v {
					t.Fatalf("meta %s = %s, want %s", k, got[k], v)
				}
			}
			if c.want.allow == "" {
				if len(allows) != 0 {
					t.Fatalf("unexpected allow rows: %v", allows)
				}
			} else if len(allows) != 1 || textOf(allows[0], allowField) != c.want.allow ||
				!strings.HasPrefix(textOf(allows[0], allowReason), reason+" (") || textOf(allows[0], allowRowID) != mems[0].ID.String() {
				t.Fatalf("allow rows = %v, want one on %s", allows, c.want.allow)
			}
			if hooked != c.want.redacted {
				t.Fatalf("D21 hook fields = %d, want %d", hooked, c.want.redacted)
			}
		})
	}
}

// TestMetaSecretOnEdit: an edit whose --meta value holds a secret goes
// through the same flow as a create (write.go's update path): a bare
// --yes under ask leaves the row's metadata unchanged, --secret-unstored
// stores the redacted leaf.
func TestMetaSecretOnEdit(t *testing.T) {
	f := setup(t, WithRedactHook(func(context.Context, RedactedWrite) error { return nil })) // default policy: ask
	ctx := context.Background()
	r := f.memory(t, "plain body")
	mw := metaWrite(t, []metaArg{{Text: "token=key " + fakeKey + " end"}})
	for i, caller := range []op.Caller{{Actor: testActor, Yes: true}, {Actor: testActor, Yes: true, Interactive: true}} {
		_, err := f.eng.Run(ctx, memMetaEditOp(f, mw), op.ArgsDecoder{Args: []string{r.ID.String()}}, caller)
		if !errs.Has(err, errs.NeedsConfirm) || strings.Contains(err.Error(), fakeKey) {
			t.Fatalf("bare --yes #%d: err = %v, want E_NEEDS_CONFIRM without the secret", i, err)
		}
	}
	_, got, err := f.eng.Get(ctx, Ref{ID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Meta) != 0 {
		t.Fatalf("a refused edit stored metadata: %v", metaValues(t, got))
	}
	if _, err = f.eng.Run(ctx, memMetaEditOp(f, mw), op.ArgsDecoder{Args: []string{r.ID.String()}},
		op.Caller{Actor: testActor, Secret: op.SecretChoice{Unstored: true}}); err != nil {
		t.Fatal(err)
	}
	if _, got, err = f.eng.Get(ctx, Ref{ID: r.ID}); err != nil {
		t.Fatal(err)
	}
	if v := metaValues(t, got)["token"]; v != strconv.Quote("key "+refs.Unstored+" end") {
		t.Fatalf("edited meta token = %s", v)
	}
}
