package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/onboard"
)

func fileURL(p string) string { return "file://" + filepath.ToSlash(p) }

// store.read and store.marker against real file:// remotes (ONB 4.4).
func TestReadAndMarkerFile(t *testing.T) {
	s := newSandbox(t)
	root := t.TempDir()
	withMarker := filepath.Join(root, "me.git")
	noMarker := filepath.Join(root, "plain.git")
	empty := filepath.Join(root, "empty.git")
	bareRepo(t, withMarker, rowfs.MarkerFile, "lore-org.yaml", "notes.md")
	bareRepo(t, noMarker, "README.md")
	bareRepo(t, empty)
	p := s.prober(Options{})
	ctx := ctxT(t)

	if r := p.ReadRemote(ctx, fileURL(withMarker), Route{}); r.Class != ClassOK || len(r.Heads) != 1 || r.Heads[0] != "main" || r.Empty {
		t.Errorf("read marker repo: %+v", r)
	}
	if r := p.ReadRemote(ctx, fileURL(empty), Route{}); r.Class != ClassOK || !r.Empty {
		t.Errorf("read empty repo: %+v", r)
	}
	if r := p.ReadRemote(ctx, fileURL(filepath.Join(root, "nothere.git")), Route{}); r.Class != ClassNoAccess {
		t.Errorf("read missing repo: %+v", r)
	}

	cases := []struct {
		url              string
		present, isEmpty bool
	}{
		{fileURL(withMarker), true, false},
		{fileURL(noMarker), false, false},
		{fileURL(empty), false, true},
	}
	for _, c := range cases {
		m := p.StoreMarker(ctx, c.url, Route{})
		if m.Class != ClassOK || m.Present != c.present || m.EmptyRepo != c.isEmpty {
			t.Errorf("marker %s: %+v", c.url, m)
		}
	}
	if m := p.StoreMarker(ctx, fileURL(filepath.Join(root, "nothere.git")), Route{}); m.Class != ClassNoAccess {
		t.Errorf("marker missing repo: %+v", m)
	}
	rf := p.RootFiles(ctx, fileURL(withMarker), Route{}, "lore-org.yaml", "absent.yaml", rowfs.MarkerFile)
	if rf.Class != ClassOK || strings.Join(rf.Found, ",") != "lore-org.yaml,"+rowfs.MarkerFile {
		t.Errorf("root files: %+v", rf)
	}
	if rf := p.RootFiles(ctx, fileURL(withMarker), Route{}, "a/b"); rf.Class != ClassBadInput {
		t.Errorf("root file with slash: %+v", rf)
	}
	assertTmpClean(t, s.tmp)
}

func TestClassStatus(t *testing.T) {
	want := map[Class]onboard.Status{ClassOK: onboard.StatusOK, ClassTimeout: onboard.StatusError, ClassToolMissing: onboard.StatusError, ClassBadInput: onboard.StatusError,
		ClassNoWrite: onboard.StatusFail, ClassNoAccess: onboard.StatusFail, ClassAuthDenied: onboard.StatusFail, ClassUnreachable: onboard.StatusFail, ClassHostKey: onboard.StatusFail, ClassNotLoggedIn: onboard.StatusFail, ClassFailed: onboard.StatusFail}
	for c, w := range want {
		if c.Status() != w {
			t.Errorf("%s: %s", c, c.Status())
		}
	}
}

// The write test's dry run makes no ref and leaves no temp repo (ONB 4.4).
func TestWriteRemoteFileDryRun(t *testing.T) {
	s := newSandbox(t)
	remote := filepath.Join(t.TempDir(), "me.git")
	bareRepo(t, remote, "a.md")
	p := s.prober(Options{})
	before := gitT(t, remote, "count-objects")
	w := p.WriteRemote(ctxT(t), fileURL(remote), Route{}, "mch_01")
	if w.Class != ClassOK {
		t.Fatalf("write: %+v", w)
	}
	if refs := gitT(t, remote, "for-each-ref"); strings.Contains(refs, "refs/lore/probe") {
		t.Errorf("dry run created a ref: %s", refs)
	}
	if objs := gitT(t, remote, "count-objects"); objs != before {
		t.Errorf("dry run sent objects: %s", objs)
	}
	assertTmpClean(t, s.tmp)
	for _, bad := range []string{"", "a/b", "..", strings.Repeat("x", 65)} {
		if w := p.WriteRemote(ctxT(t), fileURL(remote), Route{}, bad); w.Class != ClassBadInput {
			t.Errorf("machine id %q: %+v", bad, w)
		}
	}
}

func assertTmpClean(t *testing.T, tmp string) {
	t.Helper()
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), probeTmpPattern) {
			t.Errorf("temp repo left behind: %s", e.Name())
		}
	}
}

// fakeSSHTransport is an ssh that logs its argv and serves git over local
// bare repos under $FAKE_ROOT, refusing like GitHub does: a repo path with
// "deny" gets GitHub's read-only push refusal at receive-pack connect (the
// point where GitHub checks push rights, before a dry run stops), "nokey"
// gets the publickey refusal. A local file:// remote cannot model this: a
// dry run never runs hooks and never touches the objects folder, so a
// read-only bare repo or a refusing pre-receive hook still answers ok.
const fakeSSHTransport = `
echo "$@" >> "$FAKE_LOG"
last=""; for a in "$@"; do last="$a"; done
case "$last" in
  *receive-pack*deny*) echo "ERROR: Permission to acme-hub/deny.git denied to mira." >&2; echo "fatal: Could not read from remote repository." >&2; exit 128;;
  *nokey*) echo "git@github.com: Permission denied (publickey)." >&2; exit 255;;
esac
cmd=${last%% *}; p=${last#* }; p=$(printf '%s' "$p" | tr -d "'")
exec git "${cmd#git-}" "$FAKE_ROOT/${p#/}"
`

