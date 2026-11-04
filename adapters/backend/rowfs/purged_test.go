package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestPurgedFileMatrix: ApplyPurge adds ids to a folder's _purged.json in
// the v0.1.17 format ({"_v":1,"ids":[sorted]}, canonical) for the root and
// a project folder; it unions with ids already listed, keeps keys another
// writer added, leaves a file that already lists every id byte for byte,
// and Purged reads back exactly the union. A file that is not an object,
// or whose ids is not a list, is an error (never an empty set).
func TestPurgedFileMatrix(t *testing.T) {
	ctx := context.Background()
	a, b, c := newID(t), newID(t), newID(t)
	sorted := func(xs ...ids.AnyID) []string {
		out := make([]string, len(xs))
		for i, x := range xs {
			out[i] = x.String()
		}
		slices.Sort(out)
		return out
	}
	cases := []struct {
		name    string
		before  string // "" = no file
		add     []ids.AnyID
		want    []string
		keepKey string // a key of before that must survive
		same    bool   // the file must not be rewritten
	}{
		{"no file yet", "", []ids.AnyID{b, a}, sorted(a, b), "", false},
		{"union with listed ids", `{"_v":1,"ids":["` + c.String() + `"]}`, []ids.AnyID{a}, sorted(a, c), "", false},
		{"duplicate ids in one call", "", []ids.AnyID{a, a}, sorted(a), "", false},
		{"other keys kept", `{"_v":1,"ids":[],"note":"kept"}`, []ids.AnyID{a}, sorted(a), `"note"`, false},
		{"non-string entries skipped", `{"_v":1,"ids":[7,"` + c.String() + `"]}`, []ids.AnyID{a}, sorted(a, c), "", false},
		{"already listed: untouched", `{"_v":1,"ids":["` + a.String() + `"]}`, []ids.AnyID{a}, sorted(a), "", true},
	}
	for _, fname := range []string{FolderRoot, "acme-hub", "org"} {
		for _, tc := range cases {
			t.Run(fname+"/"+tc.name, func(t *testing.T) {
				r := openRoot(t)
				f := folder(t, r, fname)
				full := filepath.Join(f.dataDir, PurgedFileName)
				if tc.before != "" {
					if err := os.MkdirAll(f.dataDir, 0o750); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(full, []byte(tc.before), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				applied, err := f.ApplyPurge(ctx, nil, tc.add)
				if err != nil {
					t.Fatal(err)
				}
				got, err := f.Purged()
				if err != nil {
					t.Fatal(err)
				}
				if keys := slices.Sorted(func(yield func(string) bool) {
					for k := range got {
						if !yield(k) {
							return
						}
					}
				}); !slices.Equal(keys, tc.want) {
					t.Fatalf("purged = %v, want %v", keys, tc.want)
				}
				raw := string(mustReadFile(t, full))
				if tc.same {
					if raw != tc.before || len(applied.Paths) != 0 {
						t.Fatalf("an already-listed id rewrote the file: %q paths %v", raw, applied.Paths)
					}
					return
				}
				if !strings.Contains(raw, `"_v": 1`) && !strings.Contains(raw, `"_v":1`) {
					t.Fatalf("no _v 1: %s", raw)
				}
				if tc.keepKey != "" && !strings.Contains(raw, tc.keepKey) {
					t.Fatalf("lost key %s: %s", tc.keepKey, raw)
				}
				if want := f.rel + "/" + PurgedFileName; !slices.Contains(applied.Paths, want) {
					t.Fatalf("paths %v do not name %s (a git store would not commit it)", applied.Paths, want)
				}
			})
		}
	}
	for name, bad := range map[string]string{"not an object": `["x"]`, "ids not a list": `{"ids":"x"}`, "not json": `{`} {
		t.Run("bad/"+name, func(t *testing.T) {
			f := folder(t, openRoot(t), FolderRoot)
			if err := os.MkdirAll(f.dataDir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.dataDir, PurgedFileName), []byte(bad), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := f.Purged(); !errs.Has(err, errs.InvalidInput) {
				t.Fatalf("Purged: %v, want E_INVALID_INPUT", err)
			}
			if _, err := f.ApplyPurge(ctx, nil, []ids.AnyID{a}); err == nil {
				t.Fatal("ApplyPurge over an unreadable list must fail, never rewrite it")
			}
		})
	}
	if _, err := folder(t, openRoot(t), FolderRoot).ApplyPurge(ctx, nil, nil); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("a purge with no id: %v", err)
	}
}

// TestApplyPurgeIsOneBatch: the tombstones and the list are written all or
// none: a stale change writes neither the rows nor _purged.json, and Undo
// of a successful batch restores both (the file disappears again).
func TestApplyPurgeIsOneBatch(t *testing.T) {
	ctx := context.Background()
	g := newGen(t)
	f := folder(t, openRoot(t), "acme")
	id := newID(t)
	first := g.row(id, "secret-ish", hlc.HLC{})
	apply(t, f, store.Change{Row: first})
	stamp := g.clock.Now(first.HLC)
	tomb, err := store.FromRow(row.Row{Table: memories, ID: id, HLC: stamp, Deleted: true, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor})
	if err != nil {
		t.Fatal(err)
	}
	// stale: expects a HLC the file does not hold
	if _, serr := f.ApplyPurge(ctx, []store.Change{{Row: tomb, Expect: tomb.HLC}}, []ids.AnyID{id}); serr == nil {
		t.Fatal("a stale purge must fail")
	}
	if set, serr2 := f.Purged(); serr2 != nil || len(set) != 0 {
		t.Fatalf("a failed purge listed ids: %v %v", set, serr2)
	}
	a, err := f.ApplyPurge(ctx, []store.Change{{Row: tomb, Expect: first.HLC}}, []ids.AnyID{id})
	if err != nil {
		t.Fatal(err)
	}
	if set, _ := f.Purged(); !set[id.String()] {
		t.Fatal("id not listed")
	}
	if err := a.Undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, PurgedFileName)); !os.IsNotExist(err) {
		t.Fatalf("undo left _purged.json: %v", err)
	}
	if cur, err := f.Get(memories, id); err != nil || cur.HLC != first.HLC {
		t.Fatalf("undo did not restore the row: %v %v", cur, err)
	}
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
