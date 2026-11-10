//go:build setupe2e

package setupe2e

// world.go — builds one fake machine for a Shape and starts the real
// `lore setup web` in it.
//
//	Build(dir, shape)        <dir>/home (HOME), <dir>/fakebin, <dir>/remote, tsv data
//	  └─ Start(port, lore)   fake GitHub API + `lore setup web --no-open --port <port>`
//	       cwd = the shape's start folder inside HOME, env -i style:
//	       PATH=fakebin:<lore dir>:/usr/bin:/bin, HOME, LORE_GITHUB_API, …
//	  └─ Info()              the one JSON line setupworld prints
//
// Accounts (fixed, in gh's order): octo-personal (user:email scope) and octo-work
// (no user:email, so its emails are unreadable like a real token without
// that scope). Keys are ssh-keygen'd once per process (keyCache) and
// copied, so 1000+ worlds do not pay for key generation.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Account is one GitHub login of the world.
type Account struct {
	Login  string   `json:"login"`
	Emails []string `json:"emails"`
	// Key is the private key (~ form) that authenticates as this login
	// over ssh; "" when the shape has none for it.
	Key string `json:"key"`

	token       string
	scopes      string
	id          int
	displayName string
}

// accountSeeds are the world's possible logins, in gh order.
var accountSeeds = []Account{
	{Login: "octo-personal", Emails: []string{"person@example.com", "person.alt@example.com"}, scopes: "'gist', 'read:org', 'repo', 'user:email'", id: 1001, displayName: "Pat Example"},
	{Login: "octo-work", Emails: []string{"work@example.org"}, scopes: "'gist', 'read:org', 'repo'", id: 2002, displayName: "Pat Example (work)"},
}

// Person names written into ~/.gitconfig.
const (
	gitName       = "Pat Example"
	fallbackEmail = "dev@example.com"
	// SetupName is the name the existing setup has in the setups, removed,
	// disabled and trashed homes.
	ExistingSetup = "personal"
	// startPlainRel etc. are the start folders inside HOME.
	startPlainRel = "Downloads/lore1"
	startGitRel   = "code/app"
	startLoreRel  = "code/notes"
	// binSubdir is where setupworld builds lore when no --lore is given.
	binSubdir = "bin"
)

// World is one built fake machine.
type World struct {
	Dir      string
	Home     string
	Shape    Shape
	Accounts []Account

	api *fakeAPI
	cmd *exec.Cmd
	// runEnv is the server's environment once Start ran (Info.Env).
	runEnv []string
	// invite is the line Invite made (Info.Invite).
	invite string
	url    string
	output *syncBuffer
	// lore is the binary Start ran (RunLore uses it).
	lore string
	// hangProfile is a login whose GitHub /user read never answers (set
	// through HangProfile before Start): discovery cannot read that
	// account's profile in time.
	hangProfile string
}

// HangProfile makes the fake GitHub API never answer login's profile read
// (/user), so lore's discovery gives up at its own deadline. Call it before
// Start; a page suite uses it to check what the page says about an account
// whose profile could not be read.
func (w *World) HangProfile(login string) { w.hangProfile = login }

// Info is the JSON line setupworld prints (contract "World contract").
type Info struct {
	URL      string    `json:"url"`
	Home     string    `json:"home"`
	Config   string    `json:"config"`
	Logs     Logs      `json:"logs"`
	Accounts []Account `json:"accounts"`
	Shape    Shape     `json:"shape"`
	// Start is the folder lore setup web runs in (extra, for consumers).
	Start string `json:"start"`
	// Env is the environment the server runs with (PATH with the fake
	// tools, HOME, LORE_GITHUB_API), empty before Start, so a consumer
	// runs `lore doctor --all --json` on the same machine the page sees.
	Env []string `json:"env,omitempty"`
	// Invite is an invite line from the hub team (setupworld --invite;
	// invite.go), "" when none was made.
	Invite string `json:"invite,omitempty"`
}

