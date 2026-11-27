package wire

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/op"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

// TestPersonalRowRefsAcrossFolders pins bug hunt H-283 (devco:D-1021): a
// note in me/archive citing ${row:<id>} of a note kept in me/work gets
// its filled-in text in the me/archive cache, though `memory add --to
// me/archive` opens me/archive alone, so search finds it by the cited
// note's words as a cache built from nothing does; the citation keeps its
// row_refs line while the cited note is in the trash, so `trash restore`
// fills it in again; and a write in me/work learns that the me/archive
// cache cites the row it wrote (siblingsCite). Before it the archive
// cache had no line at all until `lore search rebuild`.
func TestPersonalRowRefsAcrossFolders(t *testing.T) {
	t.Parallel()
	ctx := background()
	home := t.TempDir()
	bare := filepath.Join(t.TempDir(), "me.git")
	gitIn(t, t.TempDir(), nil, "init", "-q", "--bare", "-b", "main", bare)
	dir := t.TempDir()
	a := newTestAppHome(t, dir, home)
	if _, err := a.InitStore(ctx, opsstores.InitIn{Name: opsstores.PersonalName, URL: bare, RepoOptional: true}); err != nil {
		t.Fatalf("store init me on a bare repo: %v", err)
	}
	do := func(name, in string) string {
		t.Helper()
		app := newTestAppHome(t, dir, home)
		e, ok := app.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		res, err := app.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{Yes: true})
		if err != nil {
			t.Fatalf("%s %s: %v", name, in, err)
		}
		out, err := res.JSON()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	memID := regexp.MustCompile(`"id":\s*"(mem_[0-9a-f]+)"`)
	note := func(body, to string) string {
		t.Helper()
		in, err := json.Marshal(struct {
			Body   string `json:"body"`
			To     string `json:"to"`
			Create bool   `json:"create"`
		}{body, to, true})
		if err != nil {
			t.Fatal(err)
		}
		out := do("memory.add", string(in))
		m := memID.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no memory id in %s", out)
		}
		return m[1]
	}
	cited := note("Gateway timeouts are 30 s.", "me/work")
	note("See ${row:"+cited+"} before a deploy.", "me/archive")

	archive := func(when, wantText string) {
		t.Helper()
		refsOut := do(opssql.OpRun, `{"query":"SELECT ref_id FROM row_refs","store":"me/archive"}`)
		if !strings.Contains(refsOut, cited) {
			t.Errorf("[D-1021] %s: me/archive row_refs has no line for the cited me/work note %s:\n%s", when, cited, refsOut)
		}
		exp := do(opssql.OpRun, `{"query":"SELECT text FROM row_expanded","store":"me/archive"}`)
		if !strings.Contains(exp, wantText) {
			t.Errorf("[D-1021] %s: me/archive row_expanded misses %q (the me/work note's text):\n%s", when, wantText, exp)
		}
	}
	archive("after memory add --to me/archive", "See Gateway timeouts are 30 s.")
	do("memory.delete", `{"id":"`+cited+`"}`)
	refsOut := do(opssql.OpRun, `{"query":"SELECT ref_id FROM row_refs","store":"me/archive"}`)
	if !strings.Contains(refsOut, cited) {
		t.Errorf("[D-1021] with the cited note in the trash: me/archive row_refs lost the cited id:\n%s", refsOut)
	}
	do("trash.restore", `{"target":"`+cited+`"}`)
	archive("after trash restore", "See Gateway timeouts are 30 s.")

	// a write that opens me/work alone asks the other folders' files
	var files []string
	if err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (d.Name() == "work.db" || d.Name() == "archive.db") {
			files = append(files, p)
		}
		return err
	}); err != nil || len(files) != 2 {
		t.Fatalf("the me/work and me/archive cache files: %v %v", files, err)
	}
	work := files[0]
	if filepath.Base(work) != "work.db" {
		work = files[1]
	}
	c := newCachesFor(home)
	c.paths[store.Source("me/work")] = work
	if !c.siblingsCite(ctx, "me/work", []string{"mem_0000", cited}) {
		t.Errorf("[D-1021] siblingsCite: the me/archive cache cites %s, not reported", cited)
	}
	if c.siblingsCite(ctx, "me/work", []string{"mem_0000"}) {
		t.Errorf("[D-1021] siblingsCite: an id no cache file cites is reported")
	}
}

// TestCacheExpanderKeepsEveryRowID: every ${row:<id>} a text asks for is
// one of the ids the cache records, filled in or not, so the row arriving
// later re-expands the text (devco:D-1021); refs.Expander alone keeps
// only the ids a value came from.
func TestCacheExpanderKeepsEveryRowID(t *testing.T) {
	t.Parallel()
	const known, missing = "mem_01a00000000070008000000000000001", "mem_01a00000000070008000000000000002"
	rv := refsResolverFunc(func(ref refs.Ref) (refs.Resolved, error) {
		if ref.Target == known {
			return refs.Resolved{Text: "known note"}, nil
		}
		return refs.Resolved{}, refs.ErrUnresolved
	})
	text := "a ${row:" + known + "} and ${row:" + missing + "/title} and ${var:nope}"
	got, ids, err := cacheExpander{rv: rv}.Expand(text)
	if err != nil {
		t.Fatal(err)
	}
	if want := "a known note and ${row:" + missing + "/title} and ${var:nope}"; got != want {
		t.Errorf("expanded = %q, want %q", got, want)
	}
	if strings.Join(ids, ",") != known+","+missing {
		t.Errorf("ids = %v, want both row ids (the missing one too)", ids)
	}
}

// refsResolverFunc adapts a function to refs.Resolver.
type refsResolverFunc func(refs.Ref) (refs.Resolved, error)

// Resolve implements refs.Resolver.
func (f refsResolverFunc) Resolve(ref refs.Ref) (refs.Resolved, error) { return f(ref) }
