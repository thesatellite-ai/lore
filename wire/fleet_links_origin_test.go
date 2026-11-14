package wire

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/logic/onboard"
)

// fakeRepo makes a folder that the fleet walk takes for a git repo (a .git
// entry), with lore data when lore is true (a non-empty .lore/).
func fakeRepo(t *testing.T, dir string, lore bool) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if lore {
		data := filepath.Join(dir, ".lore", "data")
		if err := os.MkdirAll(data, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "_meta.json"), []byte(`{"_v":2,"project_id":"prj_01a1a5aca7f57659be7320c5f4b70002"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return resolvedPath(dir)
}

// symlink links link to target or fails the test.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestFixRepoWalkFollowsLinkedRepo (devco:O-1): a setup folder holding a
// symlink to a repo elsewhere counts that repo (with lore data: run in;
// without: counted and named as no lore data), once even when two links
// reach it, and remembers the link for doctor's line. Before, the walk
// saw the link as a file and dropped it silently.
func TestFixRepoWalkFollowsLinkedRepo(t *testing.T) {
	t.Parallel()
	work := resolvedPath(t.TempDir())
	fleet, elsewhere := filepath.Join(work, "fleet"), filepath.Join(work, "elsewhere")
	alpha := fakeRepo(t, filepath.Join(fleet, "alpha"), true)
	gamma := fakeRepo(t, filepath.Join(elsewhere, "gamma"), true)
	delta := fakeRepo(t, filepath.Join(elsewhere, "delta"), false)
	symlink(t, gamma, filepath.Join(fleet, "gamma"))
	symlink(t, gamma, filepath.Join(fleet, "gamma-again"))
	symlink(t, delta, filepath.Join(fleet, "delta"))
	symlink(t, filepath.Join(work, "missing"), filepath.Join(fleet, "dangling"))

	found, err := fixRepoWalk{bases: []string{fleet}}.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(found.Roots, sortedCopy(alpha, gamma)) {
		t.Fatalf("roots %v, want %s and %s", found.Roots, alpha, gamma)
	}
	if !slices.Equal(found.NoLore, []string{delta}) {
		t.Fatalf("no lore data %v, want [%s]", found.NoLore, delta)
	}
	if link := found.Links[gamma]; link != filepath.Join(fleet, "gamma") {
		t.Fatalf("link of %s = %q", gamma, link)
	}

	// the doctor --all fallback scan follows the link the same way
	scanned, err := ScanLoreRepos(context.Background(), []string{fleet}, 3)
	if err != nil {
		t.Fatal(err)
	}
	var roots []string
	for _, r := range scanned {
		roots = append(roots, r.Root)
	}
	if !slices.Equal(roots, sortedCopy(alpha, gamma)) {
		t.Fatalf("scan found %v", roots)
	}
}

// sortedCopy is the paths sorted.
func sortedCopy(p ...string) []string {
	out := slices.Clone(p)
	slices.Sort(out)
	return out
}

// TestLinkedRepoNextNamesTheSetupEdit (devco:O-1): a linked repo outside
// every setup's folders gets a line naming the link, the real folder and
// the setup edit that adds it, keeping the setup's current folders (the
// flag replaces the list); none when a setup's folders hold the repo.
func TestLinkedRepoNextNamesTheSetupEdit(t *testing.T) {
	t.Parallel()
	work := resolvedPath(t.TempDir())
	fleet, elsewhere := filepath.Join(work, "fleet"), filepath.Join(work, "elsewhere")
	gamma := fakeRepo(t, filepath.Join(elsewhere, "gamma"), true)
	cfg := onboard.Config{Setups: []onboard.SetupEntry{{Name: "fleet", Folders: []string{fleet + "/**"}}}}
	got := linkedRepoNext(cfg, work, filepath.Join(fleet, "gamma"), gamma)
	want := "lore setup edit --name fleet --folder '" + fleet + "/**' --folder '" + gamma + "'"
	if !strings.Contains(got, "reached through the link "+filepath.Join(fleet, "gamma")) || !strings.HasSuffix(got, want) {
		t.Fatalf("line %q\nwant it to end %q", got, want)
	}
	cfg = onboard.Config{Setups: []onboard.SetupEntry{{Name: "fleet", Folders: []string{fleet + "/**", elsewhere + "/**"}}}}
	if got = linkedRepoNext(cfg, work, filepath.Join(fleet, "gamma"), gamma); got != "" {
		t.Fatalf("a repo a setup's folders hold: %q", got)
	}
}

// TestSetupStoreKnownReposReadsUseRecords (devco:O-16): a repo lore ran in
// (a cache use record, as every lore fix --all child leaves) is a known
// repo for doctor --all even when it has no repo id yet (a repo just
// migrated from the 0.1.17 format), so doctor never says "lore has not
// run in it" right after fix --all ran there. A record of a folder with no
// lore data, or one gone, adds nothing.
func TestSetupStoreKnownReposReadsUseRecords(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	work := resolvedPath(t.TempDir())
	migrated := fakeRepo(t, filepath.Join(work, "migrated"), true)
	plain := fakeRepo(t, filepath.Join(work, "plain"), false)
	root := filepath.Join(home, cacheDir)
	for _, r := range []string{migrated, plain, filepath.Join(work, "gone")} {
		if err := sqlite.RecordUse(root, r, nil); err != nil {
			t.Fatal(err)
		}
	}
	a := openWith(t, gitRepo(t), Options{Home: home})
	known, err := setupStore{a: a}.KnownRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var roots []string
	for _, k := range known {
		roots = append(roots, k.Root)
	}
	if !slices.Equal(roots, []string{migrated}) {
		t.Fatalf("known repos %v, want [%s]", roots, migrated)
	}
}

// fakeLookup answers host lookups without the network.
func fakeLookup(err error) hostLookup {
	return func(context.Context, string) ([]string, error) {
		if err != nil {
			return nil, err
		}
		return []string{"192.0.2.1"}, nil
	}
}

// TestOriginFault (devco:O-19): doctor names an origin that is a missing
// folder (no lookup at all) or a host that does not exist, with the git
// step; an existing bare repo, a host that resolves, and a lookup with no
// definite answer (offline, timeout) say nothing.
func TestOriginFault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := gitRepo(t)
	bare := filepath.Join(t.TempDir(), "kit.git")
	gitIn(t, filepath.Dir(bare), nil, "init", "-q", "--bare", bare)
	noLookup := func(context.Context, string) ([]string, error) {
		t.Error("a path remote must not look anything up")
		return nil, errors.New("unused")
	}
	set := func(url string) { gitIn(t, repo, nil, "remote", "set-url", "origin", url) }
	if line := originFault(ctx, repo, noLookup); line != "" {
		t.Fatalf("no origin: %q", line)
	}
	gitIn(t, repo, nil, "remote", "add", "origin", bare)
	if line := originFault(ctx, repo, noLookup); line != "" {
		t.Fatalf("existing bare repo: %q", line)
	}
	set(strings.TrimSuffix(bare, ".git")) // git appends .git itself
	if line := originFault(ctx, repo, noLookup); line != "" {
		t.Fatalf("bare repo named without .git: %q", line)
	}
	gone := filepath.Join(filepath.Dir(bare), "kit-gone.git")
	set(gone)
	line := originFault(ctx, repo, noLookup)
	if !strings.Contains(line, "no git repository at "+gone) || !strings.Contains(line, "git remote set-url origin <new-url>") {
		t.Fatalf("missing folder: %q", line)
	}
	set("ssh://git@nohost.invalid/acme/kit.git")
	notFound := &net.DNSError{Err: "no such host", Name: "nohost.invalid", IsNotFound: true}
	if line = originFault(ctx, repo, fakeLookup(notFound)); !strings.Contains(line, "no such host: nohost.invalid") {
		t.Fatalf("unknown host: %q", line)
	}
	if line = originFault(ctx, repo, fakeLookup(&net.DNSError{Err: "timeout", Name: "nohost.invalid", IsTimeout: true})); line != "" {
		t.Fatalf("a lookup timeout is no answer: %q", line)
	}
	set("https://git.example.test/acme/kit.git")
	if line = originFault(ctx, repo, fakeLookup(nil)); line != "" {
		t.Fatalf("host resolves: %q", line)
	}
}
