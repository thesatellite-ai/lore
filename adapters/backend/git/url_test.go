package git

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// TestCanonicalURL covers every url form of spec 7.2 and proves no
// credential survives into the url lore writes (D24).
func TestCanonicalURL(t *testing.T) {
	t.Parallel()
	const secret = "s3cr3t-tok"
	cases := map[string]struct{ url, norm string }{
		"git@github.com:agency/lore-company":                  {"ssh://git@github.com/agency/lore-company", "github.com/agency/lore-company"},
		"git@github.com:agency/lore-company.git":              {"ssh://git@github.com/agency/lore-company.git", "github.com/agency/lore-company"},
		"ssh://git@GitHub.com:2222/agency/brain":              {"ssh://git@GitHub.com:2222/agency/brain", "github.com/agency/brain"},
		"ssh://git:" + secret + "@host.example/a/b":           {"ssh://git@host.example/a/b", "host.example/a/b"},
		"https://" + secret + "@github.com/agency/x.git":      {"https://github.com/agency/x.git", "github.com/agency/x"},
		"https://user:" + secret + "@gitlab.example/g/sub/r/": {"https://gitlab.example/g/sub/r/", "gitlab.example/g/sub/r"},
		"http://10.0.0.5/team/store":                          {"http://10.0.0.5/team/store", "10.0.0.5/team/store"},
	}
	for raw, want := range cases {
		got, err := CanonicalURL(raw)
		if err != nil || got != want.url {
			t.Errorf("CanonicalURL(%q) = %q, %v; want %q", raw, got, err, want.url)
			continue
		}
		if strings.Contains(got, secret) {
			t.Errorf("CanonicalURL(%q) kept the credential", raw)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		r, err := RemoteOf(u)
		if err != nil || r.Norm != want.norm {
			t.Errorf("RemoteOf(%q).Norm = %q, %v; want %q", got, r.Norm, err, want.norm)
		}
	}
	if got, err := CanonicalURL("git@github.com:a/b?folder=org"); err != nil || got != "ssh://git@github.com/a/b?folder=org" {
		t.Errorf("folder query lost: %q %v", got, err)
	}
	for _, bad := range []string{"s3://bucket/x", "lore://host/s", "https:///nohost", "https://github.com/", "https://github.com/a/../b", "ssh://~/x", "https://h.example/a//b", "https://h.example/-upload-pack/x"} {
		if _, err := CanonicalURL(bad); !errs.Has(err, errs.StoreScheme) {
			t.Errorf("CanonicalURL(%q): err = %v, want E_STORE_SCHEME", bad, err)
		}
	}
}

func TestCloneBase(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	got, err := CloneBase(home, Remote{Norm: "github.com/agency/lore-company"})
	if err != nil || got != filepath.Join(home, ClonesDir, "github.com", "agency", "lore-company") {
		t.Fatalf("CloneBase = %q %v", got, err)
	}
	if _, err := CloneBase("relative", Remote{Norm: "h/a/b"}); !errs.Has(err, errs.BadPath) {
		t.Fatalf("relative home: %v", err)
	}
}

func TestOpenerRefusals(t *testing.T) {
	t.Parallel()
	open := NewOpener()
	cases := map[string]struct {
		home string
		code errs.Code
	}{
		"https://github.com/a/b":            {"", errs.BadPath}, // no home
		"https://github.com/a/b?x=1":        {t.TempDir(), errs.StoreScheme},
		"https://github.com/a/b?folder=../": {t.TempDir(), errs.BadPath},
		"ftp://github.com/a/b":              {t.TempDir(), errs.StoreScheme},
	}
	for raw, c := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := open(context.Background(), u, store.OpenOptions{Home: c.home}); !errs.Has(err, c.code) {
			t.Errorf("open %q: err = %v, want %s", raw, err, c.code)
		}
	}
}

func TestPorcelainVerdict(t *testing.T) {
	t.Parallel()
	const dst = "refs/heads/main"
	out := "To ../remote.git\n!\tabc:refs/heads/main\t[rejected] (fetch first)\nDone\n"
	flag, summary, ok := porcelainVerdict(out, dst)
	if !ok || flag != flagRejected || summary != "[rejected] (fetch first)" {
		t.Fatalf("verdict = %q %q %v", flag, summary, ok)
	}
	if _, _, ok := porcelainVerdict("fatal: could not read from remote\n", dst); ok {
		t.Fatal("a verdict was invented for a failed connection")
	}
}

func TestRowRel(t *testing.T) {
	t.Parallel()
	id := "mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00"
	good := map[string]string{
		".lore/data/memories/" + id + ".json":          "memories/" + id + ".json",
		"org/.lore/data/memories/" + id + ".json":      "memories/" + id + ".json",
		"team/web/.lore/data/memories/" + id + ".json": "memories/" + id + ".json",
	}
	for p, want := range good {
		if got, ok := rowRel(p); !ok || got != want {
			t.Errorf("rowRel(%q) = %q %v", p, got, ok)
		}
	}
	for _, p := range []string{"lore-store.yaml", ".lore/data/_meta.json", "../.lore/data/memories/" + id + ".json", ".x/.lore/data/memories/" + id + ".json", "memories/" + id + ".json"} {
		if _, ok := rowRel(p); ok {
			t.Errorf("rowRel(%q) accepted", p)
		}
	}
}

// TestRemoteGitForm pins the origin spelling lore writes into a store
// clone: an ssh url without a port goes back to git's scp-like form, so a
// person's url.<base>.insteadOf rule keyed on `git@host:` still applies
// (GDOCS-1: every store looked unreachable behind such a rule); a port,
// https and http keep their url form; no credential survives either way.
func TestRemoteGitForm(t *testing.T) {
	t.Parallel()
	const secret = "s3cr3t-tok"
	cases := map[string]string{
		"git@github.com:agency/notes":                  "git@github.com:agency/notes",
		"ssh://git@github.com/agency/notes.git":        "git@github.com:agency/notes.git",
		"ssh://git:" + secret + "@GitHub.com/a/b":      "git@GitHub.com:a/b",
		"github.com:agency/notes":                      "github.com:agency/notes",
		"ssh://git@host.example:2222/agency/notes":     "ssh://git@host.example:2222/agency/notes",
		"https://" + secret + "@github.com/agency/n":   "https://github.com/agency/n",
		"http://git.internal/agency/notes?folder=team": "http://git.internal/agency/notes",
	}
	for raw, want := range cases {
		canon, err := CanonicalURL(raw)
		if err != nil {
			t.Fatalf("CanonicalURL(%q): %v", raw, err)
		}
		u, err := url.Parse(canon)
		if err != nil {
			t.Fatalf("parse %q: %v", canon, err)
		}
		r, err := RemoteOf(u)
		if err != nil {
			t.Fatalf("RemoteOf(%q): %v", canon, err)
		}
		if r.Git != want {
			t.Errorf("%q: Git = %q, want %q", raw, r.Git, want)
		}
		if strings.Contains(r.Git, secret) {
			t.Errorf("%q: the credential survived into %q", raw, r.Git)
		}
	}
}
