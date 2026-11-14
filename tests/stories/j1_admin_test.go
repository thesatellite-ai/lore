//go:build stories && !windows

package stories

// j1_admin_test.go — story J1, the org admin with many repos (page:
// J1-admin-many-repos.md). One admin, one laptop, every kind of repo under
// ~/code: a fresh code repo, a big lore 0.1.17 repo (5.5k rows), a small
// 0.1.17 repo, a monorepo, a docs-only repo and a repo with no lore data.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The admin's names (placeholders).
const (
	j1Org     = "acmeco"
	j1Setup   = "acme"
	j1Folders = "~/code/**"
	j1OldLine = "is on the lore 0.1.17 format"
)

var j1Ada = journeys.Person{Name: "Ada Admin", Email: "ada.admin@example.test", Login: "ada-gh"}

// j1Apply answers one step of the guided setup with a JSON answer object,
// failing with `lore setup check`'s reasons when lore refuses it.
func j1Apply(t testing.TB, m *journeys.Machine, dir, setup, step, answers string) {
	t.Helper()
	if r := m.Lore(dir, "setup", "apply", setup, "--step", step, "--answers", answers, "--json"); r.Code != 0 {
		t.Fatalf("setup apply %s --step %s: %s\ncheck says: %s", setup, step, r, m.Lore(dir, "setup", "check", setup, "--step", step, "--answers", answers, "--json").Stdout)
	}
}

// j1StartTeam is the admin's guided setup (the team-lead journey's answers)
// with the org's own repos named in the team step: every repo of the org's
// GitHub owner is an own repo, so its notes stay in the repo.
func j1StartTeam(t testing.TB, m *journeys.Machine, dir, storeURL string) {
	t.Helper()
	q := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	for _, st := range [][2]string{
		{"kind", `{"kind":"admin","name":` + q(j1Setup) + `}`},
		{"identity", `{"name":` + q(j1Ada.Name) + `,"email":` + q(j1Ada.Email) + `}`},
		{"github", `{"github_account":` + q(j1Ada.Login) + `,"host_alias":"github.com"}`},
		{"personal_store", `{"personal_store":"local"}`},
		{"team", `{"org":` + q(j1Org) + `,"store_url":` + q(storeURL) + `,"own":["github.com/acme-co/*"]}`},
		{"tools", `{"tools":["claude_code"]}`},
		{"secrets", `{"backend":"none"}`},
		{"folders", `{"folders":[` + q(j1Folders) + `],"default":false}`},
		{"this_folder", `{}`},
		{"summary", `{}`},
	} {
		j1Apply(t, m, dir, j1Setup, st[0], st[1])
	}
	m.Lore(dir, "skill", "install", "--tool", "claude_code").OK(t)
	m.Lore(dir, "sync").OK(t)
}

// j1ID is the id of a --json add answer.
func j1ID(t testing.TB, r teamharness.Result) string {
	t.Helper()
	var o struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &o); err != nil || o.ID+o.Data.ID == "" {
		t.Fatalf("no id in the --json answer (%v): %s", err, r)
	}
	if o.Data.ID != "" {
		return o.Data.ID
	}
	return o.ID
}

// j1Count is the number of rows of a list --json answer.
func j1Count(t testing.TB, r teamharness.Result) int {
	t.Helper()
	var o struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &o); err != nil {
		t.Fatalf("not a list --json answer (%v): %s", err, r)
	}
	return len(o.Data)
}

