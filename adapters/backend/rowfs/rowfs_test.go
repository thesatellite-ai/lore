package rowfs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

const memories row.Table = "memories"

// gen builds rows with one test clock.
type gen struct {
	t     *testing.T
	clock *hlc.Clock
}

func newGen(t *testing.T) *gen {
	t.Helper()
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	return &gen{t: t, clock: hlc.NewClock(actor, time.Now)}
}

func (g *gen) row(id ids.AnyID, body string, prev hlc.HLC) store.Row {
	g.t.Helper()
	v, err := value.TextOf(body)
	if err != nil {
		g.t.Fatal(err)
	}
	stamp := g.clock.Now(prev)
	r, err := store.FromRow(row.Row{Table: memories, ID: id, HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{"body": v}})
	if err != nil {
		g.t.Fatal(err)
	}
	return r
}

func newID(t *testing.T) ids.AnyID {
	t.Helper()
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	return id.Any()
}

func openRoot(t *testing.T, opts ...Option) *Root {
	t.Helper()
	r, err := Open(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func folder(t *testing.T, r *Root, name string) *Folder {
	t.Helper()
	f, err := r.Folder(name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func apply(t *testing.T, f *Folder, changes ...store.Change) *Applied {
	t.Helper()
	a, err := f.Apply(context.Background(), changes)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return a
}

func TestCleanFolder(t *testing.T) {
	t.Parallel()
	good := map[string]string{"": FolderRoot, ".": FolderRoot, "org": "org", "acme-hub": "acme-hub", "team/web_2": "team/web_2"}
	for in, want := range good {
		got, err := CleanFolder(in)
		if err != nil || got != want {
			t.Errorf("CleanFolder(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"..", "../x", "/abs", "a/../b", ".lore", "a/.git", `a\b`, "a//b", "a/", " x", "./a", "é"} {
		if _, err := CleanFolder(bad); !errs.Has(err, errs.BadPath) {
			t.Errorf("CleanFolder(%q): err = %v, want E_BAD_PATH", bad, err)
		}
	}
}

// TestStoreDirReserved (D-ad): _store, where a store keeps its root
// folder's rows, can never be a folder (no segment may start with _), and
// the refusal says so with the reserved names.
func TestStoreDirReserved(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{StoreDataDir, "_drafts", "team/_store"} {
		_, err := CleanFolder(bad)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.BadPath {
			t.Fatalf("CleanFolder(%q): err = %v, want E_BAD_PATH", bad, err)
		}
		if !strings.Contains(e.Hint, StoreDataDir+"/ holds the store's own rows") || !strings.Contains(e.Hint, "tasks") {
			t.Errorf("CleanFolder(%q): hint %q does not name _store/ and the reserved names", bad, e.Hint)
		}
	}
}

// TestFolderNamesNotTables (D-d): no folder segment may be a table name —
// inside a store a folder's rows sit at <folder>/<table>/<id>.json, so a
// folder called "tasks" could not be told from a table folder. The refusal
// is E_BAD_PATH naming the segment, the reserved names and a next step.
func TestFolderNamesNotTables(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"tasks", "memories", "team/tasks", "rules/web", "a/b/teams"} {
		_, err := CleanFolder(bad)
		if !errs.Has(err, errs.BadPath) {
			t.Fatalf("CleanFolder(%q): err = %v, want E_BAD_PATH", bad, err)
		}
		env := errs.EnvelopeOf(err).Error
		if !strings.Contains(env.Message, "the name of a table") || !strings.Contains(env.Hint, "pick another name") || !strings.Contains(env.Hint, "memories, ") {
			t.Errorf("CleanFolder(%q) = %q / %q: want the cause, the reserved list and the next step", bad, env.Message, env.Hint)
		}
	}
	names := ReservedFolderNames()
	if !slices.IsSorted(names) || !slices.Contains(names, "tasks") || !slices.Contains(names, "rules") || slices.Contains(names, "org") {
		t.Fatalf("reserved names %v: want every table name, sorted, and no plain folder name", names)
	}
	for _, ok := range []string{"tasks-notes", "my_tasks", "org", "team/web"} {
		if _, err := CleanFolder(ok); err != nil {
			t.Errorf("CleanFolder(%q): %v", ok, err)
		}
	}
}

func TestParseRowPath(t *testing.T) {
	t.Parallel()
	id := newID(t)
	table, got, ok := ParseRowPath(RowPath(memories, id))
	if !ok || table != memories || got != id {
		t.Fatalf("round trip: %v %v %v", table, got, ok)
	}
	for _, bad := range []string{"memories/x.json", "Memories/" + id.String() + ".json", "memories/" + id.String() + ".txt", id.String() + ".json", "a/b/" + id.String() + ".json", "_meta.json"} {
		if _, _, ok := ParseRowPath(bad); ok {
			t.Errorf("ParseRowPath(%q) accepted", bad)
		}
	}
}

// TestBytesOnDisk proves canonical bytes in = bytes on disk, and that root
// and org folders keep separate rows.
func TestBytesOnDisk(t *testing.T) {
	t.Parallel()
	r, g := openRoot(t), newGen(t)
	root, org := folder(t, r, FolderRoot), folder(t, r, FolderOrg)
	a := g.row(newID(t), "root row", hlc.HLC{})
	b := g.row(newID(t), "org row", hlc.HLC{})
	applied := apply(t, root, store.Change{Row: a})
	apply(t, org, store.Change{Row: b})
	if want := StoreDataDir + "/" + RowPath(memories, a.ID); applied.Paths[0] != want {
		t.Fatalf("path %q, want %q", applied.Paths[0], want)
	}
	disk, err := os.ReadFile(filepath.Join(r.Dir(), StoreDataDir, "memories", a.ID.String()+".json"))
	if err != nil || !bytes.Equal(disk, a.Doc) {
		t.Fatalf("file on disk differs from the document: %v", err)
	}
	if _, err = os.Stat(filepath.Join(r.Dir(), "org", "memories", b.ID.String()+".json")); err != nil {
		t.Fatalf("org row not under org/: %v", err)
	}
	if _, err = os.Stat(filepath.Join(r.Dir(), LoreDir)); err == nil {
		t.Fatal("a store write made a .lore folder")
	}
	if _, err = root.Get(memories, b.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("org row visible in the root folder: %v", err)
	}
	rows, _, err := org.Read(context.Background(), "")
	if err != nil || len(rows) != 1 || rows[0].ID != b.ID {
		t.Fatalf("org read: %v %v", rows, err)
	}
}

// TestApplyRestoresOnFailure blocks the second file of a batch (its table
// folder is a regular file) and checks the first file is put back.
func TestApplyRestoresOnFailure(t *testing.T) {
	t.Parallel()
	r, g := openRoot(t), newGen(t)
	f := folder(t, r, FolderRoot)
	first := g.row(newID(t), "v1", hlc.HLC{})
	apply(t, f, store.Change{Row: first})
	if err := os.WriteFile(filepath.Join(f.dataDir, "rules"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	next := g.row(first.ID, "v2", first.HLC)
	fresh := g.row(newID(t), "new", hlc.HLC{})
	doc, err := row.Decode(fresh.Doc)
	if err != nil {
		t.Fatal(err)
	}
	doc.Table = "rules"
	blocked, err := store.FromRow(doc)
	if err != nil {
		t.Fatal(err)
	}
	created := g.row(newID(t), "created then removed", hlc.HLC{})
	_, err = f.Apply(context.Background(), []store.Change{{Row: next, Expect: first.HLC}, {Row: created}, {Row: blocked}})
	if err == nil {
		t.Fatal("a batch with an unwritable file succeeded")
	}
	got, err := f.Get(memories, first.ID)
	if err != nil || !bytes.Equal(got.Doc, first.Doc) {
		t.Fatalf("replaced file not restored: %v", err)
	}
	if _, err := f.Get(memories, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("created file not removed: %v", err)
	}
}

func TestApplyStaleWritesNothing(t *testing.T) {
	t.Parallel()
	r, g := openRoot(t), newGen(t)
	f := folder(t, r, FolderRoot)
	a := g.row(newID(t), "a", hlc.HLC{})
	apply(t, f, store.Change{Row: a})
	fresh := g.row(newID(t), "fresh", hlc.HLC{})
	if _, err := f.Apply(context.Background(), []store.Change{{Row: fresh}, {Row: g.row(a.ID, "b", a.HLC), Expect: g.clock.Now()}}); !errors.Is(err, store.ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	if _, err := f.Get(memories, fresh.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("half a batch written: %v", err)
	}
}

func TestValidateBatch(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	good := g.row(newID(t), "x", hlc.HLC{})
	other := g.row(newID(t), "y", hlc.HLC{})
	indented := good
	indented.Doc = append([]byte(" "), good.Doc...)
	mismatch := good
	mismatch.ID = other.ID
	big := good
	big.Doc = bytes.Repeat([]byte("x"), MaxFileSize+1)
	cases := map[string]struct {
		changes []store.Change
		code    errs.Code
	}{
		"not canonical":   {[]store.Change{{Row: indented}}, errs.InvalidInput},
		"header mismatch": {[]store.Change{{Row: mismatch}}, errs.InvalidInput},
		"too large":       {[]store.Change{{Row: big}}, errs.BodyTooLarge},
		"no hlc":          {[]store.Change{{Row: store.Row{Table: memories, ID: good.ID, Doc: good.Doc}}}, errs.InvalidInput},
	}
	for name, c := range cases {
		if err := ValidateBatch(c.changes); !errs.Has(err, c.code) {
			t.Errorf("%s: err = %v, want %s", name, err, c.code)
		}
	}
	if err := ValidateBatch([]store.Change{{Row: good}, {Row: good}}); !errors.Is(err, store.ErrBatch) {
		t.Errorf("duplicate: err = %v", err)
	}
	if err := ValidateBatch([]store.Change{{Row: good}, {Row: other}}); err != nil {
		t.Errorf("good batch refused: %v", err)
	}
}

// TestSymlinksRefused links the data folder, a table folder and a row file
// and checks no read or write follows any of them.
func TestSymlinksRefused(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	outside := t.TempDir()

	t.Run("data folder", func(t *testing.T) {
		r := openRoot(t)
		if err := os.Symlink(outside, filepath.Join(r.Dir(), FolderOrg)); err != nil {
			t.Fatal(err)
		}
		f := folder(t, r, FolderOrg)
		if _, err := f.Apply(context.Background(), []store.Change{{Row: g.row(newID(t), "x", hlc.HLC{})}}); !ErrSymlink(err) {
			t.Fatalf("write through a linked data folder: %v", err)
		}
		if _, _, err := f.Read(context.Background(), ""); !ErrSymlink(err) {
			t.Fatalf("read through a linked data folder: %v", err)
		}
	})
	t.Run("row file", func(t *testing.T) {
		r := openRoot(t)
		f := folder(t, r, FolderRoot)
		a := g.row(newID(t), "x", hlc.HLC{})
		apply(t, f, store.Change{Row: a})
		target := filepath.Join(outside, "row.json")
		if err := os.WriteFile(target, a.Doc, 0o600); err != nil {
			t.Fatal(err)
		}
		linked := g.row(newID(t), "y", hlc.HLC{})
		if err := os.Symlink(target, filepath.Join(f.dataDir, filepath.FromSlash(RowPath(memories, linked.ID)))); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Get(memories, linked.ID); !ErrSymlink(err) {
			t.Fatalf("get through a linked file: %v", err)
		}
		if _, err := f.Apply(context.Background(), []store.Change{{Row: linked}}); !ErrSymlink(err) {
			t.Fatalf("write over a linked file: %v", err)
		}
		rows, _, err := f.Read(context.Background(), "")
		if err != nil || len(rows) != 1 || rows[0].ID != a.ID {
			t.Fatalf("read must skip the link only: %v %v", rows, err)
		}
		problems, err := f.Problems(context.Background())
		if err != nil || len(problems) != 1 || problems[0].Reason != "symlink ignored" {
			t.Fatalf("problems = %v %v", problems, err)
		}
	})
}

func TestSizeCapOnRead(t *testing.T) {
	t.Parallel()
	r := openRoot(t)
	f := folder(t, r, FolderRoot)
	id := newID(t)
	full := filepath.Join(f.dataDir, filepath.FromSlash(RowPath(memories, id)))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, bytes.Repeat([]byte(" "), MaxFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(memories, id); !errs.Has(err, errs.BodyTooLarge) {
		t.Fatalf("oversized file: %v", err)
	}
}

// TestReadStatCursor walks the stat cursor: unchanged files are not
// returned, a rewrite with equal bytes is not a change, a same-size edit
// that keeps the mtime is caught inside the racy window and (proving the
// stat cache is really used) not caught outside it.
func TestReadStatCursor(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := func() time.Time { return now }
	r, g, ctx := openRoot(t, WithClock(clock)), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	a := g.row(newID(t), "aaaa", hlc.HLC{})
	apply(t, f, store.Change{Row: a})
	rows, cur, err := f.Read(ctx, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("first read: %d %v", len(rows), err)
	}
	full := filepath.Join(f.dataDir, filepath.FromSlash(RowPath(memories, a.ID)))
	if err = os.WriteFile(full, a.Doc, 0o600); err != nil { // same bytes, new mtime
		t.Fatal(err)
	}
	if rows, cur, err = f.Read(ctx, cur); err != nil || len(rows) != 0 {
		t.Fatalf("same bytes reported as a change: %d %v", len(rows), err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	b := g.row(a.ID, "bbbb", a.HLC)
	b2 := g.row(a.ID, "cccc", b.HLC)
	if len(b.Doc) != len(a.Doc) || len(b2.Doc) != len(a.Doc) {
		t.Fatal("test rows must keep the size")
	}
	sameStat := func(doc []byte) {
		t.Helper()
		if err = os.WriteFile(full, doc, 0o600); err != nil {
			t.Fatal(err)
		}
		if err = os.Chtimes(full, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	sameStat(b.Doc)
	rows, cur, err = f.Read(ctx, cur)
	if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].Doc, b.Doc) {
		t.Fatalf("racy same-stat edit missed: %d %v", len(rows), err)
	}
	now = now.Add(time.Hour) // far past the racy window
	_, cur, err = f.Read(ctx, cur)
	if err != nil {
		t.Fatal(err)
	}
	sameStat(b2.Doc)
	if rows, _, err = f.Read(ctx, cur); err != nil || len(rows) != 0 {
		t.Fatalf("outside the racy window a same-stat file must come from the cursor: %d %v", len(rows), err)
	}
}

func TestMissingIsNotADelete(t *testing.T) {
	t.Parallel()
	r, g, ctx := openRoot(t), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	a, b := g.row(newID(t), "a", hlc.HLC{}), g.row(newID(t), "b", hlc.HLC{})
	apply(t, f, store.Change{Row: a}, store.Change{Row: b})
	_, cur, err := f.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(f.dataDir, filepath.FromSlash(RowPath(memories, a.ID)))); err != nil {
		t.Fatal(err)
	}
	rows, _, err := f.Read(ctx, cur)
	if err != nil || len(rows) != 0 {
		t.Fatalf("a vanished file was reported: %v %v", rows, err)
	}
	missing, err := f.Missing(ctx, cur)
	if err != nil || len(missing) != 1 || missing[0] != RowPath(memories, a.ID) {
		t.Fatalf("missing = %v %v", missing, err)
	}
	if none, err := f.Missing(ctx, "garbage"); err != nil || len(none) != 0 {
		t.Fatalf("unreadable cursor: %v %v", none, err)
	}
}

// TestControlAndStrayFiles: a data folder's control files, OS clutter and
// in-flight tmp files are never rows nor problems; a bad row file is a
// problem in both layouts. A code repo's .lore/data is lore's alone, so a
// stray file or folder there is a problem too; a store folder also holds
// the folders below it and a person's own files, so only its table
// folders are read and the rest is left alone.
func TestControlAndStrayFiles(t *testing.T) {
	t.Parallel()
	for _, l := range []Layout{LayoutStore, LayoutRepo} {
		t.Run(l.String(), func(t *testing.T) { testControlAndStrayFiles(t, l) })
	}
}

func testControlAndStrayFiles(t *testing.T, l Layout) {
	r, g, ctx := openRoot(t, WithLayout(l)), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	a := g.row(newID(t), "a", hlc.HLC{})
	apply(t, f, store.Change{Row: a})
	other := newID(t)
	writes := map[string][]byte{
		MetaFileName:                     []byte(`{"v":1}`),
		PurgedFileName:                   []byte(`[]`),
		".DS_Store":                      nil,
		"memories/.DS_Store":             nil,
		"stray.txt":                      []byte("x"),
		"memories/notes.json":            []byte("{}"),
		RowPath(memories, other):         a.Doc, // a document under the wrong id
		"Bad-Table/" + other.String():    nil,
		"memories/" + tmpPrefix + "live": []byte("in flight"),
	}
	for rel, b := range writes {
		full := filepath.Join(f.dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rows, _, err := f.Read(ctx, "")
	if err != nil || len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatalf("read = %v %v", rows, err)
	}
	problems, err := f.Problems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range problems {
		got[p.Path] = true
	}
	want := []string{"memories/notes.json", RowPath(memories, other)}
	if l == LayoutRepo {
		want = append(want, "stray.txt", "Bad-Table")
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("problem %q not reported (got %v)", w, problems)
		}
	}
	if len(problems) != len(want) {
		t.Errorf("problems = %v, want exactly %v", problems, want)
	}
}

func TestLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	one, err := Open(dir, WithLockTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	two, err := Open(dir, WithLockTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	held, err := one.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = two.Lock(ctx); !errors.Is(err, store.ErrLocked) || !errs.Has(err, errs.StoreLocked) {
		t.Fatalf("second holder: err = %v, want ErrLocked", err)
	}
	if _, err = one.Lock(ctx); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("same root, second writer: err = %v, want ErrLocked", err)
	}
	if err = held.Release(); err != nil {
		t.Fatal(err)
	}
	if err = held.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	again, err := two.Lock(ctx)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := one.Lock(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, StoreStateDir, LockFileName)); err != nil {
		t.Fatalf("a store's lock file not at .lore-state/lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, LoreDir)); err == nil {
		t.Fatal("a store's lock made a .lore folder")
	}
	repo := t.TempDir()
	rr, err := Open(repo, WithLayout(LayoutRepo))
	if err != nil {
		t.Fatal(err)
	}
	l, err := rr.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, LoreDir, LockFileName)); err != nil {
		t.Fatalf("a code repo's lock file not at .lore/lock: %v", err)
	}
}

func TestMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := ReadMarker(dir); !errors.Is(err, store.ErrNotAStore) {
		t.Fatalf("no marker: %v", err)
	}
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	want := Marker{ID: id, Visibility: VisibilityShareable, Created: "2026-11-04", OrgFolder: FolderOrg}
	if err = WriteMarker(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMarker(dir)
	want.Version = MarkerVersion
	if err != nil || got != want {
		t.Fatalf("round trip: %+v %v, want %+v", got, err, want)
	}
	// D-e: a new store names no org folder; only the store that holds the
	// org folder does (written with its lore-org.yaml).
	nm, err := NewMarker(time.Now())
	if err != nil || nm.OrgFolder != "" {
		t.Fatalf("NewMarker org folder %q %v, want none", nm.OrgFolder, err)
	}
	if enc, eerr := nm.Encode(); eerr != nil || strings.Contains(string(enc), "org_folder") {
		t.Fatalf("new marker:\n%s %v", enc, eerr)
	}
	old, err := ParseMarker([]byte("lore_store: 1\ncreated: 2026-01-02\n"))
	if err != nil || old.Visibility != VisibilityInternal || !old.ID.IsZero() {
		t.Fatalf("v0.1.17 marker: %+v %v", old, err)
	}
	bad := map[string]errs.Code{
		"lore_store: 0\n":                         errs.ConfigInvalid,
		"lore_store: 9\n":                         errs.OldLore,
		"lore_store: 2\nvisibility: public\n":     errs.ConfigInvalid,
		"lore_store: 2\nid: mem_0123\n":           errs.ConfigInvalid,
		"lore_store: [\n":                         errs.ConfigInvalid,
		"lore_store: 2\nid: " + newID(t).String(): errs.ConfigInvalid,
	}
	for in, code := range bad {
		if _, err := ParseMarker([]byte(in)); !errs.Has(err, code) {
			t.Errorf("ParseMarker(%q): err = %v, want %s", in, err, code)
		}
	}
	if _, err := (Marker{}).Encode(); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("marker without id encoded: %v", err)
	}
}

func TestCheckLocal(t *testing.T) {
	t.Parallel()
	if err := CheckLocal(t.TempDir()); err != nil {
		t.Fatalf("local temp dir refused: %v", err)
	}
	for _, p := range []string{"/home/x/Dropbox/store", "/Users/x/Library/Mobile Documents/com~apple~CloudDocs/s", "/home/x/OneDrive/a"} {
		if err := CheckLocal(p); !errs.Has(err, errs.NetworkFS) {
			t.Errorf("CheckLocal(%q) = %v, want E_NETWORK_FS", p, err)
		}
	}
}

func TestOpenRefusals(t *testing.T) {
	t.Parallel()
	if _, err := Open("relative/path"); !errs.Has(err, errs.BadPath) {
		t.Errorf("relative: %v", err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "absent")); !errs.Has(err, errs.NotAStore) {
		t.Errorf("absent: %v", err)
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(file); !errs.Has(err, errs.NotAStore) {
		t.Errorf("file: %v", err)
	}
	if _, err := openRoot(t).Folder("../escape"); !errs.Has(err, errs.BadPath) || !strings.Contains(err.Error(), "folder") {
		t.Errorf("escape: %v", err)
	}
}

// TestReadUnchangedKeepsCursor (B1-16): a read that finds nothing changed
// and no racy file answers with the cursor it was given, so the caller's
// cache is not rewritten on every command; a racy file moves the time on
// until it is past the window, and any change gives a new cursor.
func TestReadUnchangedKeepsCursor(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := func() time.Time { return now }
	r, g, ctx := openRoot(t, WithClock(clock)), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	a := g.row(newID(t), "aaaa", hlc.HLC{})
	apply(t, f, store.Change{Row: a})
	_, cur, err := f.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour) // the file is racy under the first cursor's time
	_, cur2, err := f.Read(ctx, cur)
	if err != nil || cur2 == cur {
		t.Fatalf("a racy file must move the cursor's time on: same=%v %v", cur2 == cur, err)
	}
	now = now.Add(time.Hour)
	rows, cur3, err := f.Read(ctx, cur2)
	if err != nil || len(rows) != 0 || cur3 != cur2 {
		t.Fatalf("nothing changed: want the same cursor and no rows, got %d rows, same=%v %v", len(rows), cur3 == cur2, err)
	}
	b := g.row(newID(t), "bbbb", hlc.HLC{})
	apply(t, f, store.Change{Row: b})
	rows, cur4, err := f.Read(ctx, cur3)
	if err != nil || len(rows) != 1 || cur4 == cur3 {
		t.Fatalf("a new file: %d rows, same cursor=%v %v", len(rows), cur4 == cur3, err)
	}
}
