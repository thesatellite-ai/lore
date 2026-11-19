//go:build journeys && !windows

package journeys

// storeroot_reads_test.go — devco D-505: Lee stands in the root of his
// org store's clone (the folder that holds every project folder) and reads.
// memory list always answered there; pattern, tag, repo and link list
// refused "store acme has no project row yet, and a read never makes one".
// Now each lists every project's rows, as memory list does; project show
// (which has no default project there) says why and how to name one; and
// nothing is written to the clone.

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The store-root reads journey's names (placeholders).
const (
	jRootSlug    = "github.com/acme-co/root-web"
	jRootFolder  = "client_root"
	jRootPattern = "Retry with backoff"
	jRootTag     = "flaky_net"
	// devco:D-656: a second repo linked to a second folder
	jRootFolder2 = "client_two"
	jRootRepo1   = "web-front"
	jRootRepo2   = "api-back"
)

func TestJourneyStoreRootReads(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	var storeDir string
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", storeDir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in the store clone: %v\n%s", args, err, out)
		}
		return string(out)
	}

	w.Step("day1/rows-in-a-store-folder", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jRootFolder).OK(t)
		c := lee.Clone(w.Remote(jRootSlug), "root-web")
		lee.MoveClone(t, c, filepath.Join(parent, "root-web"))
		lee.LoreIn(c, "init", "--non-interactive", "--name=root-web").OK(t)
		lee.LoreIn(c, "org", "link", tlOrg, "--project", jRootFolder, "--yes").OK(t)
		lee.Lore(c.Dir, "pattern", "add", "--title", jRootPattern, "--body", "wait and retry", "--to", tlOrg).OK(t)
		lee.Lore(c.Dir, "tag", "add", "--name", jRootTag, "--to", tlOrg).OK(t)
		lee.Lore(c.Dir, "memory", "add", "--body", "the store root reads", "--to", tlOrg).OK(t)
		t1 := jID(t, lee.Lore(c.Dir, "task", "add", "Root task", "--to", tlOrg, "--json").OK(t))
		p := jPath(t, lee.Lore(c.Dir, "path", t1, "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join(jRootFolder, "tasks", t1+".json"))
		if storeDir == p.Path {
			t.Fatalf("lore path %s = %s, want it under the %s folder of the store clone", t1, p.Path, jRootFolder)
		}
		lee.Lore(c.Dir, "sync").OK(t)
		// devco:D-656: repos recorded in two folders (a task naming a
		// --repo the folder's project has not seen records it there)
		lee.Lore(parent, "store", "folder", "add", "team", jRootFolder2).OK(t)
		lee.Lore(c.Dir, "task", "add", "Web task", "--to", tlOrg+"/"+jRootFolder, "--repo", jRootRepo1).OK(t)
		lee.Lore(c.Dir, "task", "add", "Api task", "--to", tlOrg+"/"+jRootFolder2, "--repo", jRootRepo2).OK(t)
		lee.Lore(c.Dir, "sync").OK(t)
	})

	w.Step("day2/every-list-answers-at-the-store-root", func() {
		w.AdvanceDays(1)
		before := git("status", "--porcelain")
		head := git("rev-parse", "HEAD")
		for _, c := range []struct {
			args []string
			want string
		}{
			{[]string{"memory", "list"}, "the store root reads"},
			{[]string{"pattern", "list"}, jRootPattern},
			{[]string{"pattern", "search", "backoff"}, jRootPattern},
			{[]string{"tag", "list"}, jRootTag},
			{[]string{"repo", "list"}, ""},
			{[]string{"link", "list"}, ""},
		} {
			r := lee.Lore(storeDir, c.args...)
			if r.Code != 0 || strings.Contains(r.Out(), "has no project row yet") {
				t.Errorf("lore %s at the store root refused; want it to list every project's rows:\n%s", strings.Join(c.args, " "), r)
				continue
			}
			if c.want != "" && !strings.Contains(r.Out(), c.want) {
				t.Errorf("lore %s at the store root lacks %q:\n%s", strings.Join(c.args, " "), c.want, r)
			}
		}
		// devco:D-656: repo list over every project reads every folder's
		// repos, at the store root and under --setup from home alike
		for _, at := range []struct {
			dir  string
			args []string
		}{
			{storeDir, []string{"repo", "list"}},
			{lee.Home, []string{"repo", "list", "--setup", tlLeadSetup}},
		} {
			r := lee.Lore(at.dir, at.args...).OK(t)
			for _, want := range []string{jRootRepo1, jRootRepo2} {
				if !strings.Contains(r.Out(), want) {
					t.Errorf("lore %s in %s lacks the repo %s recorded in its folder:\n%s", strings.Join(at.args, " "), at.dir, want, r)
				}
			}
		}
		r := lee.Lore(storeDir, "project", "show")
		if r.Code == 0 || strings.Contains(r.Out(), "has no project row yet") || !strings.Contains(r.Out(), "root of a store") || !strings.Contains(r.Out(), "--store") || !strings.Contains(r.Out(), "--setup") {
			t.Errorf("lore project show at the store root: want a refusal saying it is the root of a store, naming --store and --setup:\n%s", r)
		}
		if got := git("status", "--porcelain"); got != before {
			t.Errorf("reads at the store root changed the clone's status:\n%s\n->\n%s", before, got)
		}
		if got := git("rev-parse", "HEAD"); got != head {
			t.Errorf("reads at the store root committed to the clone: HEAD %s -> %s", head, got)
		}
	})
}
