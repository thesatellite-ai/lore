//go:build stories && !windows

package stories

// ownershape.go — the owner-shaped machine (story O, tests/stories/O-owner-shape.md):
// one hermetic laptop laid out the way the owner's own computer is, so every
// store and setup story can run on the shape real bugs came from instead of
// the harness's plain one-identity machine. What it adds to a journeys
// Machine:
//
//	two git identities      ~/.gitconfig holds the personal one; an
//	                        [includeIf "gitdir:~/code/<org>/"] block gives
//	                        every repo under the org folder the org one
//	an ssh host alias       ~/.ssh/config "Host <alias> / HostName github.com"
//	                        with the org account's key, and
//	                        url."git@<alias>:<owner>/".insteadOf for each of
//	                        the org's GitHub owners (https and ssh forms)
//	git over the alias      the fake ssh serves git-upload-pack and
//	                        git-receive-pack from the fake GitHub's bare
//	                        repos, so a push through the alias lands on the
//	                        same file-backed remote a plain url reaches
//	a GitHub org move       MoveRemote renames a bare repo to another owner
//	                        (GitHub's transfer) and can keep the old one as a
//	                        stale copy
//	repo shapes             GitignoredLore (.lore/ in .gitignore) and
//	                        ClaudeLinkOut (CLAUDE.md a symlink out of the repo)
//
// Everything is made up: logins, emails, owners, aliases.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// OwnerShape is what an owner-shaped machine is made of.
type OwnerShape struct {
	// Personal is the git identity of ~/.gitconfig and the GitHub login of
	// ~/.ssh/id_ed25519 (github.com's default key).
	Personal journeys.Person
	// Org is the git identity every repo under ~/code/<OrgFolder>/ gets
	// through includeIf, and the GitHub login of the alias's key.
	Org journeys.Person
	// OrgFolder is the folder under ~/code whose repos are the org's.
	OrgFolder string
	// Alias is the ~/.ssh/config Host of the org account (HostName github.com).
	Alias string
	// OrgOwners are the GitHub owners whose urls git rewrites to the alias
	// (the org, and the org a store moved to).
	OrgOwners []string
}

// OwnerMachine is a journeys Machine shaped like the owner's laptop.
type OwnerMachine struct {
	*journeys.Machine
	Shape OwnerShape
	s     *Story
}

// Code is ~/code/<parts...> on this machine.
func (o *OwnerMachine) Code(parts ...string) string {
	return filepath.Join(append([]string{o.Home, "code"}, parts...)...)
}

// OrgDir is ~/code/<OrgFolder>/<parts...>: a repo here gets the org identity.
func (o *OwnerMachine) OrgDir(parts ...string) string {
	return o.Code(append([]string{o.Shape.OrgFolder}, parts...)...)
}

// CloneTo clones r over its ssh url (what the owner pastes) into dir.
func (o *OwnerMachine) CloneTo(r *teamharness.Remote, dir string) *teamharness.Clone {
	o.s.T.Helper()
	c := o.Clone(r, filepath.Base(dir))
	o.MoveClone(o.s.T, c, dir)
	return c
}

