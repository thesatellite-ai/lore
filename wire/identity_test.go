package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// Store ids of the identity test (arbitrary sto_ ids).
const (
	shareStoreID = "sto_01a1a5aca7f57659be7320c5f4b70011"
	innerStoreID = "sto_01a1a5aca7f57659be7320c5f4b70012"
)

// TestStoreIdentitiesAndShareable (CFG4, CFG6, spec 7.3): every attached
// store but the repo is listed in precedence order with its sto_ id,
// folder and visibility; an org store attached at two folders gives two
// sources with ONE id; only shareable markers are shareable, and a marker
// without a visibility counts as internal (fail closed).
func TestStoreIdentitiesAndShareable(t *testing.T) {
	ctx := background()
	shareURL := folderStoreAt(t, shareStoreID, rowfs.VisibilityShareable)
	innerURL := folderStoreAt(t, innerStoreID, rowfs.VisibilityInternal)
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityShareable)
	// a marker with no visibility line: absent = internal
	oddURL := folderStoreAt(t, "sto_01a1a5aca7f57659be7320c5f4b70013", rowfs.VisibilityShareable)
	setVisibilityLine(t, oddURL, "")
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n  - name: share\n    url: "+shareURL+"\n  - name: inner\n    url: "+innerURL+"\n  - name: odd\n    url: "+oddURL+"\n")
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "agency", URL: orgURL, Folder: "acme"}, {Name: "agency/org", URL: orgURL, Folder: "org"}}, nil
	}
	a := openWith(t, repo, Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})

	got, err := a.StoreIdentities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[store.Source]StoreIdentity{}
	var order []store.Source
	for _, si := range got {
		if si.Name == store.SourceRepo {
			t.Fatal("the repo store is listed")
		}
		byName[si.Name] = si
		order = append(order, si.Name)
	}
	want := map[store.Source]StoreIdentity{
		"share":      {Name: "share", ID: shareStoreID, Shareable: true},
		"inner":      {Name: "inner", ID: innerStoreID},
		"agency":     {Name: "agency", ID: orgStoreID, Folder: "acme", Shareable: true},
		"agency/org": {Name: "agency/org", ID: orgStoreID, Folder: "org", Shareable: true},
	}
	for n, w := range want {
		g, ok := byName[n]
		if !ok || g.ID != w.ID || g.Shareable != w.Shareable || strings.Trim(g.Folder, "/.") != w.Folder {
			t.Errorf("%s: got %+v (listed %v), want %+v", n, g, ok, w)
		}
	}
	if g, ok := byName["odd"]; !ok || g.Shareable {
		t.Errorf("a marker without visibility: %+v (listed %v), want internal", g, ok)
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var engOrder []store.Source
	for _, n := range eng.Stores() {
		if n != store.SourceRepo {
			engOrder = append(engOrder, n)
		}
	}
	if !slices.Equal(order, engOrder) {
		t.Fatalf("identities order %v, engine order %v", order, engOrder)
	}

	shareable, err := a.ShareableStores(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wantShare []store.Source
	for _, n := range engOrder {
		if want[n].Shareable {
			wantShare = append(wantShare, n)
		}
	}
	if !slices.Equal(shareable, wantShare) {
		t.Fatalf("ShareableStores = %v, want %v", shareable, wantShare)
	}
}

// setVisibilityLine rewrites the visibility line of the folder store at
// url's marker to vis ("" removes the line).
func setVisibilityLine(t *testing.T, url, vis string) {
	t.Helper()
	p := filepath.Join(strings.TrimPrefix(url, "file://"), rowfs.MarkerFile)
	var out []string
	found := false
	for _, ln := range strings.Split(string(mustRead(t, p)), "\n") {
		if strings.HasPrefix(ln, "visibility:") {
			found = true
			if vis == "" {
				continue
			}
			ln = "visibility: " + vis
		}
		out = append(out, ln)
	}
	if !found {
		t.Fatal("the marker has no visibility line")
	}
	if err := os.WriteFile(p, []byte(strings.Join(out, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUnknownVisibilityRefused: a marker naming a visibility other than
// internal or shareable never opens (E_CONFIG_INVALID), so it can never be
// read as shareable.
func TestUnknownVisibilityRefused(t *testing.T) {
	for _, vis := range []string{"public", "Shareable", "shared", "yes", "internal,shareable"} {
		t.Run(vis, func(t *testing.T) {
			u := folderStoreAt(t, shareStoreID, rowfs.VisibilityShareable)
			setVisibilityLine(t, u, vis)
			repo := gitRepo(t)
			writeStoresYAML(t, repo, "stores:\n  - name: odd\n    url: "+u+"\n")
			a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
			if _, err := a.ShareableStores(background()); !errs.Has(err, errs.ConfigInvalid) {
				t.Fatalf("got %v, want E_CONFIG_INVALID", err)
			}
		})
	}
}
