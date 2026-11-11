package renderhost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// runOp runs one registered operation with a JSON input and returns the
// row id it answered (data.id or a top-level id; "" when none) or the error.
func runOp(t *testing.T, a *wire.App, name, in string) (string, error) {
	t.Helper()
	e, ok := a.Registry().Lookup(name)
	if !ok {
		t.Fatalf("%s is not registered", name)
	}
	res, err := a.Run(context.Background(), e, op.JSONDecoder{Body: []byte(in)}, op.Caller{})
	if err != nil {
		return "", err
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s answer %s: %v", name, b, err)
	}
	if out.Data.ID != "" {
		return out.Data.ID, nil
	}
	return out.ID, nil
}

// TestContextForTask (CONTEXT_BUDGET 6.8, later:497): --for-task prints
// the task first, then each --ref target (a rule printed with its table
// as kind), then what applies to the file the title names (glob, mention,
// folder mention and semantic picks, each why prefixed "names <path>:"),
// then the notes on the task's words; a superseded rule never shows; an
// unknown task id is E_NOT_FOUND.
func TestContextForTask(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	fx := newPlannerFixture(t)
	h := New(Options{App: fx.a})
	task, err := runOp(t, fx.a, "task.add", `{"title":"Fix rounding in src/pay/ledger.go","body":"see the ledger notes","ref":["`+fx.unrelated+`"]}`)
	if err != nil || task == "" {
		t.Fatalf("task.add %q %v", task, err)
	}
	f := contextFlags{forTask: task}
	ans, err := h.contextAnswer(ctx, &f, nil)
	if err != nil {
		t.Fatal(err)
	}
	var picks []contextPick
	if err = json.Unmarshal(ans.Data, &picks); err != nil {
		t.Fatal(err)
	}
	if len(picks) < 2 || picks[0].ID != task || picks[0].Kind != ctxKindTask || picks[0].Why != "the task" {
		t.Fatalf("first pick %+v", picks)
	}
	if picks[1].ID != fx.unrelated || picks[1].Kind != string(schema.TableRules) || !strings.HasPrefix(picks[1].Why, "ref of the task (") {
		t.Fatalf("ref pick %+v", picks[1])
	}
	want := map[string]string{
		fx.glob: "names src/pay/ledger.go: glob **/*.go", fx.mention: "names src/pay/ledger.go: mentions src/pay/ledger.go",
		fx.hotfix: "names src/pay/ledger.go: mentions src/pay/", fx.semantic: "names src/pay/ledger.go: semantic",
		fx.memory: "words", fx.decision: "words",
	}
	got := map[string]string{}
	for _, p := range picks[2:] {
		got[p.ID] = p.Why
	}
	for id, why := range want {
		if !strings.HasPrefix(got[id], why) {
			t.Fatalf("pick %s why %q, want %q (all %v)", id, got[id], why, got)
		}
	}
	if _, ok := got[fx.superseded]; ok || len(got) != len(want) {
		t.Fatalf("picks %v, want %v", got, want)
	}
	if !strings.HasPrefix(ans.Text, "# lore context for task "+task+"\n\n## "+groupTask+"\n") {
		t.Fatalf("text head:\n%s", ans.Text)
	}

	bad := contextFlags{forTask: "tsk_01a1a5aca7f57659be7320c5f4b700ff"}
	if _, err = h.contextAnswer(ctx, &bad, nil); errs.CodeOf(err) != errs.NotFound {
		t.Fatalf("unknown task: %v", err)
	}
}

// TestRenderPrivateExpansion (SQ3, 13.2, later:497): the private render
// fills a ${var:…} reference in an internal memory (the row file keeps the
// reference), and refuses the whole render, writing nothing, when the
// filled text holds a secret that neither the stored text nor the value
// held alone (the scanner on the expanded text).
func TestRenderPrivateExpansion(t *testing.T) {
	ctx := context.Background()
	a, repo := privApp(t)
	home := t.TempDir()
	h := New(Options{App: a, Home: home})
	if _, err := runOp(t, a, "vars.set", `{"name":"db_host","value":"pgmain.internal.example","to":"inner"}`); err != nil {
		t.Fatal(err)
	}
	mem, err := runOp(t, a, "memory.add", `{"body":"The primary database is ${var:db_host}.","to":"inner"}`)
	if err != nil {
		t.Fatal(err)
	}
	var w strings.Builder
	if err = h.renderPrivate(ctx, &w, repo, false, false); err != nil {
		t.Fatal(err)
	}
	p, err := h.planPrivate(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(p.dir, privTestInnerID+privateFileExt)
	body, err := readUnder(t, p.dir, privTestInnerID+privateFileExt)
	if err != nil || !strings.Contains(string(body), "The primary database is pgmain.internal.example.") || strings.Contains(string(body), "${var:db_host}") {
		t.Fatalf("private file %q %v", body, err)
	}
	mid, err := ids.ParseAny(mem)
	if err != nil {
		t.Fatal(err)
	}
	if _, r, gerr := a.Get(ctx, engine.Ref{ID: mid}); gerr != nil || renderField(r, renderFieldBody) != "The primary database is ${var:db_host}." {
		t.Fatalf("row file lost its reference: %q %v", renderField(r, renderFieldBody), gerr)
	}

	// A key prefix in the text and the rest in a variable: neither is a
	// secret alone, the filled text is (built at run time, no key in source).
	if _, err = runOp(t, a, "vars.set", `{"name":"key_tail","value":"`+stripeTail+`","to":"inner"}`); err != nil {
		t.Fatal(err)
	}
	if _, err = runOp(t, a, "memory.add", `{"body":"Billing calls use `+stripePrefix+`${var:key_tail} in staging.","to":"inner"}`); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	w.Reset()
	err = h.renderPrivate(ctx, &w, repo, false, false)
	if err == nil || !strings.Contains(err.Error(), "${var:key_tail}") {
		t.Fatalf("render with a secret behind a reference: %v\n%s", err, w.String())
	}
	if _, serr := os.Stat(file); !os.IsNotExist(serr) {
		t.Fatalf("refused render wrote the store file: %v", serr)
	}
}

// The two halves of a Stripe-shaped live key (scanner test input only).
var (
	stripePrefix = "sk_" + "live_"
	stripeTail   = "4eC39HqLyjWDarjtT1zd" + "p7dcQx"
)
