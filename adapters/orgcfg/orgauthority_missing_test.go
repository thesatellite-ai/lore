package orgcfg

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/partialclone"
	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// TestOrgFileUnreadVersionHeld: in a blobless clone offline, a version of
// lore-org.yaml the clone does not hold is held back and named ("not in
// this store clone"), never skipped as if the commit deleted the file.
// Break-verified: the old readFirstBlob read the missing blob as "no file"
// and the member's change in the middle vanished from the held list
// (store growth SG-3a, devco:D-997).
func TestOrgFileUnreadVersionHeld(t *testing.T) {
	ctx := context.Background()
	dir, _ := authOrg(t)
	bad := authCommit(t, dir, authMember, authMembE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:outside/notes.git\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n",
	})
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n    - url: git@example.invalid:acme/client.git\n",
	})
	partialclone.Git(t, dir, "config", "uploadpack.allowFilter", "true")
	partialclone.Git(t, dir, "config", "uploadpack.allowAnySHA1InWant", "true")
	clone := filepath.Join(t.TempDir(), "clone")
	partialclone.Git(t, filepath.Dir(clone), "clone", "-q", "--filter=blob:none", "file://"+filepath.ToSlash(dir), clone)
	t.Setenv(gitenv.EnvOffline, gitenv.On)

	_, held := acceptedOrgFile(ctx, clone, "HEAD", "org")
	named := false
	for _, h := range held {
		if strings.HasPrefix(bad, h.Commit) && strings.Contains(h.Why, "not in this store clone") {
			named = true
		}
	}
	if !named {
		t.Errorf("acceptedOrgFile offline in a blobless clone held %v; want the member's unread change %s held as not in this clone", held, bad)
	}
	if _, _, _, err := readFirstBlob(ctx, clone, "HEAD~1", orgFilePaths("org")); err == nil {
		t.Error("readFirstBlob of a version the clone lacks returned no error; want one (never read as no file)")
	}
	if _, _, found, err := readFirstBlob(ctx, clone, "HEAD~1", []string{"org/none.yaml"}); found || err != nil {
		t.Errorf("readFirstBlob of a path no tree names = found %v, %v; want not found, no error", found, err)
	}
}
