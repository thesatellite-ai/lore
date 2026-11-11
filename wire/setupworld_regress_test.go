package wire_test

// setupworld_regress_test.go — the fake machine the setup regression tests
// (*_regress_test.go in this package) run the REAL wire setup ops on: a
// temp HOME with ~/.ssh keys made by ssh-keygen (with or without .pub),
// ~/.ssh/config aliases, ~/.gitconfig with includeIf identities, gh's
// hosts.yml, and fake gh / ssh / git programs first on PATH. Every fake
// call appends one line `<tool> account=<login or -> args=<args>` to its
// log, so a test can assert which GitHub account each network call was
// made as (gh: the login whose token is in GH_TOKEN, else the active one;
// ssh: the login the key it used belongs to). RW_NET_DELAY (seconds) makes
// every fake ssh and gh network call that slow.
//
// The harness only uses API that existed before the bugs it pins were
// fixed (opstest, op names as strings, onboard value types), so each
// regression test can be dropped into an old revision and seen to fail.
// Tokens are placeholders built at run time, never key-shaped literals.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
	"github.com/thesatellite-ai/lore/wire"
)

// rwAccount is one GitHub account of the fake machine.
type rwAccount struct {
	Login string
	Email string
	// Key is the private key's basename under ~/.ssh (ssh logs in as
	// Login with it).
	Key string
}

// rwShape picks the machine (same dimensions as tests/setupe2e's shape).
type rwShape struct {
	// GHAccounts is how many accounts gh is logged in to (0..2).
	GHAccounts int
	// GHActiveSecond makes the second account gh's active login.
	GHActiveSecond bool
	// SSH: "none" | "default_only" | "several" | "missing_key".
	SSH string
	// NoPub removes every .pub beside the private keys.
	NoPub bool
	// IncludeIf is how many extra identities ~/.gitconfig has in includeIf
	// blocks (0..2), each with its own folder.
	IncludeIf int
	// StartPlain starts in a plain folder instead of a git repo.
	StartPlain bool
}

// rwWorld is one fake machine plus the App over its start folder.
type rwWorld struct {
	T        *testing.T
	H        *opstest.H
	Home     string
	Dir      string
	Remote   string
	Start    string
	Accounts []rwAccount
	// IncludeDirs are the includeIf folders (~/clients/<n>), one per extra
	// identity.
	IncludeDirs []string
}

// rwAllAccounts are the machine's possible accounts in order: the first
// one is the default ssh key's (github.com), the second an alias's.
var rwAllAccounts = []rwAccount{
	{Login: "octo-personal", Email: "person@example.com", Key: "id_ed25519"},
	{Login: "octo-work", Email: "work@example.org", Key: "work-key"},
}

