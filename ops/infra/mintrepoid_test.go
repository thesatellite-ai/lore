package infra_test

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/infra"
)

// TestMintRepoIDAgreesAcrossClones pins B2-7: two clones of one repo
// (ssh on one machine, https on the other) mint the same repo id, so
// their repos rows are one row after a pull; another project or another
// address gets another id.
func TestMintRepoIDAgreesAcrossClones(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pid, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	mint := func(p ids.ID[ids.Project], url string) string {
		t.Helper()
		id, err := infra.MintRepoID(ctx, nil, p, []infra.Remote{{Name: "origin", URL: url}})
		if err != nil {
			t.Fatal(err)
		}
		return id.String()
	}
	a := mint(pid, "git@github.com:agency/site.git")
	if b := mint(pid, "https://github.com/agency/site"); a != b {
		t.Fatalf("ssh and https clones minted %s and %s", a, b)
	}
	if c := mint(pid, "https://github.com/tomas/site"); c == a {
		t.Fatal("a fork's address minted the upstream's id")
	}
	other, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	if d := mint(other, "git@github.com:agency/site.git"); d == a {
		t.Fatal("another project minted the same id")
	}
	if _, err := ids.Parse[ids.Repo](a); err != nil {
		t.Fatalf("minted id %s: %v", a, err)
	}
}
