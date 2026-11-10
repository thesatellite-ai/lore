package teamharness

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Environment variables lore reads that the harness sets per actor
// (LORE_TEAM_SPEC.md §1 "Environment variables").
const (
	EnvLoreActor   = "LORE_ACTOR"
	EnvLoreSession = "LORE_SESSION"
	EnvLoreNow     = "LORE_NOW"
	EnvLoreOrg     = "LORE_ORG"
	EnvAutopush    = "LORE_LIVE_AUTOPUSH"
	EnvLoreTrace   = "LORE_TRACE"
)

// nowLayout is how LORE_NOW is written (RFC 3339 in UTC, second precision).
const nowLayout = time.RFC3339

// Actor is one person or one agent session: a HOME (a machine account) plus
// the environment its commands run with. Agent sessions on the same machine
// share their owner's HOME, exactly like tmux sessions on one server.
type Actor struct {
	w     *World
	Name  string // label used in clone dirs and wrapper logs
	Email string
	// GitName overrides Name as git's user.name (SetGitName).
	GitName string
	Home    string
	env     map[string]string
}

// Human creates a person with a fresh HOME whose ~/.gitconfig sets their git
// identity and routes https://github.com/ and git@github.com: to the fake host.
func (w *World) Human(name, email string) *Actor {
	w.T.Helper()
	a := &Actor{w: w, Name: name, Email: email, Home: filepath.Join(w.Root, "homes", name), env: map[string]string{}}
	mustMkdir(w.T, a.Home)
	a.writeGitconfig(false)
	return a
}

// Agent is an agent session on this actor's machine (same HOME), started the
// way `lore agent start` leaves it: LORE_ACTOR=agent:<label>, LORE_SESSION set.
func (a *Actor) Agent(label string) *Actor {
	b := a.copyAs(label)
	b.env[EnvLoreActor] = "agent:" + label
	b.env[EnvLoreSession] = "s-" + label
	return b
}

// Session is the same actor in another session (another tmux pane with the
// same LORE_ACTOR), for the "same actor, two sessions" cases of F7.
func (a *Actor) Session(id string) *Actor {
	b := a.copyAs(a.Name + "-" + id)
	b.env[EnvLoreSession] = id
	return b
}

func (a *Actor) copyAs(name string) *Actor {
	env := make(map[string]string, len(a.env))
	for k, v := range a.env {
		env[k] = v
	}
	return &Actor{w: a.w, Name: name, Email: a.Email, GitName: a.GitName, Home: a.Home, env: env}
}

// SetEnv sets (or with value "" removes) one environment variable for every
// later command of this actor.
func (a *Actor) SetEnv(key, value string) {
	if value == "" {
		delete(a.env, key)
		return
	}
	a.env[key] = value
}

// Env returns one environment variable this actor's commands get ("" when unset).
func (a *Actor) Env(key string) string { return a.env[key] }

// SetNow fixes this actor's clock (LORE_NOW) at t.
func (a *Actor) SetNow(t time.Time) { a.env[EnvLoreNow] = t.UTC().Format(nowLayout) }

// Now is the actor's fixed clock, or the zero time when unset.
func (a *Actor) Now() time.Time {
	v, ok := a.env[EnvLoreNow]
	if !ok {
		return time.Time{}
	}
	t, err := time.Parse(nowLayout, v)
	if err != nil {
		a.w.T.Fatalf("bad %s %q: %v", EnvLoreNow, v, err)
	}
	return t
}

// Advance moves the actor's fixed clock forward by d (SetNow must come first).
func (a *Actor) Advance(d time.Duration) {
	now := a.Now()
	if now.IsZero() {
		a.w.T.Fatal("Advance before SetNow")
	}
	a.SetNow(now.Add(d))
}

// Offline cuts this machine off from the fake GitHub: every fetch and push of
// every session sharing this HOME fails as with no network.
func (a *Actor) Offline() { a.writeGitconfig(true) }

// Online restores the network.
func (a *Actor) Online() { a.writeGitconfig(false) }

