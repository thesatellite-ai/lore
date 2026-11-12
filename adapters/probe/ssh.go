package probe

import (
	"bufio"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ssh programs and files.
const (
	sshBin       = "ssh"
	sshKeygenBin = "ssh-keygen"
	sshDir       = ".ssh"
	sshConfig    = "config"
	pubSuffix    = ".pub"
	// maxIncludeDepth bounds nested Include directives (ssh itself stops at 16).
	maxIncludeDepth = 8
	// maxPubKeySize: a public key file is one line; anything larger is not one.
	maxPubKeySize = 16 << 10
)

// SSHAuthResult is the github.ssh_auth / github.ssh_user probe (ONB 4.3).
type SSHAuthResult struct {
	Outcome
	// Host is the host ssh connected to (the alias, else github.com).
	Host string `json:"host"`
	// User is the account the host greeted ("Hi <user>!"); "" unless ok.
	User string `json:"user,omitempty"`
}

// Greetings of git hosts on ssh -T (exit 1 is normal for GitHub: no shell).
var reGreeting = []*regexp.Regexp{
	regexp.MustCompile(`Hi ([A-Za-z0-9][A-Za-z0-9-]*)! You've successfully authenticated`),
	regexp.MustCompile(`Welcome to GitLab, @([A-Za-z0-9_.-]+)!`),
	regexp.MustCompile(`logged in as ([A-Za-z0-9_.-]+)\.`), // Bitbucket
}

// reDeployKey is GitHub's greeting for a deploy key: "Hi <owner>/<repo>!"
// (the key belongs to one repo, so no account is greeted).
var reDeployKey = regexp.MustCompile(`Hi ([A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+)! You've successfully authenticated`)

// SSHAuth runs ssh -T -o BatchMode=yes -o ConnectTimeout=10 git@<host>,
// host being route's alias or github.com (with the -i key fallback when no
// alias). Classes: ok (User set), auth_denied, host_key, unreachable,
// timeout, tool_missing, bad_input, failed.
func (p *Prober) SSHAuth(ctx context.Context, route Route) SSHAuthResult {
	m := remoteMemoFrom(ctx)
	if m == nil {
		return p.sshAuthOnce(ctx, route)
	}
	// One ssh -T per route in a run that shares remote answers (a doctor
	// run, a setup step's checks; snapshot.go).
	start := time.Now()
	r, shared, ok := share(ctx, &m.mu, m.sshAuth, route, "ssh -T "+route.sshHost(),
		func() SSHAuthResult { return p.sshAuthOnce(ctx, route) })
	if !ok {
		return SSHAuthResult{Outcome: timedOut(start), Host: route.sshHost()}
	}
	if shared {
		r.Took = time.Since(start) // this check's own wait, not the login's
	}
	return r
}

// sshAuthOnce is SSHAuth without the run's memo: one ssh -T.
func (p *Prober) sshAuthOnce(ctx context.Context, route Route) SSHAuthResult {
	start := time.Now()
	host := route.sshHost()
	if err := route.Validate(); err != nil {
		return SSHAuthResult{Outcome: Outcome{Class: ClassBadInput, Detail: err.Error()}, Host: host}
	}
	cctx, done, ok := p.slot(ctx, p.o.SSHTimeout)
	defer done()
	if !ok {
		return SSHAuthResult{Outcome: timedOut(start), Host: host}
	}
	return p.sshAuthRun(cctx, route, start)
}

// SSHAuthWithin is SSHAuth with its own run time limit: the wait for a
// limiter slot is bounded by ctx only, and the ssh run itself by timeout,
// so a batch of probes queued behind the limiter does not time out while
// waiting (the setup page's "logs in as" table and "Test all keys").
func (p *Prober) SSHAuthWithin(ctx context.Context, route Route, timeout time.Duration) SSHAuthResult {
	start := time.Now()
	host := route.sshHost()
	if err := route.Validate(); err != nil {
		return SSHAuthResult{Outcome: Outcome{Class: ClassBadInput, Detail: err.Error()}, Host: host}
	}
	if err := p.o.Limiter.acquire(ctx); err != nil {
		return SSHAuthResult{Outcome: timedOut(start), Host: host}
	}
	defer p.o.Limiter.release()
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return p.sshAuthRun(cctx, route, start)
}

// sshAuthRun is one ssh -T under cctx (slot already taken) and its classification.
func (p *Prober) sshAuthRun(cctx context.Context, route Route, start time.Time) SSHAuthResult {
	host := route.sshHost()
	args := append([]string{"-T"}, route.sshOptions(p.o.Home)...)
	args = append(args, sshUser+"@"+host)
	r := p.run(cctx, cmdSpec{bin: sshBin, args: args, env: []string{envLCAll + "=C"}})
	if r.timedOut || r.missing || r.startErr {
		o := failure(r, sshBin, classifyRemote)
		o.Took = time.Since(start)
		return SSHAuthResult{Outcome: o, Host: host}
	}
	text := r.stderr + "\n" + r.stdout
	for _, re := range reGreeting {
		if m := re.FindStringSubmatch(text); m != nil {
			return SSHAuthResult{Outcome: Outcome{Class: ClassOK, Took: time.Since(start)}, Host: host, User: m[1]}
		}
	}
	if m := reDeployKey.FindStringSubmatch(text); m != nil {
		// A deploy key answers for one repo, not an account: the key
		// works, so "no answer" was wrong (realmac m7), but it logs in as
		// nobody lore could pick, so it stays not ok, with the repo named.
		return SSHAuthResult{Outcome: Outcome{Class: ClassFailed, Detail: "deploy key for " + m[1] + ": it reaches that one repo, not an account", Took: time.Since(start)}, Host: host}
	}
	o := Outcome{Class: classifyRemote(r.stderr), Detail: Sanitize(firstLine(r.stderr)), Took: time.Since(start)}
	if o.Class == ClassOK || o.Class == ClassNoWrite || o.Class == ClassNoAccess {
		o.Class = ClassFailed // no greeting: never "ok" without a user
	}
	return SSHAuthResult{Outcome: o, Host: host}
}

// SSHHost is one Host block of ~/.ssh/config (ONB 4.3).
type SSHHost struct {
	// Patterns are the Host line's patterns, as written ("github-acme", "*").
	Patterns []string `json:"patterns"`
	// HostName, User, IdentityFile and IdentitiesOnly as set in this block.
	HostName       string   `json:"host_name,omitempty"`
	User           string   `json:"user,omitempty"`
	IdentityFiles  []string `json:"identity_files,omitempty"`
	IdentitiesOnly string   `json:"identities_only,omitempty"` // "yes" / "no" / "" (unset)
	// Source is the file the block came from (the config or an Include).
	Source string `json:"source"`
}

// SSHConfig is the parsed ~/.ssh/config, Includes followed, Match blocks
// skipped (they need a live connection to evaluate).
type SSHConfig struct {
	Hosts []SSHHost `json:"hosts"`
}

// SSHAlias is one alias whose effective HostName is github.com, with the
// values ssh would use for it (first match wins, as in ssh_config(5)).
type SSHAlias struct {
	Alias          string   `json:"alias"`
	HostName       string   `json:"host_name"`
	IdentityFiles  []string `json:"identity_files,omitempty"`
	IdentitiesOnly bool     `json:"identities_only"`
	// User is the first User a matching block sets ("" when none does).
	User string `json:"user,omitempty"`
}

// ReadSSHConfig parses <home>/.ssh/config. A missing file is an empty
// config, not an error.
func (p *Prober) ReadSSHConfig(ctx context.Context) (SSHConfig, error) {
	base := filepath.Join(p.o.Home, sshDir)
	var cfg SSHConfig
	err := p.parseSSHFile(ctx, filepath.Join(base, sshConfig), base, 0, &cfg)
	return cfg, err
}

// parseSSHFile appends the Host blocks of file (and its Includes) to cfg.
func (p *Prober) parseSSHFile(ctx context.Context, file, base string, depth int, cfg *SSHConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	return ParseSSHConfig(f, file, func(pattern string) error {
		if depth >= maxIncludeDepth {
			return nil
		}
		pattern = expandHome(pattern, p.o.Home)
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(base, pattern)
		}
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if err := p.parseSSHFile(ctx, m, base, depth+1, cfg); err != nil {
				return err
			}
		}
		return nil
	}, cfg)
}

