package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A person's own ssh command (a jump-host wrapper in GIT_SSH_COMMAND, or
// core.sshCommand) is how their plain git reaches the store host, so the
// store probes keep it; lore's BatchMode line replaced it and store.read
// failed where `git ls-remote` worked. A key route (Q5 c) still needs
// lore's own line with -i. Break-verified: remoteGitSpec always setting
// GIT_SSH_COMMAND fails the wrapper cases (the fake ssh refuses).
func TestRemoteProbesKeepPersonSSHCommand(t *testing.T) {
	s := newSandbox(t)
	root := t.TempDir()
	log := filepath.Join(root, "ssh.log")
	wrapLog := filepath.Join(root, "wrap.log")
	t.Setenv("FAKE_ROOT", root)
	t.Setenv("FAKE_LOG", log)
	// the plain ssh on PATH cannot reach the host; only the wrapper can
	fakeBin(t, s.bin, "ssh", `echo "$@" >> "$FAKE_LOG"; echo "ssh: connect to host git.example.test port 22: Connection refused" >&2; exit 255`)
	wrapper := filepath.Join(root, "jump-ssh")
	fakeBin(t, root, "jump-ssh", `echo "$@" >> "`+wrapLog+`"`+"\n"+strings.ReplaceAll(fakeSSHTransport, `echo "$@" >> "$FAKE_LOG"`, ""))
	bareRepo(t, filepath.Join(root, "acme-hub", "lore-team.git"), "a.md")
	url := "ssh://git@git.example.test/acme-hub/lore-team.git"

	t.Run("GIT_SSH_COMMAND", func(t *testing.T) {
		t.Setenv("GIT_SSH_COMMAND", wrapper)
		p := s.prober(Options{})
		if r := p.ReadRemote(ctxT(t), url, Route{}); r.Class != ClassOK {
			t.Fatalf("read through the person's wrapper: %+v", r)
		}
		if w := p.WriteRemote(ctxT(t), url, Route{}, "mch_01"); w.Class != ClassOK {
			t.Fatalf("write through the person's wrapper: %+v", w)
		}
		if b, _ := os.ReadFile(wrapLog); strings.Contains(string(b), "BatchMode") {
			t.Errorf("lore added options to the person's own command: %s", b)
		}
	})

	t.Run("core.sshCommand", func(t *testing.T) {
		t.Setenv("GIT_SSH_COMMAND", "")
		_ = os.Unsetenv("GIT_SSH_COMMAND") // git runs an empty one as a program
		gitT(t, s.home, "config", "--global", keyCoreSSHCommand, wrapper)
		defer gitT(t, s.home, "config", "--global", "--unset", keyCoreSSHCommand)
		p := s.prober(Options{})
		if r := p.ReadRemote(ctxT(t), url, Route{}); r.Class != ClassOK {
			t.Fatalf("read through core.sshCommand: %+v", r)
		}
	})

	t.Run("key route uses lore's line", func(t *testing.T) {
		t.Setenv("GIT_SSH_COMMAND", wrapper)
		_ = os.Remove(log)
		p := s.prober(Options{})
		_ = p.ReadRemote(ctxT(t), url, Route{SSHKey: "~/.ssh/id_acme"})
		b, _ := os.ReadFile(log)
		if !strings.Contains(string(b), "BatchMode=yes") || !strings.Contains(string(b), "IdentitiesOnly=yes") {
			t.Errorf("key route did not run lore's ssh line: %q", b)
		}
	})

	t.Run("no command of their own: lore's line", func(t *testing.T) {
		t.Setenv("GIT_SSH_COMMAND", "")
		_ = os.Remove(log)
		p := s.prober(Options{})
		_ = p.ReadRemote(ctxT(t), url, Route{})
		if b, _ := os.ReadFile(log); !strings.Contains(string(b), "BatchMode=yes") {
			t.Errorf("default ssh may prompt: %q", b)
		}
	})
}
