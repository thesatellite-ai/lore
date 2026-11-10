//go:build setupe2e

package setupe2e

// fakes.go — the fake gh, ssh and git programs (POSIX sh, written into
// <dir>/fakebin at build time) and the fake GitHub REST API (an
// in-process HTTP server the lore binary reaches through LORE_GITHUB_API).
//
// Data they share, all under the world dir W:
//
//	W/accounts.tsv   login<TAB>token<TAB>active(1|0)<TAB>scopes   (gh's logins, in order)
//	W/keys.tsv       key basename<TAB>login                       (which account a private key belongs to)
//	W/remote/<owner>/<repo>.git                                   (bare repos = GitHub; gh repo create adds one)
//	W/gh.log, W/ssh.log, W/git.log                                (one line per call: "<tool> account=<login|-> args=<args>";
//	                                                              ssh's line has "from=<dir|->" before args, see below)
//
// Cause of an ssh call: fakebin/git exports the folder it runs in (its
// cwd after any leading -C) as SETUPE2E_GIT_CWD, and fakebin/ssh logs it
// as from=<dir> (spaces and % percent-encoded; "-" when ssh was not run by
// git, e.g. lore's own ssh -T probe). A judge tells by it which repo's git
// made a call (a code repo's live fetch of its own origin vs a setup's
// store clone), instead of by when the call happened.
//
// Account rules, like the real tools:
//   - gh acts as the login whose token is in GH_TOKEN, else gh's active login;
//   - ssh acts as the login the used key belongs to (-i, else the Host
//     alias's IdentityFile, else ~/.ssh/id_ed25519); a key nobody owns, a
//     missing key file or a .pub given as -i is denied;
//   - an account owns <login>/* and nothing else (push, create, read).
//
// Token values are placeholders minted per world at run time; nothing
// key- or token-shaped is written in this file.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Log line format shared by the three fake programs.
const logFormat = `%s account=%s args=%s`

// ghScript is fakebin/gh. @W@ is replaced with the world dir.
const ghScript = `#!/bin/sh
# fake gh (setupe2e): acts as GH_TOKEN's login, else the active login.
W=@W@
tok_login(){ awk -F'\t' -v t="$1" '$2==t{print $1;exit}' "$W/accounts.tsv"; }
active(){ awk -F'\t' '$3=="1"{print $1;exit}' "$W/accounts.tsv"; }
token_of(){ awk -F'\t' -v u="$1" '$1==u{print $2;exit}' "$W/accounts.tsv"; }
acct=""
[ -n "$GH_TOKEN" ] && acct=$(tok_login "$GH_TOKEN")
[ -n "$GH_TOKEN" ] && [ -z "$acct" ] && acct="?badtoken"
[ -z "$acct" ] && acct=$(active)
echo "gh account=${acct:--} args=$*" >> "$W/gh.log"
u=""; prev=""; for a in "$@"; do [ "$prev" = "--user" ] && u="$a"; prev="$a"; done
notlogged(){ echo "You are not logged into any GitHub hosts. To log in, run: gh auth login" >&2; exit 1; }
[ "$acct" = "?badtoken" ] && { echo "HTTP 401: Bad credentials (https://api.github.com/user)" >&2; exit 1; }
owns(){ case "$1" in "$acct"/*) return 0;; esac; return 1; }
case "$1 $2" in
  "auth status")
    [ -s "$W/accounts.tsv" ] || notlogged
    echo "github.com" >&2
    while IFS='	' read -r l t a s; do
      act=false; [ "$a" = 1 ] && act=true
      printf '  ✓ Logged in to github.com account %s (keyring)\n  - Active account: %s\n  - Git operations protocol: ssh\n  - Token: placeholder\n  - Token scopes: %s\n\n' "$l" "$act" "$s" >&2
    done < "$W/accounts.tsv"
    exit 0;;
  "auth token")
    [ -s "$W/accounts.tsv" ] || notlogged
    [ -z "$u" ] && u=$(active)
    t=$(token_of "$u"); [ -n "$t" ] || { echo "no oauth token found for github.com account $u" >&2; exit 1; }
    echo "$t"; exit 0;;
  "auth git-credential")
    cat > /dev/null
    [ -s "$W/accounts.tsv" ] || exit 1
    printf 'protocol=https\nhost=github.com\nusername=x-access-token\npassword=%s\n' "$(token_of "$acct")"; exit 0;;
  "auth switch")
    [ -n "$u" ] && [ -n "$(token_of "$u")" ] || { echo "no account $u" >&2; exit 1; }
    awk -F'\t' -v OFS='\t' -v u="$u" '{ $3 = ($1==u) ? "1" : "0"; print }' "$W/accounts.tsv" > "$W/accounts.tsv.new" && mv "$W/accounts.tsv.new" "$W/accounts.tsv"
    sed -i "s/^    user: .*/    user: $u/" "$HOME/.config/gh/hosts.yml"
    echo "✓ Switched active account for github.com to $u" >&2; exit 0;;
esac
[ -n "$acct" ] || notlogged
if [ "$1" = api ]; then
  path=""; method=GET
  shift
  while [ $# -gt 0 ]; do
    case "$1" in
      -X|--method) method="$2"; shift 2;;
      --jq|-q|-f|-F|--field|--raw-field|-H|--header) shift 2;;
      -*) shift;;
      *) [ -z "$path" ] && path="$1"; shift;;
    esac
  done
  r="${path#repos/}"; owner="${r%%/*}"; rest="${r#*/}"; repo="${rest%%/*}"
  [ -d "$W/remote/$owner/$repo.git" ] && owns "$owner/$repo" || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
  case "$rest" in
    "$repo") printf 'true\ntrue\n'; exit 0;;
    "$repo"/collaborators/*) exit 0;;
  esac
  echo "gh: Not Found (HTTP 404)" >&2; exit 1
fi
if [ "$1 $2" = "repo create" ]; then
  r="$3"; owner="${r%%/*}"
  owns "$r" || { echo "GraphQL: $acct cannot create a repository for $owner. (createRepository)" >&2; exit 1; }
  [ -d "$W/remote/$r.git" ] && { echo "GraphQL: Name already exists on this account (createRepository)" >&2; exit 1; }
  mkdir -p "$W/remote/$owner" && /usr/bin/git init -q --bare -b main "$W/remote/$r.git"
  echo "✓ Created repository $r on GitHub"; exit 0
fi
echo "fake gh: unhandled $*" >&2; exit 1
`