// ParseSSHConfig reads one ssh config text into cfg (pure apart from the
// include callback, which is called for each Include pattern in place).
// Keywords are case-insensitive; "Key value" and "Key=value" both work.
func ParseSSHConfig(r io.Reader, source string, include func(pattern string) error, cfg *SSHConfig) error {
	sc := bufio.NewScanner(r)
	cur := -1 // index in cfg.Hosts of the open Host block, -1 = global / Match
	for sc.Scan() {
		key, val := splitSSHLine(sc.Text())
		if key == "" {
			continue
		}
		switch key {
		case "host":
			cfg.Hosts = append(cfg.Hosts, SSHHost{Patterns: strings.Fields(val), Source: source})
			cur = len(cfg.Hosts) - 1
		case "match":
			cur = -1
		case "include":
			if include != nil {
				for _, pat := range strings.Fields(val) {
					if err := include(pat); err != nil {
						return err
					}
				}
			}
		default:
			if cur < 0 {
				continue
			}
			h := &cfg.Hosts[cur]
			switch key {
			case "hostname":
				h.HostName = val
			case "user":
				h.User = val
			case "identityfile":
				h.IdentityFiles = append(h.IdentityFiles, unquote(val))
			case "identitiesonly":
				h.IdentitiesOnly = strings.ToLower(val)
			}
		}
	}
	return sc.Err()
}

