package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestOldRowsDecodesOnlyOldFiles (devco:D-1016, bug hunt H-278): counting
// a folder's v1 rows (lore doctor, for every store folder the repo does
// not attach) gives the counts a full read gives, and decodes only the
// files that declare an older format: a current row is read for its "_v"
// alone. A file that does not decode is not counted.
func TestOldRowsDecodesOnlyOldFiles(t *testing.T) {
	r, g, ctx := openRoot(t), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	for i := range 3 {
		apply(t, f, store.Change{Row: g.row(newID(t), "current "+strconv.Itoa(i), hlc.HLC{})})
	}
	v1ID := "mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00"
	write := func(id, doc string) string {
		t.Helper()
		parsed, err := ids.ParseAny(id)
		if err != nil {
			t.Fatal(err)
		}
		rel := RowPath(memories, parsed)
		if err := os.WriteFile(filepath.Join(f.dataDir, filepath.FromSlash(rel)), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		return rel
	}
	v1 := write(v1ID, `{"_v":1,"_table":"memories","id":"`+v1ID+`","body":"b","created_at":"2026-01-02T03:04:05.678901234Z","updated_at":"2026-01-02T03:04:05.678901234Z","updated_by":"act_01936f0000007a1b8c2d3e4f5a6b7c8d"}`)
	broken := write("mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f01", `{"_v":1,"_table":"memories",`)
	var mu sync.Mutex
	var decoded []string
	oldRowsDecodeHook = func(rel string) { mu.Lock(); decoded = append(decoded, rel); mu.Unlock() }
	t.Cleanup(func() { oldRowsDecodeHook = nil })

	got, err := f.OldRows(ctx, row.FormatVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[memories] != 1 {
		t.Fatalf("old rows %v, want memories 1", got)
	}
	slices.Sort(decoded)
	if want := []string{v1, broken}; !slices.Equal(decoded, want) {
		t.Fatalf("decoded %v, want only the files declaring _v 1: %v", decoded, want)
	}
	rows, _, err := f.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	full := 0
	for _, sr := range rows {
		if d, err := sr.Decode(); err == nil && d.SourceVersion < row.FormatVersion {
			full++
		}
	}
	if full != got[memories] {
		t.Fatalf("a full read counts %d old rows, OldRows %d", full, got[memories])
	}
}

// TestDeclaredVersion: the top-level "_v" of a row document, wherever it
// sits; none, or one that is not an integer, is false (the file is then
// decoded in full).
func TestDeclaredVersion(t *testing.T) {
	for doc, want := range map[string]int{
		`{"_v":2,"_table":"memories"}`:                     2,
		"\ufeff" + `{"_table":"m","body":{"_v":9},"_v":1}`: 1,
		`{"_table":"memories"}`:                            -1,
		`{"_v":"2"}`:                                       -1,
		`{"_v":2.5}`:                                       -1,
		`[1]`:                                              -1,
		`{"_v":`:                                           -1,
	} {
		v, ok := declaredVersion([]byte(doc))
		if (want < 0) == ok || ok && v != want {
			t.Errorf("declaredVersion(%q) = %d, %v; want %d", doc, v, ok, want)
		}
	}
}
