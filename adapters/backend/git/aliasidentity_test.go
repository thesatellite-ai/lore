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

// TestSameRepoViaAliasPort443Route (devco D-628): an ssh config that sends
// the store host to its own port-443 route (Host github.com / HostName
// ssh.github.com) does not rename it, as identityHost already says. An
// alias host the config does not name (one git reaches through
// url.<base>.insteadOf) is then still the same store by the single-label
// rule; before, the route line made SameRepoViaAlias call it another host,
// so the clone lore uses for the org's store matched no org entry.
func TestSameRepoViaAliasPort443Route(t *testing.T) {
	SetAliasHosts(func(host string) (string, bool) {
		switch host {
		case "github.com":
			return "ssh.github.com", true // the port-443 route
		case "other-gh":
			return "gitlab.com", true // a real rename to a third host
		}
		return "", false
	})
	t.Cleanup(func() { SetAliasHosts(nil) })
	remote := func(raw string) Remote {
		t.Helper()
		r, err := RemoteOfRaw(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		return r
	}
	store := remote("git@github.com:acme-co/team-notes.git")
	for raw, want := range map[string]bool{
		"git@insteadof-gh:acme-co/team-notes.git": true,  // unnamed single-label alias
		"git@insteadof-gh:acme-co/other.git":      false, // another repository
		"git@other-gh:acme-co/team-notes.git":     false, // the config sends it to gitlab.com
		"git@gitlab.com:acme-co/team-notes.git":   false, // another public host
	} {
		if got := SameRepoViaAlias(remote(raw), store); got != want {
			t.Errorf("SameRepoViaAlias(%s, %s) = %v, want %v", raw, store.Norm, got, want)
		}
		if got := SameRepoViaAlias(store, remote(raw)); got != want {
			t.Errorf("SameRepoViaAlias(%s, %s) = %v, want %v (reversed)", store.Norm, raw, got, want)
		}
	}
	if got := IdentityHost("github.com"); got != "github.com" {
		t.Errorf("IdentityHost(github.com) = %q, want github.com (a route is not an alias)", got)
	}
}

// TestAliasToProviderRouteIsTheProvider (devco D-629): an alias whose
// HostName is a provider's own port-443 route (Host work-443 / HostName
// ssh.github.com / Port 443) is the provider's host: its store identity is
// github.com/<owner>/<repo> and it is the same store as the github.com
// spelling. Before, the identity host was ssh.github.com: another host.
func TestAliasToProviderRouteIsTheProvider(t *testing.T) {
	SetAliasHosts(func(host string) (string, bool) {
		switch host {
		case "work-443":
			return "ssh.github.com", true
		case "lab-443":
			return "altssh.gitlab.com", true
		case "bb-443":
			return "altssh.bitbucket.org", true
		}
		return "", false
	})
	t.Cleanup(func() { SetAliasHosts(nil) })
	for alias, canon := range map[string]string{
		"git@work-443:acme-co/team-notes.git": "git@github.com:acme-co/team-notes.git",
		"git@lab-443:acme-co/team-notes.git":  "https://gitlab.com/acme-co/team-notes",
		"git@bb-443:acme-co/team-notes.git":   "git@bitbucket.org:acme-co/team-notes.git",
	} {
		a, err := RemoteOfRaw(alias)
		if err != nil {
			t.Fatal(err)
		}
		c, err := RemoteOfRaw(canon)
		if err != nil {
			t.Fatal(err)
		}
		if a.Norm != c.Norm {
			t.Errorf("Norm(%s) = %s, want %s (the provider's host)", alias, a.Norm, c.Norm)
		}
		if !SameRepoViaAlias(a, c) {
			t.Errorf("SameRepoViaAlias(%s, %s) = false, want the same store", alias, canon)
		}
	}
	if got := IdentityHost("work-443"); got != "github.com" {
		t.Errorf("IdentityHost(work-443) = %q, want github.com", got)
	}
}

// TestRouteHostRemoteIsTheProvider (devco D-630): a remote typed directly
// on a provider's port-443 route host, with no ssh alias, is the
// provider's repository: one store identity with the provider spelling.
func TestRouteHostRemoteIsTheProvider(t *testing.T) {
	SetAliasHosts(func(string) (string, bool) { return "", false }) // the ssh config names nothing
	t.Cleanup(func() { SetAliasHosts(nil) })
	for route, canon := range map[string]string{
		"git@ssh.github.com:acme-co/team-notes.git":             "git@github.com:acme-co/team-notes.git",
		"ssh://git@ssh.github.com:443/acme-co/team-notes.git":   "https://github.com/acme-co/team-notes",
		"git@altssh.gitlab.com:acme-co/team-notes.git":          "git@gitlab.com:acme-co/team-notes.git",
		"ssh://git@altssh.bitbucket.org:443/acme-co/team-notes": "git@bitbucket.org:acme-co/team-notes.git",
	} {
		r, err := RemoteOfRaw(route)
		if err != nil {
			t.Fatalf("%s: %v", route, err)
		}
		c, err := RemoteOfRaw(canon)
		if err != nil {
			t.Fatal(err)
		}
		if r.Norm != c.Norm || !SameRepoViaAlias(r, c) {
			t.Errorf("RemoteOfRaw(%s).Norm = %s, want %s (the provider's repository)", route, r.Norm, c.Norm)
		}
	}
	if got := IdentityHost("ssh.github.com"); got != "github.com" {
		t.Errorf("IdentityHost(ssh.github.com) = %q, want github.com", got)
	}
}