// sshScript is fakebin/ssh.
const sshScript = `#!/bin/sh
# fake ssh (setupe2e): GitHub over ssh, served from $W/remote.
W=@W@
key=""; dest=""; cmd=""; all="$*"
while [ $# -gt 0 ]; do
  case "$1" in
    -i) key="$2"; shift 2;;
    -o|-p|-l|-F|-E|-c|-m|-J|-L|-R|-D|-S|-W|-b|-e|-Q|-O|-w|-I|-B) shift 2;;
    -*) shift;;
    *) if [ -z "$dest" ]; then dest="$1"; shift; else cmd="$*"; break; fi;;
  esac
done
host="${dest#*@}"
blk(){ [ -f "$HOME/.ssh/config" ] || return 0; awk -v h="$host" -v k="$1" 'tolower($1)=="host"{m=0;for(i=2;i<=NF;i++)if($i==h)m=1;next} m&&tolower($1)==k{print $2;exit}' "$HOME/.ssh/config"; }
hn=$(blk hostname); [ -z "$hn" ] && hn="$host"
if [ -z "$key" ]; then
  key=$(blk identityfile)
  case "$key" in "~/"*) key="$HOME/${key#\~/}";; esac
  [ -z "$key" ] && key="$HOME/.ssh/id_ed25519"
fi
acct=$(awk -F'\t' -v k="$(basename "$key")" '$1==k{print $2;exit}' "$W/keys.tsv" 2>/dev/null)
case "$key" in *.pub) acct="";; esac
[ -f "$key" ] || acct=""
from=$(printf '%s' "${SETUPE2E_GIT_CWD:--}" | sed 's/%/%25/g; s/ /%20/g')
echo "ssh account=${acct:--} from=$from args=$all" >> "$W/ssh.log"
deny(){ echo "git@github.com: Permission denied (publickey)." >&2; exit 255; }
[ "$hn" = "github.com" ] || { echo "ssh: Could not resolve hostname $host: Name or service not known" >&2; exit 255; }
case "$key" in *.pub) echo "Load key \"$key\": invalid format" >&2; deny;; esac
[ -f "$key" ] || { echo "Warning: Identity file $key not accessible: No such file or directory." >&2; deny; }
[ -n "$acct" ] || deny
if [ -z "$cmd" ]; then
  echo "Hi $acct! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1
fi
prog="${cmd%% *}"; repo=$(echo "$cmd" | sed "s/^[^ ]* //; s/'//g; s#^/##")
owner="${repo%%/*}"
bare="$W/remote/$repo"; case "$bare" in *.git) ;; *) bare="$bare.git";; esac
if [ "$owner" != "$acct" ] || [ ! -d "$bare" ]; then
  printf 'ERROR: Repository not found.\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n' >&2
  exit 128
fi
case "$prog" in git-upload-pack|git-receive-pack) exec /usr/bin/$prog "$bare";; esac
echo "fake ssh: unsupported command $cmd" >&2; exit 1
`

