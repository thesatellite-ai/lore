//go:build !windows

package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/execfile"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/ids"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// accessWorld is a machine whose git host is reachable only through an
// ssh alias: the bare repos live under mirror/, the fake ssh
// (GIT_SSH_COMMAND) serves them for the host "fakealias" only and refuses
// the canonical host git.example.invalid, so a git call reaches a store
// only when lore rewrites the canonical url to the alias (-c insteadOf).
type accessWorld struct {
	root, mirror, repo string
}

const (
	accessCanonHost = "git.example.invalid"
	accessAlias     = "fakealias"
	accessCanonURL  = "git@" + accessCanonHost + ":acme/lore-x.git"
	accessAliasURL  = "git@" + accessAlias + ":acme/lore-x.git"
)

// accessFakeSSH serves git over "ssh" from $ACCESS_MIRROR (git runs it as:
// ssh [options] <host> "<git command> '<path>'") like two accounts on one
// host: the alias is the org account and serves acme/ only (any other repo
// is "not found" for it, as GitHub answers a key of another account); the
// canonical host is the person's own account and serves me/ only.
const accessFakeSSH = `#!/bin/sh
host=""; cmd=""
for a in "$@"; do host="$cmd"; cmd="$a"; done
case "$host" in
  *@` + accessAlias + `|` + accessAlias + `)
    case "$cmd" in *"'acme/"*) ;; *) echo "ERROR: Repository not found." >&2; echo "fatal: Could not read from remote repository." >&2; exit 128;; esac;;
  *@` + accessCanonHost + `|` + accessCanonHost + `)
    case "$cmd" in *"'me/"*) ;; *) echo "ssh: Could not resolve hostname $host" >&2; exit 255;; esac;;
  *) echo "ssh: Could not resolve hostname $host" >&2; exit 255;;
esac
cd "$ACCESS_MIRROR" || exit 1
eval "$cmd"
`

