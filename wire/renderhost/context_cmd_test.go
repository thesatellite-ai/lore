package renderhost

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// plannerFixture is a repo with one row per way `lore context` picks a
// row (later:455): a glob rule, a rule naming the file, a semantic rule, a
// hotfix naming the folder, a tagged rule, two notes sharing the file's
// words, an unrelated rule and a superseded rule that must never show.
type plannerFixture struct {
	a                                       *wire.App
	glob, mention, semantic, hotfix, tagged string
	memory, decision, unrelated, superseded string
}

func newPlannerFixture(t *testing.T) plannerFixture {
	t.Helper()
	ctx := context.Background()
	a, project := repoApp(t)
	mk := func(table row.Table, f map[string]string, extra map[string]value.Value) string {
		t.Helper()
		fs := map[string]value.Value{"project_id": text(t, project), "source_kind": text(t, "manual")}
		for k, s := range f {
			fs[k] = text(t, s)
		}
		for k, v := range extra {
			fs[k] = v
		}
		w, err := a.Create(ctx, store.SourceRepo, table, fs)
		if err != nil {
			t.Fatalf("create %s %v: %v", table, f, err)
		}
		return w.Row.ID.String()
	}
	tag, err := a.Create(ctx, store.SourceRepo, schema.TableTags, map[string]value.Value{"project_id": text(t, project), "name": text(t, "billing")})
	if err != nil {
		t.Fatal(err)
	}
	tags, err := value.ListOf(value.KindText, text(t, tag.Row.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	rule := func(body, activation string, f map[string]string, extra map[string]value.Value) string {
		m := map[string]string{"body": body, "severity": "must", "activation": activation}
		for k, s := range f {
			m[k] = s
		}
		return mk(schema.TableRules, m, extra)
	}
	var fx plannerFixture
	fx.a = a
	fx.glob = rule("Go files use gofmt.", schema.RuleActivationGlob, map[string]string{"globs": `["**/*.go"]`}, nil)
	fx.mention = rule("Touch src/pay/ledger.go only with a ledger test.", schema.RuleActivationAlways, nil, nil)
	fx.semantic = rule("Amounts are integer cents.", schema.RuleActivationSemantic, map[string]string{"applies_to_description": "money ledger amounts"}, nil)
	fx.hotfix = mk(schema.TableHotfixes, map[string]string{"title": "Retry storm", "body": "Cap retries in src/pay/ at 3.", "severity": "high"}, nil)
	fx.tagged = rule("Invoices are immutable.", schema.RuleActivationAlways, nil, map[string]value.Value{schema.FieldTagIDs: tags})
	fx.memory = mk(schema.TableMemories, map[string]string{"kind": schema.MemoryKindCore, "body": "The ledger was rewritten in March."}, nil)
	fx.decision = mk(schema.TableDecisions, map[string]string{"title": "Ledger storage", "body": "We keep the ledger in Postgres.", "status": "accepted"}, nil)
	fx.unrelated = rule("Docs use sentence case.", schema.RuleActivationAlways, nil, nil)
	fx.superseded = rule("Old ledger rule for src/pay/ledger.go.", schema.RuleActivationAlways, nil, map[string]value.Value{renderFieldSuperseded: text(t, fx.mention)})
	return fx
}

// picksOf decodes an answer's --json data.
func picksOf(t *testing.T, data []byte) map[string]contextPick {
	t.Helper()
	var ps []contextPick
	if err := json.Unmarshal(data, &ps); err != nil {
		t.Fatalf("data %s: %v", data, err)
	}
	out := map[string]contextPick{}
	for _, p := range ps {
		out[p.ID] = p
	}
	return out
}

// TestContextPlanner (CONTEXT_BUDGET 6.8, 6.10, later:455) drives the row
// modes of `lore context` through contextAnswer: --for-file picks by glob,
// path mention, folder mention, semantic description and note words, each
// with its why, rules before hotfixes before notes; --topic picks the
// tagged rows only and an unknown topic is E_NOT_FOUND; --for-prompt picks
// the semantic rule first and the word matches; a superseded rule and an
// unrelated rule never show; no mode is E_INVALID_INPUT; a tiny
// --max-bytes keeps the header and the "more:" line and prints fewer
// rows in full.
func TestContextPlanner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	fx := newPlannerFixture(t)
	h := New(Options{App: fx.a})

	cases := []struct {
		name  string
		f     contextFlags
		want  map[string]string // id -> why prefix
		title string
	}{
		{name: "for-file", f: contextFlags{forFile: "src/pay/ledger.go"}, title: "# lore context for src/pay/ledger.go", want: map[string]string{
			fx.glob: "glob **/*.go", fx.mention: "mentions src/pay/ledger.go", fx.hotfix: "mentions src/pay/",
			fx.semantic: "semantic", fx.memory: "words", fx.decision: "words"}},
		{name: "topic", f: contextFlags{topic: "Billing"}, title: "# lore context for topic Billing", want: map[string]string{fx.tagged: "topic Billing"}},
		{name: "for-prompt", f: contextFlags{forPrompt: "how is the ledger stored"}, title: "# lore context for the prompt", want: map[string]string{
			fx.semantic: "semantic ledger", fx.memory: "words", fx.decision: "words", fx.mention: "words"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := c.f
			ans, err := h.contextAnswer(ctx, &f, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := picksOf(t, ans.Data)
			if len(got) != len(c.want) {
				t.Fatalf("picks %+v, want %v", got, c.want)
			}
			for id, why := range c.want {
				if p, ok := got[id]; !ok || !strings.HasPrefix(p.Why, why) || p.Bytes == 0 {
					t.Fatalf("pick %s = %+v, want why %q", id, p, why)
				}
				if !strings.Contains(ans.Text, id) {
					t.Fatalf("text misses %s:\n%s", id, ans.Text)
				}
			}
			for _, never := range []string{fx.superseded, fx.unrelated} {
				if _, ok := got[never]; ok {
					t.Fatalf("picked %s", never)
				}
			}
			if !strings.HasPrefix(ans.Text, c.title+"\n") {
				t.Fatalf("title: %q", ans.Text)
			}
		})
	}

	// Group order in the for-file text: rules, then hotfixes, then notes.
	f := contextFlags{forFile: "src/pay/ledger.go"}
	ans, err := h.contextAnswer(ctx, &f, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, hf, n := strings.Index(ans.Text, "## "+groupRules), strings.Index(ans.Text, "## "+groupHotfixes), strings.Index(ans.Text, "## "+groupNotes)
	if r < 0 || hf <= r || n <= hf {
		t.Fatalf("group order %d %d %d:\n%s", r, hf, n, ans.Text)
	}

	// --for-prompt: the semantic rule ranks above the word matches.
	pf := contextFlags{forPrompt: "how is the ledger stored"}
	if ans, err = h.contextAnswer(ctx, &pf, nil); err != nil {
		t.Fatal(err)
	}
	if s, m := strings.Index(ans.Text, fx.semantic), strings.Index(ans.Text, fx.mention); s < 0 || m < s {
		t.Fatalf("semantic rule not first:\n%s", ans.Text)
	}

	// A tiny budget: header and more line stay, fewer rows in full.
	small := contextFlags{forFile: "src/pay/ledger.go", maxBytes: 700}
	ans, err = h.contextAnswer(ctx, &small, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Text) > 700+contextMoreReserve || !strings.Contains(ans.Text, "\nmore: lore search \"") || strings.Count(ans.Text, "</") >= 6 {
		t.Fatalf("small budget (%d bytes):\n%s", len(ans.Text), ans.Text)
	}

	// Refusals.
	for _, c := range []struct {
		name string
		f    contextFlags
		code errs.Code
	}{
		{"unknown topic", contextFlags{topic: "nope"}, errs.NotFound},
		{"no mode", contextFlags{}, errs.InvalidInput},
	} {
		f := c.f
		if _, err = h.contextAnswer(ctx, &f, nil); errs.CodeOf(err) != c.code {
			t.Fatalf("%s: err %v, want %s", c.name, err, c.code)
		}
	}

	// Nothing applies: the search hint, no rows.
	none := contextFlags{forPrompt: "zzqx"}
	ans, err = h.contextAnswer(ctx, &none, nil)
	if err != nil || !strings.Contains(ans.Text, "(nothing in the store applies here") || len(picksOf(t, ans.Data)) != 0 {
		t.Fatalf("nothing applies: %q %v", ans.Text, err)
	}
}

// runHook runs one hook call and decodes its output ("" = printed nothing).
func runHook(t *testing.T, h *Host, f contextFlags, stdin string) (hookSpecific, string) {
	t.Helper()
	var out bytes.Buffer
	if err := h.runContextHook(context.Background(), &f, strings.NewReader(stdin), &out); err != nil {
		t.Fatalf("hook %s: %v", stdin, err)
	}
	if out.Len() == 0 {
		return hookSpecific{}, ""
	}
	var got hookOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("hook output %q: %v", out.String(), err)
	}
	return got.HookSpecificOutput, out.String()
}

// TestContextHookPlanner (CONTEXT_BUDGET 6.9, later:455): the hook picks
// by the input's file (an absolute path under cwd made relative) or its
// prompt, keeps the input's event name, prints nothing when the input
// names neither; with a session id a second identical call prints nothing
// (the seen set), a new session or no session id gets the rows again; an
// unknown tool, a missing --hook-stdin and bad JSON are errors, and
// through contextAnswer an error prints nothing and is logged to
// ~/.lore/logs/hooks.log.
func TestContextHookPlanner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fx := newPlannerFixture(t)
	h := New(Options{App: fx.a})
	cc := contextFlags{hook: toolprofile.NameClaudeCode, hookStdin: true}

	cwd := t.TempDir()
	abs := filepath.Join(cwd, "src", "pay", "ledger.go")
	in := func(session, extra string) string {
		return `{"session_id":"` + session + `","cwd":` + jsonString(t, cwd) + `,` + extra + `}`
	}
	fileIn := `"tool_input":{"file_path":` + jsonString(t, abs) + `}`

	got, raw := runHook(t, h, cc, in("s1", fileIn))
	if got.HookEventName != "PreToolUse" || !strings.Contains(got.AdditionalContext, fx.glob) || !strings.Contains(got.AdditionalContext, "# lore context for src/pay/ledger.go") {
		t.Fatalf("file hook: %s", raw)
	}
	if _, raw = runHook(t, h, cc, in("s1", fileIn)); raw != "" {
		t.Fatalf("same session again printed %s", raw)
	}
	if got, _ = runHook(t, h, cc, in("s2", fileIn)); !strings.Contains(got.AdditionalContext, fx.glob) {
		t.Fatalf("new session: %+v", got)
	}
	for i := 0; i < 2; i++ {
		if got, _ = runHook(t, h, cc, in("", fileIn)); !strings.Contains(got.AdditionalContext, fx.glob) {
			t.Fatalf("no session id, call %d: %+v", i+1, got)
		}
	}

	got, raw = runHook(t, h, cc, in("p1", `"hook_event_name":"UserPromptSubmit","prompt":"how is the ledger stored"`))
	if got.HookEventName != "UserPromptSubmit" || !strings.Contains(got.AdditionalContext, fx.decision) || !strings.Contains(got.AdditionalContext, "# lore context for the prompt") {
		t.Fatalf("prompt hook: %s", raw)
	}
	if _, raw = runHook(t, h, cc, in("p2", `"hook_event_name":"Stop"`)); raw != "" {
		t.Fatalf("no file and no prompt printed %s", raw)
	}

	for _, c := range []struct {
		name  string
		f     contextFlags
		stdin string
	}{
		{"unknown tool", contextFlags{hook: "vim", hookStdin: true}, `{}`},
		{"no --hook-stdin", contextFlags{hook: toolprofile.NameClaudeCode}, `{}`},
		{"bad json", cc, `{not json`},
	} {
		f := c.f
		var out bytes.Buffer
		if err := h.runContextHook(context.Background(), &f, strings.NewReader(c.stdin), &out); err == nil || out.Len() != 0 {
			t.Fatalf("%s: err %v out %q", c.name, err, out.String())
		}
	}
	bad := cc
	ans, err := h.contextAnswer(context.Background(), &bad, strings.NewReader(`{not json`))
	if err != nil || ans.Text != "" {
		t.Fatalf("hook error through contextAnswer: %+v %v", ans, err)
	}
	log, rerr := readUnder(t, home, filepath.Join(".lore", "logs", "hooks.log"))
	if rerr != nil || !strings.Contains(string(log), "lore context --hook:") {
		t.Fatalf("hooks.log %q %v", log, rerr)
	}
}

// jsonString is s as a JSON string literal.
func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