// splitSSHLine returns a config line's lower-case keyword and its value.
func splitSSHLine(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", ""
	}
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return strings.ToLower(line), ""
	}
	key := strings.ToLower(line[:i])
	val := strings.TrimLeft(line[i:], " \t")
	val = strings.TrimPrefix(val, "=")
	return key, strings.TrimSpace(val)
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// matches reports whether name matches a Host line's patterns: any
// positive pattern matches and no negated one does (ssh_config(5)).
func (h SSHHost) matches(name string) bool {
	hit := false
	for _, pat := range h.Patterns {
		neg := strings.HasPrefix(pat, "!")
		pat = strings.TrimPrefix(pat, "!")
		ok, _ := path.Match(strings.ToLower(pat), strings.ToLower(name))
		if ok && neg {
			return false
		}
		hit = hit || ok
	}
	return hit
}

// Effective returns the values ssh would use for host name: each keyword's
// first value among the blocks that match, IdentityFile accumulating.
func (c SSHConfig) Effective(name string) SSHAlias {
	out := SSHAlias{Alias: name}
	only := ""
	for _, h := range c.Hosts {
		if !h.matches(name) {
			continue
		}
		if out.HostName == "" {
			out.HostName = h.HostName
		}
		if out.User == "" {
			out.User = h.User
		}
		if only == "" {
			only = h.IdentitiesOnly
		}
		out.IdentityFiles = append(out.IdentityFiles, h.IdentityFiles...)
	}
	if out.HostName == "" {
		out.HostName = name
	}
	out.IdentitiesOnly = only == "yes"
	return out
}

// GitHubAliases lists every literal Host pattern whose effective HostName
// is github.com (github.com itself included when it has a block), in file
// order, each once.
func (c SSHConfig) GitHubAliases() []SSHAlias {
	var out []SSHAlias
	seen := map[string]bool{}
	for _, h := range c.Hosts {
		for _, pat := range h.Patterns {
			if strings.ContainsAny(pat, "*?!") || seen[pat] {
				continue
			}
			seen[pat] = true
			if eff := c.Effective(pat); strings.EqualFold(eff.HostName, GitHubHost) {
				out = append(out, eff)
			}
		}
	}
	return out
}

// PublicKey is one ~/.ssh/*.pub key (ONB 4.3).
type PublicKey struct {
	Outcome
	Path        string `json:"path"`
	Bits        string `json:"bits,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"` // "SHA256:…"
	Comment     string `json:"comment,omitempty"`
	Type        string `json:"type,omitempty"` // "ED25519", "RSA"
}

// PublicKeys lists <home>/.ssh/*.pub with each fingerprint.
func (p *Prober) PublicKeys(ctx context.Context) []PublicKey {
	files, _ := filepath.Glob(filepath.Join(p.o.Home, sshDir, "*"+pubSuffix))
	out := make([]PublicKey, 0, len(files))
	for _, f := range files {
		out = append(out, p.KeyFingerprint(ctx, f))
	}
	return out
}

// publicKeyPrefixes start every OpenSSH public key line.
var publicKeyPrefixes = []string{"ssh-", "ecdsa-", "sk-ssh-", "sk-ecdsa-"}

// KeyFingerprint runs ssh-keygen -lf on a public key. It refuses (class
// bad_input, nothing run) a path not ending in .pub, a file that is not a
// public key line, or one that holds a private key, so a private key is
// never handed to a program by a probe. Classes: ok, bad_input, timeout,
// tool_missing, failed.
func (p *Prober) KeyFingerprint(ctx context.Context, keyPath string) PublicKey {
	start := time.Now()
	keyPath = expandHome(keyPath, p.o.Home)
	out := PublicKey{Path: keyPath}
	if o := checkPublicKeyFile(keyPath); o != nil {
		out.Outcome = *o
		return out
	}
	return p.keygenFingerprint(ctx, keyPath, out, start)
}

