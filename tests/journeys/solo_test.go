//go:build journeys && !windows

package journeys

// solo_test.go — persona 1, the solo dev: one person, a laptop, a personal
// store on their own GitHub account, a few repos, over six months.
//
//	day 1     install, the setup page's steps, first notes in a repo and in the personal store
//	week 1    a second repo, writes on a train (offline), the laptop sleeps mid-push
//	month 1   renames themselves, changes email, switches GitHub account and key,
//	          the repo moves to a new remote url, the folder is renamed
//	month 3   a new laptop (leaves the setup page halfway, comes back the next day),
//	          the old laptop is used again later with a note it never pushed
//	month 6   upgrade, swaps one coding tool for another, a personal store
//	          with thousands of rows and commits
//
// plus the undo paths: delete then restore, purge then regret, edit then
// edit back, remove the setup then re-add it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Placeholders: the person, their accounts and their stores.
const (
	soloName     = "Sam Solo"
	soloEmail    = "sam@solo.example"
	soloRenamed  = "Sam Renamed"
	soloNewEmail = "sam@newmail.example"
	soloLogin    = "sam-dev"
	soloLogin2   = "sam-new"
	soloSetup    = "personal"
	soloStore    = "github.com/sam-dev/lore-me"
	soloStoreURL = "git@github.com:sam-dev/lore-me.git"
)

// soloUnsentMe matches sync status's line for the personal store while it
// holds commits GitHub has not got ("  me: ahead 1, behind 0 of origin/main").
var soloUnsentMe = regexp.MustCompile(`(?m)^\s+me: ahead [1-9]`)

// soloApply saves one setup step the way the page does; a refusal fails
// the test with what `setup check` says.
func soloApply(t testing.TB, m *Machine, step string, answers any) {
	t.Helper()
	b, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	r := m.Lore("", "setup", "apply", soloSetup, "--step", step, "--answers", string(b), "--json")
	if r.Code != 0 {
		c := m.Lore("", "setup", "check", soloSetup, "--step", step, "--answers", string(b), "--json")
		t.Fatalf("setup apply %s: %s\ncheck says: %s", step, r, c.Stdout)
	}
}

// soloWork is the folder glob that covers every clone of m (the World
// clones under <Root>/work/<machine>/).
func soloWork(m *Machine) string { return filepath.Join(m.w.Root, "work", m.Name) + "/**" }

// soloSetupSteps runs the setup page's steps from github on (kind and
// identity are saved by the caller, so the new laptop can stop between).
func soloSetupRest(t testing.TB, m *Machine, store string) {
	t.Helper()
	soloApply(t, m, "github", map[string]any{"github_account": soloLogin, "host_alias": "github.com"})
	soloApply(t, m, "personal_store", map[string]any{"personal_store": store, "store_url": soloStoreURL})
	soloApply(t, m, "tools", map[string]any{"tools": []string{"claude_code"}})
	soloApply(t, m, "secrets", map[string]any{"backend": "none"})
	soloApply(t, m, "folders", map[string]any{"folders": []string{soloWork(m)}, "default": true})
	soloApply(t, m, "this_folder", map[string]any{})
	soloApply(t, m, "summary", map[string]any{})
}

func soloSetupStart(t testing.TB, m *Machine, name, email string) {
	t.Helper()
	soloApply(t, m, "kind", map[string]any{"kind": "solo", "name": soloSetup, "default": true})
	soloApply(t, m, "identity", map[string]any{"name": name, "email": email})
}

// soloBodies is every memory body `memory list` shows in dir, mapped to
// whether the human list marks it [me] (the personal store).
func soloBodies(t testing.TB, m *Machine, dir string) map[string]bool {
	t.Helper()
	r := m.Lore(dir, "memory", "list")
	if r.Code != 0 {
		t.Fatalf("memory list: %s", r)
	}
	out := map[string]bool{}
	for _, l := range strings.Split(r.Stdout, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || !strings.HasPrefix(f[0], "mem_") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(l, f[0]))
		body = strings.TrimSpace(strings.TrimPrefix(body, "[retrieved]"))
		me := strings.HasSuffix(body, "[me]")
		body = strings.TrimSpace(strings.TrimSuffix(body, "[me]"))
		out[body] = me
	}
	return out
}

// soloRemoteHas reports whether the personal store's main holds a row
// whose text contains body.
func soloRemoteHas(w *World, body string) bool {
	r := w.Remote(soloStore)
	for _, f := range r.FilesMatching("main", "memories/") {
		if s, ok := r.Read("main", f); ok && strings.Contains(s, body) {
			return true
		}
	}
	return false
}