// gitScript is fakebin/git: logs, then runs the real git.
const gitScript = `#!/bin/sh
# fake git (setupe2e): one log line per call, then the real git.
# lore's https route (adapters/probe/route.go httpsArgs) passes a
# credential helper running "gh auth token --user <login>": that login is
# the https account. https://github.com/ is served from $W/https/<login>/,
# a view holding only <login>'s own repos (so another owner's repo is not
# found, like GitHub for a private repo), $W/https/-none-/ without a login.
W=@W@
cwd=$PWD; prev=""
for a do
  case "$prev" in
    -C) case "$a" in /*) cwd="$a";; *) cwd="$cwd/$a";; esac; prev=""; continue;;
    -c) prev=""; continue;;
  esac
  case "$a" in -C|-c) prev="$a";; -*) prev="";; *) break;; esac
done
export SETUPE2E_GIT_CWD="$cwd"
acct=""
k=$(printf '%s' "$GIT_SSH_COMMAND" | awk '{for(i=1;i<NF;i++) if($i=="-i"){print $(i+1);exit}}' | tr -d "'\"")
[ -n "$k" ] && [ -f "$k" ] && acct=$(awk -F'\t' -v k="$(basename "$k")" '$1==k{print $2;exit}' "$W/keys.tsv")
hu=$(printf '%s\n' "$@" | sed -n 's/.*--user \([A-Za-z0-9-]*\)).*/\1/p' | head -1)
view=-none-
if [ -n "$hu" ] && awk -F'\t' -v u="$hu" '$1==u{f=1} END{exit !f}' "$W/accounts.tsv"; then
  view="$hu"; acct="$hu"
  # the link is made once and atomically (a temp link renamed over): checks
  # run git concurrently, and "ln -sfn" unlinks then relinks, so another
  # call could find no repo in between
  mkdir -p "$W/https/$hu" "$W/remote/$hu"
  if [ ! -L "$W/https/$hu/$hu" ]; then
    ln -s "$W/remote/$hu" "$W/https/$hu/.$hu.$$" && mv -Tf "$W/https/$hu/.$hu.$$" "$W/https/$hu/$hu"
  fi
fi
mkdir -p "$W/https/-none-"
echo "git account=${acct:--} args=$*" >> "$W/git.log"
for a do
  shift
  case "$a" in url.https://github.com/.insteadOf=*) a="url.$W/https/$view/.insteadOf=${a#*.insteadOf=}";; esac
  set -- "$@" "$a"
done
exec /usr/bin/git -c "url.$W/https/$view/.insteadOf=https://github.com/" "$@"
`

// writeFakebin writes the three fake programs into dir/fakebin.
func writeFakebin(dir string) error {
	bin := filepath.Join(dir, "fakebin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return fmt.Errorf("fakebin: %w", err)
	}
	for name, body := range map[string]string{"gh": ghScript, "ssh": sshScript, "git": gitScript} {
		if err := writeExecutable(filepath.Join(bin, name), strings.ReplaceAll(body, "@W@", shellQuote(dir))); err != nil {
			return fmt.Errorf("fakebin %s: %w", name, err)
		}
	}
	return nil
}

// fakebinMode is the mode of the fake programs: anyone may run them.
const fakebinMode = 0o755

// writeExecutable writes a program the world will exec. The
// open-write-close holds syscall.ForkLock for reading, which every fork
// takes for writing: shapes build in parallel, and a child forked while
// the file is open for writing would inherit the descriptor until its own
// exec, making a run of the script in that window fail with ETXTBSY
// ("text file busy").
func writeExecutable(path, body string) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, []byte(body), fakebinMode)
}

// shellQuote single-quotes s for sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// LogLine is one parsed fake-tool log line.
type LogLine struct {
	Tool    string
	Account string
	// From is the folder of the git that ran this ssh ("-" when git did
	// not run it); only ssh lines have it (see the header: cause of an ssh call).
	From string
	Args string
}

// logFromField starts ssh's from=<dir> field, between account and args.
const logFromField = " from="

// noFrom is From of a line without the field or of an ssh git did not run.
const noFrom = "-"

