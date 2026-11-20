//go:build journeys && !windows

package journeys

// move_linked_test.go — devco:D-731 (owner use pass 5, brief item 26):
// after `lore move` took an own repo's notes into its store folder, the
// repo still tracked a tombstone per moved row plus their comments, two
// actor rows and the project row. Now a move out of a linked repo removes
// the repo copy outright once the store holds the row: Lee seeds a repo
// with a rule, a memory with a comment, an open and a done task, links it
// to a folder, runs `lore move --from repo --to <org>/<folder> --yes`,
// commits through lore's own hooks and pushes; `git ls-files .lore/data`
// holds no moved row (only the format marker, policy, repo, actor and
// project rows the repo keeps), every row and the comment are in the folder with the same
// ids, and Ann reads them all from a fresh clone. An unlinked repo still
// keeps its tombstone after a move (it is still a place people write).

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	jMlSlug   = "github.com/acme-co/billing-app"
	jMlSlug2  = "github.com/acme-co/billing-solo"
	jMlFolder = "client_billing"
)

// jMlIDs are the rows jMlSeed wrote into the repo.
type jMlIDs struct {
	Rule, Memory, Comment, Open, Done string
}

// all lists every seeded id.
func (s jMlIDs) all() []string { return []string{s.Rule, s.Memory, s.Comment, s.Open, s.Done} }

// jMlAdd runs a write with --json and returns the first row id it prints
// (task add nests the row, the other writes print it at the top).
func jMlAdd(t *testing.T, m *Machine, repo *teamharness.Clone, args ...string) string {
	t.Helper()
	out := m.LoreIn(repo, append(args, "--json")...).OK(t).Stdout
	id := jMlIDRe.FindStringSubmatch(out)
	if id == nil {
		t.Fatalf("%v printed no id:\n%s", args, out)
	}
	return id[1]
}

// jMlIDRe finds a row id in JSON output.
var jMlIDRe = regexp.MustCompile(`"id": ?"([a-z]{3}_[0-9a-z-]+)"`)

// jMlSeed fills repo with a rule, a memory with a comment, an open and a
// done task (all --to repo), commits and pushes.
func jMlSeed(t *testing.T, m *Machine, repo *teamharness.Clone) jMlIDs {
	t.Helper()
	m.LoreIn(repo, "init", "--non-interactive", "--name=billing").OK(t)
	var s jMlIDs
	s.Rule = jMlAdd(t, m, repo, "rule", "add", "--body", "Round invoices to the cent", "--severity", "must", "--to", "repo", "--yes")
	s.Memory = jMlAdd(t, m, repo, "memory", "add", "--body", "the billing cache warms at boot", "--to", "repo")
	s.Comment = jMlAdd(t, m, repo, "comment", "add", "--on-table", "memories", "--on-id", s.Memory, "--body", "checked on staging", "--to", "repo")
	s.Open = jMlAdd(t, m, repo, "task", "add", "--title", "Billing open task", "--to", "repo")
	s.Done = jMlAdd(t, m, repo, "task", "add", "--title", "Billing done task", "--to", "repo")
	m.LoreIn(repo, "task", "done", "--id", s.Done).OK(t)
	repo.CommitAll("lore notes")
	repo.Git("push", "-q", "origin", "HEAD:main")
	if out := repo.Git("ls-files", ".lore/data"); !strings.Contains(out, "/memories/") {
		t.Fatalf("the seeded repo does not track its rows:\n%s", out)
	}
	return s
}

func TestJourneyMoveOutOfLinkedRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	place := tlOrg + "/" + jMlFolder
	mainRef := "refs/heads/main"
	repo := jPlainRepo(t, lee, w.Remote(jMlSlug), filepath.Join(parent, "billing-app"))
	var s jMlIDs
	var invite string

	w.Step("day1/a-linked-repo-with-notes-and-a-comment", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jMlFolder).OK(t)
		s = jMlSeed(t, lee, repo)
		lee.LoreIn(repo, "org", "link", tlOrg, "--project", jMlFolder, "--yes").OK(t)
		if st := repo.Status(); st != "" {
			repo.CommitAll("lore: link")
		}
		_, invite = tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
	})

	w.Step("day2/move-commit-through-the-hooks-nothing-left", func() {
		w.AdvanceDays(1)
		lee.LoreIn(repo, "move", "--from", "repo", "--to", place, "--yes").OK(t).
			Says(t, "✓ moved ").Never(t, "W_MOVED_KEPT", "was not found")
		repo.Git("add", "-A", ".lore/data")
		c := repo.TryGit("commit", "-m", "lore: move the notes to "+place)
		if c.Code != 0 || strings.Contains(c.Out(), "by removing the file") || strings.Contains(c.Out(), "E_INTERNAL") {
			t.Fatalf("the commit after the move failed: exit %d\n%s", c.Code, c.Out())
		}
		jMlOnlyKept(t, "the linked repo", repo.Git("ls-files", ".lore/data"))
		if n := jAmLiveTasks(t, repo.Git); n != 0 {
			t.Errorf("%d moved tasks are still on the repo's live ref", n)
		}
		repo.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "sync").OK(t)
		for _, id := range s.all() {
			if !jMlStoreHas(tt.store, mainRef, id) {
				t.Errorf("the folder has no row %s after the move", id)
			}
			lee.LoreIn(repo, "show", "--id", id).OK(t).Never(t, "two live rows", "E_AMBIGUOUS_ID", "E_NOT_FOUND")
		}
		lee.LoreIn(repo, "comment", "list", "--on-table", "memories", "--on-id", s.Memory).OK(t).Says(t, "checked on staging")
	})

	w.Step("day3/a-teammate-pulls-and-reads-every-row-and-the-comment", func() {
		w.AdvanceDays(1)
		ann := tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, invite, tlAnn, []string{tlFolders})
		peer := ann.Clone(w.Remote(jMlSlug), "billing-app")
		ann.LoreIn(peer, "sync").OK(t)
		jMlOnlyKept(t, "Ann's clone", peer.Git("ls-files", ".lore/data"))
		for _, id := range s.all() {
			ann.LoreIn(peer, "show", "--id", id).OK(t).Never(t, "two live rows", "E_AMBIGUOUS_ID", "E_NOT_FOUND")
		}
		ann.LoreIn(peer, "comment", "list", "--on-table", "memories", "--on-id", s.Memory).OK(t).Says(t, "checked on staging")
	})

	w.Step("day3/an-unlinked-repo-keeps-its-tombstone", func() {
		solo := jPlainRepo(t, lee, w.Remote(jMlSlug2), filepath.Join(parent, "billing-solo"))
		u := jMlSeed(t, lee, solo)
		lee.LoreIn(solo, "move", "--from", "repo", "--to", place, "--where", "table=memories", "--yes").OK(t).Says(t, "✓ moved ")
		solo.Git("add", "-A", ".lore/data")
		if c := solo.TryGit("commit", "-m", "lore: move a memory"); c.Code != 0 {
			t.Fatalf("the commit after the move failed: exit %d\n%s", c.Code, c.Out())
		}
		if out := solo.Git("ls-files", ".lore/data/memories"); !strings.Contains(out, u.Memory) {
			t.Errorf("the unlinked repo dropped the moved memory's tombstone (want it kept):\n%s", out)
		}
		if list := lee.LoreIn(solo, "memory", "list", "--store", place, "--json").OK(t).Stdout; !strings.Contains(list, u.Memory) {
			t.Errorf("the folder does not list the memory moved out of the unlinked repo:\n%s", list)
		}
	})
}

// jMlKept are the .lore/data entries a linked repo keeps after its notes
// moved out: the format marker, its branch-mode policy (read by its
// hooks), its repo row, and the actor and project rows those reference.
// No moved table (rules, memories, tasks, comments) may be left.
var jMlKept = []string{".lore/data/_meta.json", ".lore/data/policies/", ".lore/data/repos/", ".lore/data/actors/", ".lore/data/projects/"}

// jMlOnlyKept fails for each tracked path of files outside jMlKept.
func jMlOnlyKept(t *testing.T, who, files string) {
	t.Helper()
	for _, f := range strings.Split(files, "\n") {
		kept := f == ""
		for _, k := range jMlKept {
			kept = kept || strings.HasPrefix(f, k)
		}
		if !kept {
			t.Errorf("%s still tracks %s after the move", who, f)
		}
	}
}

// jMlStoreHas reports whether the store's ref holds a file named for id.
func jMlStoreHas(r *teamharness.Remote, ref, id string) bool {
	for _, f := range r.FilesMatching(ref, jMlFolder+"/") {
		if strings.Contains(f, id) {
			return true
		}
	}
	return false
}
