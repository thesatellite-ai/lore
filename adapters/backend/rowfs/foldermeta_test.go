package rowfs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// Two project ids in text order (prjLow < prjHigh).
const (
	prjLow  = "prj_01936f0000007a1b8c2d3e4f5a6b7c01"
	prjHigh = "prj_01936f0000007a1b8c2d3e4f5a6b7c02"
	prjBase = "prj_01936f0000007a1b8c2d3e4f5a6b7c00"
)

// metaPath is an org folder's _meta.json path in a store.
const metaPath = "org/.lore/data/" + MetaFileName

// purgedPath is an org folder's _purged.json path in a store.
const purgedPath = "org/.lore/data/" + PurgedFileName

// mergeBoth merges in both orders and fails when the bytes differ: the
// folder-file merge must never depend on which side is ours.
func mergeBoth(t *testing.T, p, base, ours, theirs string) string {
	t.Helper()
	var b []byte
	if base != "" {
		b = []byte(base)
	}
	ab, err := MergeFolderFile(p, b, []byte(ours), []byte(theirs))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	ba, err := MergeFolderFile(p, b, []byte(theirs), []byte(ours))
	if err != nil {
		t.Fatalf("merge swapped: %v", err)
	}
	if string(ab) != string(ba) {
		t.Fatalf("order-dependent merge:\n%s\n%s", ab, ba)
	}
	if !json.Valid(ab) {
		t.Fatalf("merged %s is not JSON", ab)
	}
	return strings.TrimSpace(string(ab))
}