// ReadLog parses one fake tool's log; a missing log is no calls.
func ReadLog(path string) ([]LogLine, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read log: %w", err)
	}
	defer f.Close()
	var out []LogLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		tool, rest, ok := strings.Cut(sc.Text(), " account=")
		if !ok {
			return nil, fmt.Errorf("read log %s: bad line %q", path, sc.Text())
		}
		acct, args, _ := strings.Cut(rest, " args=")
		from := noFrom
		if a, f, ok := strings.Cut(acct, logFromField); ok {
			acct = a
			if d, err := url.PathUnescape(f); err == nil {
				from = d
			}
		}
		out = append(out, LogLine{Tool: tool, Account: acct, From: from, Args: args})
	}
	return out, sc.Err()
}

// fakeAPI is the fake GitHub REST API: /user, /user/emails, /user/repos
// and /avatars/<login>, answering the world's placeholder tokens.
type fakeAPI struct {
	w   *World
	srv *http.Server
	ln  net.Listener
}

func startFakeAPI(w *World) (*fakeAPI, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("fake api: %w", err)
	}
	a := &fakeAPI{w: w, ln: ln}
	a.srv = &http.Server{Handler: http.HandlerFunc(a.serve)}
	go func() { _ = a.srv.Serve(ln) }() // Serve returns ErrServerClosed on Close
	return a, nil
}

func (a *fakeAPI) URL() string { return "http://" + a.ln.Addr().String() }

func (a *fakeAPI) Close() { _ = a.srv.Close() }

// apiUser is the /user answer.
type apiUser struct {
	ID        int     `json:"id"`
	Login     string  `json:"login"`
	Name      string  `json:"name"`
	Email     *string `json:"email"`
	AvatarURL string  `json:"avatar_url"`
}

type apiEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

type apiRepo struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	SSHURL   string `json:"ssh_url"`
	Private  bool   `json:"private"`
}

type apiMessage struct {
	Message string `json:"message"`
}

func (a *fakeAPI) serve(rw http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/avatars/") {
		rw.Header().Set("Content-Type", "image/png")
		_, _ = rw.Write(tinyPNG)
		return
	}
	var acct *Account
	auth := r.Header.Get("Authorization")
	for i := range a.w.Accounts {
		if auth == "Bearer "+a.w.Accounts[i].token || auth == "token "+a.w.Accounts[i].token {
			acct = &a.w.Accounts[i]
		}
	}
	if acct == nil {
		writeJSON(rw, http.StatusUnauthorized, apiMessage{Message: "Bad credentials"})
		return
	}
	switch path {
	case "/user":
		if acct.Login == a.w.hangProfile {
			// A profile read that never answers: lore's discovery gives up
			// at its own deadline and the request's context ends.
			<-r.Context().Done()
			return
		}
		// GitHub's header is "a, b, c"; gh auth status quotes each scope.
		rw.Header().Set("X-OAuth-Scopes", strings.ReplaceAll(acct.scopes, "'", ""))
		writeJSON(rw, http.StatusOK, apiUser{ID: acct.id, Login: acct.Login, Name: acct.displayName, AvatarURL: a.URL() + "/avatars/" + acct.Login})
	case "/user/emails":
		if !strings.Contains(acct.scopes, "user:email") {
			writeJSON(rw, http.StatusNotFound, apiMessage{Message: "Not Found"})
			return
		}
		out := make([]apiEmail, 0, len(acct.Emails))
		for i, e := range acct.Emails {
			out = append(out, apiEmail{Email: e, Primary: i == 0, Verified: true})
		}
		writeJSON(rw, http.StatusOK, out)
	case "/user/repos":
		out := []apiRepo{}
		ents, _ := os.ReadDir(filepath.Join(a.w.Dir, "remote", acct.Login)) // no dir = no repos
		for _, e := range ents {
			n := strings.TrimSuffix(e.Name(), ".git")
			out = append(out, apiRepo{FullName: acct.Login + "/" + n, Name: n, SSHURL: "git@github.com:" + acct.Login + "/" + n + ".git", Private: true})
		}
		writeJSON(rw, http.StatusOK, out)
	default:
		writeJSON(rw, http.StatusNotFound, apiMessage{Message: "Not Found"})
	}
}

func writeJSON[T apiUser | []apiEmail | []apiRepo | apiMessage](rw http.ResponseWriter, code int, v T) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(v) // the client went away
}

// tinyPNG is a valid 1x1 grey PNG (the avatar).
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x00, 0x00, 0x00, 0x00, 0x3a, 0x7e, 0x9b,
	0x55, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x68, 0x00, 0x00, 0x00,
	0x82, 0x00, 0x81, 0x77, 0xcd, 0x72, 0xb6, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}