// Logs are the fake tools' log paths.
type Logs struct {
	GH  string `json:"gh"`
	SSH string `json:"ssh"`
	Git string `json:"git"`
}

// Info returns the world's description; URL is empty before Start.
func (w *World) Info() Info {
	return Info{URL: w.url, Home: w.Home, Config: filepath.Join(w.Home, ".lore", "config.yaml"),
		Logs:     Logs{GH: filepath.Join(w.Dir, "gh.log"), SSH: filepath.Join(w.Dir, "ssh.log"), Git: filepath.Join(w.Dir, "git.log")},
		Accounts: w.Accounts, Shape: w.Shape, Start: w.StartDir(), Env: w.runEnv, Invite: w.invite}
}

// StartDir is the folder lore setup web starts in.
func (w *World) StartDir() string {
	switch w.Shape.Start {
	case StartLoreRepo:
		return filepath.Join(w.Home, startLoreRel)
	case StartGitRepo:
		return filepath.Join(w.Home, startGitRel)
	}
	return filepath.Join(w.Home, startPlainRel)
}

// URL is the setup link once started.
func (w *World) URL() string { return w.url }

// Build makes the fake machine under dir (which must be empty or
// missing). lore is the binary used to put ~/.lore in the removed /
// disabled / trashed states and to init the lore repo start folder.
func Build(ctx context.Context, dir string, s Shape, lore string) (*World, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("world dir: %w", err)
	}
	ents, _ := os.ReadDir(dir) // a missing dir is fine
	for _, e := range ents {
		if e.Name() != binSubdir { // setupworld may have built lore into <dir>/bin
			return nil, fmt.Errorf("world dir %s is not empty", dir)
		}
	}
	w := &World{Dir: dir, Home: filepath.Join(dir, "home"), Shape: s}
	steps := []func(context.Context) error{w.mkdirs, w.writeAccounts, w.writeSSH, w.writeGitconfig, w.writeRemotes, w.writeStart}
	for _, f := range steps {
		if err := f(ctx); err != nil {
			return nil, err
		}
	}
	if err := writeFakebin(dir); err != nil {
		return nil, err
	}
	if err := w.writeLoreHome(ctx, lore); err != nil {
		return nil, err
	}
	// Building ran git and lore; the logs start empty for the run itself.
	for _, l := range []string{"gh.log", "ssh.log", "git.log"} {
		if err := os.WriteFile(filepath.Join(dir, l), nil, 0o644); err != nil {
			return nil, fmt.Errorf("reset log: %w", err)
		}
	}
	return w, nil
}

func (w *World) mkdirs(context.Context) error {
	for _, d := range []string{w.Home, filepath.Join(w.Home, startPlainRel), filepath.Join(w.Home, ".config"), filepath.Join(w.Dir, "remote")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}
	}
	return os.WriteFile(filepath.Join(w.Home, startPlainRel, "README.txt"), []byte("release notes placeholder\n"), 0o644)
}

