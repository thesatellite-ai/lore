package contextop

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// fakeHost records the input and the hook input it was given.
type fakeHost struct {
	got    FetchIn
	hookIn string
}

func (h *fakeHost) Fetch(_ context.Context, in FetchIn, hookIn io.Reader) (Answer, error) {
	h.got = in
	b, err := io.ReadAll(hookIn)
	if err != nil {
		return Answer{}, err
	}
	h.hookIn = string(b)
	return Answer{Text: "# lore context for x\n", Data: []byte(`[{"id":"rul_1"}]`)}, nil
}

func TestFetchNeedsMode(t *testing.T) {
	_, err := Fetch(context.Background(), &fakeHost{}, FetchIn{MaxBytes: 10}, strings.NewReader(""))
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--for-file <path>") {
		t.Fatalf("no mode: %v", err)
	}
}

// The text is the human form; --json is {"data": …} as in v0.1.17.
func TestFetchTextAndJSON(t *testing.T) {
	h := &fakeHost{}
	out, err := Fetch(context.Background(), h, FetchIn{ForFile: "a.go"}, strings.NewReader("ignored"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Text() != "# lore context for x\n" || h.hookIn != "" {
		t.Fatalf("text %q hook input %q", out.Text(), h.hookIn)
	}
	b, err := json.Marshal(out)
	if err != nil || string(b) != `{"data":[{"id":"rul_1"}]}` {
		t.Fatalf("json: %s %v", b, err)
	}
}

// The hook input comes from stdin with --hook-stdin, from hook_input over
// REST (which wins and implies --hook-stdin), and never from stdin
// without --hook-stdin.
func TestFetchHookInput(t *testing.T) {
	cases := []struct {
		name string
		in   FetchIn
		want string
	}{
		{"stdin", FetchIn{Hook: "claude_code", HookStdin: true}, "from-stdin"},
		{"hook_input", FetchIn{Hook: "claude_code", HookInput: "from-body"}, "from-body"},
		{"no --hook-stdin", FetchIn{Hook: "claude_code"}, ""},
	}
	for _, c := range cases {
		h := &fakeHost{}
		if _, err := Fetch(context.Background(), h, c.in, strings.NewReader("from-stdin")); err != nil {
			t.Fatal(err)
		}
		if h.hookIn != c.want || (c.in.HookInput != "" && !h.got.HookStdin) {
			t.Fatalf("%s: hook input %q, stdin flag %v", c.name, h.hookIn, h.got.HookStdin)
		}
	}
}

type env struct{}

func (env) ContextHost(context.Context) (Host, error) {
	return nil, errs.New(errs.Unsupported, "context: not available in this build")
}

// Local, read; hook_input has no CLI flag; no host is E_UNSUPPORTED.
func TestOpsSpec(t *testing.T) {
	reg, err := op.NewRegistry(Ops(env{})...)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reg.Lookup(OpFetch)
	if !ok || !e.Spec().Local || e.Spec().Permission != op.PermRead {
		t.Fatalf("spec: %v %+v", ok, e.Spec())
	}
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{"topics":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Invoke(context.Background(), in); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("no host: %v", err)
	}
}

// TestFetchMaxBytes: a negative --max-bytes is E_INVALID_INPUT with a
// hint, in every mode, before the host runs; 0 (the default) and a
// positive cap reach the host unchanged.
func TestFetchMaxBytes(t *testing.T) {
	modes := []FetchIn{{ForFile: "a.go"}, {Topics: true}, {ForTask: "tsk_1"}, {Topic: "billing"}, {ForPrompt: "fix it"}, {Hook: "claude_code", HookInput: "{}"}}
	for _, m := range modes {
		for _, n := range []int{-1, -16000, math.MinInt, 0, 1, 16000} {
			in := m
			in.MaxBytes = n
			h := &fakeHost{}
			_, err := Fetch(context.Background(), h, in, strings.NewReader(""))
			if n < 0 {
				e := errs.EnvelopeOf(err).Error
				if !errs.Has(err, errs.InvalidInput) || !strings.Contains(e.Message, "--max-bytes") || e.Hint == "" {
					t.Errorf("%+v: E_INVALID_INPUT naming --max-bytes with a hint, got %v", in, err)
				}
				if h.got != (FetchIn{}) {
					t.Errorf("%+v: refused before the host runs", in)
				}
				continue
			}
			if err != nil || h.got.MaxBytes != n {
				t.Errorf("%+v: reaches the host with the cap, got %v %+v", in, err, h.got)
			}
		}
	}
}
