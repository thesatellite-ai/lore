package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestSecretsLinkHolder (SEC-LINK1, SECRETS 5.2): `lore secrets link`
// without --holder records this machine (App.ThisHolder: the mch_ id) for
// a personal manager, records no holder for a shared one (op, vault,
// aws-secrets: secrets.holder_for_shared is off, SQ9), and writes a typed
// --holder as given for either.
func TestSecretsLinkHolder(t *testing.T) {
	ctx := background()
	// an internal repo: a client repo refuses every secret reference (BATCH 5)
	a := openWith(t, gitRepo(t), Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	me, _, err := a.ThisHolder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const typed = "act_" + "0123456789abcdef0123456789abcdef"
	for _, c := range []struct {
		name, ref, holder string
		want              string // the holder option written, "" = none
	}{
		{"personal manager, this machine", "env://LORE_DB", "", me},
		{"shared manager, no holder", "op://Acme/Hub/key", "", ""},
		{"shared manager, typed holder", "vault://secret/acme#db", typed, typed},
		{"personal manager, typed holder", "gopass://acme/db", typed, typed},
	} {
		t.Run(c.name, func(t *testing.T) {
			txt, err := value.TextOf("db at ${secret:unstored}")
			if err != nil {
				t.Fatal(err)
			}
			w, err := a.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": txt, "origin_url": txt})
			if err != nil {
				t.Fatal(err)
			}
			id := w.Row.ID.String()
			in := `{"id":"` + id + `","at":1,"ref":"` + c.ref + `"`
			if c.holder != "" {
				in += `,"holder":"` + c.holder + `"`
			}
			if _, err = run(t, a, settings.OpSecretsLink, in+`}`, true); err != nil {
				t.Fatal(err)
			}
			_, r, err := a.Get(ctx, engine.Ref{ID: w.Row.ID})
			if err != nil {
				t.Fatal(err)
			}
			got := r.Fields["name"].String() + " " + r.Fields["origin_url"].String()
			if entry, _, _ := strings.Cut(c.ref, "#"); !strings.Contains(got, "${secret:"+entry) {
				t.Fatalf("not linked: %s", got)
			}
			has := strings.Contains(got, "holder=")
			if c.want == "" && has || c.want != "" && !strings.Contains(got, "holder="+c.want) {
				t.Fatalf("want holder %q: %s", c.want, got)
			}
		})
	}
}