// writeAccounts writes accounts.tsv (gh's logins) with fresh placeholder tokens.
func (w *World) writeAccounts(context.Context) error {
	var b strings.Builder
	for i := 0; i < w.Shape.GHAccounts; i++ {
		a := accountSeeds[i]
		a.Emails = append([]string(nil), a.Emails...)
		a.token = "placeholder-" + a.Login + "-" + randHex(8)
		active := (i == 0 && w.Shape.GHActive == ActiveFirst) || (i == 1 && w.Shape.GHActive == ActiveSecond)
		flag := "0"
		if active {
			flag = "1"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", a.Login, a.token, flag, a.scopes)
		w.Accounts = append(w.Accounts, a)
	}
	if err := os.WriteFile(filepath.Join(w.Dir, "accounts.tsv"), []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	return w.writeHostsYML()
}

// writeHostsYML writes gh's ~/.config/gh/hosts.yml (what lore reads for
// gh's logins without running gh); none without accounts, like a gh that
// never logged in. The fake gh's auth switch rewrites it.
func (w *World) writeHostsYML() error {
	if len(w.Accounts) == 0 {
		return nil
	}
	active := w.Accounts[0].Login
	if w.Shape.GHActive == ActiveSecond {
		active = w.Accounts[1].Login
	}
	var b strings.Builder
	b.WriteString("github.com:\n    users:\n")
	for _, a := range w.Accounts {
		fmt.Fprintf(&b, "        %s:\n", a.Login)
	}
	fmt.Fprintf(&b, "    git_protocol: ssh\n    user: %s\n", active)
	d := filepath.Join(w.Home, ".config", "gh")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return fmt.Errorf("gh config: %w", err)
	}
	return os.WriteFile(filepath.Join(d, "hosts.yml"), []byte(b.String()), 0o600)
}

// keyOwner is who a key file authenticates as; "" = nobody (denied).
func (w *World) keyOwner(i int) string {
	if i < len(w.Accounts) {
		return w.Accounts[i].Login
	}
	return ""
}

// writeSSH builds ~/.ssh for the shape and keys.tsv.
func (w *World) writeSSH(context.Context) error {
	if w.Shape.SSH == SSHNone {
		return os.WriteFile(filepath.Join(w.Dir, "keys.tsv"), nil, 0o644)
	}
	sshDir := filepath.Join(w.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return fmt.Errorf("ssh dir: %w", err)
	}
	type key struct{ file, owner string }
	var keys []key
	var config string
	switch w.Shape.SSH {
	case SSHDefaultOnly:
		keys = []key{{"id_ed25519", w.keyOwner(0)}}
	case SSHSeveral:
		keys = []key{{"id_personal", w.keyOwner(0)}, {"id_work", w.keyOwner(1)}, {"id_old", ""}}
		config = "Host github.com\n\tIdentityFile ~/.ssh/id_personal\n" +
			"Host github-personal\n\tHostName github.com\n\tUser git\n\tIdentityFile ~/.ssh/id_personal\n" +
			"Host github-work\n\tHostName github.com\n\tUser git\n\tIdentityFile ~/.ssh/id_work\n" +
			"Host build-box\n\tHostName 10.0.0.9\n\tUser ci\n"
	case SSHMissingKey:
		keys = []key{{"id_ed25519", w.keyOwner(0)}}
		config = "Host github-gone\n\tHostName github.com\n\tUser git\n\tIdentityFile ~/.ssh/id_gone\n"
	}
	var tsv strings.Builder
	for _, k := range keys {
		if err := copyKey(filepath.Join(sshDir, k.file), w.Shape.Keys == KeysWithPub); err != nil {
			return err
		}
		if k.owner != "" {
			fmt.Fprintf(&tsv, "%s\t%s\n", k.file, k.owner)
			for i := range w.Accounts {
				if w.Accounts[i].Login == k.owner && w.Accounts[i].Key == "" {
					w.Accounts[i].Key = "~/.ssh/" + k.file
				}
			}
		}
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(config), 0o600); err != nil {
			return fmt.Errorf("ssh config: %w", err)
		}
	}
	return os.WriteFile(filepath.Join(w.Dir, "keys.tsv"), []byte(tsv.String()), 0o644)
}

