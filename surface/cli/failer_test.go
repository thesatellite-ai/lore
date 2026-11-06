package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// diagIn / diagOut are an operation whose output is printed in full and
// then fails (op.Failer, as `lore whoami` with an ambiguous org).
type diagIn struct {
	Fail string `json:"fail,omitempty" flag:"fail" desc:"the error code to fail with after printing"`
}

type diagOut struct {
	Report string `json:"report"`
	Error  string `json:"error,omitempty"`
	fail   error
}

func (o diagOut) Text() string   { return "report line\n" }
func (o diagOut) Failure() error { return o.fail }

// directRunner decodes and invokes, nothing else.
type directRunner struct{}

func (directRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, _ op.Caller) (op.Result, error) {
	in, err := e.Decode(d)
	if err != nil {
		return op.Result{}, err
	}
	return e.Invoke(ctx, in)
}

// TestFailerPrintsThenFails (P3-ORG-G-3): a Failer result prints its
// output on stdout, then the error line + hint on stderr, and exits with
// the error's status; with --json it prints exactly one JSON document
// (the data, holding its own error text) and nothing on stderr; a nil
// Failure() is a plain success.
func TestFailerPrintsThenFails(t *testing.T) {
	t.Parallel()
	diag := op.Define(op.Spec{Name: "diag.show", Summary: "Diagnose", Permission: op.PermRead, Access: op.AccessRead},
		func(_ context.Context, in diagIn) (diagOut, error) {
			out := diagOut{Report: "r"}
			if in.Fail != "" {
				out.fail = errs.New(errs.Code(in.Fail), "it is ambiguous").WithHint("pick one")
				out.Error = "it is ambiguous"
			}
			return out, nil
		})
	reg, err := op.NewRegistry(diag)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []errs.Code{errs.OrgAmbiguous, errs.OrgDisagrees, errs.IdentityFailed, errs.Internal} {
		d, ok := errs.Lookup(code)
		if !ok {
			t.Fatalf("%s not registered", code)
		}
		h := execute(Build(reg, directRunner{}), "", "diag", "show", "--fail", string(code))
		if h.exit != d.Exit || h.stdout != "report line\n" || h.stderr != "ERROR: ["+string(code)+"] it is ambiguous\n  hint: pick one\n" {
			t.Fatalf("%s human: exit %d stdout %q stderr %q", code, h.exit, h.stdout, h.stderr)
		}
		j := execute(Build(reg, directRunner{}), "", "diag", "show", "--fail", string(code), "--json")
		dec := json.NewDecoder(strings.NewReader(j.stdout))
		var first map[string]json.RawMessage
		if err := dec.Decode(&first); err != nil {
			t.Fatalf("%s json: %q: %v", code, j.stdout, err)
		}
		if dec.More() || j.exit != d.Exit || j.stderr != "" || string(first["error"]) != `"it is ambiguous"` {
			t.Fatalf("%s json: exit %d stdout %q stderr %q (one document, data.error, the exit)", code, j.exit, j.stdout, j.stderr)
		}
	}
	ok := execute(Build(reg, directRunner{}), "", "diag", "show")
	if ok.exit != 0 || ok.stderr != "" || ok.stdout != "report line\n" {
		t.Fatalf("no failure: exit %d stdout %q stderr %q", ok.exit, ok.stdout, ok.stderr)
	}
}
