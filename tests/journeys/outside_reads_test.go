//go:build journeys && !windows

package journeys

// outside_reads_test.go — an admin works from the plain parent folder of
// their clients' clones, not a git repo (devco D-421, D-422). From there
// `rule add --to <org>/<folder>` already worked; the reads of the same
// rows did not (E_NOT_A_STORE, "run git init here first"), nor did
// `repo add --project …` or a resource of a kind defined in the org folder.
// Now every read takes the same --to (or --store), a read naming no store
// reads the setup's org store, and the folder never becomes a repo.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Names of the outside-reads journey (placeholders).
const (
	jorFolder = "sync"
	jorRule   = "every sync job retries three times"
	jorKind   = "code_repo"
	jorRes    = "sync_web"
	jorMount  = "web"
	jorOrigin = "git@github.com:acme-co/sync-web.git"
)

func TestJourneyOutsideReads(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code") // inside the setup's folders, not a repo
	to := tlOrg + "/" + jorFolder

	w.Step("day1/start-team", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		lee.Lore(parent, "member", "add", "--email", tlAnn.Email, "--name", tlAnn.Name, "--handle", "ann", "--yes").OK(t)
	})

	w.Step("day2/admin-reads-from-the-parent", func() {
		w.AdvanceDays(1)
		lee.Lore(parent, "store", "folder", "add", "team", jorFolder).OK(t)
		lee.Lore(parent, "rule", "add", "--to", to, "--body", jorRule, "--yes").OK(t)

		// D-421: the reads of what was just written, with the same --to
		lee.Lore(parent, "rule", "list", "--to", to).OK(t).Says(t, jorRule).Never(t, "E_NOT_A_STORE", "git init")
		lee.Lore(parent, "rule", "list", "--store", to).OK(t).Says(t, jorRule)
		lee.Lore(parent, "search", "--query", "retries", "--to", to).OK(t).Says(t, jorRule).Never(t, "E_NOT_A_STORE")
		// no flag: the setup's org store
		lee.Lore(parent, "lint").OK(t).Never(t, "E_NOT_A_STORE", "git init")
		actors := lee.Lore(parent, "actor", "list", "--json").OK(t).Never(t, "E_NOT_A_STORE")
		from, into := jorActorIDs(t, actors.Stdout, tlAnn.Email, tlLee.Email)
		lee.Lore(parent, "actor", "merge", "--from", from, "--into", into, "--dry-run").OK(t).Never(t, "E_NOT_A_STORE", "git init")

		// D-422: a kind of the org folder, a resource of it in the project folder
		lee.Lore(parent, "resource", "kind", "add", "--kind", jorKind, "--to", tlOrg+"/org", "--yes").OK(t)
		lee.Lore(parent, "resource", "add", "--kind", jorKind, "--name", jorRes, "--to", to, "--yes").OK(t).Never(t, "E_KIND_UNKNOWN")
		// no flag: every folder of the org's stores is read
		lee.Lore(parent, "resource", "list", "--kind", jorKind).OK(t).Says(t, jorRes).Never(t, "E_NOT_A_STORE")
		lee.Lore(parent, "rule", "list").OK(t).Says(t, jorRule)

		// D-422: a repo recorded by url, its project named by id
		var folders []jFolderRow
		folders = jFolderList(t, lee.Lore(parent, "store", "folder", "list", "--json").OK(t))
		prj := ""
		for _, f := range folders {
			if f.Folder == jorFolder {
				prj = f.ID
			}
		}
		if prj == "" {
			t.Fatalf("store folder list has no %s folder: %+v", jorFolder, folders)
		}
		lee.Lore(parent, "repo", "add", "--project", prj, "--mount-name", jorMount, "--origin", jorOrigin, "--yes").OK(t).Never(t, "E_NOT_A_STORE")
		lee.Lore(parent, "repo", "list", "--project", prj).OK(t).Says(t, jorMount)

		if _, err := os.Stat(filepath.Join(parent, ".git")); !os.IsNotExist(err) {
			t.Fatalf("a command run from %s made it a git repo (%v)", parent, err)
		}
		if _, err := os.Stat(filepath.Join(parent, ".lore")); !os.IsNotExist(err) {
			t.Fatalf("a command run from %s made .lore/ there (%v)", parent, err)
		}
	})
}

// jorActorIDs picks the act_ ids of the two people (by email) from
// `actor list --json`.
func jorActorIDs(t testing.TB, out, fromEmail, intoEmail string) (string, string) {
	t.Helper()
	var got struct {
		Data []struct {
			ID        string `json:"id"`
			StableKey string `json:"stable_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("actor list --json: %v\n%s", err, out)
	}
	var from, into string
	for _, a := range got.Data {
		switch a.StableKey {
		case "human:" + fromEmail:
			from = a.ID
		case "human:" + intoEmail:
			into = a.ID
		}
	}
	if from == "" || into == "" {
		t.Fatalf("actor list has no %s or %s:\n%s", fromEmail, intoEmail, out)
	}
	return from, into
}