// writeGitconfig writes ~/.gitconfig: the user plus include_if identities.
func (w *World) writeGitconfig(context.Context) error {
	email := fallbackEmail
	if len(w.Accounts) > 0 {
		email = w.Accounts[0].Emails[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[user]\n\tname = %s\n\temail = %s\n[init]\n\tdefaultBranch = main\n", gitName, email)
	for i := 0; i < w.Shape.IncludeIf; i++ {
		rel := fmt.Sprintf("work%d", i+1)
		idEmail := fmt.Sprintf("extra%d@example.org", i+1)
		if i+1 < len(w.Accounts) {
			idEmail = w.Accounts[i+1].Emails[0]
		}
		if err := os.MkdirAll(filepath.Join(w.Home, rel), 0o755); err != nil {
			return fmt.Errorf("includeIf dir: %w", err)
		}
		if err := os.WriteFile(filepath.Join(w.Home, rel, ".gitconfig-identity"), fmt.Appendf(nil, "[user]\n\tname = %s\n\temail = %s\n", gitName, idEmail), 0o644); err != nil {
			return fmt.Errorf("includeIf identity: %w", err)
		}
		fmt.Fprintf(&b, "[includeIf \"gitdir:~/%s/\"]\n\tpath = ~/%s/.gitconfig-identity\n", rel, rel)
	}
	return os.WriteFile(filepath.Join(w.Home, ".gitconfig"), []byte(b.String()), 0o644)
}

// writeRemotes makes the bare repos GitHub has: <login>/notes for every
// account, and <owner>/lore-me when ~/.lore names it as a store (the v0117
// one holds a v0.1.17 store, see seedOldStore).
func (w *World) writeRemotes(ctx context.Context) error {
	var repos []string
	for _, a := range w.Accounts {
		repos = append(repos, a.Login+"/notes")
	}
	if owner := w.StoreOwner(); owner != "" {
		repos = append(repos, owner+"/"+personalRepo)
	}
	for _, r := range repos {
		if err := w.git(ctx, "", "init", "-q", "--bare", "-b", "main", filepath.Join(w.Dir, "remote", r+".git")); err != nil {
			return err
		}
	}
	if w.Shape.LoreHome == HomeV0117 && w.StoreOwner() != "" {
		return w.seedOldStore(ctx, filepath.Join(w.Dir, "remote", w.StoreOwner(), personalRepo+".git"))
	}
	return nil
}

// personalRepo is the GitHub repo name of the personal store ~/.lore names.
const personalRepo = "lore-me"

// StoreOwner is the login whose <login>/lore-me ~/.lore names as its
// personal store; "" when it names none (no account, empty or invalid
// home). The v0117 store belongs to gh's ACTIVE login: with the second
// login active, ssh's plain github.com host still authenticates as the
// first login (its key is id_ed25519 / Host github.com), so a v0.1.17
// machine whose old store url is git@github.com:<second>/lore-me.git is
// the wrong-key case the startup fetch judge needs (judgeStartFetch).
func (w *World) StoreOwner() string {
	if len(w.Accounts) == 0 || w.Shape.LoreHome == HomeEmpty || w.Shape.LoreHome == HomeInvalidYAML {
		return ""
	}
	if w.Shape.LoreHome == HomeV0117 && w.Shape.GHActive == ActiveSecond {
		return w.Accounts[1].Login
	}
	return w.Accounts[0].Login
}

// oldMarker is a v0.1.17 store marker (rowfs.MarkerFile, format 1: no id).
const oldMarker = "lore_store: 1\ncreated: 2025-06-01\n"

// seedOldStore pushes one commit holding a v0.1.17 store root into bare,
// so the startup fetch of a v0117 home has a real old store to read.
func (w *World) seedOldStore(ctx context.Context, bare string) error {
	work := filepath.Join(w.Dir, "seed-old-store")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return fmt.Errorf("seed old store: %w", err)
	}
	if err := os.WriteFile(filepath.Join(work, "lore-store.yaml"), []byte(oldMarker), 0o644); err != nil {
		return fmt.Errorf("seed old store: %w", err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "lore-store.yaml"},
		{"-c", "user.email=" + fallbackEmail, "commit", "-q", "-m", "lore store (v0.1.17)"},
		{"push", "-q", bare, "main"},
	} {
		if err := w.git(ctx, work, args...); err != nil {
			return err
		}
	}
	return os.RemoveAll(work)
}

// writeStart makes the start folders (all three exist in every world).
func (w *World) writeStart(ctx context.Context) error {
	for _, rel := range []string{startGitRel, startLoreRel} {
		d := filepath.Join(w.Home, rel)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("start dir: %w", err)
		}
		if err := w.git(ctx, d, "init", "-q", "-b", "main"); err != nil {
			return err
		}
		owner := "octo-personal"
		if len(w.Accounts) > 0 {
			owner = w.Accounts[0].Login
		}
		if err := w.git(ctx, d, "remote", "add", "origin", "git@github.com:"+owner+"/"+filepath.Base(rel)+".git"); err != nil {
			return err
		}
	}
	return nil
}

