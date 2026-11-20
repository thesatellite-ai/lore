package wire

import (
	"context"
	"testing"

	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestOrgLinkFirstLinkResolvesClientInSameRun is devco:D-733: in a fresh
// clone of a client repo (no .lore/, no link yet) the run's org answer is
// gathered before `lore org link` writes the link, and the leak guard the
// live hook installs after a successful link asks that same resolution
// whether the repo is a client repo. The kept answer had no link, so the
// first link installed no hook and only a second identical link did. The
// link (and unlink) now drop the kept answer once the store holds the
// change, so the rest of the run reads the repo as it is now.
func TestOrgLinkFirstLinkResolvesClientInSameRun(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	if _, err := orgHostAt(t, w.repo, mira).Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}

	// one run: something reads the org answer before the link (as the
	// write hooks do), then the link, then the guard's question
	h := orgHostAt(t, w.repo, mira)
	if c := h.res.ClientRepo(ctx); c.Org != "" {
		t.Fatalf("before any link the repo resolved as a client repo of %q; the test needs an unlinked repo", c.Org)
	}
	if _, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme"}, true); err != nil {
		t.Fatalf("link: %v", err)
	}
	if c := h.res.ClientRepo(ctx); c.Org != "agency" || c.Folder != "acme" {
		t.Fatalf("after the link, the same run still reads the answer from before it: ClientRepo = %+v, want org agency folder acme", c)
	}

	// unlink: the same run reads the repo as unlinked again
	if _, err := h.Unlink(ctx, opsorgcmd.UnlinkIn{Org: "agency"}, true); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if c := h.res.ClientRepo(ctx); c.Org != "" {
		t.Fatalf("after the unlink, the same run still reads the link: ClientRepo = %+v", c)
	}
}

// TestOrgResolutionForgetGathersAgain: Forget drops the kept answer, so
// the next Situation gathers again and keeps that new answer for the run.
func TestOrgResolutionForgetGathersAgain(t *testing.T) {
	t.Parallel()
	r := NewOrgResolution(t.TempDir(), t.TempDir(), nil)
	ctx := context.Background()
	if _, err := r.Situation(ctx); err != nil {
		t.Fatal(err)
	}
	r.Forget()
	r.mu.Lock()
	kept := r.done || r.sit != nil
	r.mu.Unlock()
	if kept {
		t.Fatal("Forget left the kept answer in place")
	}
	if _, err := r.Situation(ctx); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	kept = r.done
	r.mu.Unlock()
	if !kept {
		t.Fatal("the Situation after Forget must keep its new answer for the run")
	}
	var none *OrgResolution
	none.Forget() // a nil resolution (an embedder with no org) is a no-op
}
