package git

import (
	"path/filepath"
	"testing"
	"time"
)

// fakeSSHConfig installs a resolver answering like a ~/.ssh/config with
// `Host work-gh` / `HostName github.com` and `Host github.com` /
// `HostName ssh.github.com` (the port-443 route), for the test only.
func fakeSSHConfig(t *testing.T) {
	t.Helper()
	SetAliasHosts(func(host string) (string, bool) {
		switch host {
		case "work-gh":
			return "github.com", true
		case "github.com":
			return "ssh.github.com", true
		}
		return "", false
	})
	t.Cleanup(func() { SetAliasHosts(nil) })
}

// D-493: an ssh alias url, the canonical ssh url and the https url of one
// repository are one store identity (Remote.Norm), so a clone whose
// origin uses the alias is matched to the org's entry.
func TestRemoteNormResolvesSSHAlias(t *testing.T) {
	fakeSSHConfig(t)
	want := "github.com/acme-co/team-notes"
	for _, raw := range []string{
		"git@work-gh:acme-co/team-notes.git",
		"ssh://git@work-gh/acme-co/team-notes",
		"git@github.com:acme-co/team-notes.git",
		"https://github.com/acme-co/team-notes",
	} {
		r, err := RemoteOfRaw(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if r.Norm != want {
			t.Errorf("%s: Norm = %q, want %q", raw, r.Norm, want)
		}
	}
	r, _ := RemoteOfRaw("git@work-gh:acme-co/team-notes.git")
	if r.URL != "ssh://git@work-gh/acme-co/team-notes.git" {
		t.Errorf("URL = %q: the alias must stay in the url git is given", r.URL)
	}
	// An https host is never resolved through the ssh config.
	if r, _ := RemoteOfRaw("https://work-gh/acme-co/team-notes"); r.Norm != "work-gh/acme-co/team-notes" {
		t.Errorf("https Norm = %q", r.Norm)
	}
	// A host the config does not rename keeps its name.
	if r, _ := RemoteOfRaw("git@gitlab.com:acme-co/team-notes.git"); r.Norm != "gitlab.com/acme-co/team-notes" {
		t.Errorf("gitlab Norm = %q", r.Norm)
	}
}

// A clone recorded under an alias key before D-493 (a person's own clone)
// is still found by the canonical url.
func TestCloneDirForFindsAliasKeyedRecord(t *testing.T) {
	home := t.TempDir()
	own := filepath.Join(t.TempDir(), "my-notes")
	if err := RecordClone(home, "work-gh/acme-co/team-notes", CloneEntry{Path: own, Why: WhyOwn}, time.Now()); err != nil {
		t.Fatal(err)
	}
	fakeSSHConfig(t)
	r, err := RemoteOfRaw("git@github.com:acme-co/team-notes.git")
	if err != nil {
		t.Fatal(err)
	}
	dir, isOwn, err := CloneDirFor(home, r, "")
	if err != nil {
		t.Fatal(err)
	}
	if dir != own || !isOwn {
		t.Fatalf("CloneDirFor = %q own=%v, want %q own=true", dir, isOwn, own)
	}
}