// writeLoreHome puts ~/.lore in the shape's state; lore is run in the
// world (fake PATH and HOME) for the states only the binary can write.
func (w *World) writeLoreHome(ctx context.Context, lore string) error {
	loreDir := filepath.Join(w.Home, ".lore")
	email, handle := fallbackEmail, "dev"
	if len(w.Accounts) > 0 {
		email, handle = w.Accounts[0].Emails[0], w.Accounts[0].Login
	}
	identity := fmt.Sprintf("identity:\n    key: human:%s\n    name: %s\n    handle: %s\n", email, gitName, handle)
	write := func(name, body string) error {
		if err := os.MkdirAll(loreDir, 0o755); err != nil {
			return fmt.Errorf("lore home: %w", err)
		}
		return os.WriteFile(filepath.Join(loreDir, name), []byte(body), 0o644)
	}
	if w.Shape.Start == StartLoreRepo {
		// The repo is made a lore repo on a bare machine first (lore init
		// reads config.yaml: a broken one, or a store it cannot reach,
		// refuses), then ~/.lore is wiped and put in the shape's state.
		if out, err := w.runLore(ctx, lore, filepath.Join(w.Home, startLoreRel), "init", "--name", "notes"); err != nil {
			return fmt.Errorf("lore init: %w\n%s", err, out)
		}
		if err := os.RemoveAll(loreDir); err != nil {
			return fmt.Errorf("lore home: %w", err)
		}
	}
	switch w.Shape.LoreHome {
	case HomeEmpty:
	case HomeInvalidYAML:
		if err := write("config.yaml", identity+"setups:\n    - name: personal\n      kind: solo\n     folders: [~/code/**\n\tdefault: true\n"); err != nil {
			return err
		}
	case HomeV0117:
		store := "local" // v0.1.17's value for "no remote store"
		if owner := w.StoreOwner(); owner != "" {
			store = "git@github.com:" + owner + "/" + personalRepo + ".git"
		}
		if err := write("config.yaml", identity+"personal:\n    store: "+store+"\n"); err != nil {
			return err
		}
		if err := write("identity.yaml.migrated", fmt.Sprintf("stable_key: human:%s\ndisplay_name: %s\n", email, gitName)); err != nil {
			return err
		}
		if err := write("identity.toml.migrated", fmt.Sprintf("stable_key = \"human:%s\"\ndisplay_name = \"%s\"\n", email, gitName)); err != nil {
			return err
		}
	default: // setups, removed, disabled, trashed: one saved setup first
		var b strings.Builder
		b.WriteString(identity)
		fmt.Fprintf(&b, "setups:\n    - name: %s\n      kind: solo\n      default: true\n      folders:\n        - ~/code/**\n", ExistingSetup)
		fmt.Fprintf(&b, "      identity:\n        key: human:%s\n        name: %s\n        handle: %s\n        emails:\n            - %s\n", email, gitName, handle, email)
		if len(w.Accounts) > 0 {
			a := w.Accounts[0]
			fmt.Fprintf(&b, "      github:\n        user: %s\n        host_alias: github.com\n        gh_account: %s\n", a.Login, a.Login)
			if a.Key != "" {
				fmt.Fprintf(&b, "        ssh_key: %s\n", a.Key)
			}
			fmt.Fprintf(&b, "      personal_store: git@github.com:%s/lore-me.git\n", a.Login)
		} else {
			b.WriteString("      personal_store: none\n")
		}
		if err := write("config.yaml", b.String()); err != nil {
			return err
		}
		var cmds [][]string
		switch w.Shape.LoreHome {
		case HomeDisabled:
			cmds = [][]string{{"setup", "disable", ExistingSetup}}
		case HomeTrashed:
			cmds = [][]string{{"setup", "remove", ExistingSetup}}
		case HomeRemoved:
			cmds = [][]string{{"setup", "remove", ExistingSetup}, {"setup", "trash", "purge", ExistingSetup, "--confirm"}}
		}
		for _, c := range cmds {
			if out, err := w.runLore(ctx, lore, w.Home, c...); err != nil {
				return fmt.Errorf("lore %s: %w\n%s", strings.Join(c, " "), err, out)
			}
		}
	}
	return nil
}

