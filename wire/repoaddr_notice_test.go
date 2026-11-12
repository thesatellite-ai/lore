package wire_test

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/wire"
)

// TestAskAddressOnlyFirstRunInTerminal: Q2 asks only on the run that
// first records the address, and only in a terminal (ported from the old
// binary's sync_core_repoid_test.go).
func TestAskAddressOnlyFirstRunInTerminal(t *testing.T) {
	t.Parallel()
	cases := []struct{ isNew, terminal, want bool }{
		{true, true, true}, {true, false, false}, {false, true, false}, {false, false, false},
	}
	for _, c := range cases {
		if got := wire.AskAddress(opsinfra.AddressNotice{New: c.isNew}, c.terminal); got != c.want {
			t.Errorf("new=%v terminal=%v: ask=%v, want %v", c.isNew, c.terminal, got, c.want)
		}
	}
}

// TestAddressNoticeCaseOnlyOffersNoFork pins PROV3: a case-only
// difference names set-url and never `lore repo fork`; a real change
// offers all three commands.
func TestAddressNoticeCaseOnlyOffersNoFork(t *testing.T) {
	t.Parallel()
	c := wire.AddressNotice("rep_x", opsinfra.AddressNotice{Key: "github.com/Agency/x", Recorded: "github.com/agency/x", CaseOnly: true})
	if strings.Contains(c, "fork") || !strings.Contains(c, "lore repo set-url github.com/Agency/x") {
		t.Fatalf("case-only notice: %s", c)
	}
	n := wire.AddressNotice("rep_x", opsinfra.AddressNotice{Key: "github.com/tomas/x", Recorded: "github.com/agency/x"})
	for _, want := range []string{"lore repo set-url github.com/tomas/x ", "--mirror", "lore repo fork", "rep_x"} {
		if !strings.Contains(n, want) {
			t.Fatalf("notice lacks %q: %s", want, n)
		}
	}
}

// TestSyncRepoAddressesAsksOnceInTerminal: a changed remote prints the
// notice; without a terminal nothing is asked and the address stays
// unconfirmed (a later terminal run does not ask either); the first
// terminal run that records a new address asks,
// and answering 2 records it as a mirror, so the next run is silent
// (PROVENANCE_DESIGN 10.3, PROV2, Q2).
func TestSyncRepoAddressesAsksOnceInTerminal(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	git := func(cmd *exec.Cmd) {
		t.Helper()
		cmd.Dir = h.Repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", cmd.Args, err, out)
		}
	}
	git(exec.Command("git", "remote", "add", "origin", "https://github.com/agency/x.git"))
	// What `lore init` writes: _meta.json and the repo id (the remote it
	// finds is the primary address).
	pid, err := h.App.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.App.SeedRepoMeta(ctx, pid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.App.EnsureRepoIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	var first bytes.Buffer
	h.App.SyncRepoAddresses(ctx, &first, strings.NewReader(""), true)
	if first.String() != "" {
		t.Fatalf("the recorded remote needs no notice:\n%s", first.String())
	}
	git(exec.Command("git", "remote", "set-url", "origin", "https://github.com/tomas/x.git"))
	var agent bytes.Buffer
	h.App.SyncRepoAddresses(ctx, &agent, strings.NewReader("2\n"), false)
	if !strings.Contains(agent.String(), "! remote changed: this clone uses github.com/tomas/x") || strings.Contains(agent.String(), "? what is") {
		t.Fatalf("agent run:\n%s", agent.String())
	}
	// Unanswered, it stays unconfirmed: the notice again, and no question
	// in a terminal either (it is no longer the run that recorded it).
	var later bytes.Buffer
	h.App.SyncRepoAddresses(ctx, &later, strings.NewReader("2\n"), true)
	if !strings.Contains(later.String(), "github.com/tomas/x") || strings.Contains(later.String(), "? what is") {
		t.Fatalf("later run:\n%s", later.String())
	}
	git(exec.Command("git", "remote", "set-url", "origin", "https://github.com/tomas/y.git"))
	var person bytes.Buffer
	h.App.SyncRepoAddresses(ctx, &person, strings.NewReader("2\n"), true)
	if !strings.Contains(person.String(), "? what is github.com/tomas/y?") {
		t.Fatalf("person run:\n%s", person.String())
	}
	var again bytes.Buffer
	h.App.SyncRepoAddresses(ctx, &again, strings.NewReader(""), true)
	if strings.Contains(again.String(), "tomas/y") || strings.Contains(again.String(), "? what is") {
		t.Fatalf("after the mirror answer:\n%s", again.String())
	}
}

// TestSyncRepoAddressesFirstAddressIsRecorded (B3-16): a repo set up with
// a local-path remote has no confirmed address; the first sync of a clone
// with a real remote (a new laptop) records it as the primary, silently:
// nothing changed, so there is no "remote changed" notice and no fork offer.
func TestSyncRepoAddressesFirstAddressIsRecorded(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = h.Repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", cmd.Args, err, out)
		}
	}
	git("remote", "add", "origin", "/srv/remotes/site.git") // a local path: no address
	pid, err := h.App.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.App.SeedRepoMeta(ctx, pid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.App.EnsureRepoIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	var local bytes.Buffer
	h.App.SyncRepoAddresses(ctx, &local, strings.NewReader(""), true)
	if local.String() != "" {
		t.Fatalf("a local-path remote needs no notice:\n%s", local.String())
	}
	git("remote", "set-url", "origin", "git@git.example.test:pat/site.git")
	var first bytes.Buffer
	h.App.SyncRepoAddresses(ctx, &first, strings.NewReader("3\n"), true)
	if first.String() != "" {
		t.Fatalf("the first real address was reported instead of recorded:\n%s", first.String())
	}
	m, _, err := h.App.ReadRepoMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.URLs) != 1 || m.URLs[0] != "git.example.test/pat/site" {
		t.Fatalf("_meta.json urls = %v, want the first address confirmed", m.URLs)
	}
	// From now on a different address is a real change: the notice again.
	git("remote", "set-url", "origin", "git@git.example.test:other/site.git")
	var moved bytes.Buffer
	h.App.SyncRepoAddresses(ctx, &moved, strings.NewReader(""), false)
	if !strings.Contains(moved.String(), "! remote changed: this clone uses git.example.test/other/site, the repo record says git.example.test/pat/site") {
		t.Fatalf("a later move:\n%s", moved.String())
	}
}