// store.write over ssh with the setup's alias (Q5, Q6). Break-verified:
// classifyRemote's noWriteWords removed → the deny case reads no_access /
// failed and this test fails.
func TestWriteRemoteSSHAlias(t *testing.T) {
	s := newSandbox(t)
	root := t.TempDir()
	log := filepath.Join(root, "ssh.log")
	t.Setenv("FAKE_ROOT", root)
	t.Setenv("FAKE_LOG", log)
	fakeBin(t, s.bin, "ssh", fakeSSHTransport)
	bareRepo(t, filepath.Join(root, "acme-hub", "lore-team.git"), "a.md")
	bareRepo(t, filepath.Join(root, "acme-hub", "deny.git"), "a.md")
	p := s.prober(Options{})
	ctx := ctxT(t)
	alias := Route{HostAlias: "github-acme"}

	cases := []struct {
		name, url string
		route     Route
		want      Class
	}{
		{"writable scp via alias", "git@github.com:acme-hub/lore-team.git", alias, ClassOK},
		{"writable ssh:// via alias", "ssh://git@github.com/acme-hub/lore-team.git", alias, ClassOK},
		{"read-only", "git@github.com:acme-hub/deny.git", alias, ClassNoWrite},
		{"key refused", "git@github.com:acme-hub/nokey.git", alias, ClassAuthDenied},
	}
	for _, c := range cases {
		if w := p.WriteRemote(ctx, c.url, c.route, "mch_01"); w.Class != c.want {
			t.Errorf("%s: got %+v, want %s", c.name, w, c.want)
		}
	}
	// the read-only repo still reads: the write test is what tells them apart
	if r := p.ReadRemote(ctx, "git@github.com:acme-hub/deny.git", alias); r.Class != ClassOK {
		t.Errorf("read of read-only repo: %+v", r)
	}
	b, _ := os.ReadFile(log)
	logged := string(b)
	if !strings.Contains(logged, "git@github-acme") || strings.Contains(logged, "git@github.com ") {
		t.Errorf("alias not used:\n%s", logged)
	}
	if !strings.Contains(logged, "BatchMode=yes") {
		t.Errorf("ssh may prompt:\n%s", logged)
	}
	if refs := gitT(t, filepath.Join(root, "acme-hub", "lore-team.git"), "for-each-ref"); strings.Contains(refs, "refs/lore/probe") {
		t.Errorf("dry run created a ref: %s", refs)
	}
	assertTmpClean(t, s.tmp)

	// Q5 (c): no alias, a key: GIT_SSH_COMMAND carries -i and IdentitiesOnly
	_ = os.Remove(log)
	if r := p.ReadRemote(ctx, "git@github.com:acme-hub/lore-team.git", Route{SSHKey: "~/.ssh/id_acme"}); r.Class != ClassOK {
		t.Fatalf("key route read: %+v", r)
	}
	b, _ = os.ReadFile(log)
	if want := "-i " + filepath.Join(s.home, ".ssh/id_acme") + " -o IdentitiesOnly=yes"; !strings.Contains(string(b), want) {
		t.Errorf("key route args %q lack %q", b, want)
	}
}

func TestRemoteRefusesCredentialURL(t *testing.T) {
	s := newSandbox(t)
	p := s.prober(Options{})
	tok := "ghp_" + strings.Repeat("Q7w6", 9)
	u := "https://mira:" + tok + "@github.com/acme-hub/r.git"
	for _, o := range []Outcome{p.ReadRemote(ctxT(t), u, Route{}).Outcome, p.WriteRemote(ctxT(t), u, Route{}, "m1").Outcome, p.StoreMarker(ctxT(t), u, Route{}).Outcome} {
		if o.Class != ClassBadInput || strings.Contains(o.Detail, tok) {
			t.Errorf("credential url: %+v", o)
		}
	}
	if r := p.ReadRemote(ctxT(t), "git@github.com:a/b.git", Route{HostAlias: "-oProxyCommand=evil"}); r.Class != ClassBadInput {
		t.Errorf("bad alias: %+v", r)
	}
}

// A hanging remote is a timeout, killed at the probe's deadline.
func TestReadRemoteTimeout(t *testing.T) {
	s := newSandbox(t)
	fakeBin(t, s.bin, "ssh", "sleep 10")
	p := s.prober(Options{GitTimeout: 300 * time.Millisecond})
	start := time.Now()
	r := p.ReadRemote(ctxT(t), "git@github.com:acme-hub/r.git", Route{})
	if r.Class != ClassTimeout {
		t.Errorf("got %+v", r)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("not killed at the deadline: %s", time.Since(start))
	}
}

func TestGitIdentity(t *testing.T) {
	s := newSandbox(t)
	p := s.prober(Options{})
	if id := p.GlobalIdentity(ctxT(t)); id.Class != ClassOK || id.Name != "" || id.Email != "" {
		t.Errorf("unset: %+v", id)
	}
	gitT(t, s.home, "config", "--global", "user.name", "Mira")
	gitT(t, s.home, "config", "--global", "user.email", "mira@agency.example")
	if id := p.GlobalIdentity(ctxT(t)); id.Name != "Mira" || id.Email != "mira@agency.example" {
		t.Errorf("global: %+v", id)
	}
	repo := t.TempDir()
	gitT(t, repo, "init", "--quiet", ".")
	gitT(t, repo, "config", "user.email", "mira@acme-hub.example")
	if id := p.RepoIdentity(ctxT(t), repo); id.Class != ClassOK || id.Email != "mira@acme-hub.example" || id.Name != "Mira" {
		t.Errorf("repo: %+v", id)
	}
}