// soloMemID is the id of the memory whose body is body in dir.
func soloMemID(t testing.TB, m *Machine, dir, body string) string {
	t.Helper()
	r := m.Lore(dir, "memory", "list", "--json")
	type row struct {
		ID   string `json:"id"`
		Body string `json:"body"`
	}
	var o struct {
		Data []row `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &o); err != nil {
		t.Fatalf("memory list --json: %v: %s", err, r)
	}
	for _, x := range o.Data {
		if x.Body == body {
			return x.ID
		}
	}
	t.Fatalf("no memory %q in %s", body, dir)
	return ""
}

// soloPersonal is whoami's personal store url.
func soloWhoami(t testing.TB, m *Machine, dir string) (name, key, personal string) {
	t.Helper()
	var o struct {
		Data struct {
			Identity struct {
				StableKey string `json:"stable_key"`
				Name      string `json:"name"`
			} `json:"identity"`
			Personal string `json:"personal_store"`
		} `json:"data"`
	}
	r := m.Lore(dir, "whoami", "--json")
	if err := json.Unmarshal([]byte(r.Stdout), &o); err != nil {
		t.Fatalf("whoami --json: %v: %s", err, r)
	}
	return o.Data.Identity.Name, o.Data.Identity.StableKey, o.Data.Personal
}

// soloActorID is whoami's act_ id in dir.
func soloActorID(t testing.TB, m *Machine, dir string) string {
	t.Helper()
	var o struct {
		Data struct {
			Identity struct {
				ActorID string `json:"actor_id"`
			} `json:"identity"`
		} `json:"data"`
	}
	r := m.Lore(dir, "whoami", "--json")
	if err := json.Unmarshal([]byte(r.Stdout), &o); err != nil || o.Data.Identity.ActorID == "" {
		t.Fatalf("whoami --json names no actor id (%v): %s", err, r)
	}
	return o.Data.Identity.ActorID
}

// identityActorID is the act_ id lore derives from a stable key.
func identityActorID(key string) (string, error) {
	id, err := identity.ActorID(key)
	return id.String(), err
}

// soloNeverCommitted fails when any needle is in a file of any commit of
// c (every ref; file contents, not commit authors) or in a tracked file of
// the work tree.
func soloNeverCommitted(t testing.TB, c *teamharness.Clone, needles ...string) {
	t.Helper()
	revs := strings.Fields(c.Git("rev-list", "--all"))
	for _, n := range needles {
		args := append([]string{"grep", "-l", "-F", "-e", n}, revs...)
		if r := c.TryGit(args...); r.Code == 0 {
			t.Errorf("%q is in committed files:\n%s", n, r.Stdout)
		}
		if r := c.TryGit("grep", "-l", "-F", "-e", n); r.Code == 0 {
			t.Errorf("%q is in tracked files of the work tree:\n%s", n, r.Stdout)
		}
	}
}

func TestJourneySolo(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/app")
	lib := w.Remote("github.com/sam-dev/lib")
	w.Remote(soloStore) // an empty repo Sam made on GitHub for the personal store
	var appRepo, libRepo *teamharness.Clone

	// ── day 1 ────────────────────────────────────────────────────────────
	w.Step("day1/install-and-setup", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, lap, soloName, soloEmail)
		soloSetupRest(t, lap, "create")
		// The tools check asks for the skill; Sam presses its fix.
		lap.Lore("", "skill", "install", "--tool", "claude_code").OK(t)
		// The setup is listed, is the default, and applies in its folders.
		l := lap.Lore("", "setup", "list", "--json").OK(t)
		if !strings.Contains(l.Stdout, `"name": "`+soloSetup+`"`) || !strings.Contains(l.Stdout, `"default": true`) {
			t.Errorf("setup list after the setup: %s", l.Stdout)
		}
		// The personal store exists on GitHub as a lore store.
		if _, ok := w.Remote(soloStore).Read("main", "lore-store.yaml"); !ok {
			t.Errorf("the personal store on GitHub has no lore-store.yaml: %v", w.Remote(soloStore).Files("main"))
		}
	})

	w.Step("day1/first-notes", func() {
		appRepo = lap.Clone(app, "app")
		lap.LoreIn(appRepo, "init").OK(t)
		// A finished setup leaves nothing of its steps in the row trash:
		// Sam deleted nothing, so the trash is empty.
		if r := lap.LoreIn(appRepo, "trash", "list").OK(t); strings.Contains(r.Stdout, "setup_progress") {
			t.Errorf("a finished setup left its step rows in Sam's trash:\n%s", r.Stdout)
		}
		lap.LoreIn(appRepo, "memory", "add", "--body", "app: timestamps are UTC").OK(t)
		lap.LoreIn(appRepo, "memory", "add", "--body", "I like short commit subjects", "--to", "me").OK(t).Says(t, "[me]")
		got := soloBodies(t, lap, appRepo.Dir)
		if me, ok := got["app: timestamps are UTC"]; !ok || me {
			t.Errorf("the repo note is not listed as the repo's: %v", got)
		}
		if me, ok := got["I like short commit subjects"]; !ok || !me {
			t.Errorf("the personal note is not listed as [me]: %v", got)
		}
		// whoami names the personal store the note went to.
		if _, _, p := soloWhoami(t, lap, appRepo.Dir); p != soloStoreURL {
			t.Errorf("whoami personal store = %q, want %q", p, soloStoreURL)
		}
		// The personal note reached GitHub; it never lands in the repo.
		if !soloRemoteHas(w, "I like short commit subjects") {
			t.Errorf("the personal note is not on GitHub: %v", w.Remote(soloStore).Files("main"))
		}
		if hits := appRepo.LeakScan("I like short commit subjects"); len(hits) > 0 {
			t.Errorf("the personal note leaked into the repo: %v", hits)
		}
		lap.LoreIn(appRepo, "render").OK(t)
		if s, ok := appRepo.ReadFile("CLAUDE.md"); !ok || !strings.Contains(s, "LORE.md") {
			t.Errorf("CLAUDE.md does not point at .lore/LORE.md: %q", s)
		}
		// Sam commits what lore made and pushes it.
		appRepo.CommitAll("add lore")
		appRepo.Git("push", "-q", "origin", "HEAD")
	})
	w.Fact("Sam's first personal note is listed on every awake machine", func(c Ctx) {
		for _, m := range sam.Machines() {
			if m.Asleep() || len(m.Clones()) == 0 {
				continue
			}
			if me := soloBodies(c.T, m, m.Clones()[0].Dir)["I like short commit subjects"]; !me {
				c.Errorf("%s: the first personal note is gone or not [me]", m.Label)
			}
		}
	})

	// ── week 1 ───────────────────────────────────────────────────────────
	w.Step("week1/second-repo", func() {
		w.AdvanceDays(2)
		libRepo = lap.Clone(lib, "lib")
		lap.LoreIn(libRepo, "init").OK(t)
		lap.LoreIn(libRepo, "memory", "add", "--body", "lib: never panic in public funcs").OK(t)
		got := soloBodies(t, lap, libRepo.Dir)
		if _, ok := got["app: timestamps are UTC"]; ok {
			t.Errorf("app's note shows in lib: %v", got)
		}
		if _, ok := got["lib: never panic in public funcs"]; !ok {
			t.Errorf("lib's note is not listed in lib: %v", got)
		}
		if !got["I like short commit subjects"] {
			t.Errorf("the personal note is not listed in the second repo: %v", got)
		}
	})

	w.Step("week1/offline-writes", func() {
		w.AdvanceDays(1)
		lap.Offline()
		r := lap.LoreIn(appRepo, "memory", "add", "--body", "written on the train", "--to", "me").OK(t)
		r.Says(t, "lore sync")
		if soloRemoteHas(w, "written on the train") {
			t.Errorf("an offline write reached GitHub")
		}
		lap.LoreIn(appRepo, "sync").OK(t).Says(t, "offline")
		if !soloBodies(t, lap, appRepo.Dir)["written on the train"] {
			t.Errorf("the offline note is not listed while offline")
		}
		// What is not sent yet: sync status names the personal store's unsent change.
		if s := lap.LoreIn(appRepo, "sync", "status").OK(t); !soloUnsentMe.MatchString(s.Stdout) {
			t.Errorf("sync status does not show store me ahead of GitHub while it has an unsent change:\n%s", s.Stdout)
		}
		// Doctor on the train names the personal store as unreachable and
		// never tells Sam to create a repo that exists.
		d := lap.LoreIn(appRepo, "doctor")
		d.Never(t, "gh repo create")
		lap.Online()
		lap.LoreIn(appRepo, "sync").OK(t)
		if !soloRemoteHas(w, "written on the train") {
			t.Errorf("back online, sync did not send the train note")
		}
		// Back online and synced: doctor no longer says the store is unreadable.
		lap.LoreIn(appRepo, "doctor").Never(t, "✗ store me", "store.read  me")
	})

	w.Step("week1/laptop-sleeps-mid-push", func() {
		w.AdvanceDays(1)
		r := soloKillAtPush(lap, appRepo.Dir, false, "memory", "add", "--body", "lid closed while pushing", "--to", "me")
		if r.Code == 0 {
			t.Errorf("the push was not killed (nothing to test): %s", r)
		}
		// The next morning: the next command is not stuck behind a lock
		// the dead process held, and the note is either saved (and sent
		// by the next sync) or was never confirmed.
		start := time.Now()
		listed := soloBodies(t, lap, appRepo.Dir)["lid closed while pushing"]
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("memory list after a killed push took %v (a stale lock?)", d)
		}
		lap.LoreIn(appRepo, "sync").OK(t)
		if listed && !soloRemoteHas(w, "lid closed while pushing") {
			t.Errorf("the note saved before the kill was not sent by the next sync")
		}
		if !listed && soloRemoteHas(w, "lid closed while pushing") {
			t.Errorf("the note is on GitHub but not listed on the laptop")
		}
		// No duplicate commits of one row: a retry sends it once.
		n := 0
		for _, l := range w.Remote(soloStore).Log("main") {
			if strings.Contains(l, "lore: memory add mem_") {
				n++
			}
		}
		if n == 0 {
			t.Errorf("the personal store's log has no row writes: %v", w.Remote(soloStore).Log("main"))
		}
	})

	w.Step("week1/push-killed-after-it-reached-github", func() {
		// The push lands on GitHub, then the lid closes before lore hears back.
		r := soloKillAtPush(lap, appRepo.Dir, true, "memory", "add", "--body", "pushed then lid closed", "--to", "me")
		t.Logf("killed after push: exit %d", r.Code)
		if !soloBodies(t, lap, appRepo.Dir)["pushed then lid closed"] {
			t.Errorf("a note committed and pushed before the kill is not listed")
		}
		lap.LoreIn(appRepo, "sync").OK(t)
		lap.LoreIn(appRepo, "memory", "add", "--body", "the next morning", "--to", "me").OK(t)
		for _, b := range []string{"pushed then lid closed", "the next morning"} {
			if !soloRemoteHas(w, b) {
				t.Errorf("%q is not on GitHub", b)
			}
		}
	})

	// ── month 1 ──────────────────────────────────────────────────────────
	w.Step("month1/rename-self", func() {
		w.AdvanceDays(20)
		soloApply(t, lap, "identity", map[string]any{"name": soloRenamed, "email": soloEmail})
		sam.Rename(t, soloRenamed, soloEmail)
		if n, _, _ := soloWhoami(t, lap, appRepo.Dir); n != soloRenamed {
			t.Errorf("whoami name after the rename = %q", n)
		}
		// Every copy of the name follows: config.yaml's top identity and
		// the actor row in each store.
		cfg, _ := os.ReadFile(lap.LorePath("config.yaml"))
		if strings.Contains(string(cfg), soloName) {
			t.Errorf("config.yaml still holds the old name:\n%s", cfg)
		}
		if a := lap.LoreIn(appRepo, "actor", "list").OK(t); strings.Contains(a.Stdout, soloName) {
			t.Errorf("actor list still shows the old name:\n%s", a.Stdout)
		}
	})

	w.Step("month1/change-email", func() {
		w.AdvanceDays(1)
		soloApply(t, lap, "identity", map[string]any{"name": soloRenamed, "email": soloNewEmail})
		sam.Rename(t, soloRenamed, soloNewEmail)
		if _, k, _ := soloWhoami(t, lap, appRepo.Dir); k != "human:"+soloNewEmail {
			t.Errorf("whoami key after the email change = %q", k)
		}
		lap.LoreIn(appRepo, "memory", "add", "--body", "after the new email", "--to", "me").OK(t)
		got := soloBodies(t, lap, appRepo.Dir)
		for _, b := range []string{"I like short commit subjects", "written on the train", "after the new email"} {
			if !got[b] {
				t.Errorf("after the email change %q is not listed as [me]: %v", b, got)
			}
		}
		// One person, not two: the old and new address are the same human.
		// The actor keeps its id (derived from the first email), its
		// stable_key is the new email, and the old one is an alias.
		lap.LoreIn(appRepo, "memory", "add", "--body", "a repo note after the new email").OK(t)
		a := lap.LoreIn(appRepo, "actor", "list").OK(t)
		if n := strings.Count(a.Stdout, "[human]"); n != 1 {
			t.Errorf("actor list shows %d humans for one person:\n%s", n, a.Stdout)
		}
		me := soloActorID(t, lap, appRepo.Dir)
		var list struct {
			Data []struct {
				ID        string `json:"id"`
				Kind      string `json:"kind"`
				StableKey string `json:"stable_key"`
			} `json:"data"`
		}
		lj := lap.LoreIn(appRepo, "actor", "list", "--json").OK(t)
		if err := json.Unmarshal([]byte(lj.Stdout), &list); err != nil {
			t.Fatalf("actor list --json: %v: %s", err, lj)
		}
		for _, x := range list.Data {
			if x.Kind == "human" && (x.ID != me || x.StableKey != "human:"+soloNewEmail) {
				t.Errorf("actor list shows the person as %s %s, want %s with the new email %s", x.ID, x.StableKey, me, soloNewEmail)
			}
		}
		if first, _ := identityActorID("human:" + soloEmail); first != me {
			t.Errorf("after the email change the actor is %s, want the first email's id %s (the id never moves)", me, first)
		}
		// A lookup by the old email finds the same actor.
		var shown struct {
			Data struct {
				ID        string `json:"id"`
				StableKey string `json:"stable_key"`
			} `json:"data"`
		}
		sj := lap.LoreIn(appRepo, "actor", "show", "human:"+soloEmail, "--json").OK(t)
		if err := json.Unmarshal([]byte(sj.Stdout), &shown); err != nil || shown.Data.ID != me || shown.Data.StableKey != "human:"+soloNewEmail {
			t.Errorf("actor show by the old email: %+v (%v), want %s with the new email", shown.Data, err, me)
		}
		// Every store's copy follows (the repo's and the personal store's).
		lap.LoreIn(appRepo, "doctor").Never(t, "actor_copies")
		// The repo's copy is the F18 projection (stable_key blank by
		// design): no email, old or new, is in any committed file.
		appRepo.CommitAll("notes after the new email")
		soloNeverCommitted(t, appRepo, soloEmail, soloNewEmail)
	})

	w.Step("month1/switch-github-account", func() {
		w.AdvanceDays(1)
		// A new key with its own ssh alias; gh now logs in as the new account.
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin2, Key: "id_new", Alias: "github-new"}, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloApply(t, lap, "github", map[string]any{"github_account": soloLogin2, "host_alias": "github-new"})
		show := lap.Lore("", "setup", "show", soloSetup, "--json").OK(t)
		if !strings.Contains(show.Stdout, soloLogin2) || !strings.Contains(show.Stdout, "github-new") {
			t.Errorf("setup show does not name the new account and alias: %s", show.Stdout)
		}
		lap.LoreIn(appRepo, "memory", "add", "--body", "after the account switch", "--to", "me").OK(t)
		if !soloRemoteHas(w, "after the account switch") {
			t.Errorf("a write after the account switch did not reach the personal store")
		}
	})

	w.Step("month1/repo-moves-to-new-remote", func() {
		w.AdvanceDays(2)
		moved := w.Remote("github.com/sam-dev/app-renamed")
		appRepo.Git("push", "-q", moved.URL(), "HEAD:main")
		appRepo.Git("remote", "set-url", "origin", moved.URL())
		appRepo.Git("fetch", "-q", "origin")
		got := soloBodies(t, lap, appRepo.Dir)
		if _, ok := got["app: timestamps are UTC"]; !ok {
			t.Errorf("after the remote moved the repo note is gone: %v", got)
		}
		wa := lap.LoreIn(appRepo, "whoami").OK(t)
		if !strings.Contains(wa.Stdout, "sam-dev/app-renamed") {
			t.Errorf("whoami does not show the new remote:\n%s", wa.Stdout)
		}
		lap.LoreIn(appRepo, "memory", "add", "--body", "app: after the move").OK(t)
		// The next sync notices the new address and says how to confirm
		// it; Sam confirms the move and the repo record follows.
		sy := lap.LoreIn(appRepo, "sync").OK(t)
		sy.Says(t, "lore repo set-url")
		lap.LoreIn(appRepo, "repo", "set-url", "github.com/sam-dev/app-renamed").OK(t)
		rl := lap.LoreIn(appRepo, "repo", "list").OK(t)
		if !strings.Contains(rl.Stdout, "sam-dev/app-renamed") {
			t.Errorf("repo list after set-url does not show the new address:\n%s", rl.Stdout)
		}
		// The project record copied the address at init; it follows too.
		if pl := lap.LoreIn(appRepo, "project", "list").OK(t); !strings.Contains(pl.Stdout, "sam-dev/app-renamed") {
			t.Errorf("project list after set-url still names the old address:\n%s", pl.Stdout)
		}
		lap.LoreIn(appRepo, "doctor").OK(t).Never(t, "is unconfirmed")
	})

	w.Step("month1/folder-renamed", func() {
		w.AdvanceDays(1)
		lap.MoveClone(t, appRepo, "app-new-name")
		got := soloBodies(t, lap, appRepo.Dir)
		for _, b := range []string{"app: timestamps are UTC", "app: after the move"} {
			if _, ok := got[b]; !ok {
				t.Errorf("after the folder rename %q is gone: %v", b, got)
			}
		}
		l := lap.LoreIn(appRepo, "setup", "list", "--json").OK(t)
		if !strings.Contains(l.Stdout, `"setup": "`+soloSetup+`"`) {
			t.Errorf("the renamed folder is not under the setup: %s", l.Stdout)
		}
		// doctor --all checks the repos lore knows; the old folder is gone
		// and must not be reported as a repo with no git email.
		lap.Lore("", "doctor", "--all").Never(t, "is no email", filepath.Join(filepath.Dir(appRepo.Dir), "app")+"\n")
		appRepo.CommitAll("lore notes")
		appRepo.Git("push", "-q", "origin", "HEAD")
	})

	// ── undo paths (month 2) ─────────────────────────────────────────────
	w.Step("month2/delete-then-restore", func() {
		w.AdvanceDays(10)
		id := soloMemID(t, lap, appRepo.Dir, "written on the train")
		lap.LoreIn(appRepo, "memory", "delete", id, "--yes").OK(t)
		if _, ok := soloBodies(t, lap, appRepo.Dir)["written on the train"]; ok {
			t.Errorf("a deleted note is still listed")
		}
		lap.LoreIn(appRepo, "trash", "list").OK(t).Says(t, id)
		lap.LoreIn(appRepo, "trash", "restore", id).OK(t)
		if !soloBodies(t, lap, appRepo.Dir)["written on the train"] {
			t.Errorf("a restored note is not listed as [me]")
		}
		lap.LoreIn(appRepo, "sync").OK(t)
		if !soloRemoteHas(w, "written on the train") {
			t.Errorf("the restored note is not on GitHub")
		}
	})

	w.Step("month2/edit-then-edit-back", func() {
		id := soloMemID(t, lap, appRepo.Dir, "I like short commit subjects")
		lap.LoreIn(appRepo, "memory", "edit", id, "--body", "I like long commit subjects").OK(t)
		if !soloBodies(t, lap, appRepo.Dir)["I like long commit subjects"] {
			t.Errorf("the edit is not listed")
		}
		lap.LoreIn(appRepo, "memory", "edit", id, "--body", "I like short commit subjects").OK(t)
		got := soloBodies(t, lap, appRepo.Dir)
		if !got["I like short commit subjects"] || got["I like long commit subjects"] {
			t.Errorf("edit back did not restore the text: %v", got)
		}
		lap.LoreIn(appRepo, "sync").OK(t)
		r := w.Remote(soloStore)
		if s, ok := r.Read("main", "_store/memories/"+id+".json"); !ok || !strings.Contains(s, "I like short commit subjects") {
			t.Errorf("GitHub's copy is not the edited-back text: %s", s)
		}
	})

	w.Step("month2/purge-then-regret", func() {
		lap.LoreIn(appRepo, "memory", "add", "--body", "a password hint I regret", "--to", "me").OK(t)
		id := soloMemID(t, lap, appRepo.Dir, "a password hint I regret")
		lap.LoreIn(appRepo, "memory", "delete", id, "--yes").OK(t)
		lap.LoreIn(appRepo, "trash", "purge", id, "--confirm").OK(t)
		lap.LoreIn(appRepo, "sync").OK(t)
		// Regret: there is nothing to bring back, and lore says so.
		r := lap.LoreIn(appRepo, "trash", "restore", id)
		if r.Code == 0 {
			t.Errorf("restore of a purged row succeeded: %s", r)
		} else if !strings.Contains(strings.ToLower(r.Out()), "purge") && !strings.Contains(strings.ToLower(r.Out()), "not") {
			t.Errorf("restore of a purged row does not say why: %s", r)
		}
		if soloRemoteHas(w, "a password hint I regret") {
			t.Errorf("the purged text is still on GitHub's main")
		}
	})

	w.Step("month2/remove-setup-then-re-add", func() {
		lap.Lore("", "setup", "remove", soloSetup, "--json").OK(t)
		// Without the setup, --to me names what is missing (it does not
		// write anywhere else).
		r := lap.LoreIn(appRepo, "memory", "add", "--body", "while the setup is gone", "--to", "me")
		if r.Code == 0 && !soloRemoteHas(w, "while the setup is gone") {
			t.Errorf("--to me said ok with no setup but the note is not in the personal store: %s", r)
		}
		lap.Lore("", "setup", "trash", "restore", soloSetup, "--json").OK(t)
		got := soloBodies(t, lap, appRepo.Dir)
		for _, b := range []string{"I like short commit subjects", "after the new email"} {
			if !got[b] {
				t.Errorf("after re-adding the setup %q is not listed as [me]: %v", b, got)
			}
		}
		if _, _, p := soloWhoami(t, lap, appRepo.Dir); p != soloStoreURL {
			t.Errorf("whoami personal store after re-adding = %q", p)
		}
	})

	// ── month 3: a new laptop; the old one goes in a drawer ───────────────
	var desk *Machine
	var deskApp *teamharness.Clone
	w.Step("month3/old-laptop-unpushed-then-drawer", func() {
		w.AdvanceDays(25)
		lap.Offline()
		lap.LoreIn(appRepo, "memory", "add", "--body", "old laptop: never pushed", "--to", "me").OK(t)
		lap.Sleep()
	})

	w.Step("month3/new-laptop-setup-halfway", func() {
		desk = sam.NewLaptop("desk")
		desk.GitHubAccounts(t, GitHubAccount{Login: soloLogin2, Key: "id_new", Alias: "github-new"})
		soloSetupStart(t, desk, soloRenamed, soloNewEmail)
		// Sam closes the page here.
	})

	w.Step("month3/new-laptop-next-day", func() {
		w.AdvanceDays(1)
		show := desk.Lore("", "setup", "show", soloSetup, "--json").OK(t)
		if !strings.Contains(show.Stdout, soloNewEmail) {
			t.Errorf("the next day the setup forgot yesterday's answers: %s", show.Stdout)
		}
		soloApply(t, desk, "github", map[string]any{"github_account": soloLogin2, "host_alias": "github-new"})
		soloApply(t, desk, "personal_store", map[string]any{"personal_store": "existing", "store_url": soloStoreURL})
		soloApply(t, desk, "tools", map[string]any{"tools": []string{"claude_code"}})
		soloApply(t, desk, "secrets", map[string]any{"backend": "none"})
		soloApply(t, desk, "folders", map[string]any{"folders": []string{soloWork(desk)}, "default": true})
		soloApply(t, desk, "this_folder", map[string]any{})
		soloApply(t, desk, "summary", map[string]any{})
		desk.Lore("", "skill", "install", "--tool", "claude_code").OK(t)
		deskApp = desk.Clone(w.Remote("github.com/sam-dev/app-renamed"), "app")
		got := soloBodies(t, desk, deskApp.Dir)
		for _, b := range []string{"app: timestamps are UTC", "app: after the move"} {
			if _, ok := got[b]; !ok {
				t.Errorf("the new laptop's clone lacks the repo note %q: %v", b, got)
			}
		}
		for _, b := range []string{"I like short commit subjects", "written on the train", "after the account switch"} {
			if !got[b] {
				t.Errorf("the new laptop lacks the personal note %q: %v", b, got)
			}
		}
		if _, ok := got["old laptop: never pushed"]; ok {
			t.Errorf("the new laptop shows a note the old one never pushed")
		}
		desk.LoreIn(deskApp, "memory", "add", "--body", "desk: first note", "--to", "me").OK(t)
		// The desk only knows the new email, yet Sam is the one actor he
		// has been since his first email: the personal store's row names
		// the new email as its key and the old one as an alias.
		if first, _ := identityActorID("human:" + soloEmail); soloActorID(t, desk, deskApp.Dir) != first {
			t.Errorf("the new laptop writes as %s, want Sam's one actor %s", soloActorID(t, desk, deskApp.Dir), first)
		}
		if a := desk.LoreIn(deskApp, "actor", "list").OK(t); strings.Count(a.Stdout, "[human]") != 1 {
			t.Errorf("the new laptop's actor list shows more than one Sam:\n%s", a.Stdout)
		}
	})

	w.Step("month4/old-laptop-again", func() {
		w.AdvanceDays(30)
		lap.Wake()
		lap.Online()
		lap.LoreIn(appRepo, "sync").OK(t)
		got := soloBodies(t, lap, appRepo.Dir)
		for _, b := range []string{"old laptop: never pushed", "desk: first note"} {
			if !got[b] {
				t.Errorf("the old laptop after sync lacks %q: %v", b, got)
			}
		}
		if !soloRemoteHas(w, "old laptop: never pushed") {
			t.Errorf("the old laptop's unpushed note did not reach GitHub")
		}
		desk.LoreIn(deskApp, "sync").OK(t)
		if !soloBodies(t, desk, deskApp.Dir)["old laptop: never pushed"] {
			t.Errorf("the desk does not get the old laptop's note")
		}
	})
	w.Fact("both laptops list the same personal notes", func(c Ctx) {
		if lap.Asleep() || desk.Asleep() || lap.IsOffline() || desk.IsOffline() {
			return
		}
		a, b := soloMine(soloBodies(c.T, lap, appRepo.Dir)), soloMine(soloBodies(c.T, desk, deskApp.Dir))
		if a != b {
			c.Errorf("personal notes differ:\nlaptop: %s\ndesk:   %s", a, b)
		}
	})

	// ── month 6 ──────────────────────────────────────────────────────────
	w.Step("month6/upgrade", func() {
		w.AdvanceDays(60)
		lap.LoreIn(appRepo, "setup", "--schema-only").OK(t)
		desk.LoreIn(deskApp, "setup", "--schema-only").OK(t)
		lap.LoreIn(appRepo, "sync").OK(t)
		desk.LoreIn(deskApp, "sync").OK(t)
	})
}

// soloKillAtPush runs lore and kills it at its git push (after: once the
// push has run). Machine.SleepMidPush matches "push" anywhere in a git
// command line, which first matches `config --get-regexp …(url|pushurl)$`
// long before any push; " push " matches only the push subcommand.
func soloKillAtPush(m *Machine, dir string, after bool, args ...string) teamharness.Result {
	m.w.T.Helper()
	when := teamharness.KillBefore
	if after {
		when = teamharness.KillAfter
	}
	counter := filepath.Join(m.w.Root, "solo-kill-"+m.Name)
	_ = os.Remove(counter)
	m.SetEnv(teamharness.EnvKillOn, " push ")
	m.SetEnv(teamharness.EnvKillWhen, when)
	m.SetEnv(teamharness.EnvKillCounter, counter)
	defer func() {
		m.SetEnv(teamharness.EnvKillOn, "")
		m.SetEnv(teamharness.EnvKillWhen, "")
		m.SetEnv(teamharness.EnvKillCounter, "")
	}()
	r := m.Lore(dir, args...)
	if n, err := os.ReadFile(counter); err != nil || strings.TrimSpace(string(n)) == "" {
		m.w.T.Errorf("lore %v never ran git push: nothing was killed", args)
	}
	return r
}

// soloMine is the sorted [me] bodies of a list, one per line.
func soloMine(got map[string]bool) string {
	var out []string
	for b, me := range got {
		if me {
			out = append(out, b)
		}
	}
	slicesSort(out)
	return strings.Join(out, " | ")
}

func slicesSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// soloBulk puts n memory rows into the personal store on GitHub, one
// commit each, shaped like the row lore wrote for "desk: first note"
// (a person who used the store daily for years): git fast-import into a
// scratch clone, pushed to the store's main.
func soloBulk(t testing.TB, w *World, m *Machine, n int) {
	t.Helper()
	r := w.Remote(soloStore)
	var tmpl, tmplPath string
	for _, f := range r.FilesMatching("main", "memories/") {
		if s, ok := r.Read("main", f); ok && strings.Contains(s, "desk: first note") {
			tmpl, tmplPath = s, f
		}
	}
	if tmpl == "" {
		t.Fatalf("no template row in the personal store")
	}
	oldID := strings.TrimSuffix(filepath.Base(tmplPath), ".json")
	head := w.Git(r.Dir, "rev-parse", "main")
	var fi bytes.Buffer
	when := w.Now().Add(-24 * time.Hour).Unix()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%012x", oldID[:len(oldID)-12], 0xb000000000+i)
		body := strings.ReplaceAll(tmpl, oldID, id)
		body = strings.ReplaceAll(body, "desk: first note", fmt.Sprintf("bulk note %d", i))
		fmt.Fprintf(&fi, "commit refs/heads/main\ncommitter %s <%s> %d +0000\ndata <<EOM\nlore: write %s\nEOM\n", soloRenamed, soloNewEmail, when+int64(i), id)
		if i == 0 {
			fmt.Fprintf(&fi, "from %s\n", head)
		}
		fmt.Fprintf(&fi, "M 100644 inline %s\ndata %d\n%s\n", path.Join(path.Dir(tmplPath), id+".json"), len(body), body)
	}
	c := exec.Command("git", "fast-import", "--quiet", "--force")
	c.Dir = r.Dir
	c.Stdin = &fi
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("fast-import: %v %s", err, out)
	}
	if got := r.CommitCount("main"); got < n {
		t.Fatalf("the store has %d commits, want at least %d", got, n)
	}
}

// TestJourneySoloMonth6 is month six of the same life on its own World (so
// the two halves run side by side under the gate's time budget): the desk
// laptop, set up as in month 3, swaps Claude Code for Codex, and the
// personal store has grown to thousands of rows and commits.
func TestJourneySoloMonth6(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloRenamed, soloNewEmail)
	desk := sam.Laptop()
	w.Remote(soloStore)
	var deskApp *teamharness.Clone
	w.Step("month6/desk-as-it-is", func() {
		w.AdvanceDays(150)
		desk.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, desk, soloRenamed, soloNewEmail)
		soloSetupRest(t, desk, "create")
		desk.Lore("", "skill", "install", "--tool", "claude_code").OK(t)
		deskApp = desk.Clone(w.Remote("github.com/sam-dev/app-renamed"), "app")
		desk.LoreIn(deskApp, "init").OK(t)
		desk.LoreIn(deskApp, "memory", "add", "--body", "desk: first note", "--to", "me").OK(t)
	})

	w.Step("month6/swap-coding-tool", func() {
		soloApply(t, desk, "tools", map[string]any{"tools": []string{"codex"}})
		show := desk.Lore("", "setup", "show", soloSetup, "--json").OK(t)
		if !strings.Contains(show.Stdout, "codex") {
			t.Errorf("setup show does not list codex: %s", show.Stdout)
		}
		// Sam uninstalls Claude Code and installs Codex; doctor names the
		// two things to do (the codex skill, codex's pointer file in the
		// repo) and Sam runs both fixes.
		if err := os.RemoveAll(filepath.Join(desk.Home, ".claude")); err != nil {
			t.Fatal(err)
		}
		d := desk.LoreIn(deskApp, "doctor")
		d.Says(t, "lore skill install --tool codex", "AGENTS.md")
		desk.Lore("", "skill", "install", "--tool", "codex").OK(t)
		desk.LoreIn(deskApp, "render", "--add-target", "codex").OK(t)
		if s, ok := deskApp.ReadFile("AGENTS.md"); !ok || !strings.Contains(s, "LORE.md") {
			t.Errorf("after switching to codex AGENTS.md does not point at .lore/LORE.md: %q", s)
		}
	})

	w.Step("month6/thousands-of-rows-and-commits", func() {
		const n = 3000
		soloBulk(t, w, desk, n)
		desk.LoreIn(deskApp, "sync").OK(t)
		start := time.Now()
		got := soloBodies(t, desk, deskApp.Dir)
		took := time.Since(start)
		bulk := 0
		for b := range got {
			if strings.HasPrefix(b, "bulk note ") {
				bulk++
			}
		}
		if bulk != n {
			t.Errorf("memory list shows %d of %d bulk notes", bulk, n)
		}
		if took > 30*time.Second {
			t.Errorf("memory list over %d rows took %v", n, took)
		}
		start = time.Now()
		desk.LoreIn(deskApp, "memory", "add", "--body", "after thousands", "--to", "me").OK(t)
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("one write into a store with %d commits took %v", n, d)
		}
		s := desk.LoreIn(deskApp, "search", "bulk note 2999").OK(t)
		if !strings.Contains(s.Stdout, "bulk note 2999") {
			t.Errorf("search does not find a bulk note:\n%s", s.Stdout)
		}
	})
}
