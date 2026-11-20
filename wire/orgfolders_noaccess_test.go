package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestFolderScanSkipsRefusedStore (devco D-726): every folder scan (store
// folder list, project list, a bare folder target, an id read from the
// home folder) walks all of the org's stores. A store whose last fetch the
// forge refused (the link reader's no-access record) was opened again on
// every scan and listed as "could not read <store>: …". Now it is skipped
// without an open and named once as "no access", never as unreadable.
func TestFolderScanSkipsRefusedStore(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, aliasOrgConfig)
	const url = "git@git.example.org:acme/vault.git"
	dir, _, err := git.CloneDirFor(home, git.Remote{Norm: "git.example.org/acme/vault"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(git.StateDirOf(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(git.NoAccessPath(dir), []byte("denied 2026-01-02T03:04:05Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{opts: Options{Home: home}}
	scan := a.foldersOf(t.Context(), []opsstores.Entry{{URL: url}})
	if len(scan.Unread) != 0 {
		t.Errorf("a refused store reads as unreadable: %v", scan.Unread)
	}
	if len(scan.NoAccess) != 1 || !strings.Contains(scan.NoAccess[0], "vault") {
		t.Fatalf("NoAccess = %v, want the vault store", scan.NoAccess)
	}
	if d := scan.missDetail(); !strings.Contains(d, "skipped") || strings.Contains(d, "could not read") {
		t.Errorf("missDetail = %q", d)
	}
	if _, serr := os.Stat(git.PathsOf(dir).Lock); serr == nil {
		t.Error("the refused store's clone was opened (its lock exists)")
	}
}

// TestFolderScanReadsRefusedStoreWithACopy (devco D-741): the no-access
// record of a clone this machine has fetched before (one run refused with
// other credentials) must not skip it: a read by id from the home folder
// then looked only in the other org's folders. Such a clone is never
// listed as no access, and an id no store holds names each store it did
// not look in.
func TestFolderScanReadsRefusedStoreWithACopy(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, aliasOrgConfig)
	const url = "git@git.example.org:acme/vault.git"
	dir, _, err := git.CloneDirFor(home, git.Remote{Norm: "git.example.org/acme/vault"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(git.StateDirOf(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{git.NoAccessFile: "missing 2026-01-02T03:04:05Z\n", git.FetchStampFile: "2026-01-01T03:04:05Z\n"} {
		if err := os.WriteFile(filepath.Join(git.StateDirOf(dir), name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if refusedClone(home, url, "") {
		t.Fatal("a clone fetched before is skipped for its no-access record")
	}
	// (the scan then opens the clone like any other: journey
	// TestJourneyStoreRefusedOnceIsReadAgain reads its rows end to end)
	err = noStoreHoldsErr("mem_01a1a5aca7f57659be7320c5f4b70033", []string{"ops/web"}, []string{"vault (no access)"})
	if msg := err.Error(); !strings.Contains(msg, "looked in ops/web") || !strings.Contains(msg, "did not look in vault (no access)") {
		t.Errorf("refusal %q does not name the store it skipped", msg)
	}
}