// OwnerShaped adds a persona whose first laptop is shaped like the owner's
// (see the file comment) and returns that laptop.
func (s *Story) OwnerShaped(sh OwnerShape) *OwnerMachine {
	t := s.T
	t.Helper()
	m := s.Persona(sh.Personal.Name, sh.Personal.Email).Laptop()
	m.GitHubAccounts(t,
		journeys.GitHubAccount{Login: sh.Personal.Login, Key: "id_ed25519"},
		journeys.GitHubAccount{Login: sh.Org.Login, Key: "id_" + sh.OrgFolder, Alias: sh.Alias},
	)
	// The fake ssh GitHubAccounts wrote answers ssh -T only; this one also
	// carries git over the alias to the fake GitHub's bare repos.
	ghDir := filepath.Join(s.Root, "github-"+m.Name)
	writeExec(t, filepath.Join(ghDir, "bin", "ssh"), ownerSSH)
	m.SetEnv("OWNER_FAKE_HOSTS", filepath.Join(s.Root, "hosts"))
	o := &OwnerMachine{Machine: m, Shape: sh, s: s}
	o.WriteGitconfig(t)
	if err := os.MkdirAll(o.OrgDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return o
}

// WriteGitconfig appends the owner's includeIf identity and the alias's
// insteadOf rules to ~/.gitconfig. The harness rewrites that file on
// Online/Offline; call this again after either.
func (o *OwnerMachine) WriteGitconfig(t testing.TB) {
	t.Helper()
	sh := o.Shape
	orgCfg := filepath.Join(o.Home, ".gitconfig-"+sh.OrgFolder)
	writeFile(t, orgCfg, fmt.Sprintf("[user]\n\tname = %s\n\temail = %s\n", sh.Org.Name, sh.Org.Email))
	var b strings.Builder
	fmt.Fprintf(&b, "[includeIf \"gitdir:~/code/%s/\"]\n\tpath = %s\n", sh.OrgFolder, orgCfg)
	for _, owner := range sh.OrgOwners {
		fmt.Fprintf(&b, "[url \"git@%s:%s/\"]\n\tinsteadOf = git@github.com:%s/\n\tinsteadOf = https://github.com/%s/\n", sh.Alias, owner, owner, owner)
	}
	appendFile(t, filepath.Join(o.Home, ".gitconfig"), b.String())
}

// GitUser is git's user.email in dir as git itself resolves it there
// (includeIf included): what a commit made in dir is authored as.
func (o *OwnerMachine) GitUser(t testing.TB, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "config", "user.email")
	cmd.Env = append(os.Environ(), "HOME="+o.Home, "GIT_CONFIG_NOSYSTEM=1")
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// MoveRemote is a GitHub transfer of r to newOwner: the bare repo moves to
// github.com/<newOwner>/<repo>; keepOld leaves a stale copy at the old
// address (a redirect a later push must not rely on). It returns the new remote.
func (s *Story) MoveRemote(t testing.TB, r *teamharness.Remote, newOwner string, keepOld bool) *teamharness.Remote {
	t.Helper()
	parts := strings.Split(r.Slug, "/")
	slug := parts[0] + "/" + newOwner + "/" + parts[2]
	to := filepath.Join(s.Root, "hosts", slug+".git")
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if keepOld {
		s.Git(s.Root, "clone", "-q", "--bare", r.Dir, to)
	} else if err := os.Rename(r.Dir, to); err != nil {
		t.Fatal(err)
	}
	return s.Remote(slug)
}

// GitignoredLore makes dir a plain repo that ignores .lore/.
func GitignoredLore(t testing.TB, dir string) {
	t.Helper()
	PlainRepo(t, dir)
	writeFile(t, filepath.Join(dir, ".gitignore"), ".lore/\n")
}

// ClaudeLinkOut makes dir a plain repo whose CLAUDE.md is a symlink to rel
// (a path outside the repo, relative to the repo's folder).
func ClaudeLinkOut(t testing.TB, dir, rel string) {
	t.Helper()
	PlainRepo(t, dir)
	if err := os.Symlink(rel, filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
}

// writeExec writes an executable file.
func writeExec(t testing.TB, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// ownerSSH is the journeys fake ssh plus git transport: a host whose
// HostName (from ~/.ssh/config) is github.com and a key some login owns
// gets git-upload-pack / git-receive-pack run on the bare repo under
// $OWNER_FAKE_HOSTS/github.com, as GitHub would serve it.
const ownerSSH = `#!/bin/sh
W="$FAKE_GH_DIR"
key=""; host=""
while [ $# -gt 0 ]; do
  case "$1" in
  -i) key="$2"; shift 2;;
  -T) shift;;
  -o|-p|-l|-F|-E) shift 2;;
  -V) echo "OpenSSH_9.6p1 (fake)" >&2; exit 0;;
  -*) shift;;
  *) host="$1"; shift; break;;
  esac
done
host="${host#*@}"
blk(){ [ -f "$HOME/.ssh/config" ] || return 0; awk -v h="$host" -v k="$1" 'tolower($1)=="host"{m=0;for(i=2;i<=NF;i++)if($i==h)m=1;next} m&&tolower($1)==k{print $2;exit}' "$HOME/.ssh/config"; }
hn=$(blk hostname); [ -n "$hn" ] || hn="$host"
[ "$hn" = "github.com" ] || { echo "ssh: Could not resolve hostname $host: Name or service not known" >&2; exit 255; }
[ -n "$key" ] || key=$(blk identityfile)
case "$key" in "~/"*) key="$HOME/${key#\~/}";; esac
[ -n "$key" ] || key="$HOME/.ssh/id_ed25519"
[ -f "$key" ] || { echo "git@github.com: Permission denied (publickey)." >&2; exit 255; }
login=$(awk -v k="$(basename "$key")" '$1==k{print $2}' "$W/keymap")
[ -n "$login" ] || { echo "git@github.com: Permission denied (publickey)." >&2; exit 255; }
if [ $# -eq 0 ]; then
  echo "Hi $login! You've successfully authenticated, but GitHub does not provide shell access." >&2
  exit 1
fi
cmd="$*"
verb="${cmd%% *}"
path=$(printf '%s' "${cmd#* }" | tr -d "'\"")
path="${path#/}"
case "$verb" in
git-upload-pack|git-receive-pack|git-upload-archive) ;;
*) echo "fake ssh: unsupported command: $*" >&2; exit 128;;
esac
repo="$OWNER_FAKE_HOSTS/github.com/$path"
[ -d "$repo" ] || repo="$repo.git"
[ -d "$repo" ] || { echo "ERROR: Repository not found." >&2; exit 128; }
exec "$LORE_TEAM_REAL_GIT" "${verb#git-}" "$repo"
`
