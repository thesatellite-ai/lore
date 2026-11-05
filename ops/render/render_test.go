package render

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

type fakeHost struct {
	got RunIn
	ans Answer
}

func (h *fakeHost) Render(_ context.Context, in RunIn) (Answer, error) {
	h.got = in
	return h.ans, nil
}

func TestTargetsAndOut(t *testing.T) {
	in := RunIn{Target: []string{"codex, cursor", "", "CLAUDE.md"}}
	got := in.Targets()
	if len(got) != 3 || got[0] != "codex" || got[1] != "cursor" || got[2] != "CLAUDE.md" {
		t.Fatalf("targets: %q", got)
	}
	if in.OutPath() != DefaultOut || (RunIn{Out: "x.md"}).OutPath() != "x.md" {
		t.Fatal("out path")
	}
	for _, ro := range []RunIn{{Check: true}, {Report: true}, {Trend: true}, {DryRun: true}} {
		if !ro.ReadOnly() {
			t.Fatalf("%+v should be read only", ro)
		}
	}
	if (RunIn{NoPointer: true}).ReadOnly() {
		t.Fatal("a plain render writes")
	}
}

func TestScopesNeedsReport(t *testing.T) {
	h := &fakeHost{}
	if _, err := Run(context.Background(), h, RunIn{Scopes: true}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("--scopes alone: %v", err)
	}
	if _, err := Run(context.Background(), h, RunIn{Scopes: true, Report: true}); err != nil || !h.got.Scopes {
		t.Fatalf("--report --scopes: %v %+v", err, h.got)
	}
}

// --report --json is the report object itself (v0.1.17); a write run is
// {"output": …}; the human form is the lines.
func TestJSONShapes(t *testing.T) {
	h := &fakeHost{ans: Answer{Text: "report text\n", Data: []byte(`{"budget":16000}`)}}
	out, err := Run(context.Background(), h, RunIn{Report: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil || string(b) != `{"budget":16000}` || out.Text() != "report text\n" {
		t.Fatalf("report: %s %v %q", b, err, out.Text())
	}
	h.ans = Answer{Text: "✓ .lore/LORE.md (10 bytes)\n"}
	out, err = Run(context.Background(), h, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(out)
	if err != nil || string(b) != `{"output":"✓ .lore/LORE.md (10 bytes)\n"}` {
		t.Fatalf("write: %s %v", b, err)
	}
}

type env struct{}

func (env) RenderHost(context.Context) (Host, error) {
	return nil, errs.New(errs.Unsupported, "render: not available in this build")
}

func TestOpsSpec(t *testing.T) {
	reg, err := op.NewRegistry(Ops(env{})...)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reg.Lookup(OpRun)
	if !ok || !e.Spec().Local || e.Spec().Permission != op.PermWrite {
		t.Fatalf("spec: %v %+v", ok, e.Spec())
	}
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{"report":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Invoke(context.Background(), in); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("no host: %v", err)
	}
}
