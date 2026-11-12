package git

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// orgMeta is the org folder's _meta.json path in a store.
const orgMeta = rowfs.FolderOrg + "/" + rowfs.MetaFileName

// linkedMeta is a v0.1.17 `lore org link` _meta.json (v1, links[]).
// It already declares the 0.1.18 row format (hlc), so the first core
// write has nothing to raise and adopts it byte for byte.
const linkedMeta = `{"_v":1,"features":["hlc"],"links":[{"project_id":"prj_01936f0000007a1b8c2d3e4f5a6b7c01","remote":"git@example.invalid:client/app.git"}],"min_lore_version":"0.1.18","project_id":"prj_01936f0000007a1b8c2d3e4f5a6b7c00"}` + "\n"

// pushFile commits one file to remote's branch from a scratch clone, as
// v0.1.17's store code (or another tool) would.
func pushFile(t *testing.T, remote, rel, body string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "w")
	gitT(t, filepath.Dir(dir), "clone", "--quiet", remote, dir)
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", rel)
	gitT(t, dir, "commit", "--quiet", "-m", "link")
	gitT(t, dir, "push", "--quiet", "origin", testBranch)
}

// orgClone opens a clone of remote on the org folder.
func orgClone(t *testing.T, remote string) *Backend {
	t.Helper()
	b, err := OpenClone(t.Context(), remote, filepath.Join(t.TempDir(), "clones", "host", "store"), rowfs.FolderOrg, store.OpenOptions{}, WithMerger(newestMerger{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	return b
}

// remoteFile is rel's content at the remote branch tip.
func remoteFile(t *testing.T, remote, rel string) string {
	t.Helper()
	return gitT(t, remote, "show", testBranch+":"+rel)
}

// TestOrgLinkThenFirstCoreWriteSyncs: `lore org link` pushed a v1
// _meta.json after this clone was opened; the clone's first core write
// into that folder adopts the pushed copy byte for byte (fetched under the
// lock), so the sync pushes cleanly and the link survives.
func TestOrgLinkThenFirstCoreWriteSyncs(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := orgClone(t, remote)
	pushFile(t, remote, orgMeta, linkedMeta)
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "first", hlc.HLC{})})
	syncOK(t, b)
	if got := remoteFile(t, remote, orgMeta); got+"\n" != linkedMeta {
		t.Fatalf("remote _meta.json changed:\n%s", got)
	}
	local, err := os.ReadFile(filepath.Join(b.Dir(), filepath.FromSlash(orgMeta)))
	if err != nil || string(local) != linkedMeta {
		t.Fatalf("local _meta.json %q %v", local, err)
	}
}

// TestTwoClonesFirstWriteSameMeta: two clones write their first row into
// the same new folder before either syncs; both create the natural
// _meta.json, the same bytes, so both syncs succeed and the trees match.
func TestTwoClonesFirstWriteSameMeta(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a, b := orgClone(t, remote), orgClone(t, remote)
	ga, gb := newGen(t), newGen(t)
	write(t, a, store.Change{Row: ga.row(newID(t), "a", hlc.HLC{})})
	write(t, b, store.Change{Row: gb.row(newID(t), "b", hlc.HLC{})})
	syncOK(t, a)
	syncOK(t, b)
	syncOK(t, a)
	if ta, tb := gitT(t, a.Dir(), "rev-parse", "HEAD^{tree}"), gitT(t, b.Dir(), "rev-parse", "HEAD^{tree}"); ta != tb {
		t.Fatal("clones differ")
	}
	var m struct {
		ProjectID string `json:"project_id"`
		V         int    `json:"_v"`
	}
	if err := json.Unmarshal([]byte(remoteFile(t, remote, orgMeta)), &m); err != nil || m.V != 2 || m.ProjectID == "" {
		t.Fatalf("remote meta %+v %v", m, err)
	}
}

// TestSyncMergesSplitMeta: a clone created its natural _meta.json while the
// remote had none, then `lore org link` pushed a v1 one; the sync merges the
// two (v1 project_id wins, links kept, _v the larger) instead of refusing.
func TestSyncMergesSplitMeta(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := orgClone(t, remote)
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "first", hlc.HLC{})})
	pushFile(t, remote, orgMeta, linkedMeta)
	syncOK(t, b)
	var m struct {
		ProjectID string            `json:"project_id"`
		V         int               `json:"_v"`
		Links     []json.RawMessage `json:"links"`
	}
	got := remoteFile(t, remote, orgMeta)
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatal(err)
	}
	if m.ProjectID != "prj_01936f0000007a1b8c2d3e4f5a6b7c00" || m.V != 2 || len(m.Links) != 1 {
		t.Fatalf("merged meta %s", got)
	}
	if st := gitT(t, b.Dir(), "status", "--porcelain"); st != "" {
		t.Fatalf("merge left the tree dirty:\n%s", st)
	}
}