// TestMergeFolderFileMeta is the _meta.json matrix: project_id that
// existed first (base, else the v1 side, else the smaller id), _v the
// larger, links the union by (remote, project_id), other keys three-way.
func TestMergeFolderFileMeta(t *testing.T) {
	t.Parallel()
	link := func(remote, pid string) string {
		return `{"project_id":"` + pid + `","remote":"` + remote + `"}`
	}
	cases := []struct {
		name, base, ours, theirs string
		pid                      string
		v                        int
		links                    []string
		extra                    map[string]string
	}{
		{name: "add/add same bytes", ours: `{"_v":2,"project_id":"` + prjLow + `"}`, theirs: `{"_v":2,"project_id":"` + prjLow + `"}`, pid: prjLow, v: 2},
		{name: "add/add two core ids: smaller wins", ours: `{"_v":2,"project_id":"` + prjHigh + `"}`, theirs: `{"_v":2,"project_id":"` + prjLow + `"}`, pid: prjLow, v: 2},
		{name: "v1 id beats a core id", ours: `{"_v":1,"project_id":"` + prjHigh + `"}`, theirs: `{"_v":2,"project_id":"` + prjLow + `"}`, pid: prjHigh, v: 2},
		{name: "v1 without _v vs v2: smaller", ours: `{"project_id":"` + prjHigh + `"}`, theirs: `{"_v":2,"project_id":"` + prjLow + `"}`, pid: prjLow, v: 2},
		{name: "base id kept over both", base: `{"_v":1,"project_id":"` + prjBase + `"}`, ours: `{"_v":2,"project_id":"` + prjLow + `"}`, theirs: `{"_v":1,"project_id":"` + prjHigh + `"}`, pid: prjBase, v: 2},
		{name: "one side has no id", ours: `{"_v":1}`, theirs: `{"_v":2,"project_id":"` + prjHigh + `"}`, pid: prjHigh, v: 2},
		{name: "no id anywhere", ours: `{}`, theirs: `{"other":1}`, extra: map[string]string{"other": "1"}},
		{name: "empty side (file only on one side)", ours: ``, theirs: `{"_v":2,"project_id":"` + prjLow + `"}`, pid: prjLow, v: 2},
		{
			name:   "links union, dedup by remote+project_id, sorted",
			base:   `{"_v":1,"project_id":"` + prjBase + `","links":[` + link("git@x:a/one", prjLow) + `]}`,
			ours:   `{"_v":1,"project_id":"` + prjBase + `","links":[` + link("git@x:a/one", prjLow) + `,` + link("git@x:a/two", prjLow) + `]}`,
			theirs: `{"_v":1,"project_id":"` + prjBase + `","links":[` + link("git@x:a/one", prjLow) + `,` + link("git@x:a/one", prjHigh) + `]}`,
			pid:    prjBase, v: 1,
			links: []string{"git@x:a/one|" + prjLow, "git@x:a/one|" + prjHigh, "git@x:a/two|" + prjLow},
		},
		{
			name:   "a link removed on one side comes back (union never forgets)",
			base:   `{"links":[` + link("r1", prjLow) + `]}`,
			ours:   `{"links":[]}`,
			theirs: `{"links":[` + link("r1", prjLow) + `]}`,
			links:  []string{"r1|" + prjLow},
		},
		{
			name:   "links on one side only, other side not a list",
			ours:   `{"links":"oops"}`,
			theirs: `{"links":[` + link("ü-remote", prjLow) + `]}`,
			links:  []string{"ü-remote|" + prjLow},
		},
		{
			name:   "unknown keys three-way: one side changed",
			base:   `{"note":"a","keep":1,"gone":true}`,
			ours:   `{"note":"b","keep":1,"gone":true}`,
			theirs: `{"note":"a","keep":1}`,
			extra:  map[string]string{"note": `"b"`, "keep": "1", "gone": ""},
		},
		{
			name:   "unknown keys three-way: both changed, larger wins",
			base:   `{"note":"a"}`,
			ours:   `{"note":"b"}`,
			theirs: `{"note":"c"}`,
			extra:  map[string]string{"note": `"c"`},
		},
		{
			name:   "unknown key edited on one side, deleted on the other: edit kept",
			base:   `{"note":"a"}`,
			ours:   `{}`,
			theirs: `{"note":"z"}`,
			extra:  map[string]string{"note": `"z"`},
		},
		{name: "_v max over base", base: `{"_v":3}`, ours: `{"_v":1}`, theirs: `{"_v":2}`, v: 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := mergeBoth(t, metaPath, c.base, c.ours, c.theirs)
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatal(err)
			}
			if pid := strings.Trim(string(m[metaKeyProjectID]), `"`); pid != c.pid {
				t.Errorf("project_id %q, want %q (%s)", pid, c.pid, got)
			}
			if v := version(m); v != c.v {
				t.Errorf("_v %d, want %d (%s)", v, c.v, got)
			}
			if c.links != nil {
				var ls []struct {
					Remote    string `json:"remote"`
					ProjectID string `json:"project_id"`
				}
				if err := json.Unmarshal(m[metaKeyLinks], &ls); err != nil {
					t.Fatalf("links: %v (%s)", err, got)
				}
				keys := make([]string, 0, len(ls))
				for _, l := range ls {
					keys = append(keys, l.Remote+"|"+l.ProjectID)
				}
				if !slices.Equal(keys, c.links) {
					t.Errorf("links %v, want %v", keys, c.links)
				}
			}
			for k, want := range c.extra {
				if string(m[k]) != want {
					t.Errorf("%s = %s, want %q (%s)", k, m[k], want, got)
				}
			}
		})
	}
}

// TestMergeFolderFilePurged: _purged.json ids are the union, sorted, and
// never shrink; other keys merge three-way.
func TestMergeFolderFilePurged(t *testing.T) {
	t.Parallel()
	got := mergeBoth(t, purgedPath,
		`{"ids":["mem_b"],"_v":2}`,
		`{"ids":["mem_b","mem_c"],"_v":2}`,
		`{"ids":["mem_a"],"_v":2}`)
	var p struct {
		IDs []string `json:"ids"`
		V   int      `json:"_v"`
	}
	if err := json.Unmarshal([]byte(got), &p); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.IDs, []string{"mem_a", "mem_b", "mem_c"}) || p.V != 2 {
		t.Errorf("purged %s", got)
	}
	// A _meta.json-only rule (project_id first) does not apply here: a
	// project_id key in _purged.json is just a key.
	got = mergeBoth(t, purgedPath, ``, `{"project_id":"`+prjHigh+`"}`, `{"project_id":"`+prjLow+`"}`)
	if !strings.Contains(got, prjHigh) {
		t.Errorf("generic key rule: %s", got)
	}
}

