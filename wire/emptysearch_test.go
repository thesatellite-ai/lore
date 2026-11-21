package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opsask "github.com/thesatellite-ai/lore/ops/ask"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestEmptySearchSaysWhatAndTry pins devco D-821 (hunt H-77, owner HIGH):
// a search, an ask and a <kind> search that find nothing say which stores
// and kinds they read, how the query was read, and 1-3 next commands, in
// text and in --json (searched, try). Before it they printed "(no
// matches)" (ask: one fixed sentence) and an empty list, so an agent could
// not tell an empty store from a search that read the wrong place.
func TestEmptySearchSaysWhatAndTry(t *testing.T) {
	t.Parallel()
	ctx := background()
	home := t.TempDir()
	bare := filepath.Join(t.TempDir(), "me.git")
	gitIn(t, t.TempDir(), nil, "init", "-q", "--bare", "-b", "main", bare)
	a := newTestAppHome(t, t.TempDir(), home)
	if _, err := a.InitStore(ctx, opsstores.InitIn{Name: opsstores.PersonalName, URL: bare, RepoOptional: true}); err != nil {
		t.Fatalf("store init me on a bare repo: %v", err)
	}
	add, _ := a.Registry().Lookup("memory.add")
	if _, err := a.Run(ctx, add, op.JSONDecoder{Body: []byte(`{"body":"zebra alpha note","to":"me/alpha","create":true}`)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("memory add --to me/alpha: %v", err)
	}
	repo := t.TempDir()
	gitInitIn(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	run := func(name, in string) (string, map[string]json.RawMessage) {
		t.Helper()
		b := newTestAppHome(t, repo, home)
		e, ok := b.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		res, err := b.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{})
		if err != nil {
			t.Fatalf("%s %s: %v", name, in, err)
		}
		text, _ := res.Text()
		raw, err := res.JSON()
		if err != nil {
			t.Fatalf("%s %s --json: %v", name, in, err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatalf("%s %s --json: %v: %s", name, in, err, raw)
		}
		return text, top
	}
	for _, c := range []struct {
		op, in string
		want   []string // lines the text must hold
	}{
		{opssearch.OpRun, `{"query":"kubernetes migration"}`, []string{"(no matches)", "  searched: ", "(every kind)", "  read as: words: kubernetes, migration", `  try: lore search --query "kubernetes migration" --store me`, `  try: lore ask --query "kubernetes migration"`}},
		{opssearch.OpRun, `{"query":"zebra","tables":["rule"]}`, []string{"(rule)", `  try: lore search --query "zebra"` + "\n"}},
		{"memory.search", `{"query":"kubernetes"}`, []string{"(memory)", `  try: lore memory search --query "kubernetes" --store me`, `  try: lore search --query "kubernetes"` + "\n"}},
		{"memory.search", `{"query":"kubernetes","store":"me/alpha"}`, []string{"  searched: me/alpha (memory)", `  try: lore memory search --query "kubernetes" --store me` + "\n", `  try: lore search --query "kubernetes" --store me/alpha`}},
		{opsask.OpRun, `{"query":"who is bob-zhao"}`, []string{opsask.NoAnswer, "  read as: words: bob-zhao; question: who is", `  try: lore search --query "bob-zhao"`, "  try: lore resource list"}},
		{opsask.OpRun, `{"query":"what happened yesterday","store":"me"}`, []string{"  searched: me, me/alpha", "question: what happened on a date; dates: ", `  try: lore ask --query "what happened last month" --store me`}},
	} {
		text, top := run(c.op, c.in)
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s %s: want %q in\n%s", c.op, c.in, w, text)
			}
		}
		var s emptySearched
		var try []string
		if err := json.Unmarshal(top[SearchedField], &s); err != nil || len(s.Stores) == 0 || len(s.Kinds) == 0 {
			t.Errorf("%s %s --json: want %s with stores and kinds, got %s (%v)", c.op, c.in, SearchedField, top[SearchedField], err)
		}
		if err := json.Unmarshal(top[TryField], &try); err != nil || len(try) == 0 || len(try) > emptyTryMax {
			t.Errorf("%s %s --json: want 1-%d %s commands, got %s (%v)", c.op, c.in, emptyTryMax, TryField, top[TryField], err)
		}
		for _, cmd := range try {
			if !strings.Contains(text, "  try: "+cmd+"\n") {
				t.Errorf("%s %s: --json try %q is not printed in the text", c.op, c.in, cmd)
			}
		}
	}
	// an answer that finds something says nothing more
	text, top := run(opssearch.OpRun, `{"query":"zebra","store":"me"}`)
	if strings.Contains(text, "searched:") || top[SearchedField] != nil || top[TryField] != nil {
		t.Errorf("a search with a hit: no searched/try expected, got %q %s", text, top[SearchedField])
	}
}

// TestShellArg pins the quoting of a query in a printed command: double
// quotes, or single quotes when double quotes would expand or end on it.
func TestShellArg(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"orbit deploy":   `"orbit deploy"`,
		`say "hi"`:       `'say "hi"'`,
		"cost $5":        `'cost $5'`,
		`it's "a" thing`: `'it'\''s "a" thing'`,
	} {
		if got := shellArg(in); got != want {
			t.Errorf("shellArg(%q) = %s, want %s", in, got, want)
		}
	}
}
