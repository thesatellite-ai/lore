//go:build machines && !windows

package machines

// sot_stores_test.go — one address per store id and the org entry as a
// stamped copy of the store's lore-org.yaml, through the built binary and
// `lore api` on two machines (SOURCE_OF_TRUTH rows 7 and 9):
//
//	the host renames the team store; the admin attaches it at the new url
//	  ─▶ store list marks the second address, the doctor warns with the
//	     set-url fix, a write there is refused (E_REFUSED, both urls)
//	the admin moves the store with set-url over REST
//	  ─▶ stores.yaml, orgs[].stores, the address record and the store's
//	     lore-org.yaml all name the new url; writes work; doctor clean
//	the member's next fetch reads the new lore-org.yaml
//	  ─▶ its org entry follows, stamped with the file's version
//	the member's entry is changed by hand and unstamped
//	  ─▶ doctor org_source with `lore org refresh`; the setup page's Run
//	     it (setup.fix org.source) restores it; doctor clean

import (
	"net/http"
	"strings"
	"testing"
)

func TestSotStoresMove(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	lsGHHosts(t, m)
	admin, tm := ssTeam(t, m, ssMemberEmail)
	lsCreate(t, m, "acme", kindMember, ssMemberEmail, []string{"~/code/**"}, true, tm, lsAlpha)
	repo, adminRepo := m.Repos[RepoWorkApp], admin.Repos[RepoWorkApp]
	lsRun(t, m, repo, "memory", "add", "member note before the move", "--to", "repo")
	if c := ssConfig(t, m); !strings.Contains(c, "source:") || !strings.Contains(c, "version:") {
		t.Errorf("the member's imported org entry carries no source stamp:\n%s", c)
	}

	// The host renames the store; the admin attaches it at the new url.
	ssRenameOnHost(t, tm, m, admin)
	lsRun(t, admin, adminRepo, "store", "add", "moved", ssMovedURL, "--yes")
	before := ssDoctor(t, admin, adminRepo, "store_address:")
	if len(before) != 1 || !strings.Contains(before[0], lsTeamURL) || !strings.Contains(before[0], ssMovedURL) || !strings.Contains(before[0], "fix: lore store set-url sto_") {
		t.Fatalf("doctor with the store at two addresses: want one store_address warning naming both urls and the set-url fix, got %q", before)
	}
	id := ssStoreID.FindStringSubmatch(before[0])
	if id == nil {
		t.Fatalf("no store id in %q", before[0])
	}
	marked := false
	for _, s := range ssList(t, admin, adminRepo).Stores {
		if s.Name == "moved" && strings.HasPrefix(s.Address, "second address") {
			marked = true
		}
	}
	if !marked {
		t.Errorf("store list does not mark the second address: %s", lsRun(t, admin, adminRepo, "store", "list"))
	}
	r := admin.Lore(t, adminRepo, "memory", "add", "to the second address", "--to", "moved", "--json")
	if r.Code == 0 || !strings.Contains(r.Stdout+r.Stderr, "E_REFUSED") || !strings.Contains(r.Stdout+r.Stderr, ssMovedURL) ||
		!strings.Contains(r.Stdout+r.Stderr, lsTeamURL) || !strings.Contains(r.Stdout+r.Stderr, "lore store set-url") {
		t.Fatalf("a write to the second address was not refused naming both urls and set-url: %s", r)
	}

	// The admin moves the store over REST: every place follows.
	api := csStartAPI(t, admin, adminRepo)
	out := sotStoresCall(t, api, http.MethodPost, "/v1/stores/set_url", `{"name":"`+id[1]+`","url":"`+ssMovedURL+`","yes":true}`)
	if !strings.Contains(out, "orgs[") {
		t.Errorf("set-url did not move the org entry: %s", out)
	}
	if c := ssConfig(t, admin); strings.Contains(c, lsTeamURL) || !strings.Contains(c, ssMovedURL) || !strings.Contains(c, "version:") {
		t.Errorf("the admin's config after set-url:\n%s", c)
	}
	if f := ssOrgFile(t, tm.bare+"-moved"); !strings.Contains(f, ssMovedURL) || strings.Contains(f, lsTeamURL) {
		t.Errorf("lore-org.yaml after set-url:\n%s", f)
	}
	if got := ssDoctor(t, admin, adminRepo, "store_address:"); len(got) != 0 {
		t.Errorf("doctor after set-url still warns: %q", got)
	}
	lsRun(t, admin, adminRepo, "memory", "add", "to the moved store", "--to", "moved")
	lsRun(t, admin, adminRepo, "actor", "edit", "human:"+ssMemberEmail, "--title", "Engineer")

	// The member's next fetch refreshes its org entry from lore-org.yaml.
	if c := ssConfig(t, m); strings.Contains(c, ssMovedURL) {
		t.Fatalf("the member's config moved before any fetch:\n%s", c)
	}
	lsRun(t, m, repo, "sync")
	lsRun(t, m, repo, "memory", "list")
	if c := ssConfig(t, m); !strings.Contains(c, ssMovedURL) || strings.Contains(c, "url: "+lsTeamURL) {
		t.Errorf("the member's org entry did not follow the store's lore-org.yaml after a fetch:\n%s", c)
	}
	if got := ssDoctor(t, m, repo, "store_address:"); len(got) != 0 {
		t.Errorf("member doctor after the refresh: %q", got)
	}
	lsRun(t, m, repo, "memory", "add", "member note after the move", "--to", "repo")

	// A hand-changed, unstamped entry: the doctor flags it; Run it fixes it.
	ssEditConfig(t, m, func(c string) string {
		var keep []string
		skip := false
		for _, l := range strings.Split(c, "\n") {
			trim := strings.TrimSpace(l)
			switch {
			case trim == "source:":
				indent := l[:len(l)-len(strings.TrimLeft(l, " "))]
				keep = append(keep, indent+"repos:", indent+"  - github.com/elsewhere/*")
				skip = true
				continue
			case skip && (strings.HasPrefix(trim, "store:") || strings.HasPrefix(trim, "version:")):
				continue
			}
			skip = false
			keep = append(keep, l)
		}
		return strings.Join(keep, "\n")
	})
	flag := ssDoctor(t, m, repo, "org_source:")
	if len(flag) != 1 || !strings.Contains(flag[0], "lore org refresh") {
		t.Fatalf("doctor on a hand-changed org entry: want one org_source warning with `lore org refresh`, got %q\nconfig:\n%s", flag, ssConfig(t, m))
	}
	mapi := csStartAPI(t, m, repo)
	sotStoresCall(t, mapi, http.MethodPost, "/v1/setup/fix", `{"setup":"acme","check":"org.source","yes":true}`)
	if got := ssDoctor(t, m, repo, "org_source:"); len(got) != 0 {
		t.Errorf("doctor after the Run it: %q\nconfig:\n%s", got, ssConfig(t, m))
	}
	if c := ssConfig(t, m); strings.Contains(c, "github.com/elsewhere/*") || !strings.Contains(c, "version:") {
		t.Errorf("the member's org entry after lore org refresh:\n%s", c)
	}
}

// sotStoresCall is one REST call that must answer 200.
func sotStoresCall(t *testing.T, a *csAPI, method, path, body string) string {
	t.Helper()
	got, out := a.call(t, method, path, body)
	if got != http.StatusOK {
		t.Fatalf("%s %s: HTTP %d: %s", method, path, got, out)
	}
	return out
}
