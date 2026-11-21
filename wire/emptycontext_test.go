package wire

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	contextop "github.com/thesatellite-ai/lore/ops/context"
)

// fakeContextHost answers every fetch with ans (the empty row fetch of a
// real host: the empty line and, in Read, what it read).
type fakeContextHost struct{ ans contextop.Answer }

func (f fakeContextHost) Fetch(context.Context, contextop.FetchIn, io.Reader) (contextop.Answer, error) {
	return f.ans, nil
}

// TestEmptyContextSaysWhatAndTry pins devco D-824 (hunt H-83, follow-up of
// D-821): `lore context --for-file/--for-prompt/--topic` with nothing
// applying printed a try command with a "<keywords>" placeholder that ran
// nowhere. It now says, as an empty search does, the stores and kinds read,
// how the path, prompt or topic was read, and 1-3 next commands with the
// fetch's --store, in text and in --json (searched, try); an answer holding
// rows gets nothing more.
func TestEmptyContextSaysWhatAndTry(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := t.TempDir()
	gitInitIn(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	empty := "# lore context for x\n\n" + contextop.NothingApplies + "\n"
	run := func(ans contextop.Answer, in string) (string, map[string]json.RawMessage) {
		t.Helper()
		a, err := New(Options{RepoDir: repo, Home: t.TempDir(), Actor: actor, Context: ContextHosts{Fetch: fakeContextHost{ans}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		e, ok := a.Registry().Lookup(contextop.OpFetch)
		if !ok {
			t.Fatal("no context.fetch")
		}
		res, err := a.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{})
		if err != nil {
			t.Fatalf("context %s: %v", in, err)
		}
		text, _ := res.Text()
		raw, err := res.JSON()
		if err != nil {
			t.Fatal(err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		return text, top
	}
	read := func(r contextop.Reading) contextop.Answer {
		r.Stores, r.Kinds = []string{"repo", "acme-co/eng"}, []string{"rule", "hotfix", "memory"}
		return contextop.Answer{Text: empty, Data: []byte("[]"), Read: &r}
	}
	for _, c := range []struct {
		name, in string
		ans      contextop.Answer
		want     []string
		try      []string
	}{
		{"file", `{"for_file":"services/billing/invoice.go"}`,
			read(contextop.Reading{Path: "services/billing/invoice.go", Folders: []string{"services/billing", "services"}, Words: []string{"billing", "invoice"}}),
			[]string{contextop.NothingApplies, "  searched: repo, acme-co/eng (rule, hotfix, memory)", "  read as: path: services/billing/invoice.go (globs; folder scopes services/billing, services; rows naming it or its folder); words: billing, invoice"},
			[]string{`lore search --query "billing invoice"`, "lore rule list", "lore context --topics"}},
		{"generic path", `{"for_file":"main.go"}`, read(contextop.Reading{Path: "main.go"}),
			[]string{"no topic word in the path"}, []string{"lore rule list", "lore context --topics"}},
		{"prompt", `{"for_prompt":"rotate the kafka consumer"}`, read(contextop.Reading{Words: []string{"rotate", "kafka", "consumer"}}),
			[]string{"  read as: words: rotate, kafka, consumer"},
			[]string{`lore search --query "rotate kafka consumer"`, `lore ask --query "rotate kafka consumer"`, "lore rule list"}},
		{"prompt with --store", `{"for_prompt":"kafka","store":"repo"}`, read(contextop.Reading{Words: []string{"kafka"}}),
			[]string{"  searched: repo (rule"},
			[]string{`lore search --query "kafka" --store repo`, `lore ask --query "kafka" --store repo`, "lore rule list --store repo"}},
		{"topic", `{"topic":"billing"}`, read(contextop.Reading{Topic: "billing"}),
			[]string{"  read as: topic: billing\n"}, []string{"lore context --topics", `lore search --query "billing"`}},
	} {
		text, top := run(c.ans, c.in)
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: want %q in\n%s", c.name, w, text)
			}
		}
		if strings.Contains(text, "<") {
			t.Errorf("%s: a placeholder is printed:\n%s", c.name, text)
		}
		var try []string
		if err := json.Unmarshal(top[TryField], &try); err != nil || strings.Join(try, "|") != strings.Join(c.try, "|") {
			t.Errorf("%s --json try: got %s (%v), want %q", c.name, top[TryField], err, c.try)
		}
		for _, cmd := range c.try {
			if !strings.Contains(text, "  try: "+cmd+"\n") {
				t.Errorf("%s: try %q not printed in\n%s", c.name, cmd, text)
			}
		}
		var s emptySearched
		if err := json.Unmarshal(top[SearchedField], &s); err != nil || len(s.Stores) == 0 || len(s.Kinds) == 0 {
			t.Errorf("%s --json searched: %s (%v)", c.name, top[SearchedField], err)
		}
	}
	// an answer holding rows (no Read) says nothing more
	text, top := run(contextop.Answer{Text: "# lore context for x\n\n## Rules that apply here\n\n- rul_x money in cents\n", Data: []byte(`[{"id":"rul_x"}]`)}, `{"for_file":"a.go"}`)
	if strings.Contains(text, "searched:") || top[SearchedField] != nil || top[TryField] != nil {
		t.Errorf("an answer with rows: no searched/try expected, got %q", text)
	}
}
