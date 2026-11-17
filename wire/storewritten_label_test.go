package wire

import (
	"context"
	"os"
	"strings"
	"testing"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestFolderAddNoticeNamesTheStoreWritten pins devco D-542: a folder added
// to an org's second store is committed in that store, and the "committed
// locally" notice names it (its short name), never the org entry it was
// reached under; the org's folder store is still named by the org.
func TestFolderAddNoticeNamesTheStoreWritten(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newAccessWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira", false)
	a := w.app(t, mira)
	var notes []string
	a.opts.Notice = func(s string) { notes = append(notes, s) }
	team := "git@" + accessCanonHost + ":acme/team.git"
	body := "access:\n  - url: \"git@" + accessCanonHost + ":\"\n    via: \"" + w.mirror + "/\"\n" +
		"orgs:\n  - name: acme\n    stores:\n      - url: " + team + "\n        org_folder: org\n      - url: " + accessCanonURL + "\n"
	if err := os.MkdirAll(mira, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(mira), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddFolder(ctx, opsstores.FolderAddIn{Store: "lore-x", Folder: "servers"}); err != nil {
		t.Fatalf("folder add: %v", err)
	}
	said := strings.Join(notes, "\n")
	if !strings.Contains(said, "committed locally to store lore-x;") {
		t.Errorf("the notice does not name the store written (lore-x):\n%s", said)
	}
	if strings.Contains(said, "store acme;") {
		t.Errorf("the notice names the org entry for a commit in its second store:\n%s", said)
	}
}
