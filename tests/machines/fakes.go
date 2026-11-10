//go:build machines && !windows

package machines

// fakes.go — fake gh and ssh (POSIX sh) in $FAKE_W/fakebin. Data files:
//
//	$FAKE_W/gh-accounts  "<login> active|inactive" per line (gh's logins)
//	$FAKE_W/keymap       "<key file name> <login>" per line (who a key logs in as)
//
// ssh -T git@<host> answers like GitHub: the key is -i, else the Host
// block's IdentityFile (Host/HostName/IdentityFile matched without case),
// else ~/.ssh/id_ed25519; a missing key or one nobody owns is denied; a
// host whose HostName is not github.com does not resolve. Any other ssh
// use fails. gh answers auth status, auth token, api user and api repos/<o>/<r>.

import (
	"os"
	"path/filepath"
)

const ghScript = `#!/bin/sh
W="$FAKE_W"
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
  # every repo is private and the active login may push (--jq
  # .private, .permissions.push prints one per line).
  [ -n "$act" ] || exit 1; echo true; echo true; exit 0;;
"--version ")
  echo "gh version 2.60.0 (fake)"; exit 0;;
esac
echo "fake gh: unsupported: $*" >&2
exit 1
`

const sshScript = `#!/bin/sh
W="$FAKE_W"
key=""; host=""; T=0
while [ $# -gt 0 ]; do
  case "$1" in
  -i) key="$2"; shift 2;;
  -T) T=1; shift;;
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
[ -f "$key" ] || { echo "no such identity: $key: No such file or directory" >&2; echo "git@github.com: Permission denied (publickey)." >&2; exit 255; }
login=$(awk -v k="$(basename "$key")" '$1==k{print $2}' "$W/keymap")
[ -n "$login" ] || { echo "git@github.com: Permission denied (publickey)." >&2; exit 255; }
if [ $# -eq 0 ]; then
  echo "Hi $login! You've successfully authenticated, but GitHub does not provide shell access." >&2
  exit 1
fi
echo "fake ssh: unsupported command: $*" >&2
exit 128
`

func (b *builder) fakes() {
	bin := filepath.Join(b.m.Dir, "fakebin")
	b.dir(bin, 0o755)
	for name, body := range map[string]string{"gh": ghScript, "ssh": sshScript} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			b.t.Fatal(err)
		}
	}
	// Machines without a keymap or gh logins still answer.
	b.file(filepath.Join(b.m.Dir, "keymap"), "", 0o644)
	b.file(filepath.Join(b.m.Dir, "gh-accounts"), "", 0o644)
}
