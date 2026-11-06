package host

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLiveCommitSignature (later 66, FLAG4): live commits are authored and
// committed by the repo's git identity when git has a complete, valid one
// (user.name and user.email); otherwise by lore's own identity, never an
// empty or half identity that git would refuse or misattribute.
func TestLiveCommitSignature(t *testing.T) {
	lore := liveSignatureFallback.Name + " <" + liveSignatureFallback.Email + ">"
	cases := map[string]struct {
		name, email string // "" = not set
		want        string
	}{
		"git identity wins":   {name: "Ada Lovelace", email: "ada@example.com", want: "Ada Lovelace <ada@example.com>"},
		"no identity: lore":   {want: lore},
		"name only: lore":     {name: "Ada Lovelace", want: lore},
		"email only: lore":    {email: "ada@example.com", want: lore},
		"angle bracket: lore": {name: "Ada <x>", email: "ada@example.com", want: lore},
		"blank name: lore":    {name: " ", email: "ada@example.com", want: lore},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t)
			f.s.release() // the fixture's session was opened before the identity below
			top := f.s.top
			if c.name != "" {
				gitT(t, top, "config", "user.name", c.name)
			}
			if c.email != "" {
				gitT(t, top, "config", "user.email", c.email)
			}
			r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}}
			ctx := withRun(context.Background(), r)
			b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
			if err := b.Write(ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 1, "signed", false)}}); err != nil {
				t.Fatal(err)
			}
			r.release()
			out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top,
				Args: []string{"log", "-1", "--format=%an <%ae>|%cn <%ce>", live.RefLive}})
			if err != nil {
				t.Fatal(err)
			}
			want := c.want + "|" + c.want
			if got := strings.TrimSpace(string(out)); got != want {
				t.Fatalf("live commit signed %q, want %q", got, want)
			}
		})
	}
}
