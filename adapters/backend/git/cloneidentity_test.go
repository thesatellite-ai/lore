package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestEnsureCloneIdentity: a clone without a repo-local email gets the
// identity (and the record); one still holding lore's recorded email is
// updated when the identity changed; an email the person set is kept; the
// global git config is never written.
func TestEnsureCloneIdentity(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	global := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tname = Pat Person\n\temail = pat@personal.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	want := CloneIdentity{Signature: Signature{Name: "Pat Person", Email: "pat@work.example"}, Why: "your identity in org acme"}
	open := func() *Backend {
		b := &Backend{work: dir, git: runner{bin: bin}}
		b.cfg.cloneIdentity = func(context.Context, string) CloneIdentity { return want }
		if err := b.ensureCloneIdentity(ctx, "git@example.test:acme/notes.git"); err != nil {
			t.Fatal(err)
		}
		return b
	}
	email := func() string {
		v, err := LocalConfig(ctx, dir, ConfigUserEmail)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if id, ok := open().IdentitySetUp(); !ok || id.Email != want.Email || id.Was != "" || email() != want.Email {
		t.Fatalf("first open: set %v %+v, local email %q", ok, id, email())
	}
	if _, ok := open().IdentitySetUp(); ok {
		t.Fatal("second open wrote the identity again")
	}
	want.Email = "pat@new-work.example" // the org identity changed: lore's own value follows
	if id, ok := open().IdentitySetUp(); !ok || id.Was != "pat@work.example" || email() != want.Email {
		t.Fatalf("changed identity: set %v %+v, local email %q", ok, id, email())
	}
	if out, err := exec.Command("git", "-C", dir, "config", "user.email", "pat@own-choice.example").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	want.Email = "pat@third.example"
	if _, ok := open().IdentitySetUp(); ok || email() != "pat@own-choice.example" {
		t.Fatalf("the person's own email was replaced: %q", email())
	}
	if b, _ := os.ReadFile(global); string(b) != "[user]\n\tname = Pat Person\n\temail = pat@personal.example\n" {
		t.Fatalf("the global git config changed:\n%s", b)
	}
}

// TestCloneIdentityAgentCorrected (owner bug 12): a clone holding the
// agent identity an earlier build wrote (the record names it) is given
// the person's identity at the next write or sync, and the change names
// what it was.
func TestCloneIdentityAgentCorrected(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	agent := Signature{Name: "Una Person via claude_code", Email: "agent.auto/claude_code/abcdef@lore.invalid"}
	if err := SetLocalIdentity(ctx, dir, agent); err != nil {
		t.Fatal(err)
	}
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	person := CloneIdentity{Signature: Signature{Name: "Una Person", Email: "una@example.test"}, Why: "your lore identity"}
	b := &Backend{work: dir, git: runner{bin: bin}}
	b.cfg.cloneIdentity = func(context.Context, string) CloneIdentity { return person }
	if err := b.ensureCloneIdentity(ctx, "git@example.test:una/lore-me.git"); err != nil {
		t.Fatal(err)
	}
	id, ok := b.IdentitySetUp()
	name, _ := LocalConfig(ctx, dir, ConfigUserName)
	email, _ := LocalConfig(ctx, dir, ConfigUserEmail)
	if !ok || id.Was != agent.Email || name != "Una Person" || email != "una@example.test" {
		t.Fatalf("set %v %+v; clone is %q <%s>, want the person", ok, id, name, email)
	}
}
