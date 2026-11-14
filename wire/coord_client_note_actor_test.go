package wire

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestClientNoteOpenFailedFailsClosed (run:team:rowhome-failopen): the old
// team build routed an internal row home to the client repo when the org
// world could not be opened. Today's shape of it was clientTaskNote
// dropping the openWorld error and letting the note fall through to a
// write into the repo. Now: no git repository falls through (no client
// repo exists); an actor that is neither a person nor an agent falls
// through only when the repo is known not to be a client repo, and is
// refused (E_IDENTITY_FAILED) in a client repo or one whose check failed
// (repoIsClient fails closed); any other open failure is returned. (In an
// org repo LORE_ACTOR=cron:… is ignored for the org identity, so the
// refusal is a guard, pinned here at the decision.)
func TestClientNoteOpenFailedFailsClosed(t *testing.T) {
	ctx := context.Background()
	noPerson := errs.New(errs.IdentityFailed, "coordination needs an agent actor or a person on the roster")
	clientIs := func(client bool, err error) standaloneCoord {
		return standaloneCoord{a: &App{opts: Options{RepoIsClient: func(context.Context) (bool, error) { return client, err }}}}
	}
	for _, c := range []struct {
		name     string
		h        standaloneCoord
		open     error
		done     bool
		wantCode errs.Code
	}{
		{"no git repository", clientIs(true, nil), errs.New(errs.Unsupported, "coordination needs a git repository"), false, ""},
		{"non-person, own repo", clientIs(false, nil), noPerson, false, ""},
		{"non-person, client repo", clientIs(true, nil), noPerson, true, errs.IdentityFailed},
		{"non-person, check failed", clientIs(false, errors.New("org config unreadable")), noPerson, true, errs.IdentityFailed},
		{"non-person, no check wired", standaloneCoord{a: &App{}}, noPerson, true, errs.IdentityFailed},
		{"link unknown", clientIs(false, nil), errs.New(errs.LinkUnknown, "cannot tell whether this repo is a client repo"), true, errs.LinkUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, done, err := c.h.clientNoteOpenFailed(ctx, c.open)
			if done != c.done {
				t.Fatalf("done %v, want %v (err %v)", done, c.done, err)
			}
			if c.wantCode == "" {
				if err != nil {
					t.Fatalf("falls through, got %v", err)
				}
				return
			}
			if !errs.Has(err, c.wantCode) {
				t.Fatalf("err %v, want %s", err, c.wantCode)
			}
		})
	}
}
