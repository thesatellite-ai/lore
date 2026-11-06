package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
)

// TestCleanStoresFileURLs (P2-CTX-6, SECRETS 14.2 row 16): a stores.yaml
// url holding a credential (password, token user in https, ssh:// or scp
// form) is rewritten without it in place; other entries, keys and order
// stay; a file with nothing to clean is never touched; a second run finds
// nothing left to do.
func TestCleanStoresFileURLs(t *testing.T) {
	t.Parallel()
	tok := "ghp_" + strings.Repeat("B", 20)
	cases := []struct {
		name      string
		url       string
		wantURL   string // "" = untouched
		rewritten bool
	}{
		{name: "https token user", url: "https://" + tok + "@github.com/a/b.git", wantURL: "https://github.com/a/b.git", rewritten: true},
		{name: "https password", url: "https://bob:" + tok + "@github.com/a/b.git", wantURL: "https://github.com/a/b.git", rewritten: true},
		{name: "ssh password", url: "ssh://git:" + tok + "@host/a/b.git", wantURL: "ssh://git@host/a/b.git", rewritten: true},
		{name: "ssh token user", url: "ssh://" + tok + "@host/a/b.git", wantURL: "ssh://host/a/b.git", rewritten: true},
		{name: "scp token user", url: tok + "@github.com:a/b.git", wantURL: "ssh://github.com/a/b.git", rewritten: true},
		{name: "scp plain", url: "git@github.com:a/b.git"},
		{name: "https plain user", url: "https://mira@github.com/a/b.git"},
		{name: "file", url: "file:///srv/store"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			top := t.TempDir()
			path := filepath.Join(top, filepath.FromSlash(attach.FileName))
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			body := "# team stores\nstores:\n  - name: agency\n    url: " + c.url + "\n    folder: hub\n  - name: other\n    url: file:///srv/other\n    read_only: true\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := attach.Parse([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if err = cleanStoresFileURLs(top, cfg); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if !c.rewritten {
				if got != body {
					t.Fatalf("a clean file was rewritten:\n%s", got)
				}
				return
			}
			if strings.Contains(got, tok) {
				t.Fatalf("the credential is still on disk:\n%s", got)
			}
			if c.wantURL != "" && !strings.Contains(got, "url: "+c.wantURL+"\n") {
				t.Fatalf("want url %q in:\n%s", c.wantURL, got)
			}
			again, err := attach.Parse(b)
			if err != nil {
				t.Fatal(err)
			}
			if len(again.Stores) != 2 || again.Stores[0].Folder != "hub" || again.Stores[1].Name != "other" || !again.Stores[1].ReadOnly {
				t.Fatalf("other fields lost: %+v", again.Stores)
			}
			if err = cleanStoresFileURLs(top, again); err != nil {
				t.Fatal(err)
			}
			if b2, _ := os.ReadFile(filepath.Clean(path)); string(b2) != got {
				t.Fatalf("second run changed the file")
			}
		})
	}
}
