package git

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// TestFlattenPersonal (upgrade journey B4-1, decision D5): a git personal
// store lore 0.1.17 wrote keeps notes under acme/ and everywhere/ (folder
// _meta.json at _v: 1); this lore reads `me` from _store/. One commit moves
// everywhere/'s rows there; acme/ keeps its rows (they load only in acme's
// repos, as me/acme); a folder a person made since (_v: 2) is left alone; a
// second run does nothing.
func TestFlattenPersonal(t *testing.T) {
	ctx := context.Background()
	v1 := []byte("{\n  \"_v\": 1,\n  \"project_id\": \"prj_1d7aab8ba40a7d359988601d1cc77591\"\n}\n")
	v2 := []byte("{\n  \"_v\": 2,\n  \"project_id\": \"prj_1d7aab8ba40a7d359988601d1cc77592\"\n}\n")
	src := newOldRemote(t, "", []string{"acme", "everywhere", "mine"}, map[string][]byte{
		"acme/.lore/data/_meta.json":       v1,
		"everywhere/.lore/data/_meta.json": v1,
		"mine/.lore/data/_meta.json":       v2,
	})
	var shared []byte
	for p, b := range src.rows {
		if strings.HasPrefix(p, "acme/") {
			shared = b
		}
	}
	dir := filepath.Join(t.TempDir(), "stores", "pat", "lore-me")
	b := openFlat(t, src.remote, dir, rowfs.FolderRoot)
	// the person's actor row is in each 0.1.17 folder
	writeFile(t, filepath.Join(dir, "everywhere", ".lore", "data", "memories", filepath.Base(firstPath(src.rows, "acme/"))), string(shared))
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "-c", "user.name=Pat", "-c", "user.email=pat@example.com", "commit", "--quiet", "-m", "same row twice")
	if got := PersonalFoldersV017At(dir); !slices.Equal(got, []string{"acme", "everywhere"}) {
		t.Fatalf("0.1.17 folders %v, want acme and everywhere", got)
	}
	res, err := b.FlattenPersonal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit == "" || res.Rows != 2 || !slices.Equal(res.Folders, []string{"everywhere"}) {
		t.Fatalf("flatten: %+v", res)
	}
	files := gitT(t, dir, "ls-files")
	for p := range src.rows {
		switch {
		case strings.HasPrefix(p, "everywhere/"):
			want := "_store/memories/" + filepath.Base(p)
			if !strings.Contains(files, want) || strings.Contains(files, p) {
				t.Errorf("%s did not move to %s:\n%s", p, want, files)
			}
		default: // acme/ (org scope kept) and mine/ (a 0.1.18 folder)
			if !strings.Contains(files, p) {
				t.Errorf("%s was moved; it must keep its folder", p)
			}
		}
	}
	if n := strings.Count(files, "_store/memories/"); n != 2 {
		t.Errorf("%d rows at _store/, want 2 (everywhere/'s own and its copy of the acme row):\n%s", n, files)
	}
	if _, err := os.Stat(filepath.Join(dir, "everywhere")); err == nil {
		t.Error("everywhere/ is left")
	}
	if st := gitT(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("clone not clean:\n%s", st)
	}
	if again, err := b.FlattenPersonal(ctx); err != nil || again.Commit != "" {
		t.Fatalf("second run: %+v %v", again, err)
	}
}

// firstPath is the first key of rows with prefix.
func firstPath(rows map[string][]byte, prefix string) string {
	for p := range rows {
		if strings.HasPrefix(p, prefix) {
			return p
		}
	}
	return ""
}