// TestMergeFolderFileRefusesNonJSON: a side that is not a JSON object is
// E_STALE with a hint naming the manual fix and the path, on any side.
func TestMergeFolderFileRefusesNonJSON(t *testing.T) {
	t.Parallel()
	good := []byte(`{"_v":2}`)
	for _, bad := range []string{`<<<<<<< ours`, `[1,2]`, `"text"`, `{"a":`} {
		for i := range 3 {
			sides := [3][]byte{good, good, good}
			sides[i] = []byte(bad)
			_, err := MergeFolderFile(metaPath, sides[0], sides[1], sides[2])
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != errs.Stale {
				t.Fatalf("%q at side %d: %v", bad, i, err)
			}
			if !strings.Contains(e.Message, metaPath) || !strings.Contains(e.Hint, "git checkout --ours or --theirs") || !strings.Contains(e.Hint, "lore sync") {
				t.Errorf("message/hint %q / %q", e.Message, e.Hint)
			}
		}
	}
}

// TestIsFolderFile names exactly the two folder files, at any depth.
func TestIsFolderFile(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]bool{
		MetaFileName: true, PurgedFileName: true, metaPath: true, purgedPath: true,
		"a/b/c/.lore/data/_meta.json": true, "memories/_meta.json.bak": false,
		"_meta.jsonx": false, "lore-store.yaml": false, "": false,
	} {
		if got := IsFolderFile(p); got != want {
			t.Errorf("IsFolderFile(%q) = %v", p, got)
		}
	}
}

// storeRoot opens a root with a v2 marker of the given store id.
func storeRoot(t *testing.T, sto string) *Root {
	t.Helper()
	dir := t.TempDir()
	id, err := ids.Parse[ids.Store](sto)
	if err != nil {
		t.Fatal(err)
	}
	if werr := WriteMarker(dir, Marker{Version: MarkerVersion, ID: id}); werr != nil {
		t.Fatal(werr)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readMeta(t *testing.T, f *Folder) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dataDir, MetaFileName))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// TestFirstWriteCreatesMeta: a non-root folder's first write adds