// env is the world's process environment (env -i style).
func (w *World) env(loreDir, apiURL string) []string {
	e := []string{
		"PATH=" + filepath.Join(w.Dir, "fakebin") + ":" + loreDir + ":/usr/bin:/bin",
		"HOME=" + w.Home, "TERM=dumb", "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1",
		"LORE_NO_UPDATE_CHECK=1", "LORE_HUMAN=1", "TMPDIR=" + os.TempDir(),
	}
	if apiURL != "" {
		e = append(e, "LORE_GITHUB_API="+apiURL)
	}
	return e
}

func (w *World) runLore(ctx context.Context, lore, cwd string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	c := exec.CommandContext(cctx, lore, args...)
	c.Dir = cwd
	c.Env = w.env(filepath.Dir(lore), "")
	out, err := c.CombinedOutput()
	return string(out), err
}

func (w *World) git(ctx context.Context, cwd string, args ...string) error {
	c := exec.CommandContext(ctx, "/usr/bin/git", args...)
	c.Dir = cwd
	c.Env = append(w.env("/nonexistent", ""), "GIT_AUTHOR_NAME=x", "GIT_COMMITTER_NAME=x")
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

var reSetupURL = regexp.MustCompile(`http://127\.0\.0\.1:\d+/setup/#t=[0-9a-f]+`)

// Start launches the fake API and `lore setup web` and waits until the
// server prints its link. port 0 is passed through: lore binds a free
// port itself (setupweb.Listen) and the link names it. Picking a port
// here and retrying when another process took it first was a race with a
// side effect: `lore setup web` then updated the schema (fetching a
// v0.1.17 store over ssh) BEFORE it listened, so the aborted first process
// had already made the store's first ssh connection and the retry made a
// second one, failing the one-connection rule of judgeStartFetch under
// parallel load. lore now binds first (porttaken_test.go), and port 0
// still leaves no race to retry.
func (w *World) Start(ctx context.Context, port int, lore string) error {
	api, err := startFakeAPI(w)
	if err != nil {
		return err
	}
	w.api = api
	w.lore = lore
	c := exec.Command(lore, "setup", "web", "--no-open", "--port", fmt.Sprint(port))
	c.Dir = w.StartDir()
	c.Env = w.env(filepath.Dir(lore), api.URL())
	w.runEnv = c.Env
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	w.output = &syncBuffer{}
	pr, pw := io.Pipe()
	c.Stdout, c.Stderr = pw, pw
	if err := c.Start(); err != nil {
		api.Close()
		return fmt.Errorf("start lore setup web: %w", err)
	}
	w.cmd = c
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sent := false
		for sc.Scan() {
			w.output.WriteLine(sc.Text())
			if m := reSetupURL.FindString(sc.Text()); m != "" && !sent {
				found <- m
				sent = true
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- c.Wait(); _ = pw.Close() }()
	select {
	case u := <-found:
		w.url = u
		go func() { <-exited }() // reaped on Stop
		return nil
	case err := <-exited:
		api.Close()
		return fmt.Errorf("lore setup web exited before printing its link (%v):\n%s", err, w.output.String())
	case <-time.After(60 * time.Second):
		w.Stop()
		return fmt.Errorf("lore setup web printed no link in 60s:\n%s", w.output.String())
	case <-ctx.Done():
		w.Stop()
		return ctx.Err()
	}
}

// RunLore runs the binary Start ran, in HOME (no repo, so no repo of the
// start folder fetches its own remote), with the server's environment
// (the fake gh, ssh and git on PATH), as the person would in a terminal
// of this machine after the page saved.
func (w *World) RunLore(ctx context.Context, args ...string) (string, error) {
	if w.lore == "" {
		return "", fmt.Errorf("world not started: no lore binary to run")
	}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	c := exec.CommandContext(cctx, w.lore, args...)
	c.Dir = w.Home
	c.Env = w.runEnv
	out, err := c.CombinedOutput()
	return string(out), err
}

// Output is what lore setup web printed so far.
func (w *World) Output() string {
	if w.output == nil {
		return ""
	}
	return w.output.String()
}

// Stop kills the server (its process group) and the fake API.
func (w *World) Stop() {
	if w.cmd != nil && w.cmd.Process != nil {
		_ = syscall.Kill(-w.cmd.Process.Pid, syscall.SIGTERM)
		time.Sleep(50 * time.Millisecond)
		_ = syscall.Kill(-w.cmd.Process.Pid, syscall.SIGKILL)
	}
	if w.api != nil {
		w.api.Close()
	}
}

// Origin is http://127.0.0.1:<port> of the started server.
func (w *World) Origin() string {
	u, err := url.Parse(w.url)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Token is the one-time setup token of the link.
func (w *World) Token() string {
	_, t, _ := strings.Cut(w.url, "#t=")
	return t
}

// WriteInfo writes the world's Info as JSON to path.
func (w *World) WriteInfo(path string) error {
	b, err := json.Marshal(w.Info())
	if err != nil {
		return fmt.Errorf("world info: %w", err)
	}
	return os.WriteFile(path, b, 0o644)
}

// ---------- keys ----------

var (
	keyOnce sync.Once
	keyDir  string
	keyErr  error
)

// copyKey copies the process's one generated ed25519 pair to path (and
// path.pub when withPub). The pair is a throwaway made by ssh-keygen at
// run time in a temp dir.
func copyKey(path string, withPub bool) error {
	keyOnce.Do(func() {
		keyDir, keyErr = os.MkdirTemp("", "setupe2e-key-")
		if keyErr != nil {
			return
		}
		out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "setupe2e-placeholder", "-f", filepath.Join(keyDir, "k")).CombinedOutput()
		if err != nil {
			keyErr = fmt.Errorf("ssh-keygen: %w\n%s", err, out)
		}
	})
	if keyErr != nil {
		return keyErr
	}
	for src, dst := range map[string]string{"k": path, "k.pub": path + ".pub"} {
		if src == "k.pub" && !withPub {
			continue
		}
		b, err := os.ReadFile(filepath.Join(keyDir, src))
		if err != nil {
			return fmt.Errorf("key: %w", err)
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return fmt.Errorf("key: %w", err)
		}
	}
	return nil
}

// CleanupKeys removes the process's generated key pair (TestMain and
// setupworld call it on exit; worlds hold their own copies).
func CleanupKeys() {
	if keyDir != "" {
		_ = os.RemoveAll(keyDir) // best effort: a temp dir
	}
}

// ---------- helpers ----------

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(errors.New("crypto/rand failed")) // never happens on Linux
	}
	return hex.EncodeToString(b)
}

// syncBuffer collects the server's output lines.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) WriteLine(l string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.WriteString(l)
	s.b.WriteByte('\n')
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
