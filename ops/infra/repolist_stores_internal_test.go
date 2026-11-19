package infra

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// storesEnv is an Env whose read stores each hold the repos rows given;
// every other method is the nil Env's (never called here).
type storesEnv struct {
	Env
	rows map[store.Source][]row.Row
}

func (e storesEnv) List(_ context.Context, src store.Source, table row.Table) ([]row.Row, error) {
	if table != schema.TableRepos {
		return nil, nil
	}
	return e.rows[src], nil
}

func (e storesEnv) Stores() []store.Source { return e.ReadStores() }

func (e storesEnv) ReadStores() []store.Source {
	out := make([]store.Source, 0, len(e.rows))
	for s := range e.rows {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// repoRow is a live repos row with mount name mount.
func repoRow(t *testing.T, id, mount string) row.Row {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	m, err := value.TextOf(mount)
	if err != nil {
		t.Fatal(err)
	}
	return row.Row{Table: schema.TableRepos, ID: aid, Fields: map[string]value.Value{fieldMountName: m}}
}

// TestRepoListEveryProjectReadsEveryStore (devco:D-656): repo list over
// every project (under --setup, at a store clone root) lists the repos
// recorded in each folder of the org store, not only the repo store's;
// a row two read stores both show is listed once, and a tombstone never.
func TestRepoListEveryProjectReadsEveryStore(t *testing.T) {
	const (
		webID  = "rep_0190f1a2b3c47d5e8f90a1b2c3d4e501"
		apiID  = "rep_0190f1a2b3c47d5e8f90a1b2c3d4e502"
		goneID = "rep_0190f1a2b3c47d5e8f90a1b2c3d4e503"
	)
	gone := repoRow(t, goneID, "gone")
	gone.Deleted = true
	env := storesEnv{rows: map[store.Source][]row.Row{
		"acme/client_a": {repoRow(t, webID, "web-front")},
		"acme/client_b": {repoRow(t, apiID, "api-back"), gone},
		"acme":          {repoRow(t, webID, "web-front")},
	}}
	got, err := ops{env: env}.listedRepos(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range got {
		names = append(names, repoName(s.Row))
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"api-back", "web-front"}) {
		t.Fatalf("repos listed over every project = %v; want both folders' repos once each", names)
	}
}