// _meta.json {_v:2, project_id: prj_} in the same batch (in a.Paths, undone
// with it); the id is natural, so two clones of one store write the same
// bytes and another store's folder of the same name gets another id; the
// root folder never gets one; an existing file is never touched.
func TestFirstWriteCreatesMeta(t *testing.T) {
	t.Parallel()
	const sto = "sto_01936f0000007a1b8c2d3e4f5a6b7c8d"
	g := newGen(t)
	r1, r2 := storeRoot(t, sto), storeRoot(t, sto)
	other := storeRoot(t, "sto_01936f0000007a1b8c2d3e4f5a6b7c8e")
	var docs []string
	for _, r := range []*Root{r1, r2, other} {
		f := folder(t, r, "org")
		a := apply(t, f, store.Change{Row: g.row(newID(t), "x", hlc.HLC{})})
		if !slices.Contains(a.Paths, metaPath) {
			t.Errorf("a.Paths %v lacks %s", a.Paths, metaPath)
		}
		doc, ok := readMeta(t, f)
		if !ok {
			t.Fatal("no _meta.json after the first write")
		}
		var m folderMeta
		if err := json.Unmarshal([]byte(doc), &m); err != nil || m.Version != 2 || !strings.HasPrefix(m.ProjectID, "prj_") {
			t.Fatalf("meta %s %v", doc, err)
		}
		docs = append(docs, doc)
	}
	if docs[0] != docs[1] {
		t.Errorf("two clones wrote different _meta.json:\n%s\n%s", docs[0], docs[1])
	}
	if docs[0] == docs[2] {
		t.Error("two stores' folders share a project id")
	}
	// Second write: file untouched, not in the batch.
	f := folder(t, r1, "org")
	if err := os.WriteFile(filepath.Join(f.dataDir, MetaFileName), []byte(`{"_v":1,"features":["hlc"],"min_lore_version":"0.1.18","project_id":"`+prjBase+`","links":[]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := apply(t, f, store.Change{Row: g.row(newID(t), "y", hlc.HLC{})})
	if slices.Contains(a.Paths, metaPath) {
		t.Error("an existing _meta.json was rewritten")
	}
	if doc, _ := readMeta(t, f); !strings.Contains(doc, prjBase) {
		t.Errorf("existing meta changed: %s", doc)
	}
	// Root folder: never.
	root := folder(t, r1, FolderRoot)
	ra := apply(t, root, store.Change{Row: g.row(newID(t), "z", hlc.HLC{})})
	if _, ok := readMeta(t, root); ok || slices.ContainsFunc(ra.Paths, IsFolderFile) {
		t.Error("the root folder got a _meta.json")
	}
	// A failed batch undoes the new _meta.json with the rows.
	g2 := folder(t, r2, "team")
	a2 := apply(t, g2, store.Change{Row: g.row(newID(t), "w", hlc.HLC{})})
	if err := a2.Undo(); err != nil {
		t.Fatal(err)
	}
	if _, ok := readMeta(t, g2); ok {
		t.Error("Undo left the _meta.json")
	}
}

// TestFirstWriteAdoptsSharedMeta: with a MetaSource holding the remote
// copy, the first write adopts it byte for byte; not found falls back to
// the natural id; a source error or a non-JSON shared copy fails the batch
// and writes nothing.
func TestFirstWriteAdoptsSharedMeta(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	// The shared copy already declares hlc, so adopting it raises nothing.
	shared := `{"_v":1,"features":["hlc"],"links":[{"project_id":"` + prjLow + `","remote":"git@x:c/app"}],"min_lore_version":"0.1.18","project_id":"` + prjBase + `"}` + "\n"
	cases := []struct {
		name    string
		src     MetaSource
		want    string // "" = natural id
		wantErr bool
	}{
		{"adopts", func(_ context.Context, rel string) ([]byte, bool, error) {
			if rel != metaPath {
				return nil, false, errors.New("wrong path " + rel)
			}
			return []byte(shared), true, nil
		}, shared, false},
		{"not found", func(context.Context, string) ([]byte, bool, error) { return nil, false, nil }, "", false},
		{"source error", func(context.Context, string) ([]byte, bool, error) { return nil, false, errors.New("offline") }, "", true},
		{"not json", func(context.Context, string) ([]byte, bool, error) { return []byte("<<<"), true, nil }, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := storeRoot(t, "sto_01936f0000007a1b8c2d3e4f5a6b7c8d")
			f := folder(t, r, "org")
			f.SetMetaSource(c.src)
			row := g.row(newID(t), "x", hlc.HLC{})
			_, err := f.Apply(context.Background(), []store.Change{{Row: row}})
			if c.wantErr {
				if err == nil {
					t.Fatal("batch succeeded")
				}
				if _, ok := readMeta(t, f); ok {
					t.Error("_meta.json written by a failed batch")
				}
				if _, gerr := f.Get(memories, row.ID); !errors.Is(gerr, store.ErrNotFound) {
					t.Errorf("row written by a failed batch: %v", gerr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			doc, _ := readMeta(t, f)
			if c.want != "" && doc != c.want {
				t.Errorf("adopted %q, want %q", doc, c.want)
			}
			var m folderMeta
			if c.want == "" && (json.Unmarshal([]byte(doc), &m) != nil || m.Version != 2 || !strings.HasPrefix(m.ProjectID, "prj_")) {
				t.Errorf("natural meta %s", doc)
			}
		})
	}
}
