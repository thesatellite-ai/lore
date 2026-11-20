//go:build stories && !windows

package stories

// O_personal_res_mentions_test.go — story O, devco D-764 (owner P-6, bug
// hunt H-23): in a personal knowledge base, notes that mention a resource
// as ${res:<kind>/<name>} never reached row_refs / row_expanded (the
// resolver read the `/` as name/field), and `lore resource graph --up`
// listed only resources, never the notes that use one. Here an
// owner-shaped laptop keeps a personal store on a local bare repo, writes
// notes in every name form a resource lookup takes (one before the
// resource exists), and graph --up and impact list each of them under the
// resource it mentions, from the home folder with --store.

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestStoryOPersonalResMentions: graph --up and impact list the personal
// notes that mention a resource, in every name form (devco D-764).
func TestStoryOPersonalResMentions(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	to := "me/" + psFolder
	mention := map[string]string{} // note body -> the resource it mentions
	add := func(body, res string) {
		ada.Lore(ada.Home, "memory", "add", "--body", body, "--to", to, "--yes").OK(t)
		mention[body] = res
	}
	s.Step("personal-store/notes-mention-resources", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", psFolder).OK(t)
		ada.Lore(ada.Home, "resource", "kind", "add", "--kind", "server", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "field", "add", "--kind", "server", "--field", "ip", "--type", "ip", "--to", to, "--yes").OK(t)
		add("db box is ${res:"+psFolder+":server/db_prod}, noted before it existed", "db_prod")
		for _, n := range []string{"web_prod", "db_prod"} {
			ada.Lore(ada.Home, "resource", "add", "--kind", "server", "--name", n, "--set", "ip=203.0.113.10", "--to", to, "--yes").OK(t)
		}
		add("deploy notes for ${res:server/web_prod}", "web_prod")
		add("web ip is ${res:server/web_prod/ip}", "web_prod")
		add("bare ${res:web_prod} still works", "web_prod")
		add("no reference to web_prod here", "")
	})
	s.Step("personal-store/graph-up-lists-notes", func() {
		for _, res := range []string{"web_prod", "db_prod"} {
			for _, cmd := range [][]string{
				{"resource", "graph", "--id", res, "--up", "--store", to},
				{"resource", "impact", "--id", res, "--store", to},
			} {
				r := ada.Lore(ada.Home, cmd...)
				r.OK(t)
				for body, want := range mention {
					if listed := strings.Contains(r.Out(), firstWords(body)); listed != (want == res) {
						t.Errorf("[O D-764] `lore %s`: note %q listed=%v, want %v:\n%s", strings.Join(cmd, " "), body, listed, want == res, r)
					}
				}
				j := ada.Lore(ada.Home, append(cmd, "--json")...)
				j.OK(t)
				var out struct {
					Mentions []struct {
						Row, Table, Field string
					} `json:"mentions"`
				}
				if err := json.Unmarshal([]byte(j.Out()), &out); err != nil || len(out.Mentions) == 0 {
					t.Errorf("[O D-764] `lore %s --json` has no mentions: %v\n%s", strings.Join(cmd, " "), err, j)
				}
			}
		}
	})
}

// firstWords is the start of a note body as the mention list prints it
// (its title is the first line, cut at 60 characters).
func firstWords(body string) string {
	if len(body) > 30 {
		return body[:30]
	}
	return body
}