// gitName is the git user.name written for this actor: GitName when set,
// else the actor's label. Since an identity read from git shows git's
// user.name (DECISIONS_LOG OWN6-2), one person on two machines needs the
// same GitName or the two machines write two display names for one actor.
func (a *Actor) gitName() string {
	if a.GitName != "" {
		return a.GitName
	}
	return a.Name
}

// SetGitName sets git's user.name on this actor's machine (online gitconfig).
func (a *Actor) SetGitName(name string) {
	a.w.T.Helper()
	a.GitName = name
	a.writeGitconfig(false)
}

func (a *Actor) writeGitconfig(offline bool) {
	a.w.T.Helper()
	target := a.w.hosts + "/" + fakeHost + "/"
	if offline {
		target = filepath.Join(a.w.Root, "no-network") + "/"
	}
	body := fmt.Sprintf(`[user]
	email = %s
	name = %s
[init]
	defaultBranch = main
[pull]
	rebase = false
[protocol "file"]
	allow = always
[url %q]
	insteadOf = https://%s/
	insteadOf = git@%s:
`, a.Email, a.gitName(), target, fakeHost, fakeHost)
	writeFile(a.w.T, filepath.Join(a.Home, ".gitconfig"), body)
}

// WriteConfig writes ~/.lore/config.yaml.
func (a *Actor) WriteConfig(cfg Config) {
	a.w.T.Helper()
	b, err := marshalConfig(cfg)
	if err != nil {
		a.w.T.Fatal(err)
	}
	writeFile(a.w.T, a.LorePath("config.yaml"), string(b))
}

// WriteHomeFile writes a file relative to HOME (e.g. ".lore/identity.toml").
func (a *Actor) WriteHomeFile(rel, body string) {
	a.w.T.Helper()
	writeFile(a.w.T, filepath.Join(a.Home, rel), body)
}

// LorePath is a path inside ~/.lore.
func (a *Actor) LorePath(parts ...string) string {
	return filepath.Join(append([]string{a.Home, ".lore"}, parts...)...)
}

// env returns the full environment for one command.
func (a *Actor) environ(extra []string) []string {
	env := a.w.baseEnv(a.Home, a.Name)
	keys := make([]string, 0, len(a.env))
	for k := range a.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+a.env[k])
	}
	return append(env, extra...)
}

// Lore runs lore in dir (relative paths are under HOME) and returns what the
// user saw; it never fails the test by itself.
func (a *Actor) Lore(dir string, args ...string) Result {
	a.w.T.Helper()
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(a.Home, dir)
	}
	mustMkdir(a.w.T, dir)
	return a.w.run(dir, a.environ(nil), a.w.Bin, args...)
}

// Clone clones remote into <Root>/work/<actor>/<name> over its https URL. An
// empty remote gets a first commit on main (pushed as an admin, so it works
// even when main is protected).
func (a *Actor) Clone(r *Remote, name string) *Clone {
	a.w.T.Helper()
	return a.CloneURL(r, r.URL(), name)
}

// CloneURL is Clone with an explicit URL form (ssh or https).
func (a *Actor) CloneURL(r *Remote, url, name string) *Clone {
	a.w.T.Helper()
	dir := filepath.Join(a.w.Root, "work", a.Name, name)
	mustMkdir(a.w.T, filepath.Dir(dir))
	res := a.w.run(filepath.Dir(dir), a.environ(nil), a.w.realGit, "clone", "-q", url, dir)
	if res.Code != 0 {
		a.w.T.Fatalf("clone %s: %s", url, res.Stderr)
	}
	c := &Clone{A: a, Dir: dir, Remote: r}
	if head := c.TryGit("rev-parse", "--verify", "-q", "HEAD"); head.Code != 0 {
		c.WriteFile("README.md", "# "+strings.TrimPrefix(r.Slug, fakeHost+"/")+"\n")
		c.Git("checkout", "-q", "-b", "main")
		c.CommitAll("init")
		c.AdminPush("-u", "origin", "main")
	}
	return c
}

// OpenClone wraps an existing checkout dir (a second worktree, a copy).
func (a *Actor) OpenClone(r *Remote, dir string) *Clone {
	if _, err := os.Stat(dir); err != nil {
		a.w.T.Fatalf("open clone %s: %v", dir, err)
	}
	return &Clone{A: a, Dir: dir, Remote: r}
}
