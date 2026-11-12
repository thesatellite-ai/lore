package wire_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestDoctorDuplicateReposFolds pins B2-7: two repos rows of one project
// with the same address (two machines minted an id each before either
// pushed) are reported by doctor and folded by --fix into the id
// _meta.json names; the folded id becomes an alias.
func TestDoctorDuplicateReposFolds(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	keep := h.Seed(schema.TableRepos, map[string]string{"mount_name": `"site"`, "origin_url": `"git@github.com:agency/site.git"`})
	meta := filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir, rowfs.MetaFileName)
	if err := os.WriteFile(meta, []byte(`{"project_id":`+opstest.Quote(h.Project)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.App.WriteRepoMeta(ctx, rowfs.RepoMeta{RepoID: keep}); err != nil {
		t.Fatal(err)
	}
	drop := h.Seed(schema.TableRepos, map[string]string{"mount_name": `"site-2"`, "origin_url": `"https://github.com/agency/site"`})
	other := h.Seed(schema.TableRepos, map[string]string{"mount_name": `"api"`, "origin_url": `"https://github.com/agency/api"`})

	found, err := h.App.DoctorDuplicateRepos(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !strings.Contains(found[0].Message, drop) || !strings.Contains(found[0].Message, "keeps "+keep) || strings.Contains(found[0].Message, other) {
		t.Fatalf("report: %+v", found)
	}
	fixed, err := h.App.DoctorDuplicateRepos(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed) != 1 || !fixed[0].Fixed {
		t.Fatalf("fix: %+v", fixed)
	}
	rows, err := h.App.List(ctx, store.SourceRepo, schema.TableRepos)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, r := range rows {
		left = append(left, r.ID.String())
	}
	if len(left) != 2 || strings.Contains(strings.Join(left, " "), drop) {
		t.Fatalf("repos after the fold: %v", left)
	}
	m, _, err := rowfs.ReadRepoMeta(h.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if m.RepoID != keep || len(m.Aliases) != 1 || m.Aliases[0] != drop {
		t.Fatalf("_meta.json after the fold: %+v", m)
	}
	again, err := h.App.DoctorDuplicateRepos(ctx, false)
	if err != nil || len(again) != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
}
