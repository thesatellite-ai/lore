package wire

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// testInput decodes body as the input of the registry's operation name.
func testInput(t *testing.T, name, body string) op.Input {
	t.Helper()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	e, ok := a.Registry().Lookup(name)
	if !ok {
		t.Fatalf("%s is not in the registry", name)
	}
	in, err := e.Decode(op.JSONDecoder{Body: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// TestNoProjectHintNamesOwnOrg (devco D-551): the "lore isn't set up"
// refusal in an own repo of an org names that org's link with --own and
// the reads that work without it; with no org, or another refusal, the
// error is left as it is.
func TestNoProjectHintNamesOwnOrg(t *testing.T) {
	ctx := context.Background()
	base := noProjectErr(ctx, t.TempDir())
	got := errs.EnvelopeOf(withOwnOrgLink(base, "acme")).Error.Hint
	for _, want := range []string{"lore org link --org acme --project <folder> --own", "lore store folder list --store acme", "--store acme"} {
		if !strings.Contains(got, want) {
			t.Fatalf("hint %q: want %q", got, want)
		}
	}
	if h := errs.EnvelopeOf(withOwnOrgLink(base, "")).Error.Hint; h != noProjectHint {
		t.Fatalf("no org: hint %q, want the plain one", h)
	}
	other := errs.New(errs.NotFound, "x").WithHint("y")
	if withOwnOrgLink(other, "acme") != error(other) {
		t.Fatal("another refusal was changed")
	}
}

// TestRowElsewhereSaysWhere (devco D-552): a read naming a folder that
// does not hold the row says which folder does and the read that finds it,
// never a branch hint (there is no branch outside a repo).
func TestRowElsewhereSaysWhere(t *testing.T) {
	in := testInput(t, "memory.show", `{"id":"mem_01","store":"acme/web"}`)
	err := rowElsewhereErr(in, "acme/web", "acme/sync", "memory.show")
	env := errs.EnvelopeOf(err)
	if !errs.Has(err, errs.NotFound) || !strings.Contains(env.Error.Message, "it is in acme/sync") || env.Error.Hint != "lore memory show --id mem_01 --store acme/sync" {
		t.Fatalf("got %v (hint %q)", err, env.Error.Hint)
	}
}
