package wire

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// TestClientRepoWriteRefusalNamesCause (devco N-8): a write to the repo
// store of a client repo linked by its remote (policy set's default --to
// repo) was refused "store: attached read-only", naming no store, no cause
// and no next step. It now names the repo's remote, why lore writes
// nothing there, and a --to to use instead; it stays E_STORE_READ_ONLY.
func TestClientRepoWriteRefusalNamesCause(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	app := newTestAppWith(t, repo, func(o *Options) {
		o.RepoLinkedByRemote = func(context.Context) string { return "git.example.test/client/shop" }
	})
	st, err := app.state(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.linkedByRemote == "" {
		t.Fatal("the repo is not read as linked by its remote")
	}
	werr := st.backends[0].Write(context.Background(), nil) // backends[0] is the repo store (open.go)
	if !errors.Is(werr, store.ErrReadOnly) {
		t.Fatalf("write to a linked client repo: %v, want E_STORE_READ_ONLY", werr)
	}
	var le *errs.Error
	if !errors.As(werr, &le) {
		t.Fatalf("not a typed error: %v", werr)
	}
	msg := le.Message + " " + le.Hint
	for _, want := range []string{"git.example.test/client/shop", "client repo", "--to "} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q lacks %q", msg, want)
		}
	}
	if strings.Contains(le.Message, "attached read-only") { // the CLI prints Message, not the cause chain
		t.Errorf("refusal is still the bare store error: %q", werr)
	}
}

// TestClientRepoWriteRefusalHint: the hint names the linked folder's
// attached name when there is one; for a link whose folder is not
// attached, the link's own --to (devco:D-670: it printed "--to
// <store>/<folder>", a placeholder where the link names the value); and
// with no link known here, whoami, with no --to at all.
func TestClientRepoWriteRefusalHint(t *testing.T) {
	var e *errs.Error
	if !errors.As(clientRepoWriteRefusal("git.example.test/c/r", "acme", "", false), &e) || !strings.Contains(e.Hint, "--to acme") {
		t.Fatalf("linked hint: %+v", e)
	}
	if !errors.As(clientRepoWriteRefusal("git.example.test/c/r", "", "kb-acme/web", false), &e) || !strings.Contains(e.Hint, "--to kb-acme/web") || strings.ContainsAny(e.Hint, "<>") {
		t.Fatalf("not-attached hint: %+v", e)
	}
	if !errors.As(clientRepoWriteRefusal("git.example.test/c/r", "", "", false), &e) || !strings.Contains(e.Hint, "lore whoami") || strings.ContainsAny(e.Hint, "<>") || strings.Contains(e.Hint, "--to") {
		t.Fatalf("unknown-link hint: %+v", e)
	}
}

// TestLinkedRepoNotAttachedNamesTheLink is devco:D-670 through open: a
// repo a store links by its remote, whose linked folder the org does not
// attach here, is refused naming the link's --to from RepoLinkTarget.
func TestLinkedRepoNotAttachedNamesTheLink(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	app := newTestAppWith(t, repo, func(o *Options) {
		o.RepoLinkedByRemote = func(context.Context) string { return "git.example.test/acme/web" }
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
		o.RepoLinkTarget = func(context.Context) string { return "kb-acme/web" }
	})
	st, err := app.state(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	werr := st.backends[0].Write(context.Background(), nil)
	var le *errs.Error
	if !errors.Is(werr, store.ErrReadOnly) || !errors.As(werr, &le) {
		t.Fatalf("write to a linked repo: %v, want E_STORE_READ_ONLY", werr)
	}
	if !strings.Contains(le.Hint, "--to kb-acme/web") || strings.ContainsAny(le.Hint, "<>") {
		t.Errorf("devco:D-670: hint %q does not name the link's --to kb-acme/web", le.Hint)
	}
}

// TestOwnLinkedRepoWriteRefusalIsNotClient (devco:V-3): `lore policy set`
// in an own repo linked to a store folder (`lore org link --own`) was
// refused calling it "a client repo". The refusal now says it is an own
// repo whose notes live in the linked folder, names the folder's --to, and
// never says "client repo"; a repo the hook calls a client keeps the
// client wording.
func TestOwnLinkedRepoWriteRefusalIsNotClient(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client bool
		want   string
		never  string
	}{
		{"own repo", false, "an own repo whose notes live in its linked store folder", "client repo"},
		{"client repo", true, "client repo", "own repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			app := newTestAppWith(t, repo, func(o *Options) {
				o.RepoLinkedByRemote = func(context.Context) string { return "git.example.test/acme/web" }
				o.RepoIsClient = func(context.Context) (bool, error) { return tc.client, nil }
				o.RepoLinkTarget = func(context.Context) string { return "kb-acme/web" } // the link's folder, not attached (D-670)
			})
			st, err := app.state(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var le *errs.Error
			if werr := st.backends[0].Write(context.Background(), nil); !errors.As(werr, &le) || !errors.Is(werr, store.ErrReadOnly) {
				t.Fatalf("write: %v, want E_STORE_READ_ONLY", werr)
			}
			if !strings.Contains(le.Message, tc.want) || strings.Contains(le.Message, tc.never) || !strings.Contains(le.Hint, "--to kb-acme/web") {
				t.Fatalf("refusal %q / %q: want %q, never %q, the link's --to hint", le.Message, le.Hint, tc.want, tc.never)
			}
		})
	}
}
