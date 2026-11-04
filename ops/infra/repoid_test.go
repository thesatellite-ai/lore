package infra_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/infra"
)

// checkout is a harness whose repo is a lore checkout (_meta.json with the
// project id, as `lore init` writes it) with remote origin at url.
func checkout(t *testing.T, url string) *opstest.H {
	t.Helper()
	h := opstest.New(t, opstest.Fixed(fixedAt))
	dir := filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rowfs.MetaFileName), []byte(`{"_v":1,"project_id":"`+h.Project+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	setOrigin(t, h.Repo, url)
	return h
}

// gitConfigCore is the [core] section `git init` writes (non-bare work
// tree), kept when setOrigin rewrites .git/config.
const gitConfigCore = "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n"

// setOrigin points remote origin of the repo at dir to url by writing
// .git/config (what `git remote add|set-url origin` leaves: the url key).
// Writing the file instead of running git keeps the test free of a
// subprocess with variable arguments.
func setOrigin(t *testing.T, dir, url string) {
	t.Helper()
	text := gitConfigCore + "[remote \"origin\"]\n\turl = " + url + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// meta reads the checkout's identity.
func meta(t *testing.T, h *opstest.H) rowfs.RepoMeta {
	t.Helper()
	m, _, err := rowfs.ReadRepoMeta(h.Repo)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestRepoIdentityMintedOnce pins PROV1's first step: the first check
// mints a random rep_ id with the remote as the confirmed address, writes
// a repos row with the same id, and a second check changes nothing.
func TestRepoIdentityMintedOnce(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	ctx := context.Background()
	rep, err := h.App.CheckRepoAddresses(ctx)
	if err != nil || !rep.Minted || len(rep.Notices) != 0 {
		t.Fatalf("first check: %+v %v", rep, err)
	}
	m := meta(t, h)
	if m.RepoID != rep.RepoID || !slices.Equal(m.URLs, []string{"github.com/agency/acmehub_backend"}) {
		t.Fatalf("_meta.json: %+v", m)
	}
	if id, ok := h.App.RepoIdentity(ctx); !ok || id != m.RepoID {
		t.Fatalf("RepoIdentity = %q %v", id, ok)
	}
	again, err := h.App.CheckRepoAddresses(ctx)
	if err != nil || again.Minted || again.RepoID != m.RepoID {
		t.Fatalf("second check must keep the id: %+v %v", again, err)
	}
}

// TestChangedRemoteUnconfirmed pins PROV2 and Q2: a new remote is
// recorded `unconfirmed` at once (New on the first run only), it is NOT
// added to _meta.json's confirmed urls, and the notice repeats on every
// later check until it is confirmed.
func TestChangedRemoteUnconfirmed(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	ctx := context.Background()
	if _, cerr := h.App.CheckRepoAddresses(ctx); cerr != nil {
		t.Fatal(cerr)
	}
	setOrigin(t, h.Repo, "https://github.com/agency-labs/acmehub_backend.git")
	rep, err := h.App.CheckRepoAddresses(ctx)
	if err != nil || len(rep.Notices) != 1 || !rep.Notices[0].New || rep.Notices[0].CaseOnly {
		t.Fatalf("changed remote: %+v %v", rep, err)
	}
	if n := rep.Notices[0]; n.Key != "github.com/agency-labs/acmehub_backend" || n.Recorded != "github.com/agency/acmehub_backend" {
		t.Fatalf("notice: %+v", n)
	}
	if m := meta(t, h); slices.Contains(m.URLs, "github.com/agency-labs/acmehub_backend") {
		t.Fatalf("an unconfirmed address must not be trusted: %+v", m)
	}
	again, err := h.App.CheckRepoAddresses(ctx)
	if err != nil || len(again.Notices) != 1 || again.Notices[0].New {
		t.Fatalf("the notice must repeat, not as new: %+v %v", again, err)
	}
	st, err := h.App.RepoAddressStatus(ctx)
	if err != nil || len(st.Notices) != 1 {
		t.Fatalf("doctor must list the unconfirmed address: %+v %v", st, err)
	}
}

// TestSetURLKeepsID pins PROV1/PROV2: set-url confirms the address as the
// primary, keeps the repo id, keeps the old address as confirmed history,
// and the notice is gone.
func TestSetURLKeepsID(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	ctx := context.Background()
	first, err := h.App.CheckRepoAddresses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setOrigin(t, h.Repo, "git@github.com:agency/acme-hub-api.git")
	if _, cerr := h.App.CheckRepoAddresses(ctx); cerr != nil {
		t.Fatal(cerr)
	}
	res := h.Must(infra.OpRepoSetURL, `{"url":"git@github.com:agency/acme-hub-api.git"}`)
	if h.Field(res, "repo_id") != first.RepoID {
		t.Fatalf("set-url must keep the id: %s", h.JSON(res))
	}
	m := meta(t, h)
	if m.RepoID != first.RepoID || !slices.Equal(m.URLs, []string{"github.com/agency/acme-hub-api", "github.com/agency/acmehub_backend"}) {
		t.Fatalf("_meta.json after set-url: %+v", m)
	}
	rep, err := h.App.CheckRepoAddresses(ctx)
	if err != nil || len(rep.Notices) != 0 {
		t.Fatalf("a confirmed address has no notice: %+v %v", rep, err)
	}
	mirror := h.Must(infra.OpRepoSetURL, `{"url":"https://git.agency.example/mirror/acmehub_backend.git","mirror":true}`)
	if h.Field(mirror, "mode") != "mirror" || !slices.Contains(meta(t, h).URLs, "git.agency.example/mirror/acmehub_backend") {
		t.Fatalf("mirror: %s", h.JSON(mirror))
	}
}

// TestForkNewID pins PROV1: `lore repo fork` gives the clone a new random
// id with forked_from naming the upstream, its remote as the only
// confirmed address, and a repos row with the same id.
func TestForkNewID(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	ctx := context.Background()
	up, err := h.App.CheckRepoAddresses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h.Fails(infra.OpRepoFork, `{}`, errs.InvalidInput) // the upstream's own address is not a fork
	setOrigin(t, h.Repo, "https://github.com/tomas-k/acmehub_backend.git")
	if _, cerr := h.App.CheckRepoAddresses(ctx); cerr != nil {
		t.Fatal(cerr)
	}
	res := h.Must(infra.OpRepoFork, `{}`)
	m := meta(t, h)
	if m.RepoID == up.RepoID || m.RepoID != h.Field(res, "repo_id") || m.ForkedFrom != up.RepoID ||
		!slices.Equal(m.URLs, []string{"github.com/tomas-k/acmehub_backend"}) {
		t.Fatalf("fork _meta.json: %+v (%s)", m, h.JSON(res))
	}
	if got := h.Field(res, "mount_name"); got != "acmehub_backend-fork" && got == "" {
		t.Fatalf("mount name: %s", got)
	}
	rep, err := h.App.CheckRepoAddresses(ctx)
	if err != nil || rep.RepoID != m.RepoID || len(rep.Notices) != 0 {
		t.Fatalf("the fork's remote is its own confirmed address: %+v %v", rep, err)
	}
	split := checkout(t, "git@github.com:agency/acmehub_backend.git")
	if _, cerr := split.App.CheckRepoAddresses(ctx); cerr != nil {
		t.Fatal(cerr)
	}
	setOrigin(t, split.Repo, "git@github.com:agency/acmehub_worker.git")
	split.Must(infra.OpRepoFork, `{"split":true}`)
	if sm := meta(t, split); sm.SplitFrom == "" || sm.ForkedFrom != "" {
		t.Fatalf("split: %+v", sm)
	}
}

// TestCaseOnlySameRepo pins PROV3: a remote that differs only in letter
// case is recorded with CaseOnly (same repo, fork never offered), fork is
// refused, and one set-url confirms it.
func TestCaseOnlySameRepo(t *testing.T) {
	h := checkout(t, "https://github.com/agency/acmehub_backend.git")
	ctx := context.Background()
	first, err := h.App.CheckRepoAddresses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setOrigin(t, h.Repo, "https://github.com/Agency/acmehub_backend.git")
	rep, err := h.App.CheckRepoAddresses(ctx)
	if err != nil || len(rep.Notices) != 1 || !rep.Notices[0].CaseOnly {
		t.Fatalf("case-only: %+v %v", rep, err)
	}
	h.Fails(infra.OpRepoFork, `{}`, errs.InvalidInput)
	res := h.Must(infra.OpRepoSetURL, `{"url":"https://github.com/Agency/acmehub_backend.git"}`)
	if h.Field(res, "repo_id") != first.RepoID {
		t.Fatalf("same repo keeps its id: %s", h.JSON(res))
	}
	if again, _ := h.App.CheckRepoAddresses(ctx); len(again.Notices) != 0 {
		t.Fatalf("confirmed once: %+v", again)
	}
}

// TestDoctorFlagsMismatch pins Q5's doctor half: a fork whose _meta.json
// was replaced by its upstream's (a rebase takes the upstream's side as
// "ours") still has its fork remote, which is a confirmed address of the
// fork's repos row: AddressStatus names the mismatch.
func TestDoctorFlagsMismatch(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	ctx := context.Background()
	up, err := h.App.CheckRepoAddresses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setOrigin(t, h.Repo, "https://github.com/tomas-k/acmehub_backend.git")
	h.Must(infra.OpRepoFork, `{}`)
	if st, _ := h.App.RepoAddressStatus(ctx); st.Mismatch != "" {
		t.Fatalf("a clean fork has no mismatch: %+v", st)
	}
	if werr := rowfs.WriteRepoMeta(h.Repo, rowfs.RepoMeta{RepoID: up.RepoID, URLs: []string{"github.com/agency/acmehub_backend"}}); werr != nil {
		t.Fatal(werr)
	}
	st, err := h.App.RepoAddressStatus(ctx)
	if err != nil || st.Mismatch == "" {
		t.Fatalf("the replaced identity must be flagged: %+v %v", st, err)
	}
}
