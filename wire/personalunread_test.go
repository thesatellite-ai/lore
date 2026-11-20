package wire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestPlainReadNamesStoreMe pins devco D-771 (hunt H-30): with notes in a
// folder of the personal store (`--to me/alpha`), a read naming no store
// points at --store me. Before it, in a plain folder search, sql and
// memory list refused E_NOT_A_STORE with a hint naming only --store <org>,
// and in a lore repo an empty search said "(no matches)" with no word that
// the personal store's folders were not read. The printed next step then
// answers, and a machine with no personal store is told nothing new.
func TestPlainReadNamesStoreMe(t *testing.T) {
	t.Parallel()
	ctx := background()
	home := t.TempDir()
	bare := filepath.Join(t.TempDir(), "me.git")
	gitIn(t, t.TempDir(), nil, "init", "-q", "--bare", "-b", "main", bare)
	plain := t.TempDir()
	a := newTestAppHome(t, plain, home)
	if _, err := a.InitStore(ctx, opsstores.InitIn{Name: opsstores.PersonalName, URL: bare, RepoOptional: true}); err != nil {
		t.Fatalf("store init me on a bare repo: %v", err)
	}
	add, _ := a.Registry().Lookup("memory.add")
	if _, err := a.Run(ctx, add, op.JSONDecoder{Body: []byte(`{"body":"zebra alpha note","to":"me/alpha","create":true}`)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("memory add --to me/alpha: %v", err)
	}
	setUp := t.TempDir()
	gitInitIn(t, setUp)
	if err := os.MkdirAll(filepath.Join(setUp, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	bareHome := t.TempDir() // no personal store
	run := func(dir, h, name, in string) (op.Result, error) {
		t.Helper()
		b := newTestAppHome(t, dir, h)
		e, ok := b.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		return b.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{})
	}
	reads := []struct{ op, in string }{
		{opssearch.OpRun, `{"query":"zebra"}`},
		{opssql.OpRun, `{"query":"SELECT body FROM memories_all"}`},
		{"memory.search", `{"query":"zebra"}`},
		{"memory.list", `{}`},
	}
	for _, r := range reads {
		// a plain folder: the refusal's hint leads with --store me ...
		_, err := run(plain, home, r.op, r.in)
		var le *errs.Error
		if !errors.As(err, &le) || le.Code != errs.NotAStore || !strings.HasPrefix(le.Hint, "add --store me ") {
			t.Errorf("%s %s in a plain folder: want E_NOT_A_STORE whose hint leads with --store me, got %v", r.op, r.in, err)
		}
		// ... which answers there
		withMe := strings.Replace(r.in, "{", `{"store":"me",`, 1)
		withMe = strings.Replace(withMe, ",}", "}", 1)
		res, err := run(plain, home, r.op, withMe)
		if err != nil {
			t.Fatalf("%s %s from a plain folder: %v", r.op, withMe, err)
		}
		if out, _ := res.JSON(); !strings.Contains(string(out), "alpha note") {
			t.Errorf("%s %s from a plain folder misses the me/alpha note: %s", r.op, withMe, out)
		}
		// no personal store: the hint is as before
		_, err = run(plain, bareHome, r.op, r.in)
		if !errors.As(err, &le) || strings.Contains(le.Hint, "--store me") {
			t.Errorf("%s with no personal store: the hint must not name --store me: %v", r.op, err)
		}
	}
	// a lore repo: an empty plain answer notes the unread folders, in text and --json
	for _, r := range []struct{ op, in string }{
		{opssearch.OpRun, `{"query":"zebra"}`},
		{"memory.search", `{"query":"zebra"}`},
		{"memory.list", `{}`},
		{opssql.OpRun, `{"query":"SELECT body FROM memories"}`},
	} {
		res, err := run(setUp, home, r.op, r.in)
		if err != nil {
			t.Fatalf("%s in a lore repo: %v", r.op, err)
		}
		text, _ := res.Text()
		if !strings.Contains(text, "note: your personal store's folders (me/alpha) were not read; add --store me") {
			t.Errorf("%s in a lore repo: want the unread note, got %q", r.op, text)
		}
		if out, _ := res.JSON(); !strings.Contains(string(out), `"`+PersonalUnreadField+`":`) {
			t.Errorf("%s --json in a lore repo: want %s, got %s", r.op, PersonalUnreadField, out)
		}
	}
	// a read naming its store, a non-empty answer, and no personal store: no note
	for _, c := range []struct{ why, dir, home, op, in string }{
		{"named --store me/alpha", setUp, home, opssearch.OpRun, `{"query":"nothinghere","store":"me/alpha"}`},
		{"a non-empty answer", setUp, home, opssql.OpRun, `{"query":"SELECT count(*) FROM memories"}`},
		{"no personal store", setUp, bareHome, opssearch.OpRun, `{"query":"zebra"}`},
	} {
		res, err := run(c.dir, c.home, c.op, c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.why, err)
		}
		if text, _ := res.Text(); strings.Contains(text, "were not read") {
			t.Errorf("%s: no note expected, got %q", c.why, text)
		}
	}
}

// TestEmptyAnswer pins which texts count as an empty read (the note's
// trigger): a list's or search's "(no …)" line and sql's "(no rows)".
func TestEmptyAnswer(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]bool{
		"(no matches)\n":                  true,
		"(no memories)\n  hint: x\n":      true,
		"body\n(no rows)\n":               true,
		"count(*)\n0\n(1 row)\n":          false,
		"mem_1  (no matches) in a body\n": false,
		"Found 2 hits\n":                  false,
	} {
		if got := emptyAnswer(text); got != want {
			t.Errorf("emptyAnswer(%q) = %v, want %v", text, got, want)
		}
	}
	if got := personalUnreadNote([]string{"me/a", "me/b", "me/c", "me/d"}); !strings.Contains(got, "(me/a, me/b, me/c, …)") || !strings.Contains(got, "--store me ") {
		t.Errorf("personalUnreadNote cuts at %d folders and names --store me: %q", personalUnreadMax, got)
	}
}
