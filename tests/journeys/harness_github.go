//go:build journeys && !windows

package journeys

// harness_github.go — what the setup page's GitHub step needs from a
// machine with no network: ssh keys, ssh host aliases, gh logins and fake
// `gh` and `ssh` programs that answer like GitHub does, first on the
// machine's PATH. Without them the setup page would run the developer's
// real gh and ssh (the World's PATH keeps the parent's), so every journey
// that walks `lore setup apply` through the github step gives its machine
// GitHub accounts first:
//
//	m.GitHubAccounts(t, GitHubAccount{Login: "pat-home", Key: "id_ed25519"},
//		GitHubAccount{Login: "pat-acme", Key: "acme_key", Alias: "github-acme"})
//
// The first account is gh's active login. git itself never meets these
// fakes: the World's ~/.gitconfig routes every github.com url to the fake
// GitHub's bare repos. Data files (under the machine's own folder):
//
//	gh-accounts  "<login> active|inactive" per line
//	keymap       "<key file name> <login>" per line (who a key logs in as)
//
// Every login, key comment and alias is a placeholder; keys are made fresh
// with ssh-keygen per machine and never leave the test's temp folder.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// GitHubAccount is one GitHub login on a machine: the key file in ~/.ssh
// that logs in as it, and the ssh host alias whose IdentityFile is that key
// ("" or "github.com": the key is ~/.ssh/id_ed25519, github.com's default).
type GitHubAccount struct {
	Login string
	Key   string
	Alias string
}

// deadGitHubAPI is a GitHub API address nothing listens on: a lookup fails
// at once instead of reaching the network.
const deadGitHubAPI = "http://127.0.0.1:9"

// GitHubAccounts gives m these GitHub accounts (see the file comment): keys,
// ~/.ssh/config aliases, gh's logins and hosts.yml, and fake gh/ssh first on
// PATH. Call it once per machine, before the setup page's github step.
func (m *Machine) GitHubAccounts(t testing.TB, accts ...GitHubAccount) {
	t.Helper()
	dir := filepath.Join(m.w.Root, "github-"+m.Name)
	bin := filepath.Join(dir, "bin")
	sshDir := filepath.Join(m.Home, ".ssh")
	for _, d := range []string{bin, sshDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(bin, "gh"), fakeGH, 0o755)
	write(filepath.Join(bin, "ssh"), fakeSSH, 0o755)
	var keymap, gh, sshCfg, users strings.Builder
	for i, a := range accts {
		key := filepath.Join(sshDir, a.Key)
		if _, err := os.Stat(key); err != nil {
			if out, err := exec.Command("/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", a.Key+"@placeholder", "-f", key).CombinedOutput(); err != nil {
				t.Fatalf("ssh-keygen: %v %s", err, out)
			}
		}
		fmt.Fprintf(&keymap, "%s %s\n", a.Key, a.Login)
		state := "inactive"
		if i == 0 {
			state = "active"
		}
		fmt.Fprintf(&gh, "%s %s\n", a.Login, state)
		fmt.Fprintf(&users, "            %s: {}\n", a.Login)
		if a.Alias != "" && a.Alias != "github.com" {
			fmt.Fprintf(&sshCfg, "Host %s\n\tHostName github.com\n\tUser git\n\tIdentityFile ~/.ssh/%s\n\tIdentitiesOnly yes\n\n", a.Alias, a.Key)
		}
	}
	write(filepath.Join(dir, "keymap"), keymap.String(), 0o644)
	write(filepath.Join(dir, "gh-accounts"), gh.String(), 0o644)
	write(filepath.Join(sshDir, "known_hosts"), "github.com ssh-ed25519 AAAAplaceholder\n", 0o600)
	if sshCfg.Len() > 0 {
		write(filepath.Join(sshDir, "config"), sshCfg.String(), 0o600)
	}
	ghCfg := filepath.Join(m.Home, ".config", "gh")
	if err := os.MkdirAll(ghCfg, 0o700); err != nil {
		t.Fatal(err)
	}
	active := ""
	if len(accts) > 0 {
		active = accts[0].Login
	}
	write(filepath.Join(ghCfg, "hosts.yml"), "github.com:\n    git_protocol: ssh\n    users:\n"+users.String()+"    user: "+active+"\n", 0o600)
	// The World's shim (git wrapper, lore link) stays first after the
	// fakes; nothing of the developer's PATH (a real gh or ssh) is left.
	m.SetEnv("PATH", bin+string(os.PathListSeparator)+filepath.Join(m.w.Root, "shim")+string(os.PathListSeparator)+"/usr/bin:/bin")
	m.SetEnv("FAKE_GH_DIR", dir)
	m.SetEnv("LORE_GITHUB_API", deadGitHubAPI)
	m.SetEnv("LORE_NO_UPDATE_CHECK", "1")
}

// fakeGH answers the gh calls lore makes: auth status, auth token, api user,
// api user/emails, api repos/<o>/<r> (private, pushable) and --version.
const fakeGH = `#!/bin/sh
W="$FAKE_GH_DIR"
act=$(awk '$2=="active"{print $1}' "$W/gh-accounts")
case "$1 $2" in
"auth status")
  [ -s "$W/gh-accounts" ] || { echo "You are not logged into any GitHub hosts. To log in, run: gh auth login" >&2; exit 1; }
  echo "github.com" >&2
  while read -r l s; do
    a=false; [ "$s" = active ] && a=true
    printf '  ✓ Logged in to github.com account %s (keyring)\n  - Active account: %s\n  - Git operations protocol: ssh\n  - Token: placeholder\n  - Token scopes: '"'"'repo'"'"', '"'"'read:org'"'"'\n\n' "$l" "$a" >&2
  done < "$W/gh-accounts"
  exit 0;;
"auth token")
  [ -n "$act" ] || exit 1; echo "fake-token-$act"; exit 0;;
"api user")
  [ -n "$act" ] || exit 1; printf '{"login":"%s","name":null,"email":null}\n' "$act"; exit 0;;
"api user/emails")
  echo '[]'; exit 0;;
"api repos/"*)
  [ -n "$act" ] || exit 1; echo true; echo true; exit 0;;
"--version ")
  echo "gh version 2.60.0 (fake)"; exit 0;;
esac
echo "fake gh: unsupported: $*" >&2
exit 1
`

// fakeSSH answers ssh -T git@<host> like GitHub: the key is -i, else the
// Host block's IdentityFile, else ~/.ssh/id_ed25519; a key nobody owns is
// denied; a host whose HostName is not github.com does not resolve. Any
// other use fails (git never calls it: its urls are rewritten to files).
const fakeSSH = `#!/bin/sh
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
echo "fake ssh: unsupported command: $*" >&2
exit 128
`
