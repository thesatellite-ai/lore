package probe

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// keyHome is a home with ~/.ssh holding: a key pair (work), a private key
// without .pub (bare), a lone .pub (orphan.pub) and a folder named like a
// key (dir.pub's private side is a folder).
func keyHome(t *testing.T) (*Prober, string) {
	t.Helper()
	home := t.TempDir()
	ssh := filepath.Join(home, sshDir)
	if err := os.MkdirAll(filepath.Join(ssh, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"work", "work.pub", "bare", "orphan.pub", "dir.pub"} {
		if err := os.WriteFile(filepath.Join(ssh, f), []byte("k\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return New(Options{Home: home}), home
}

func TestExpandPathAndIsFile(t *testing.T) {
	p, home := keyHome(t)
	cases := []struct {
		in     string
		want   string
		isFile bool
	}{
		{"~/.ssh/work", filepath.Join(home, ".ssh/work"), true},
		{"~", home, false}, // a folder
		{"~/.ssh/dir", filepath.Join(home, ".ssh/dir"), false}, // a folder named like a key
		{"~/.ssh/missing", filepath.Join(home, ".ssh/missing"), false},
		{filepath.Join(home, ".ssh/bare"), filepath.Join(home, ".ssh/bare"), true}, // absolute stays
		{"relative/key", "relative/key", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := p.ExpandPath(c.in); got != c.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := p.IsFile(c.in); got != c.isFile {
			t.Errorf("IsFile(%q) = %v, want %v", c.in, got, c.isFile)
		}
	}
}

func TestPrivateKeyFiles(t *testing.T) {
	p, home := keyHome(t)
	k := func(n string) string { return filepath.Join(home, sshDir, n) }
	cases := []struct {
		name  string
		extra []string
		want  []string
	}{
		// work has a .pub; orphan.pub has no private side; dir is a folder.
		{"pairs only", nil, []string{k("work")}},
		// an IdentityFile without .pub counts; a duplicate, a missing file
		// and a .pub given as extra do not.
		{"extra", []string{"~/.ssh/bare", "~/.ssh/work", "~/.ssh/nope", "~/.ssh/work.pub"}, []string{k("bare"), k("work")}},
	}
	for _, c := range cases {
		if got := p.PrivateKeyFiles(c.extra); !slices.Equal(got, c.want) {
			t.Errorf("%s: PrivateKeyFiles = %v, want %v", c.name, got, c.want)
		}
	}
	// No ~/.ssh at all: empty, never an error.
	if got := New(Options{Home: t.TempDir()}).PrivateKeyFiles(nil); len(got) != 0 {
		t.Errorf("empty home: %v", got)
	}
}

func TestKeyNearby(t *testing.T) {
	p, home := keyHome(t)
	cases := []struct{ missing, want string }{
		// a config copied from another machine: same name in this ~/.ssh
		{"/Users/pat/.ssh/work", filepath.Join(home, sshDir, "work")},
		// the same path as the candidate: nothing nearer
		{"~/.ssh/work", ""},
		// no file of that name here
		{"/Users/pat/.ssh/gone", ""},
		// a folder of that name is not a key
		{"/Users/pat/.ssh/dir", ""},
	}
	for _, c := range cases {
		if got := p.KeyNearby(c.missing); got != c.want {
			t.Errorf("KeyNearby(%q) = %q, want %q", c.missing, got, c.want)
		}
	}
}

func TestFolderStorePath(t *testing.T) {
	if testing.Short() {
		t.Skip("uses git")
	}
	s := newSandbox(t)
	p := s.prober(Options{})
	folder := filepath.Join(s.home, "stores", "me")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(s.home, "stores", "bare.git")
	bareRepo(t, bare)
	work := filepath.Join(s.home, "stores", "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, work, "init", "--quiet", ".")
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{fileuri.URL(folder, ""), folder, true},
		{"file://localhost" + fileuri.URLPath(folder), folder, true},
		{"file://~/stores/me", folder, true},                                   // host "~"
		{"file:///~/stores/me", folder, true},                                  // path /~/
		{fileuri.URL(folder, "") + "/new", filepath.Join(folder, "new"), true}, // not there yet: the read check says so
		{fileuri.URL(bare, ""), "", false},                                     // a bare git repo takes the git probes
		{fileuri.URL(work, ""), "", false},                                     // a work tree too
		{"file://elsewhere" + fileuri.URLPath(folder), "", false},              // another host
		{"file:relative/path", "", false},
		{"git@github.com:octo-personal/notes.git", "", false},
		{"https://github.com/octo-personal/notes.git", "", false},
		{"file://user@localhost" + fileuri.URLPath(folder), "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := p.FolderStorePath(c.url)
		if got != c.want || ok != c.ok {
			t.Errorf("FolderStorePath(%q) = %q, %v; want %q, %v", c.url, got, ok, c.want, c.ok)
		}
	}
}

func TestFolderStoreChecks(t *testing.T) {
	home := t.TempDir()
	p := New(Options{Home: home})
	dir := filepath.Join(home, "stores", "me")
	// Missing folder: read, write and marker all say so (no_access).
	for name, o := range map[string]Outcome{"read": p.FolderRead(dir), "write": p.FolderWrite(dir), "marker": p.FolderMarker(dir).Outcome} {
		if o.Class != ClassNoAccess || !strings.Contains(o.Detail, "~/stores/me does not exist") {
			t.Errorf("missing %s = %+v", name, o)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if o := p.FolderRead(dir); o.Class != ClassOK || !strings.Contains(o.Detail, "readable") {
		t.Errorf("read = %+v", o)
	}
	if o := p.FolderWrite(dir); o.Class != ClassOK {
		t.Errorf("write = %+v", o)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("the write test left %d files", len(left))
	}
	if m := p.FolderMarker(dir); m.Class != ClassOK || m.Present {
		t.Errorf("marker before init = %+v", m)
	}
	if err := os.WriteFile(filepath.Join(dir, "lore-store.yaml"), []byte("lore_store: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m := p.FolderMarker(dir); m.Class != ClassOK || !m.Present {
		t.Errorf("marker after init = %+v", m)
	}
	// A file where the folder should be.
	file := filepath.Join(home, "stores", "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if o := p.FolderRead(file); o.Class != ClassNoAccess || !strings.Contains(o.Detail, "is a file") {
		t.Errorf("file = %+v", o)
	}
	// Read-only folder: write fails with a plain reason, no temp path.
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	if runtime.GOOS == "windows" {
		t.Skip("a folder's read-only mode does not stop new files on Windows")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	o := p.FolderWrite(dir)
	if o.Class != ClassNoWrite || o.Detail != "cannot write in ~/stores/me: permission denied" {
		t.Errorf("read-only write = %+v", o)
	}
}

func TestRecordCached(t *testing.T) {
	ctx, tr := WithTrace(context.Background())
	at := time.Date(2026, 11, 6, 19, 27, 15, 0, time.Local)
	RecordCached(WithLabel(ctx, "prefill github.users"), "github.users", at)
	calls := tr.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	c := calls[0]
	if c.Kind != CallCache || c.Label != "prefill github.users" || !c.CachedAt.Equal(at) || c.Exit != 0 ||
		!strings.HasPrefix(c.Command, "cached, from 19:27:15: github.users") {
		t.Errorf("call = %+v", c)
	}
	// No trace: nothing happens (and nothing panics).
	RecordCached(context.Background(), "x", at)
}

func TestSanitizeWithin(t *testing.T) {
	long := strings.Repeat("é", 300) // 600 bytes, 2 per rune
	cases := []struct {
		name, in string
		limit    int
		want     string
	}{
		{"short", "a\nb", 10, "a b"},
		{"cut on a rune edge", long, 5, "éé…"},
		{"token removed", "x ghp_" + strings.Repeat("a", 30), 100, "x <redacted>"},
		{"exact", "abc", 3, "abc"},
	}
	for _, c := range cases {
		if got := SanitizeWithin(c.in, c.limit); got != c.want {
			t.Errorf("%s: %q", c.name, got)
		}
	}
	if got := Sanitize(long); len(got) > maxDetail+len("…") {
		t.Errorf("Sanitize keeps %d bytes", len(got))
	}
	if got := SanitizeWithin(long, MaxCheckDetail); len(got) > MaxCheckDetail+len("…") || !strings.HasSuffix(got, "…") {
		t.Errorf("MaxCheckDetail keeps %d bytes", len(got))
	}
}