func newAccessWorld(t *testing.T) *accessWorld {
	t.Helper()
	w := &accessWorld{root: t.TempDir()}
	w.mirror = filepath.Join(w.root, "mirror")
	for _, r := range []string{"acme/lore-x", "acme/team", "me/notes", "me/other"} {
		d := filepath.Join(w.mirror, r+".git")
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		orgGit(t, d, "init", "-q", "--bare", "-b", "main")
	}
	gc := filepath.Join(w.root, "gitconfig")
	if err := os.WriteFile(gc, []byte("[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(w.root, "fakessh")
	if err := execfile.WriteFile(ssh, []byte(accessFakeSSH), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_SSH_COMMAND", ssh)
	t.Setenv("ACCESS_MIRROR", w.mirror)
	t.Setenv("LORE_HUMAN", "1")
	t.Setenv("LORE_ORG", "")
	w.repo = w.newRepo(t, "repo")
	return w
}

// newRepo makes a git repo with one commit and an empty .lore/data.
func (w *accessWorld) newRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(w.root, name)
	if err := os.MkdirAll(filepath.Join(dir, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	orgGit(t, dir, "init", "-q", "-b", "main")
	orgGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

// home makes a HOME for who, with ~/.ssh/config naming the alias when
// withAlias, and returns its ~/.lore.
func (w *accessWorld) home(t *testing.T, who string, withAlias bool) string {
	t.Helper()
	h := filepath.Join(w.root, "home-"+who)
	if err := os.MkdirAll(filepath.Join(h, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if withAlias {
		cfg := "Host " + accessAlias + "\n  HostName " + accessCanonHost + "\n  User git\n"
		if err := os.WriteFile(filepath.Join(h, ".ssh", "config"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testenv.SetHome(t, h)
	return filepath.Join(h, ".lore")
}

// app opens the world's repo as home.
func (w *accessWorld) app(t *testing.T, home string) *App {
	t.Helper()
	return w.appIn(t, home, w.repo)
}

// appIn opens repo as home.
func (w *accessWorld) appIn(t *testing.T, home, repo string) *App {
	t.Helper()
	a, err := New(StandaloneOptions(repo, home, repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	pid, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SeedRepoMeta(context.Background(), pid); err != nil {
		t.Fatalf("seed _meta.json: %v", err)
	}
	return a
}

func (w *accessWorld) storeFiles(t *testing.T, repo string) string {
	t.Helper()
	return orgGit(t, filepath.Join(w.mirror, repo+".git"), "ls-tree", "-r", "--name-only", "main")
}

// TestAliasURLStoredCanonical: `lore store init` with an ssh-alias url
// writes the canonical url into the shared stores.yaml, records the
// person's spelling as an access rule in config.yaml, says so, and reaches
// the store (clone, push) through the alias by -c insteadOf. A second
// machine with no rule cannot reach the canonical url; its access rule
// (fetch) makes it work; doctor explains the store it cannot reach with
// the exact fix.
func TestAliasURLStoredCanonical(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newAccessWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira", true)
	a := w.app(t, mira)
	var notes []string
	a.opts.Notice = func(m string) { notes = append(notes, m) }
	if _, err := a.InitStore(ctx, opsstores.InitIn{Name: "kit", URL: accessAliasURL}); err != nil {
		t.Fatalf("store init through the alias: %v", err)
	}
	if !strings.Contains(w.storeFiles(t, "acme/lore-x"), "lore-store.yaml") {
		t.Fatal("the store marker was not pushed through the alias")
	}
	sy, err := os.ReadFile(filepath.Join(w.repo, ".lore", "stores.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sy), accessCanonURL) || strings.Contains(string(sy), accessAlias) {
		t.Fatalf("stores.yaml must name the canonical url, never the alias:\n%s", sy)
	}
	cfg := readConfig(t, mira)
	if parsed, perr := orgconf.Parse([]byte(cfg)); perr != nil || len(parsed.Access) != 1 ||
		parsed.Access[0] != (orgconf.Access{URL: "git@" + accessCanonHost + ":acme/", Via: "git@" + accessAlias + ":acme/"}) {
		t.Fatalf("config.yaml lacks the access rule (%v):\n%s", perr, cfg)
	}
	said := strings.Join(notes, "\n")
	for _, want := range []string{"ssh alias " + accessAlias, "canonical url " + accessCanonURL, "access: url git@" + accessCanonHost + ":acme/ via git@" + accessAlias + ":acme/"} {
		if !strings.Contains(said, want) {
			t.Errorf("the notice does not say %q:\n%s", want, said)
		}
	}
	if fs, derr := a.DoctorStoreAccess(ctx); derr != nil || len(fs) != 0 {
		t.Errorf("doctor on a working machine: %v %+v", derr, fs)
	}

	// Bob has the alias in ~/.ssh/config but no access rule yet.
	bob := w.home(t, "bob", true)
	b := w.appIn(t, bob, w.newRepo(t, "repo-bob"))
	if _, err := b.AddStore(ctx, opsstores.AddIn{Name: "kit2", URL: accessCanonURL}); err == nil {
		t.Fatal("the canonical url was reached without an access rule (the fake ssh must refuse it)")
	}
	if err := writeOrgConfig(bob, func(root *yaml.Node) error {
		return appendAccessNode(root, orgconf.Access{URL: "git@" + accessCanonHost + ":", Via: "git@" + accessAlias + ":"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddStore(ctx, opsstores.AddIn{Name: "kit2", URL: accessCanonURL}); err != nil {
		t.Fatalf("store add with bob's access rule: %v", err)
	}
	// the rule gone, the clone stays: doctor names the canonical url and
	// the exact fix for bob's alias
	if err := os.WriteFile(configPath(bob), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := b.DoctorStoreAccess(ctx)
	if err != nil || len(fs) != 1 {
		t.Fatalf("doctor without the rule: %v %+v", err, fs)
	}
	for _, want := range []string{accessCanonURL, "cannot reach"} {
		if !strings.Contains(fs[0].Message, want) {
			t.Errorf("finding %q does not name %q", fs[0].Message, want)
		}
	}
	for _, want := range []string{"access:\n  - url: \"git@" + accessCanonHost + ":acme/\"\n    via: \"git@" + accessAlias + ":acme/\"",
		`git config --global url."git@` + accessAlias + `:acme/".insteadOf "git@` + accessCanonHost + `:acme/"`} {
		if !strings.Contains(fs[0].Fix, want) {
			t.Errorf("fix lacks %q:\n%s", want, fs[0].Fix)
		}
	}
}

// TestAccessRuleOwnerScoped (D-ah): the rule `store init` records for an
// alias url is scoped to the owner, so a personal store under another
// owner on the same host is reached directly, not through the org's alias
// (whose account cannot see it). A host-wide rule back in config.yaml
// sends the personal store through the alias; its push fails, and the
// error names the rule (url → via, ~/.lore/config.yaml) and the next step.
func TestAccessRuleOwnerScoped(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newAccessWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira", true)
	a := w.app(t, mira)
	if _, err := a.InitStore(ctx, opsstores.InitIn{Name: "kit", URL: accessAliasURL}); err != nil {
		t.Fatalf("store init through the alias: %v", err)
	}
	personal := "git@" + accessCanonHost + ":me/notes.git"
	p := w.appIn(t, mira, w.newRepo(t, "repo-personal"))
	if _, err := p.InitStore(ctx, opsstores.InitIn{Name: "mine", URL: personal}); err != nil {
		t.Fatalf("the personal store under another owner went through the org's alias: %v", err)
	}
	if !strings.Contains(w.storeFiles(t, "me/notes"), "lore-store.yaml") {
		t.Fatal("the personal store's marker was not pushed")
	}
	parsed, err := orgconf.Parse([]byte(readConfig(t, mira)))
	if err != nil || len(parsed.Access) != 1 || orgconf.HostWide(parsed.Access[0]) {
		t.Fatalf("recorded rules %+v (%v), want one owner-scoped rule", parsed.Access, err)
	}

	// a host-wide rule (what lore once recorded) breaks the personal store
	wide := "access:\n  - url: \"git@" + accessCanonHost + ":\"\n    via: \"git@" + accessAlias + ":\"\n"
	if err := os.WriteFile(configPath(mira), []byte(wide), 0o600); err != nil {
		t.Fatal(err)
	}
	q := w.appIn(t, mira, w.newRepo(t, "repo-other"))
	_, err = q.InitStore(ctx, opsstores.InitIn{Name: "other", URL: "git@" + accessCanonHost + ":me/other.git"})
	if err == nil {
		t.Fatal("a host-wide rule did not send the personal store through the alias (the fake alias must refuse me/)")
	}
	for _, want := range []string{"git@" + accessCanonHost + ": → git@" + accessAlias + ":", "~/.lore/config.yaml", "every repo of that host", "git@" + accessCanonHost + ":<owner>/"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not name %q:\n%v", want, err)
		}
	}
}

// TestDoctorHostWideAccess (D-ah): a host-wide access rule in config.yaml
// is a store_access warning naming the stores under another owner and the
// owner-scoped lines; doctor alone rewrites nothing, doctor --fix replaces
// the rule with the org owner's.
func TestDoctorHostWideAccess(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newAccessWorld(t)
	mira := w.home(t, "mira", true)
	a := w.app(t, mira)
	body := "access:\n  - url: \"git@" + accessCanonHost + ":\"\n    via: \"git@" + accessAlias + ":\"\n" +
		"personal:\n  store: git@" + accessCanonHost + ":me/notes.git\n" +
		"orgs:\n  - name: acme\n    stores:\n      - url: " + accessCanonURL + "\n        org_folder: org\n"
	if err := os.MkdirAll(mira, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(mira), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := a.DoctorStoreAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var f DoctorFinding
	for _, x := range fs {
		if strings.Contains(x.Message, "every repo") {
			f = x
		}
	}
	if f.Check != DoctorCheckStoreAccess || f.Level != DoctorWarning {
		t.Fatalf("no host-wide warning: %+v", fs)
	}
	for _, want := range []string{"git@" + accessCanonHost + ":", "me/notes.git", "owner me", "personal"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("finding %q does not name %q", f.Message, want)
		}
	}
	if strings.Contains(f.Message, "lore-x.git, owner acme") {
		t.Errorf("the org store is named as under another owner: %q", f.Message)
	}
	for _, want := range []string{"url: \"git@" + accessCanonHost + ":acme/\"\n    via: \"git@" + accessAlias + ":acme/\"", `url."git@` + accessAlias + `:acme/".insteadOf "git@` + accessCanonHost + `:acme/"`, "lore doctor --fix"} {
		if !strings.Contains(f.Fix, want) {
			t.Errorf("fix lacks %q:\n%s", want, f.Fix)
		}
	}
	if got := readConfig(t, mira); got != body {
		t.Fatalf("doctor without --fix rewrote config.yaml:\n%s", got)
	}
	fixed, err := a.DoctorStoreAccessFix(context.Background(), true)
	if err != nil || len(fixed) == 0 || !fixed[0].Fixed {
		t.Fatalf("doctor --fix: %v %+v", err, fixed)
	}
	parsed, err := orgconf.Parse([]byte(readConfig(t, mira)))
	if err != nil || len(parsed.Access) != 1 || parsed.Access[0] != (orgconf.Access{URL: "git@" + accessCanonHost + ":acme/", Via: "git@" + accessAlias + ":acme/"}) {
		t.Fatalf("after --fix: %+v %v\n%s", parsed.Access, err, readConfig(t, mira))
	}
	if again, _ := a.DoctorStoreAccess(context.Background()); slices.ContainsFunc(again, func(x DoctorFinding) bool { return strings.Contains(x.Message, "every repo") }) {
		t.Errorf("still warned after --fix: %+v", again)
	}
}

// TestDoctorAliasInSharedConfig: an org entry (lore-org.yaml's copy) that
// names a store by this machine's ssh alias is reported with the canonical
// url and the access lines; doctor rewrites nothing.
func TestDoctorAliasInSharedConfig(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newAccessWorld(t)
	mira := w.home(t, "mira", true)
	a := w.app(t, mira)
	body := "orgs:\n  - name: acme\n    stores:\n      - url: " + accessAliasURL + "\n        org_folder: org\n"
	if err := os.MkdirAll(mira, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(mira), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := a.DoctorStoreAccess(context.Background())
	if err != nil || len(fs) != 1 {
		t.Fatalf("doctor: %v %+v", err, fs)
	}
	f := fs[0]
	for _, want := range []string{"lore-x", accessAliasURL, "ssh alias " + accessAlias, accessCanonURL} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("finding %q does not name %q", f.Message, want)
		}
	}
	if !strings.Contains(f.Fix, "via: \"git@"+accessAlias+":acme/\"") || !strings.Contains(f.Fix, "lore-org.yaml") {
		t.Errorf("fix %q", f.Fix)
	}
	if got := readConfig(t, mira); got != body {
		t.Errorf("doctor rewrote config.yaml:\n%s", got)
	}
}

// TestShortNameTargets: an org's second store is named by its short name
// in store arguments (store folder add lore-x …) and `<store>/<folder>`
// targets resolve it; the url spelling still works; output names the
// short name.
func TestShortNameTargets(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newAccessWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira", false)
	a := w.app(t, mira)
	team := "git@" + accessCanonHost + ":acme/team.git"
	body := "access:\n  - url: \"git@" + accessCanonHost + ":\"\n    via: \"" + w.mirror + "/\"\n" +
		"orgs:\n  - name: acme\n    stores:\n      - url: " + team + "\n        org_folder: org\n      - url: " + accessCanonURL + "\n"
	if err := os.MkdirAll(mira, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(mira), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := a.lookupStore(ctx, "test", "lore-x")
	if err != nil || src.URL != accessCanonURL {
		t.Fatalf("lookup by short name: %+v %v", src, err)
	}
	out, err := a.AddFolder(ctx, opsstores.FolderAddIn{Store: "lore-x", Folder: "client_a"})
	if err != nil {
		t.Fatalf("folder add by short name: %v", err)
	}
	if out.Store != "lore-x" {
		t.Errorf("output names the store %q, want lore-x", out.Store)
	}
	if !strings.Contains(w.storeFiles(t, "acme/lore-x"), "client_a/") {
		t.Fatalf("client_a not pushed to lore-x: %s", w.storeFiles(t, "acme/lore-x"))
	}
	out, err = a.AddFolder(ctx, opsstores.FolderAddIn{Store: accessCanonURL, Folder: "client_b"})
	if err != nil || out.Store != "lore-x" {
		t.Fatalf("folder add by url: %+v %v (want the short name in output)", out, err)
	}
	p, err := a.folderStore(ctx, "test", "lore-x", "client_a")
	if err != nil || p.Label != "lore-x" {
		t.Fatalf("lore-x/client_a: %+v %v", p, err)
	}
	if _, err := a.lookupStore(ctx, "test", "lore-y"); err == nil {
		t.Error("an unknown short name resolved")
	}
}
