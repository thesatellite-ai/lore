//go:build machines && !windows

package machines

// sot_stores_helpers_test.go — helpers of sot_stores_test.go (store
// address and org entry source of truth, SOURCE_OF_TRUTH rows 7 and 9).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Placeholder people and the moved store's url.
const (
	ssBossEmail   = "boss-ss@example.test"
	ssMemberEmail = "mem-ss@example.test"
	// ssMovedURL is where the team store moved (the host renamed it).
	ssMovedURL = "git@github.com:lifeorg/lore-team-moved.git"
)

// ssTeam is an admin machine that started the team store (a bare repo in
// member's folder, reached at lsTeamURL on both machines) and invited
// email; it returns the admin and the team for the member's setup.
func ssTeam(t *testing.T, member *Machine, email string) (*Machine, lsTeam) {
	t.Helper()
	bare := filepath.Join(member.Dir, "team.git")
	if out, err := exec.Command("/usr/bin/git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v %s", err, out)
	}
	tm := lsTeam{storeURL: lsTeamURL, bare: bare}
	tm.rewrite(t, member)
	admin := Build(t, ShapeOwnerLike, loreBin)
	lsGHHosts(t, admin)
	tm.rewrite(t, admin)
	lsCreate(t, admin, "boss", kindAdmin, ssBossEmail, []string{"~/code/**"}, true, tm, lsGitHub{LoginBravo, "github-bravo"})
	out := lsRun(t, admin, admin.Repos[RepoWorkApp], "team", "invite", email, "--yes")
	var o struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(out), &o)
	if tm.invite = o.Text; !strings.HasPrefix(tm.invite, "lore-invite:") {
		t.Fatalf("team invite printed no invite line: %s", out)
	}
	// C5: the invite reaches the team store with the admin's sync
	lsRun(t, admin, admin.Repos[RepoWorkApp], "sync")
	return admin, tm
}

// ssRenameOnHost is the host renaming the store repo: the bare repo moves
// to <bare>-moved, the old path stays as a link to it (the host's
// redirect), and every machine's git reaches ssMovedURL there.
func ssRenameOnHost(t *testing.T, tm lsTeam, machines ...*Machine) {
	t.Helper()
	moved := tm.bare + "-moved"
	if err := os.Rename(tm.bare, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, tm.bare); err != nil {
		t.Fatal(err)
	}
	for _, m := range machines {
		lsTeam{storeURL: ssMovedURL, bare: moved}.rewriteURL(t, m)
	}
}

// rewriteURL points m's git at tm.bare for tm.storeURL (lsTeam.rewrite
// for any url).
func (tm lsTeam) rewriteURL(t *testing.T, m *Machine) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(m.Home, ".gitconfig"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("[url \"file://" + tm.bare + "\"]\n\tinsteadOf = " + tm.storeURL + "\n"); err != nil {
		t.Fatal(err)
	}
}

// ssDoctor is the lines of `lore doctor` in cwd naming check (e.g.
// "store_address:").
func ssDoctor(t *testing.T, m *Machine, cwd, check string) []string {
	t.Helper()
	r := m.Lore(t, cwd, "doctor")
	var out []string
	for _, l := range strings.Split(r.Stdout+r.Stderr, "\n") {
		if strings.Contains(l, check) {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// ssConfig is m's ~/.lore/config.yaml.
func ssConfig(t *testing.T, m *Machine) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.Home, ".lore", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ssEditConfig rewrites m's config.yaml with edit.
func ssEditConfig(t *testing.T, m *Machine, edit func(string) string) {
	t.Helper()
	p := filepath.Join(m.Home, ".lore", "config.yaml")
	if err := os.WriteFile(p, []byte(edit(ssConfig(t, m))), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ssOrgFile is lore-org.yaml on the store's default branch, read from the
// bare repo (the host's view).
func ssOrgFile(t *testing.T, bare string) string {
	t.Helper()
	for _, p := range []string{"org/lore-org.yaml", "lore-org.yaml"} {
		if out, err := exec.Command("/usr/bin/git", "-C", bare, "show", "main:"+p).Output(); err == nil {
			return string(out)
		}
	}
	t.Fatalf("no lore-org.yaml in %s", bare)
	return ""
}

// ssStoreID is the sto_ id the store_address doctor line names.
var ssStoreID = regexp.MustCompile(`store (sto_[0-9a-f]{32}) is reached at`)

// ssStoreList is `lore store list --json` in cwd.
type ssStoreList struct {
	Stores []struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		Address string `json:"address"`
	} `json:"stores"`
}

// ssList is ssStoreList for m in cwd.
func ssList(t *testing.T, m *Machine, cwd string) ssStoreList {
	t.Helper()
	var l ssStoreList
	if err := json.Unmarshal([]byte(lsRun(t, m, cwd, "store", "list")), &l); err != nil {
		t.Fatalf("store list: %v", err)
	}
	return l
}