// newRegressWorld builds the machine for shape and opens an App whose repo
// is the start folder (~/www/proj) and whose lore home is ~/.lore.
func newRegressWorld(t *testing.T, s rwShape) *rwWorld {
	t.Helper()
	for _, tool := range []string{"git", "ssh-keygen", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s not installed: the setup regression world needs it", tool)
		}
	}
	realGit, _ := exec.LookPath("git")
	dir := t.TempDir()
	w := &rwWorld{T: t, Dir: dir, Home: filepath.Join(dir, "home"), Remote: filepath.Join(dir, "remote")}
	w.Accounts = rwAllAccounts[:s.GHAccounts]
	if s.GHAccounts == 0 {
		w.Accounts = nil
	}
	bin := filepath.Join(dir, "bin")
	sshDir := filepath.Join(w.Home, ".ssh")
	ghDir := filepath.Join(w.Home, ".config", "gh")
	for _, d := range []string{bin, sshDir, ghDir, w.Remote} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	testenv.SetHome(t, w.Home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(w.Home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	api := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		http.Error(rw, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	t.Cleanup(api.Close)
	t.Setenv("LORE_GITHUB_API", api.URL)

	// Keys for every possible account (an alias may point at one gh has
	// no login for), real ed25519 so ssh-keygen -lf works.
	for _, a := range rwAllAccounts {
		key := filepath.Join(sshDir, a.Key)
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", a.Login, "-f", key).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v %s", err, out)
		}
		if s.NoPub {
			if err := os.Remove(key + ".pub"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var cfg strings.Builder
	switch s.SSH {
	case "default_only":
		cfg.WriteString("Host github.com\n\tIdentityFile ~/.ssh/" + rwAllAccounts[0].Key + "\n")
	case "several":
		cfg.WriteString("Host github.com\n\tIdentityFile ~/.ssh/" + rwAllAccounts[0].Key + "\n")
		cfg.WriteString("Host work-gh\n\tHostName github.com\n\tUser git\n\tIdentityFile ~/.ssh/" + rwAllAccounts[1].Key + "\n")
	case "missing_key":
		cfg.WriteString("Host gone-gh\n\tHostName github.com\n\tIdentityFile ~/.ssh/not-here\n")
	}
	w.write(filepath.Join(sshDir, "config"), cfg.String(), 0o600)
	w.write(filepath.Join(sshDir, "known_hosts"), "", 0o600)

	if len(w.Accounts) > 0 {
		active := w.Accounts[0].Login
		if s.GHActiveSecond && len(w.Accounts) > 1 {
			active = w.Accounts[1].Login
		}
		var hosts strings.Builder
		hosts.WriteString("github.com:\n  users:\n")
		for _, a := range w.Accounts {
			hosts.WriteString("    " + a.Login + ":\n")
		}
		hosts.WriteString("  user: " + active + "\n  git_protocol: ssh\n")
		w.write(filepath.Join(ghDir, "hosts.yml"), hosts.String(), 0o600)
		w.write(filepath.Join(bin, "gh"), w.ghScript(active), 0o755)
	} else {
		w.write(filepath.Join(bin, "gh"), "#!/bin/sh\necho \"gh account=- args=$*\" >> "+shq(w.Log("gh"))+"\necho 'You are not logged into any GitHub hosts. To log in, run: gh auth login' >&2\nexit 1\n", 0o755)
	}
	w.write(filepath.Join(bin, "ssh"), w.sshScript(), 0o755)
	w.write(filepath.Join(bin, "git"), "#!/bin/sh\necho \"git account=- args=$*\" >> "+shq(w.Log("git"))+"\nexec "+shq(realGit)+" \"$@\"\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// ~/.gitconfig: the main identity plus IncludeIf client identities.
	var gc strings.Builder
	gc.WriteString("[user]\n\tname = Pat Example\n\temail = " + rwAllAccounts[0].Email + "\n")
	for i := range s.IncludeIf {
		n := string(rune('a' + i))
		cdir := filepath.Join(w.Home, "clients", n)
		if err := os.MkdirAll(cdir, 0o755); err != nil {
			t.Fatal(err)
		}
		w.IncludeDirs = append(w.IncludeDirs, cdir)
		inc := filepath.Join(w.Home, ".gitconfig-client-"+n)
		w.write(inc, "[user]\n\tname = Client "+n+"\n\temail = client-"+n+"@example.org\n", 0o644)
		gc.WriteString("[includeIf \"gitdir:~/clients/" + n + "/\"]\n\tpath = " + filepath.ToSlash(inc) + "\n")
	}
	w.write(filepath.Join(w.Home, ".gitconfig"), gc.String(), 0o644)

	w.Start = filepath.Join(w.Home, "www", "proj")
	if err := os.MkdirAll(w.Start, 0o755); err != nil {
		t.Fatal(err)
	}
	if !s.StartPlain {
		if out, err := exec.Command(realGit, "-C", w.Start, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	w.open()
	w.ResetLogs()
	return w
}

// open (re)builds the App over the start folder and ~/.lore.
func (w *rwWorld) open() {
	start, home := w.Start, filepath.Join(w.Home, ".lore")
	w.H = opstest.Bare(w.T, func(o *wire.Options) { o.RepoDir, o.Home = start, home })
}

// Reopen builds a fresh App (a new process: no in-memory caches).
func (w *rwWorld) Reopen() { w.open() }

func (w *rwWorld) write(path, body string, mode os.FileMode) {
	w.T.Helper()
	fakeexec.Write(w.T, path, body, mode)
}

// Log is the path of tool's call log.
func (w *rwWorld) Log(tool string) string { return filepath.Join(w.Dir, tool+".log") }

// Lines is tool's log lines (args contain sub).
func (w *rwWorld) Lines(tool, sub string) []string {
	b, _ := os.ReadFile(w.Log(tool))
	var out []string
	for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if l != "" && strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return out
}

// Settle returns once the background probes earlier operations left
// running have ended (wire.WaitAliasProbesForTest), so the calls in the logs
// after it belong to the operations that started them, whatever the
// machine's speed.
func (w *rwWorld) Settle() { wire.WaitAliasProbesForTest(w.H.App) }

// ResetLogs empties every call log.
func (w *rwWorld) ResetLogs() {
	for _, tool := range []string{"gh", "ssh", "git"} {
		_ = os.Remove(w.Log(tool))
	}
}

// Token is the placeholder token gh hands out for login (run-time value).
func (w *rwWorld) Token(login string) string {
	return "placeholder-" + strings.ReplaceAll(login, "-", "") + "-" + filepath.Base(w.Dir)
}

// Account is the account with login.
func (w *rwWorld) Account(login string) rwAccount {
	for _, a := range rwAllAccounts {
		if a.Login == login {
			return a
		}
	}
	w.T.Fatalf("no account %s", login)
	return rwAccount{}
}

// AddRemote makes the bare repo owner/name on the fake GitHub.
func (w *rwWorld) AddRemote(owner, name string) {
	w.T.Helper()
	p := filepath.Join(w.Remote, owner, name+".git")
	if out, err := exec.Command("git", "init", "--bare", "-q", p).CombinedOutput(); err != nil {
		w.T.Fatalf("git init --bare: %v %s", err, out)
	}
}

// ghScript is the fake gh for the logged-in accounts.
func (w *rwWorld) ghScript(active string) string {
	var tok, out, status strings.Builder
	for _, a := range w.Accounts {
		tok.WriteString("    " + shq(w.Token(a.Login)) + ") acct=" + a.Login + ";;\n")
		out.WriteString("  " + a.Login + ") echo " + shq(w.Token(a.Login)) + ";;\n")
		act := "false"
		if a.Login == active {
			act = "true"
		}
		status.WriteString("  ✓ Logged in to github.com account " + a.Login + " (keyring)\n  - Active account: " + act + "\n  - Git operations protocol: ssh\n  - Token: gho_************************************\n  - Token scopes: 'gist', 'read:org', 'repo'\n\n")
	}
	return `#!/bin/sh
R=` + shq(w.Remote) + `
acct=""
if [ -n "$GH_TOKEN" ]; then
  case "$GH_TOKEN" in
` + tok.String() + `    *) acct=invalid-token;;
  esac
else
  acct=` + active + `
fi
echo "gh account=$acct args=$*" >> ` + shq(w.Log("gh")) + `
case "$1" in auth) ;; *) [ -n "$RW_NET_DELAY" ] && sleep "$RW_NET_DELAY";; esac
u=""; prev=""; for a in "$@"; do [ "$prev" = "--user" ] && u="$a"; prev="$a"; done
notfound(){ echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
case "$1 $2" in
  "auth status")
    cat >&2 <<'T'
github.com
` + status.String() + `T
    exit 0;;
  "auth token")
    [ -z "$u" ] && u=` + active + `
    case "$u" in
` + out.String() + `      *) echo "no oauth token found for github.com account $u" >&2; exit 1;;
    esac; exit 0;;
  "auth git-credential")
    cat > /dev/null
    printf 'protocol=https\nhost=github.com\nusername=x-access-token\npassword=%s\n' "$(case "$acct" in
` + out.String() + `  esac)"; exit 0;;
  "repo create")
    repo="$3"; owner="${repo%%/*}"
    if [ "$owner" != "$acct" ]; then echo "GraphQL: $acct cannot create a repository for $owner. (createRepository)" >&2; exit 1; fi
    [ -d "$R/$repo.git" ] && { echo "GraphQL: Name already exists on this account (createRepository)" >&2; exit 1; }
    git init --bare -q "$R/$repo.git" >/dev/null 2>&1 || exit 1
    echo "https://github.com/$repo"; exit 0;;
  "api --silent")
    p="${3#repos/}"; o="${p%%/*}"; rest="${p#*/}"; name="${rest%%/*}"
    [ -d "$R/$o/$name.git" ] && [ "$o" = "$acct" ] && exit 0
    notfound;;
  "api -X") exit 0;;
  "api repos/"*)
    p="${2#repos/}"; o="${p%%/*}"
    [ -d "$R/$p.git" ] && [ "$o" = "$acct" ] && { printf 'true\ntrue\n'; exit 0; }
    notfound;;
esac
echo "fake gh: unhandled $*" >&2; exit 1
`
}

// sshScript is the fake ssh: resolves aliases through ~/.ssh/config,
// greets with the key's account, serves git over local bare repos the
// account owns.
func (w *rwWorld) sshScript() string {
	var keys strings.Builder
	for _, a := range rwAllAccounts {
		keys.WriteString("    " + a.Key + ") acct=" + a.Login + ";;\n")
	}
	return `#!/bin/sh
R=` + shq(w.Remote) + `
L=` + shq(w.Log("ssh")) + `
all="$*"
[ -n "$RW_NET_DELAY" ] && sleep "$RW_NET_DELAY"
key=""; dest=""; cmd=""
while [ $# -gt 0 ]; do
  case "$1" in
    -i) key="$2"; shift 2;;
    -o|-p|-l|-F|-E|-c|-m|-J|-L|-R|-D|-S|-W|-b|-e|-Q|-O|-w|-I|-B) shift 2;;
    -*) shift;;
    *) if [ -z "$dest" ]; then dest="$1"; shift; else cmd="$*"; break; fi;;
  esac
done
host="${dest#*@}"
acct=""
deny(){ echo "ssh account=- args=$all" >> "$L"; echo "git@github.com: Permission denied (publickey)." >&2; exit 255; }
bykey(){
  k="$1"; case "$k" in "~/"*) k="$HOME/${k#\~/}";; esac
  case "$k" in *.pub) echo "Load key \"$k\": invalid format" >&2; deny;; esac
  [ -f "$k" ] || { echo "Warning: Identity file $k not accessible: No such file or directory." >&2; deny; }
  case "$(basename "$k")" in
` + keys.String() + `    *) deny;;
  esac
}
blk(){ awk -v h="$host" -v k="$1" 'tolower($1)=="host"{m=0;for(i=2;i<=NF;i++)if($i==h)m=1;next} m&&tolower($1)==k{print $2;exit}' "$HOME/.ssh/config"; }
hn=$(blk hostname); [ -z "$hn" ] && hn="$host"
[ "$hn" = "github.com" ] || { echo "ssh account=- args=$all" >> "$L"; echo "ssh: Could not resolve hostname $host: Name or service not known" >&2; exit 255; }
if [ -n "$key" ]; then bykey "$key"
else
  idf=$(blk identityfile)
  if [ -n "$idf" ]; then bykey "$idf"; else bykey "$HOME/.ssh/id_ed25519"; fi
fi
case "$RW_SLOW_ALIAS" in "$host:"*) sleep "${RW_SLOW_ALIAS#*:}";; esac
echo "ssh account=$acct args=$all" >> "$L"
if [ -z "$cmd" ]; then
  echo "Hi $acct! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1
fi
prog="${cmd%% *}"; repo=$(echo "$cmd" | sed "s/^[^ ]* //; s/'//g; s#^/##")
owner="${repo%%/*}"
bare="$R/$repo"; case "$bare" in *.git) ;; *) bare="$bare.git";; esac
if [ "$acct" != "$owner" ] || [ ! -d "$bare" ]; then
  echo "ssh notfound account=$acct owner=$owner bare=$bare cmd=$cmd" >> "$L"
  echo "ERROR: Repository not found." >&2
  echo "fatal: Could not read from remote repository." >&2
  exit 128
fi
# "git upload-pack", not "git-upload-pack": Git for Windows keeps the
# dashed programs out of PATH (in libexec); only git itself is there.
case "$prog" in git-upload-pack|git-receive-pack) exec git "${prog#git-}" "$bare";; esac
echo "fake ssh: unsupported command $cmd" >&2; exit 1
`
}

// shq single-quotes s for sh.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// ---------------------------------------------------------------------
// op helpers (JSON in, small local structs out, so old revisions compile)

// rwAnswers is a step's answers in their natural JSON form.
type rwAnswers map[string]json.RawMessage

// rwQuestion / rwStep / rwPlan read setup.plan's output.
type rwQuestion struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Required bool            `json:"required"`
	Value    json.RawMessage `json:"value"`
	Choices  struct {
		Static []struct {
			Value string `json:"value"`
		} `json:"static"`
	} `json:"choices"`
}

type rwStep struct {
	ID        string       `json:"id"`
	Questions []rwQuestion `json:"questions"`
	Optional  bool         `json:"optional"`
}

type rwProgress struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type rwPlan struct {
	Steps    []rwStep     `json:"steps"`
	Progress []rwProgress `json:"progress"`
}

type rwFix struct {
	Text     string   `json:"text"`
	Command  []string `json:"command"`
	Runnable bool     `json:"runnable"`
}

type rwCheck struct {
	Check     string `json:"check"`
	Status    string `json:"status"`
	Level     string `json:"level"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	Fix       rwFix  `json:"fix"`
	CheckedAt string `json:"checked_at"`
}

type rwFieldError struct {
	Question string `json:"question"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

type rwCheckOut struct {
	Status      string         `json:"status"`
	FieldErrors []rwFieldError `json:"field_errors"`
	Checks      []rwCheck      `json:"checks"`
}

// rwStepIn is setup.check / setup.apply's input.
type rwStepIn struct {
	Setup   string    `json:"setup"`
	Step    string    `json:"step"`
	Answers rwAnswers `json:"answers,omitempty"`
}

// rwNone is the output type of a call whose output is not read.
type rwNone struct{}

// rwRun runs op name with in (marshalled to JSON), decoding the result
// into out unless out is nil.
func rwRun[I, O any](w *rwWorld, name string, in I, out *O) error {
	w.T.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		w.T.Fatal(err)
	}
	res, err := w.H.RunYes(name, string(b))
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal([]byte(w.H.JSON(res)), out); err != nil {
			w.T.Fatalf("%s output: %v\n%s", name, err, w.H.JSON(res))
		}
	}
	return nil
}

// rwMust is rwRun that fails the test on an op error.
func rwMust[I, O any](w *rwWorld, name string, in I, out *O) {
	w.T.Helper()
	if err := rwRun(w, name, in, out); err != nil {
		w.T.Fatalf("%s %s: %v", name, mustJSON(in), err)
	}
}

func mustJSON[I any](in I) string {
	b, _ := json.Marshal(in)
	return string(b)
}

// Plan is setup.plan for setup (kind solo).
func (w *rwWorld) Plan(setup string) rwPlan {
	w.T.Helper()
	var p rwPlan
	rwMust(w, "setup.plan", map[string]string{"setup": setup, "kind": "solo"}, &p)
	return p
}

// Step is the plan's step id.
func (p rwPlan) Step(id string) (rwStep, bool) {
	for _, s := range p.Steps {
		if s.ID == id {
			return s, true
		}
	}
	return rwStep{}, false
}

// Prefilled is the step's answers the plan filled itself.
func (s rwStep) Prefilled() rwAnswers {
	a := rwAnswers{}
	for _, q := range s.Questions {
		v := strings.TrimSpace(string(q.Value))
		if v == "" || v == "null" || v == `""` || v == "[]" {
			continue
		}
		a[q.ID] = q.Value
	}
	return a
}

// Check is setup.check of one step.
func (w *rwWorld) Check(setup, step string, a rwAnswers) rwCheckOut {
	w.T.Helper()
	var out rwCheckOut
	rwMust(w, "setup.check", rwStepIn{Setup: setup, Step: step, Answers: a}, &out)
	return out
}

// Apply is setup.apply of one step (the op error, if any).
func (w *rwWorld) Apply(setup, step string, a rwAnswers) error {
	w.T.Helper()
	return rwRun[rwStepIn, rwNone](w, "setup.apply", rwStepIn{Setup: setup, Step: step, Answers: a}, nil)
}

// str / list / boolean build one answer.
func str(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func list(l ...string) json.RawMessage {
	if l == nil {
		l = []string{}
	}
	b, _ := json.Marshal(l)
	return b
}

// Config is ~/.lore/config.yaml's text.
func (w *rwWorld) Config() string {
	b, _ := os.ReadFile(filepath.Join(w.Home, ".lore", "config.yaml"))
	return string(b)
}

// WriteConfig replaces ~/.lore/config.yaml and reopens the App.
func (w *rwWorld) WriteConfig(body string) {
	w.T.Helper()
	if err := os.MkdirAll(filepath.Join(w.Home, ".lore"), 0o700); err != nil {
		w.T.Fatal(err)
	}
	w.write(filepath.Join(w.Home, ".lore", "config.yaml"), body, 0o600)
	w.Reopen()
}

// rwPersonalConfig is one finished solo setup for account login.
func rwPersonalConfig(login, email string) string {
	return "setups:\n  - name: personal\n    kind: solo\n    default: true\n" +
		"    identity: {key: \"human:" + email + "\", name: Pat Example, emails: [" + email + "]}\n" +
		"    github: {user: " + login + "}\n"
}

// Calls is every call log (ssh, gh, git) as text, for a failure message:
// which fake ran, as whom, and why it refused.
func (w *rwWorld) Calls() string {
	var b strings.Builder
	for _, tool := range []string{"ssh", "gh", "git"} {
		data, _ := os.ReadFile(w.Log(tool))
		b.WriteString("[" + tool + ".log]\n" + string(data))
	}
	return b.String()
}
