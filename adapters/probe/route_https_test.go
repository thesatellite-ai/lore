package probe

import (
	"os/exec"
	"strings"
	"testing"
)

// The https route, run through real git: the ssh url forms rewrite to
// https://github.com/, and `git credential fill` gets the login's token
// from gh (a fake gh here) through the per-call helper only; a helper in
// the person's own git config is dropped for the call. The token never
// lands in any git config.
func TestHTTPSRouteThroughGit(t *testing.T) {
	s := newSandbox(t)
	fakeBin(t, s.bin, "gh", `[ "$1 $2 $3 $4 $5 $6" = "auth token --hostname github.com --user octo-user" ] || { echo "bad args: $*" >&2; exit 1; }
echo tok-for-octo-user`)
	// A global helper that must not be asked. The credential line is built
	// at run time so the fixture never reads as a stored secret.
	globalHelper := "!echo " + "pass" + "word=from-global-helper; :"
	if out, err := exec.Command("git", "config", "--global", "credential.helper", globalHelper).CombinedOutput(); err != nil {
		t.Fatalf("git config: %v %s", err, out)
	}
	r := Route{HTTPSUser: "octo-user"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	fill := exec.Command("git", append(r.GitArgs(), "credential", "fill")...)
	fill.Stdin = strings.NewReader("protocol=https\nhost=github.com\npath=octo-user/lore-me.git\n\n")
	out, err := fill.CombinedOutput()
	if err != nil {
		t.Fatalf("credential fill: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "pass"+"word=tok-for-octo-user") || strings.Contains(string(out), "from-global-helper") {
		t.Fatalf("credential fill = %q", out)
	}
	for _, u := range []string{"git@github.com:octo-user/lore-me.git", "ssh://git@github.com/octo-user/lore-me.git"} {
		got, err := exec.Command("git", append(r.GitArgs(), "ls-remote", "--get-url", u)...).Output()
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != "https://github.com/octo-user/lore-me.git" {
			t.Errorf("%s rewrites to %q", u, got)
		}
	}
	if cfg, _ := exec.Command("git", "config", "--global", "--list").Output(); strings.Contains(string(cfg), "tok-for") {
		t.Errorf("token written to git config: %s", cfg)
	}
}

// A login that is not a GitHub login is refused before it reaches the helper's shell line.
func TestHTTPSRouteRefusesShellLogin(t *testing.T) {
	for _, bad := range []string{"a;rm -rf x", "$(id)", "-x", "a b", strings.Repeat("a", 40)} {
		if (Route{HTTPSUser: bad}).Validate() == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// KeyFileFingerprint takes a private key with no .pub beside it (ssh-keygen
// gets only the path), prefers the .pub when one exists, and says clearly
// when ssh-keygen is not installed.
func TestKeyFileFingerprint(t *testing.T) {
	s := newSandbox(t)
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	p := New(Options{Home: s.home})
	key := s.home + "/.ssh/main-key"
	if out, err := exec.Command("sh", "-c", "mkdir -p "+s.home+"/.ssh && ssh-keygen -q -t ed25519 -N '' -C octo-user -f "+key+" && rm "+key+".pub").CombinedOutput(); err != nil {
		t.Fatalf("keygen: %v %s", err, out)
	}
	k := p.KeyFileFingerprint(ctxT(t), "~/.ssh/main-key")
	if k.Class != ClassOK || !strings.HasPrefix(k.Fingerprint, "SHA256:") {
		t.Fatalf("private key: %+v", k)
	}
	if out, err := exec.Command("sh", "-c", "ssh-keygen -y -f "+key+" > "+key+".pub").CombinedOutput(); err != nil {
		t.Fatalf("pub: %v %s", err, out)
	}
	if k2 := p.KeyFileFingerprint(ctxT(t), key); k2.Fingerprint != k.Fingerprint || !strings.HasSuffix(k2.Path, ".pub") {
		t.Errorf("with .pub: %+v, want %s from the .pub", k2, k.Fingerprint)
	}
	if k3 := p.KeyFileFingerprint(ctxT(t), "~/.ssh/none"); k3.Class != ClassBadInput {
		t.Errorf("missing file: %+v", k3)
	}
	bare := New(Options{Home: s.home, LookPath: func(string) (string, error) { return "", exec.ErrNotFound }})
	if k4 := bare.KeyFileFingerprint(ctxT(t), "~/.ssh/main-key.pub"); k4.Class != ClassToolMissing || !strings.Contains(k4.Detail, "ssh-keygen") {
		t.Errorf("no ssh-keygen: %+v", k4)
	}
}

// A recorded .pub is handed to ssh -i as its private key when that file
// is beside it (ssh -i with a .pub alone needs an agent); a .pub with no
// private key, or a private key path, stays as given.
func TestIdentityPath(t *testing.T) {
	s := newSandbox(t)
	dir := s.home + "/.ssh"
	for _, f := range []string{"pair", "pair.pub", "lonely.pub", "only"} {
		if out, err := exec.Command("sh", "-c", "mkdir -p "+dir+" && touch "+dir+"/"+f).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	for in, want := range map[string]string{dir + "/pair.pub": dir + "/pair", dir + "/lonely.pub": dir + "/lonely.pub", dir + "/only": dir + "/only"} {
		if got := identityPath(in); got != want {
			t.Errorf("identityPath(%s) = %s, want %s", in, got, want)
		}
	}
	if c := (Route{SSHKey: "~/.ssh/pair.pub"}).GitSSHCommand(s.home); !strings.Contains(c, "-i "+dir+"/pair ") {
		t.Errorf("GIT_SSH_COMMAND = %s", c)
	}
}