// keygenFingerprint runs ssh-keygen -lf keyPath and parses its one line
// ("256 SHA256:abc… comment (ED25519)") into out.
func (p *Prober) keygenFingerprint(ctx context.Context, keyPath string, out PublicKey, start time.Time) PublicKey {
	cctx, done, ok := p.slot(ctx, p.o.SSHTimeout)
	defer done()
	if !ok {
		out.Outcome = timedOut(start)
		return out
	}
	r := p.run(cctx, cmdSpec{bin: sshKeygenBin, args: []string{"-l", "-f", keyPath}, env: []string{envLCAll + "=C"}})
	if !r.ok() {
		out.Outcome = failure(r, sshKeygenBin, func(string) Class { return ClassFailed })
		out.Took = time.Since(start)
		return out
	}
	// "256 SHA256:abc… mira@agency.example (ED25519)"
	f := strings.Fields(strings.TrimSpace(r.stdout))
	if len(f) < 2 {
		out.Outcome = Outcome{Class: ClassFailed, Detail: "ssh-keygen gave no fingerprint", Took: time.Since(start)}
		return out
	}
	out.Bits, out.Fingerprint = f[0], f[1]
	rest := f[2:]
	if n := len(rest); n > 0 && strings.HasPrefix(rest[n-1], "(") {
		out.Type = strings.Trim(rest[n-1], "()")
		rest = rest[:n-1]
	}
	out.Comment = strings.Join(rest, " ")
	out.Outcome = Outcome{Class: ClassOK, Took: time.Since(start)}
	return out
}

// maxKeyFileSize bounds a key file KeyFileFingerprint hands to
// ssh-keygen (an OpenSSH private key is a few KiB; a big file is not one).
const maxKeyFileSize = 64 << 10

// KeyFileFingerprint is the fingerprint of the key at keyPath, private or
// public (owner 2026-11-07: a .pub is never required). With a .pub (keyPath
// itself or keyPath + .pub) it is KeyFingerprint of that file. Otherwise
// ssh-keygen -lf runs on the private key file itself: lore hands ssh-keygen
// the path only and never reads, copies or stores the file's content;
// ssh-keygen prints just the fingerprint line (an OpenSSH-format key keeps
// its public half unencrypted, so no passphrase is asked). Classes: ok,
// bad_input (no such file, not a regular file, too big), timeout,
// tool_missing (ssh-keygen is not installed), failed.
func (p *Prober) KeyFileFingerprint(ctx context.Context, keyPath string) PublicKey {
	keyPath = expandHome(keyPath, p.o.Home)
	if strings.HasSuffix(keyPath, pubSuffix) {
		return p.KeyFingerprint(ctx, keyPath)
	}
	if st, err := os.Stat(keyPath + pubSuffix); err == nil && st.Mode().IsRegular() {
		return p.KeyFingerprint(ctx, keyPath+pubSuffix)
	}
	start := time.Now()
	out := PublicKey{Path: keyPath}
	st, err := os.Stat(keyPath)
	switch {
	case err != nil:
		out.Outcome = Outcome{Class: ClassBadInput, Detail: "no key file at " + keyPath}
		return out
	case !st.Mode().IsRegular() || st.Size() > maxKeyFileSize:
		out.Outcome = Outcome{Class: ClassBadInput, Detail: keyPath + " is not a key file"}
		return out
	}
	return p.keygenFingerprint(ctx, keyPath, out, start)
}

// checkPublicKeyFile refuses anything but a small .pub file whose first
// line is an OpenSSH public key.
func checkPublicKeyFile(keyPath string) *Outcome {
	if !strings.HasSuffix(keyPath, pubSuffix) {
		return &Outcome{Class: ClassBadInput, Detail: "only a .pub public key is read; " + filepath.Base(keyPath) + " is not one"}
	}
	st, err := os.Stat(keyPath)
	switch {
	case err != nil:
		return &Outcome{Class: ClassBadInput, Detail: "no public key at " + keyPath}
	case !st.Mode().IsRegular() || st.Size() > maxPubKeySize:
		return &Outcome{Class: ClassBadInput, Detail: keyPath + " is not a public key file"}
	}
	b, err := os.ReadFile(keyPath)
	if err != nil {
		return &Outcome{Class: ClassBadInput, Detail: "cannot read " + keyPath}
	}
	text := string(b)
	if strings.Contains(text, "PRIVATE KEY") {
		return &Outcome{Class: ClassBadInput, Detail: keyPath + " holds a private key; it is never read"}
	}
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	for _, pre := range publicKeyPrefixes {
		if strings.HasPrefix(first, pre) {
			return nil
		}
	}
	return &Outcome{Class: ClassBadInput, Detail: keyPath + " is not an OpenSSH public key"}
}