// TestStoryJ1AdminManyRepos replays J1 end to end.
func TestStoryJ1AdminManyRepos(t *testing.T) {
	s := New(t, "J1")
	store := s.Remote("github.com/acme-co/lore-team")
	remotes := map[string]*teamharness.Remote{
		"app":   s.Seed(s.Remote("github.com/acme-co/app"), func(dir string) { PlainRepo(t, dir) }),
		"hub":   s.Seed(s.Remote("github.com/acme-co/hub"), func(dir string) { V017Repo(t, dir, "hub", HubShape) }),
		"svc":   s.Seed(s.Remote("github.com/acme-co/svc"), func(dir string) { V017Repo(t, dir, "svc", SmallShape) }),
		"mono":  s.Seed(s.Remote("github.com/acme-co/mono"), func(dir string) { Monorepo(t, dir) }),
		"docs":  s.Seed(s.Remote("github.com/acme-co/docs"), func(dir string) { DocsOnly(t, dir) }),
		"brick": s.Seed(s.Remote("github.com/acme-co/brick"), func(dir string) { PlainRepo(t, dir) }),
	}
	ada := s.Person(j1Ada.Name, j1Ada.Email)
	journeys.GitHubLogin(t, s.World, ada, j1Ada.Login)
	code := filepath.Join(ada.Home, "code")
	c := map[string]*teamharness.Clone{}
	refs := map[string]string{}
	var brickTree, docsTree string
	snapRefs := func() {
		for name, r := range remotes {
			refs[name] = s.RefsOf(r)
		}
	}
	noPush := func(what string) {
		t.Helper()
		for name, r := range remotes {
			s.NoPushSince(t, r, refs[name], what)
		}
	}
	oldStays := func(what string) {
		t.Helper()
		for _, name := range []string{"hub", "svc"} {
			if bad := NewFormatFiles(t, c[name].Dir); len(bad) > 0 {
				t.Errorf("[J1] %s raised the 0.1.17 repo %s: %d newer-format files, e.g. %s", what, name, len(bad), bad[0])
			}
		}
	}

	s.Step("day1/clone-every-repo", func() {
		for name, r := range remotes {
			cl := ada.Clone(r, name)
			ada.MoveClone(t, cl, filepath.Join(code, name))
			c[name] = cl
		}
		brickTree, docsTree = TreeOf(t, c["brick"].Dir), TreeOf(t, c["docs"].Dir)
		snapRefs()
	})

	s.Step("day1/start-the-team", func() {
		j1StartTeam(t, ada, c["app"].Dir, store.SSHURL())
		ada.LoreIn(c["app"], "setup", "list").OK(t).Says(t, j1Setup, "admin", j1Org)
		ada.LoreIn(c["app"], "whoami").OK(t).Says(t, j1Org, j1Ada.Email)
		if !store.Has("refs/heads/main") {
			t.Error("the team store has no main after the team step")
		}
		s.Untouched(t, c["brick"].Dir, brickTree, "the guided setup")
		oldStays("the guided setup")
		noPush("the guided setup (code repos)")
	})

	s.Step("day1/fix-all-twice", func() {
		start := time.Now()
		r := ada.Lore("", "fix", "--all", "--yes").OK(t)
		first := time.Since(start)
		for _, name := range []string{"hub", "svc"} {
			r.Says(t, c[name].Dir, j1OldLine)
		}
		r.Says(t, "lore fix --migrate", "2 on the lore 0.1.17 format, not migrated").Never(t, "Upgraded", "== "+c["brick"].Dir, "== "+c["docs"].Dir)
		s.NamedFlags(t, r)
		start = time.Now()
		r2 := ada.Lore("", "fix", "--all", "--yes").OK(t).Never(t, "Upgraded")
		second := time.Since(start)
		t.Logf("fix --all: first %v, second %v", first.Round(time.Millisecond), second.Round(time.Millisecond))
		if second > 60*time.Second {
			t.Errorf("[J1] the second fix --all took %v (nothing to do: it must be quick)", second)
		}
		r2.Says(t, c["hub"].Dir, j1OldLine)
		s.Untouched(t, c["brick"].Dir, brickTree, "lore fix --all (twice)")
		s.Untouched(t, c["docs"].Dir, docsTree, "lore fix --all (twice)")
		oldStays("lore fix --all without --migrate")
		noPush("lore fix --all")
	})

	s.Step("day1/the-old-repos-read-and-refuse-writes", func() {
		start := time.Now()
		n := j1Count(t, ada.LoreIn(c["hub"], "task", "list", "--all", "--json").OK(t))
		took := time.Since(start)
		t.Logf("task list on the 5.5k-row repo: %v", took.Round(time.Millisecond))
		if took > 15*time.Second {
			t.Errorf("[J1] a read on the 5.5k-row repo took %v (B1-16: it took 42-56 s once; a read is a few seconds)", took)
		}
		if n != HubShape["tasks"] {
			t.Errorf("[J1] the big 0.1.17 repo lists %d tasks, want %d", n, HubShape["tasks"])
		}
		w := ada.LoreIn(c["svc"], "task", "add", "--title", "a write in an old repo")
		if w.Code == 0 {
			t.Errorf("[J1] a write in a 0.1.17-format repo succeeded without lore migrate run: %s", w)
		}
		w.Says(t, j1OldLine, "lore migrate run")
		s.NamedFlags(t, w)
		ada.LoreIn(c["svc"], "migrate", "status").OK(t).Says(t, "0.1.17")
		// a write in a repo with no lore data is refused and leaves nothing behind
		ada.LoreIn(c["docs"], "task", "add", "--title", "a note in the docs repo")
		s.Untouched(t, c["docs"].Dir, docsTree, "a refused write in a repo with no lore data")
		ada.Lore("", "whoami").OK(t).Never(t, "client repo")
		oldStays("reads and a refused write")
		noPush("reads in the old repos")
	})

	var taskID, memID string
	s.Step("day2/notes-in-two-repos", func() {
		s.AdvanceDays(1)
		for _, name := range []string{"app", "mono"} {
			ada.LoreIn(c[name], "init").OK(t)
		}
		snapRefs()
		taskID = j1ID(t, ada.LoreIn(c["app"], "task", "add", "--title", "rotate the deploy keys", "--json").OK(t))
		ada.LoreIn(c["app"], "task", "edit", "--id", taskID, "--priority", "high").OK(t)
		memID = j1ID(t, ada.LoreIn(c["app"], "memory", "add", "--body", "the gateway retries twice on 502", "--json").OK(t))
		ada.LoreIn(c["app"], "memory", "edit", "--id", memID, "--body", "the gateway retries three times on 502").OK(t)
		ada.LoreIn(c["app"], "rule", "add", "--body", "never log request tokens", "--severity", "must").OK(t)
		ada.LoreIn(c["mono"], "task", "add", "--title", "split the billing service").OK(t)
		ada.LoreIn(c["mono"], "memory", "add", "--body", "ledger owns the invoice numbers").OK(t)
		ada.LoreIn(c["app"], "task", "list").OK(t).Says(t, "rotate the deploy keys")
		ada.LoreIn(c["app"], "search", "--query", "gateway").OK(t).Says(t, "three times")
		ada.LoreIn(c["mono"], "search", "--query", "ledger").OK(t).Says(t, "invoice numbers")
		ada.LoreIn(c["mono"], "task", "list").OK(t).Says(t, "split the billing service").Never(t, "rotate the deploy keys")
		noPush("adds and edits (no push without push or sync)")
		s.Untouched(t, c["brick"].Dir, brickTree, "notes in other repos")
	})

	s.Step("day2/render-and-look-around", func() {
		ada.LoreIn(c["app"], "render").OK(t)
		if b, ok := c["app"].ReadFile(".lore/LORE.md"); !ok || !strings.Contains(b, "never log request tokens") {
			t.Errorf("[J1] LORE.md does not carry the must rule after render")
		}
		ada.LoreIn(c["app"], "store", "list").OK(t).Says(t, j1Org)
		ada.LoreIn(c["app"], "where").OK(t)
		for _, name := range []string{"app", "hub", "svc", "mono"} {
			s.NamedFlags(t, ada.LoreIn(c[name], "doctor"))
		}
		ada.LoreIn(c["brick"], "doctor")
		s.Untouched(t, c["brick"].Dir, brickTree, "doctor and reads")
		noPush("render, store list, where, doctor")
	})

	s.Step("day2/members", func() {
		ada.LoreIn(c["app"], "member", "add", "--email", "ben.dev@example.test", "--name", "Ben Dev", "--yes").OK(t)
		ada.LoreIn(c["app"], "member", "list").OK(t).Says(t, "Ben Dev", j1Ada.Name)
		ada.LoreIn(c["app"], "sync").OK(t)
		ada.LoreIn(c["app"], "member", "list").OK(t).Says(t, "Ben Dev")
	})

	s.Step("day3/code-commits-through-the-hooks", func() {
		s.AdvanceDays(1)
		c["app"].WriteFile("deploy.go", "package main\n\n// keys rotate monthly\n")
		c["app"].Git("add", "-A")
		c["app"].Git("commit", "-q", "-m", "deploy: rotate keys")
		if st := c["app"].Status(); st != "" {
			t.Errorf("[J1] a code commit left the work tree dirty:\n%s", st)
		}
		fresh := ada.Clone(s.Remote("github.com/acme-co/fresh"), "fresh")
		ada.MoveClone(t, fresh, filepath.Join(code, "fresh"))
		ada.LoreIn(fresh, "init").OK(t)
		ada.LoreIn(fresh, "task", "add", "--title", "first task of a new repo").OK(t)
		fresh.WriteFile("main.go", "package main\n\nfunc main() {}\n")
		fresh.Git("add", "-A")
		fresh.Git("commit", "-q", "-m", "first commit")
		if fresh.Head() == "" {
			t.Error("[J1] the first commit of a fresh repo did not happen")
		}
		oldStays("code commits")
		s.Untouched(t, c["brick"].Dir, brickTree, "code commits in other repos")
	})
}
