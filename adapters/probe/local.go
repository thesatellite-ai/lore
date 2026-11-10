package probe

// local.go — the detections setup.plan may run (ONB 2.2 prefills): files
// read and PATH looked up, never a program that reaches the network or
// starts a slow runtime.
//
//	setup.plan ─▶ FindSecrets (LookPath only)      not DetectSecrets (runs every "--version")
//	           ─▶ GHLogins    (gh's hosts.yml)     not GHAuthStatus  (gh calls api.github.com per login)
//	           ─▶ ssh config + key files           not SSHAuth       (ssh -T per alias)
//
// Why: setup.plan answered in 4.8 s on a real Mac and longer from the
// page, because its prefills ran `gcloud --version`, `az version`,
// `aws --version`, `gopass version` one after another (seconds each,
// some check for updates online), plus `ssh -T` per GitHub alias twice
// and `gh auth status`. Plan only describes steps and fills answers; the
// network checks run when a step is checked (setup.check).

import (
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// gh's config locations (gh's own lookup order: GH_CONFIG_DIR, then
// XDG_CONFIG_HOME/gh, then ~/.config/gh).
const (
	envGHConfigDir    = "GH_CONFIG_DIR"
	envXDGConfigHome  = "XDG_CONFIG_HOME"
	ghConfigSubdir    = "gh"
	ghDefaultConfig   = ".config"
	ghHostsFile       = "hosts.yml"
	detailOnPATH      = "on PATH (version not asked)"
	detailGHConfigNot = "gh has no hosts.yml"
)

// getenv reads key from the Prober's environment (Options.Env, else the
// process's).
func (p *Prober) getenv(key string) string {
	if p.o.Env == nil {
		return os.Getenv(key)
	}
	v := ""
	prefix := key + "="
	for _, kv := range p.o.Env {
		if len(kv) > len(prefix) && kv[:len(prefix)] == prefix {
			v = kv[len(prefix):] // a later entry wins, as in env()
		}
	}
	return v
}

// FindSecrets is DetectSecrets without running anything: each secrets
// manager's program looked up on PATH. Answers and Version stay empty
// (the secrets step's check runs the version call).
func (p *Prober) FindSecrets() []SecretsDetection {
	out := make([]SecretsDetection, 0, len(secretsHints))
	for _, h := range secretsHints {
		d := SecretsDetection{Manager: h.manager, Program: h.bin}
		if exe, ok := p.LookPath(h.bin); ok {
			d.Path = exe
			d.Outcome = Outcome{Class: ClassOK, Detail: detailOnPATH}
		} else {
			d.Outcome = Outcome{Class: ClassToolMissing, Detail: h.bin + " not found on PATH"}
		}
		out = append(out, d)
	}
	return out
}

// ghHostsPath is gh's hosts.yml for this Prober's HOME and environment.
func (p *Prober) ghHostsPath() string {
	if d := p.getenv(envGHConfigDir); d != "" {
		return filepath.Join(d, ghHostsFile)
	}
	if d := p.getenv(envXDGConfigHome); d != "" {
		return filepath.Join(d, ghConfigSubdir, ghHostsFile)
	}
	return filepath.Join(p.o.Home, ghDefaultConfig, ghConfigSubdir, ghHostsFile)
}

// ghHost is one host block of hosts.yml: the logins (gh 2.40+ "users")
// and the active one ("user", also the only login in the older form).
// Only names are decoded; the token fields are never read into a field.
type ghHost struct {
	User  string               `yaml:"user"`
	Users map[string]yaml.Node `yaml:"users"`
}

// GHLogins reads gh's logins for github.com from hosts.yml: no program
// runs and nothing reaches the network, so a login whose token expired
// still shows (OK true here means "configured"; GHAuthStatus is the
// check that asks GitHub). Classes: ok (Accounts set), not_logged_in
// (no file or no github.com block), failed (the file does not parse).
func (p *Prober) GHLogins() GHAuthResult {
	b, err := os.ReadFile(p.ghHostsPath())
	if err != nil {
		return GHAuthResult{Outcome: Outcome{Class: ClassNotLoggedIn, Detail: detailGHConfigNot}}
	}
	var hosts map[string]ghHost
	if err := yaml.Unmarshal(b, &hosts); err != nil {
		return GHAuthResult{Outcome: Outcome{Class: ClassFailed, Detail: "gh hosts.yml does not parse"}}
	}
	h, ok := hosts[GitHubHost]
	if !ok {
		return GHAuthResult{Outcome: Outcome{Class: ClassNotLoggedIn, Detail: "gh has no login for " + GitHubHost}}
	}
	names := make([]string, 0, len(h.Users)+1)
	for u := range h.Users {
		names = append(names, u)
	}
	if h.User != "" && h.Users[h.User].Kind == 0 {
		names = append(names, h.User) // older one-login form
	}
	sort.Strings(names)
	out := GHAuthResult{Active: h.User, Outcome: Outcome{Class: ClassOK}}
	for _, u := range names {
		out.Accounts = append(out.Accounts, GHAccount{User: u, Active: u == h.User, OK: true})
	}
	if len(out.Accounts) == 0 {
		out.Outcome = Outcome{Class: ClassNotLoggedIn, Detail: "gh has no login for " + GitHubHost}
	}
	return out
}

// GHHostsPath is gh's hosts.yml for this Prober (the file GHLogins reads),
// for callers that key a cache on it.
func (p *Prober) GHHostsPath() string { return p.ghHostsPath() }
