package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// github.ssh_auth / github.ssh_user over ssh's real messages (ONB 4.3).
func TestSSHAuth(t *testing.T) {
	s := newSandbox(t)
	log := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_LOG", log)
	fakeBin(t, s.bin, "ssh", `echo "$@" >> "$FAKE_LOG"
case "$FAKE_SSH_T" in
  deploy) echo "Hi acme/lore-me! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1;;
  greet) echo "Hi mira! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1;;
  gitlab) echo "Welcome to GitLab, @tomas!"; exit 0;;
  denied) echo "git@github.com: Permission denied (publickey)." >&2; exit 255;;
  hostkey) echo "Host key verification failed." >&2; exit 255;;
  dns) echo "ssh: Could not resolve hostname github-acme: Name or service not known" >&2; exit 255;;
  hang) sleep 60;;
  *) echo "kex_exchange_identification: read: odd" >&2; exit 255;;
esac`)
	// Every answering case gets a deadline no loaded machine reaches (a
	// guard, not a measure: the fake answers at once); only "hang", whose
	// fake never answers in time, runs under a short one. One 400 ms
	// deadline for all made "denied" a timeout when the machine was busy.
	p := s.prober(Options{SSHTimeout: 30 * time.Second})
	hang := s.prober(Options{SSHTimeout: 400 * time.Millisecond})
	cases := []struct {
		mode string
		want Class
		user string
	}{
		{"greet", ClassOK, "mira"},
		{"deploy", ClassFailed, ""}, // a deploy key greets a repo, never an account
		{"gitlab", ClassOK, "tomas"},
		{"denied", ClassAuthDenied, ""},
		{"hostkey", ClassHostKey, ""},
		{"dns", ClassUnreachable, ""},
		{"hang", ClassTimeout, ""},
		{"odd", ClassFailed, ""},
	}
	for _, c := range cases {
		t.Setenv("FAKE_SSH_T", c.mode)
		pr := p
		if c.want == ClassTimeout {
			pr = hang
		}
		r := pr.SSHAuth(ctxT(t), Route{HostAlias: "github-acme"})
		if r.Class != c.want || r.User != c.user || r.Host != "github-acme" {
			t.Errorf("%s: %+v", c.mode, r)
		}
		if c.mode == "deploy" && !strings.HasPrefix(r.Detail, "deploy key for acme/lore-me") {
			t.Errorf("deploy key detail = %q, want it to name the repo (realmac m7: it read \"no answer\")", r.Detail)
		}
	}
	b, _ := os.ReadFile(log)
	if first, _, _ := strings.Cut(string(b), "\n"); first != "-T -o BatchMode=yes -o ConnectTimeout=5 git@github-acme" {
		t.Errorf("argv: %q", first)
	}
	t.Setenv("FAKE_SSH_T", "greet")
	if r := p.SSHAuth(ctxT(t), Route{SSHKey: "~/.ssh/id_acme.pub"}); r.Host != GitHubHost || r.User != "mira" {
		t.Errorf("key route: %+v", r)
	}
	b, _ = os.ReadFile(log)
	if !strings.Contains(string(b), "-i "+s.home+"/.ssh/id_acme.pub -o IdentitiesOnly=yes git@github.com") {
		t.Errorf("key route argv:\n%s", b)
	}
	if r := p.SSHAuth(ctxT(t), Route{HostAlias: "-oProxyCommand=x"}); r.Class != ClassBadInput {
		t.Errorf("bad alias: %+v", r)
	}
	if err := os.Remove(filepath.Join(s.bin, "ssh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", s.bin)
	if r := p.SSHAuth(ctxT(t), Route{}); r.Class != ClassToolMissing {
		t.Errorf("no ssh: %+v", r)
	}
}

const sshConfigText = `# personal
Host github.com
  IdentityFile ~/.ssh/id_personal

Host github-acme acme
    HostName github.com
    User git
    IdentityFile "~/.ssh/id_acme"
    IdentitiesOnly yes

Host github-agency
  HostName=GitHub.com
  IdentityFile ~/.ssh/id_agency

Host gitlab-work
  HostName gitlab.example

Match host *.internal
  IdentitiesOnly yes

Include conf.d/*

Host *
  IdentitiesOnly no
  IdentityFile ~/.ssh/id_default
`

func TestSSHConfig(t *testing.T) {
	s := newSandbox(t)
	dir := filepath.Join(s.home, ".ssh")
	if err := os.MkdirAll(filepath.Join(dir, "conf.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(sshConfigText), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conf.d", "client"), []byte("Host github-tomas\n HostName github.com\n IdentitiesOnly yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := s.prober(Options{})
	cfg, err := p.ReadSSHConfig(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SSHAlias{}
	var order []string
	for _, a := range cfg.GitHubAliases() {
		got[a.Alias] = a
		order = append(order, a.Alias)
	}
	if strings.Join(order, ",") != "github.com,github-acme,acme,github-agency,github-tomas" {
		t.Errorf("aliases: %v", order)
	}
	if a := got["github-acme"]; !a.IdentitiesOnly || len(a.IdentityFiles) != 2 || a.IdentityFiles[0] != "~/.ssh/id_acme" {
		t.Errorf("github-acme: %+v", a)
	}
	// first value wins: Host * says IdentitiesOnly no, after the agency block left it unset
	if a := got["github-agency"]; a.IdentitiesOnly {
		t.Errorf("github-agency: %+v", a)
	}
	if a := got["github-tomas"]; !a.IdentitiesOnly {
		t.Errorf("included alias: %+v", a)
	}
	if _, ok := got["gitlab-work"]; ok {
		t.Errorf("gitlab alias listed")
	}
	// a missing config is empty, not an error
	empty := newSandbox(t)
	if c, err := empty.prober(Options{}).ReadSSHConfig(ctxT(t)); err != nil || len(c.Hosts) != 0 {
		t.Errorf("missing config: %+v %v", c, err)
	}
	// negated patterns
	h := SSHHost{Patterns: []string{"github-*", "!github-old"}}
	if !h.matches("github-acme") || h.matches("github-old") || h.matches("gitlab") {
		t.Errorf("pattern matching")
	}
}

func TestKeyFingerprint(t *testing.T) {
	s := newSandbox(t)
	fakeBin(t, s.bin, "ssh-keygen", `case "$3" in
  *rsa*) echo "3072 SHA256:rsaFingerPrint mira@agency.example (RSA)";;
  *nocomment*) echo "256 SHA256:bare no comment (ED25519)";;
  *) echo "256 SHA256:edFingerPrint mira@agency.example (ED25519)";;
esac`)
	dir := filepath.Join(s.home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("id_acme.pub", "ssh-ed25519 AAAAC3Nza mira@agency.example\n")
	write("id_rsa.pub", "ssh-rsa AAAAB3Nza mira@agency.example\n")
	// built at run time: a fixture that looks like a private key is never committed
	priv := "-----BEGIN OPENSSH " + "PRIVATE KEY-----\nabc\n"
	write("id_acme", priv)
	write("disguised.pub", priv)
	write("junk.pub", "hello\n")
	p := s.prober(Options{})

	keys := p.PublicKeys(ctxT(t))
	byName := map[string]PublicKey{}
	for _, k := range keys {
		byName[filepath.Base(k.Path)] = k
	}
	if k := byName["id_acme.pub"]; k.Class != ClassOK || k.Fingerprint != "SHA256:edFingerPrint" || k.Type != "ED25519" || k.Comment != "mira@agency.example" || k.Bits != "256" {
		t.Errorf("ed25519: %+v", k)
	}
	if k := byName["id_rsa.pub"]; k.Type != "RSA" || k.Bits != "3072" {
		t.Errorf("rsa: %+v", k)
	}
	if k := byName["disguised.pub"]; k.Class != ClassBadInput || !strings.Contains(k.Detail, "private key") {
		t.Errorf("private key behind .pub: %+v", k)
	}
	if k := byName["junk.pub"]; k.Class != ClassBadInput {
		t.Errorf("junk: %+v", k)
	}
	if _, listed := byName["id_acme"]; listed {
		t.Errorf("private key listed")
	}
	for _, bad := range []string{"~/.ssh/id_acme", filepath.Join(dir, "missing.pub"), dir} {
		if k := p.KeyFingerprint(ctxT(t), bad); k.Class != ClassBadInput {
			t.Errorf("%s: %+v", bad, k)
		}
	}
	if k := p.KeyFingerprint(ctxT(t), "~/.ssh/id_acme.pub"); k.Class != ClassOK || k.Path != filepath.Join(dir, "id_acme.pub") {
		t.Errorf("~ path: %+v", k)
	}
}
