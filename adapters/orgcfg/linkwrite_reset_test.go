package orgcfg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// TestCommitStoreAsksTheRemoteBeforeItsFirstPush (L-2): the store repo
// was deleted and made again empty, and this clone has fetched nothing
// since (its tracking ref still shows the old branch). An org write asks
// the remote itself before pushing and is refused, so the whole old
// store never goes into the empty repo.
func TestCommitStoreAsksTheRemoteBeforeItsFirstPush(t *testing.T) {
	origin, clone := pushFixture(t)
	ctx := context.Background()
	if res, err := commitStore(ctx, putCommit(clone, func(int) {})); err != nil || !res.Pushed {
		t.Fatalf("first org write: res=%+v err=%v", res, err)
	}
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}
	testGit(t, filepath.Dir(origin), "init", "--quiet", "--bare", "--initial-branch=main", origin)
	c := putCommit(clone, func(int) {})
	c.Change = func(context.Context, *gitref.Repo, string) (map[string][]byte, error) {
		return map[string][]byte{"g.txt": []byte("y\n")}, nil
	}
	_, err := commitStore(ctx, c)
	if !errors.Is(err, ErrStoreRefused) || !errors.Is(err, gitbackend.ErrRemoteReset) {
		t.Fatalf("an org write into the wiped store: %v, want ErrStoreRefused / ErrRemoteReset", err)
	}
	if refs := testGit(t, origin, "for-each-ref"); strings.TrimSpace(refs) != "" {
		t.Fatalf("the org write refilled the wiped store: %s", refs)
	}
}
