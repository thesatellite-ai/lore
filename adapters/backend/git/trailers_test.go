package git

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// OV6/OV3 (FQ7): every commit lore makes itself — a batch and the merge
// of a concurrent writer — carries the configured trailers; blank and
// multi-line trailers are dropped.
func TestCommitTrailers(t *testing.T) {
	noGitIdentity(t)
	who := WithIdentity(func(context.Context) Signature { return Signature{Name: "L", Email: "l@example.invalid"} })
	tr := WithTrailers("Lore-Version: 0.1.18", "", "Bad: a\nb", "Lore-CI-Run: github run 1")
	remote := newRemote(t, true)
	a, b, g := clone(t, remote, who, tr), clone(t, remote, who, tr), newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "a", hlc.HLC{})})
	syncOK(t, a)
	write(t, b, store.Change{Row: g.row(newID(t), "b", hlc.HLC{})})
	syncOK(t, b)
	want := "Lore-Version: 0.1.18\nLore-CI-Run: github run 1"
	for _, rev := range []string{"HEAD", "HEAD^1"} {
		body := gitT(t, b.Dir(), "log", "-1", "--format=%(trailers:only,unfold)", rev)
		if strings.TrimSpace(body) != want {
			t.Fatalf("%s trailers %q, want %q", rev, body, want)
		}
	}
	if got := (config{}).withTrailers("subj"); got != "subj" {
		t.Fatalf("no trailers changed the message: %q", got)
	}
}
