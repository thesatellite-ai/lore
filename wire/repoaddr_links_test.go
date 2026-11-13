package wire

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// movedRemote is where the test repo moves to (github.com/acme/app was
// its address when the org folder linked it).
const movedRemote = "https://github.com/acme/app-two"

// movedKey is movedRemote's normalised key.
const movedKey = "github.com/acme/app-two"

// linkWorld is an org world whose store folder acme links this repo under
// github.com/acme/app, with the repo's identity recorded.
func linkWorld(t *testing.T) (*orgWorld, *App, string) {
	t.Helper()
	ctx := context.Background()
	w := newOrgWorld(t)
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	app, err := New(StandaloneOptions(w.repo, mira, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	// the project row `lore init` writes, named by _meta.json (the link
	// written next carries _meta.json's project id)
	withProject(t, app)
	p, err := app.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(w.repo, rowfs.LoreDir, rowfs.DataDir, rowfs.MetaFileName)
	if err = os.WriteFile(meta, []byte(`{"_v":1,"project_id":"`+p.String()+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme"}, true); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, _, err := app.EnsureRepoIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	var first bytes.Buffer
	app.SyncRepoAddresses(ctx, &first, strings.NewReader(""), true)
	if first.String() != "" {
		t.Fatalf("the recorded remote needs no notice:\n%s", first.String())
	}
	pid, ok := repoMetaProjectID(w.repo)
	if !ok {
		t.Fatal("no project id in _meta.json")
	}
	return w, app, pid
}

// hasLink reports whether store folder acme links remote.
func hasLink(t *testing.T, w *orgWorld, remote string) bool {
	t.Helper()
	for _, l := range storeLinks(t, w, "acme") {
		if l.Remote == remote {
			return true
		}
	}
	return false
}

// TestSyncMoveFollowsStoreLinks (issue #23, INTEG-CFG7-1): when the
// person answers `lore sync`'s address question with 1 (moved), the org
// store links of the repo are listed and asked about before they gain the
// new address; y writes them and names each folder, any other answer
// changes nothing and names `lore repo set-url --url <new>`, which then writes
// them. A mirror (2) or fork (3) answer changes no link and asks nothing
// about links.
func TestSyncMoveFollowsStoreLinks(t *testing.T) {
	isolate.Parent(t)
	for _, c := range []struct {
		name, answers string
		// yes: the sync runs with the caller's yes (`lore sync --yes`,
		// REST "yes"), which answers the link question without asking.
		yes          bool
		asks, writes bool
	}{
		{"moved, yes", "1\ny\n", false, true, true},
		{"moved, yes spelled out", "1\nYES\n", false, true, true},
		{"moved, no", "1\nn\n", false, true, false},
		{"moved, no answer", "1\n", false, true, false},
		{"mirror", "2\ny\n", false, false, false},
		{"fork", "3\ny\n", false, false, false},
		{"moved, --yes", "1\n", true, false, true},
		{"moved, --yes, a typed n is never read", "1\nn\n", true, false, true},
		{"mirror, --yes", "2\n", true, false, false},
		{"fork, --yes", "3\n", true, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if isolate.Run(t) {
				return
			}
			w, app, _ := linkWorld(t)
			orgGit(t, w.repo, "remote", "set-url", "origin", movedRemote)
			var out bytes.Buffer
			syncAddresses(t, app, c.yes, &out, c.answers)
			got := out.String()
			if asked := strings.Contains(got, "? write 1 link into the org stores? [y/N]: "); asked != c.asks {
				t.Fatalf("asked about links %v, want %v:\n%s", asked, c.asks, got)
			}
			if c.asks && !strings.Contains(got, "  store agency, folder acme: add link "+movedKey+"\n") {
				t.Fatalf("the question must list each link it writes:\n%s", got)
			}
			if wrote := hasLink(t, w, movedKey); wrote != c.writes {
				t.Fatalf("store folder acme links %s: %v, want %v:\n%s", movedKey, wrote, c.writes, got)
			}
			if !hasLink(t, w, "github.com/acme/app") {
				t.Fatalf("the old link must stay:\n%s", got)
			}
			if c.yes && strings.Contains(got, "store links not changed") {
				t.Fatalf("--yes must not leave the links for later:\n%s", got)
			}
			switch {
			case c.writes:
				if !strings.Contains(got, "✓ store agency, folder acme: link updated") {
					t.Fatalf("the output must name the folder updated:\n%s", got)
				}
			case c.asks:
				if !strings.Contains(got, "store links not changed; to write them: lore repo set-url --url "+movedKey+"\n") {
					t.Fatalf("a no must name the command that writes them:\n%s", got)
				}
				// the named command writes them
				if _, err := run(t, app, opsinfra.OpRepoSetURL, `{"url":"`+movedKey+`"}`, false); err != nil {
					t.Fatalf("lore repo set-url --url %s: %v", movedKey, err)
				}
				if !hasLink(t, w, movedKey) {
					t.Fatal("lore repo set-url did not write the link a no left out")
				}
			default:
				if strings.Contains(got, "link updated") || strings.Contains(got, "store links not changed") {
					t.Fatalf("a mirror or fork answer must say nothing about links:\n%s", got)
				}
			}
		})
	}
}

// syncCaptureIn / syncCaptureOut are the empty input and output of the
// test operation syncAddresses runs the pass inside.
type (
	syncCaptureIn  struct{}
	syncCaptureOut struct{}
)

// Text implements op.Texter (nothing to print: the pass writes to w).
func (syncCaptureOut) Text() string { return "" }

// syncAddresses runs the address pass the way `lore sync` does: inside a
// running operation whose caller is a person at a terminal, with or
// without the caller's yes (--yes), so engine.Confirmed sees the real
// caller rather than a test flag.
func syncAddresses(t *testing.T, app *App, yes bool, w *bytes.Buffer, answers string) {
	t.Helper()
	e := op.Define(op.Spec{Name: "test.sync", Summary: "s", Permission: op.PermRead, Access: op.AccessRead},
		func(ctx context.Context, _ syncCaptureIn) (syncCaptureOut, error) {
			app.SyncRepoAddresses(ctx, w, strings.NewReader(answers), true)
			return syncCaptureOut{}, nil
		})
	if _, err := app.Run(context.Background(), e, op.JSONDecoder{Body: []byte(`{}`)}, op.Caller{Interactive: true, Yes: yes}); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

// TestFollowMovedLinksYesAndNonInteractive (issue #23): the caller's yes
// (REST `yes`, --yes) writes the links without asking; a caller that is
// not a terminal and gave no yes gets the list and the command, nothing
// asked and nothing written.
func TestFollowMovedLinksYesAndNonInteractive(t *testing.T) {
	isolate.Parent(t)
	for _, c := range []struct {
		name          string
		terminal, yes bool
		writes        bool
	}{
		{"yes, terminal", true, true, true},
		{"yes, not a terminal", false, true, true},
		{"no yes, not a terminal", false, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if isolate.Run(t) {
				return
			}
			ctx := context.Background()
			w, app, pid := linkWorld(t)
			var out bytes.Buffer
			in := bufio.NewReader(strings.NewReader("y\n")) // never read: no question
			app.followMovedLinks(ctx, &out, in, c.terminal, c.yes, pid, movedKey, []string{movedKey, "github.com/acme/app"})
			got := out.String()
			if strings.Contains(got, "? write") {
				t.Fatalf("asked:\n%s", got)
			}
			if !strings.Contains(got, "  store agency, folder acme: add link "+movedKey+"\n") {
				t.Fatalf("the output must list each link:\n%s", got)
			}
			if wrote := hasLink(t, w, movedKey); wrote != c.writes {
				t.Fatalf("wrote %v, want %v:\n%s", wrote, c.writes, got)
			}
			if !c.writes && !strings.Contains(got, "lore repo set-url --url "+movedKey) {
				t.Fatalf("no command named:\n%s", got)
			}
		})
	}
}
