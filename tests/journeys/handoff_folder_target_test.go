//go:build journeys && !windows

package journeys

// handoff_folder_target_test.go — devco D-966 (bug hunt H-225): Lee's web
// repo is linked to the team folder client_web. He leaves a handoff in the
// other team folder with `lore handoff add --store acmeteam/client_api`,
// the spelling `--store` documents (<store>/<folder>), as `--to` works for
// every other kind. It refused "write the actor row to store
// acmeteam/client_api: no store named acmeteam/client_api is attached":
// handoff names its target in a `store` field (its --to is the old alias
// of --for), and only a `to` field had its <store>/<folder> attached for
// the run. Now the handoff and every row the write makes with it (the
// writer's actor row, the folder's project row) land in client_api, and
// the handoff is listed there.

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The handoff folder-target journey's names (placeholders).
const (
	jHoSlug   = "github.com/acme-co/handoff-web"
	jHoLinked = "client_web"
	jHoOther  = "client_api"
	jHoMark   = "d966handoff"
)

func TestJourneyHandoffStoreFolderFromLinkedRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	var web, otherID string
	other := tlOrg + "/" + jHoOther

	w.Step("day1/repo-linked-to-one-folder", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jHoLinked).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", jHoOther).OK(t)
		c := lee.Clone(w.Remote(jHoSlug), "handoff-web")
		lee.MoveClone(t, c, filepath.Join(parent, "handoff-web"))
		lee.LoreIn(c, "init", "--non-interactive", "--name=handoff-web").OK(t)
		lee.LoreIn(c, "org", "link", tlOrg, "--project", jHoLinked, "--yes").OK(t)
		web = c.Dir
	})

	w.Step("day1/handoff-into-the-other-folder", func() {
		r := lee.Lore(web, "handoff", "add", "--store", other, "--for", tlLee.Name, "--body", jHoMark, "--yes")
		if r.Code != 0 || strings.Contains(r.Stderr+r.Stdout, "is attached") {
			t.Fatalf("[D-966] `lore handoff add --store %s` from a repo linked to %s refused:\n%s", other, jHoLinked, r)
		}
		r.Says(t, "handoff")
		ls := lee.Lore(web, "handoff", "list", "--store", other, "--json").OK(t)
		if m := regexp.MustCompile(`"id": "(hnd_[0-9a-f]{32})"`).FindStringSubmatch(ls.Out()); m != nil {
			otherID = m[1]
		} else {
			t.Fatalf("no handoff id in:\n%s", ls)
		}
		// the same spelling from --to of another kind, for comparison
		lee.Lore(web, "memory", "add", "--to", other, "--body", "d966memory", "--yes").OK(t)
	})

	w.Step("day1/the-handoff-is-in-that-folder", func() {
		r := lee.Lore(web, "handoff", "list", "--store", other).OK(t)
		if !strings.Contains(r.Stdout, jHoMark) {
			t.Errorf("[D-966] `lore handoff list --store %s` misses the handoff written there:\n%s", other, r)
		}
		mine := lee.Lore(web, "handoff", "list", "--store", tlOrg+"/"+jHoLinked).OK(t)
		if strings.Contains(mine.Stdout, jHoMark) {
			t.Errorf("[D-966] the handoff written with --store %s landed in the linked folder %s:\n%s", other, jHoLinked, mine)
		}
	})

	w.Step("day1/store-team-and-from-home-like-to", func() {
		// --store team is the folder the repo is linked to, as --to team is
		lee.Lore(web, "handoff", "add", "--store", "team", "--for", tlLee.Name, "--body", jHoMark+"team", "--yes").OK(t)
		want := jHoMark + "team [" + tlOrg + "/" + jHoLinked + "]"
		if r := lee.Lore(web, "handoff", "list").OK(t); !strings.Contains(r.Stdout, want) {
			t.Errorf("[D-966] `handoff add --store team` did not land in the linked folder %s (want %q):\n%s", jHoLinked, want, r)
		}
		// a handoff written with no --store goes to the linked folder too
		lee.Lore(web, "handoff", "add", "--for", tlLee.Name, "--body", jHoMark+"plain", "--yes").OK(t)
		// from the home folder --store <org>/<folder> names the place, as --to does
		lee.Lore(lee.Home, "handoff", "add", "--store", other, "--for", tlLee.Name, "--body", jHoMark+"home", "--yes").OK(t)
		if r := lee.Lore(web, "handoff", "list", "--store", other).OK(t); !strings.Contains(r.Stdout, jHoMark+"home") {
			t.Errorf("[D-966] `handoff add --store %s` from the home folder did not land there:\n%s", other, r)
		}
	})

	w.Step("day1/every-handoff-read-finds-the-linked-folder-by-its-name", func() {
		// the linked folder spelled <org>/<folder>, as memory list takes it
		linked := tlOrg + "/" + jHoLinked
		marks := []string{jHoMark + "team", jHoMark + "plain"}
		for _, dir := range []string{web, lee.Home} {
			for _, rd := range [][]string{
				{"handoff", "list", "--store", linked},
				{"handoff", "list", "--store", tlOrg},
				{"handoff", "search", "--query", jHoMark + "plain", "--store", linked},
			} {
				r := lee.Lore(dir, rd...).OK(t)
				for _, mk := range marks {
					if rd[1] == "search" && mk != jHoMark+"plain" {
						continue
					}
					if !strings.Contains(r.Stdout, mk) {
						t.Errorf("[D-966] `lore %s` (in %s) misses the handoff %s of the linked folder:\n%s", strings.Join(rd, " "), dir, mk, r)
					}
				}
			}
		}
		// show of the handoff in the other folder, by its <org>/<folder>
		for _, dir := range []string{web, lee.Home} {
			lee.Lore(dir, "handoff", "show", "--id", otherID, "--store", other, "--json").OK(t).Says(t, jHoMark)
		}
		// the memory of the same folder, read the same way, for comparison
		lee.Lore(web, "memory", "add", "--body", "d966linkedmem", "--yes").OK(t)
		lee.Lore(web, "memory", "list", "--store", linked).OK(t).Says(t, "d966linkedmem")
	})
}
