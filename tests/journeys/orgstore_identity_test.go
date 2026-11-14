//go:build journeys && !windows

package journeys

// orgstore_identity_test.go — a person writes into their org's store from a
// repo no org matches (plainfiles:4, decision C19). Mira's git user.email
// is her org key, which the identity chain skips outside the org, so the
// open falls back to the OS user (human:<user>@<host>). The row she writes
// with `--to <org store>/<folder>` used to be stamped with that OS-user
// actor while its git commit was signed as Mira; it now carries her
// identity in the org, and no OS-user actor row lands in the org store.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyOrgStoreWriteIdentity(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Stone", "mira@example.test")
	m := mira.Laptop()
	team := w.Remote("github.com/acme-co/lore-team")
	side := jPlainRepo(t, m, w.Remote("github.com/elsewhere/side"), filepath.Join(m.Home, "code", "side"))
	const orgKey = "human:mira@example.test"
	var memID string

	w.Step("day1/org-and-a-folder", func() {
		m.LoreIn(side, "store", "init", "--name", "acme", "--url", team.SSHURL()).OK(t)
		m.LoreIn(side, "org", "create", "--org", "acme", "--store", team.SSHURL(), "--own", "github.com/acme-co/*", "--key", orgKey, "--name", "Mira", "--yes").OK(t)
		m.LoreIn(side, "init").OK(t)
		m.LoreIn(side, "store", "folder", "add", "--store", "acme", "--folder", "clients").OK(t)
		// the repo matches no org: the open's identity is the OS-user fallback
		m.LoreIn(side, "whoami").OK(t).Says(t, "OS user").Never(t, orgKey)
	})

	w.Step("day1/write-into-the-org-store", func() {
		var out struct {
			ID string `json:"id"`
		}
		tlJSON(t, m.LoreIn(side, "memory", "add", "--body", "invoices go out on the 1st", "--to", "acme/clients", "--json").OK(t), &out)
		memID = out.ID
		if memID == "" {
			t.Fatal("memory add --json named no id")
		}
	})

	w.Step("day1/the-row-carries-the-org-identity", func() {
		root := filepath.Join(m.Home, ".lore", "stores")
		var rowPath string
		actors := map[string]string{} // actor id -> stable_key, every actor row of the store folder
		if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) {
				return err
			}
			switch {
			case filepath.Base(p) == memID+".json":
				rowPath = p
			case filepath.Base(filepath.Dir(p)) == "actors" && filepath.Base(filepath.Dir(filepath.Dir(p))) == "clients":
				actors[strings.TrimSuffix(filepath.Base(p), ".json")] = jField(t, p, "stable_key")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if rowPath == "" {
			t.Fatalf("no row file %s under %s", memID, root)
		}
		by := jField(t, rowPath, "created_by")
		if actors[by] != orgKey {
			t.Fatalf("the row in the org store is stamped %s (stable_key %q), want Mira's org identity %s; actors there: %v", by, actors[by], orgKey, actors)
		}
		for id, key := range actors {
			if key != orgKey {
				t.Errorf("an actor row other than the org identity landed in the org store: %s %q", id, key)
			}
		}
	})
}

// jField is the text of one top-level field of the JSON file at p.
func jField(t testing.TB, p, field string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	s, _ := v[field].(string)
	return s
}
