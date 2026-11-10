package settings_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceSecretAnswers (RES 3.5, SECRETS 2.5/4, BUGFIX-12): a secret in
// a resource value is asked about (E_NEEDS_CONFIRM, nothing written), and
// each answer the hint offers works: --secret-ref stores the value as
// ${secret:<ref>}, --secret-unstored as ${secret:unstored}; the secret is in
// neither the row nor its history.
func TestResourceSecretAnswers(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, ownRepo) // a client repo refuses secret references (BATCH 5)
	h.MustYes(settings.OpKindAdd, `{"kind":"server"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"server","field":"access_key","type":"text"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"server","name":"hub_a"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"server","name":"hub_b"}`)
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in source
	edit := `{"id":"%s","set":["access_key=` + key + `"]}`

	msg := h.Fails(settings.OpResEdit, strings.Replace(edit, "%s", "hub_a", 1), errs.NeedsConfirm)
	if strings.Contains(msg, key) {
		t.Fatalf("the refusal echoes the secret: %s", msg)
	}
	run := func(name string, ch op.SecretChoice) error {
		t.Helper()
		e, ok := h.App.Registry().Lookup(settings.OpResEdit)
		if !ok {
			t.Fatal("no resource.edit")
		}
		actor, err := ids.Parse[ids.Actor](opstest.Actor)
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(strings.Replace(edit, "%s", name, 1))}, op.Caller{Actor: actor, Yes: true, Secret: ch})
		return err
	}
	for _, c := range []struct {
		name, want string
		ch         op.SecretChoice
	}{
		{"hub_a", "${secret:gopass://acme/hub/aws-key}", op.SecretChoice{Refs: []string{"gopass://acme/hub/aws-key"}}},
		{"hub_b", "${secret:unstored}", op.SecretChoice{Unstored: true}},
	} {
		if err := run(c.name, c.ch); err != nil {
			t.Fatalf("%s answer %+v: %v", c.name, c.ch, err)
		}
		got := h.JSON(h.Must(settings.OpResShow, `{"id":"`+c.name+`"}`))
		if strings.Contains(got, key) || !strings.Contains(got, c.want) {
			t.Errorf("%s stored %s, want %s and no secret", c.name, got, c.want)
		}
	}
	// no row file (fields or the history copy) holds the secret, and the
	// resource row files hold the reference (the walk is not vacuous)
	refFiles := 0
	err := filepath.WalkDir(h.Repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), key) {
			t.Errorf("%s holds the secret", p)
		}
		if strings.Contains(string(b), "${secret:gopass://acme/hub/aws-key}") {
			refFiles++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if refFiles == 0 {
		t.Error("no row file holds the --secret-ref reference")
	}
}
