package row_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/wire"
)

// TestMoveQuestionMatrix (CLI_REFERENCE 2.7, G-ENGINE-4/5): without a
// terminal and without --yes, `lore move` answers E_NEEDS_CONFIRM whose
// message is the preview: the target and its class, the row's title with
// "id kept" and where it is now, every metadata value that travels (a
// sensitive one masked), and per key whether the target already holds the
// key row ("reused key") or it stays where it is; a row without metadata
// has neither section. Nothing moves.
func TestMoveQuestionMatrix(t *testing.T) {
	h := opstest.WithStores(t, []string{"me", "kb"}, func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil } // an own repo
	})
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"vpn_host","type":"text","sensitive":true}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"team","type":"text","to":"kb"}`)
	withMeta := h.ID(h.Must("memory.add", `{"body":"has metadata","meta":["client=acme","vpn_host=probe-vpn-7","team=core"]}`))
	plain := h.ID(h.Must("memory.add", `{"body":"plain row"}`))
	cases := []struct {
		name, id, to string
		want, never  []string
	}{
		{"internal store with metadata", withMeta, "kb",
			[]string{"visible to everyone with access to kb (internal)", `"has metadata" (` + withMeta + `, id kept), now in repo`,
				"metadata that travels:", `client: "acme"`, "vpn_host: •••", "keys in kb:", "client  key mky_", "stays in repo (no new key is created)",
				"team  reused key mky_", "Move it?"},
			[]string{"probe-vpn-7", "kept back"}},
		{"personal store", plain, "me",
			[]string{"visible to everyone with access to me (personal)", `"plain row" (` + plain + `, id kept)`, "Move it?"},
			[]string{"metadata that travels", "keys in"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := h.Fails(row.OpMove, `{"id":"`+c.id+`","to":"`+c.to+`"}`, errs.NeedsConfirm)
			for _, w := range c.want {
				if !strings.Contains(msg, w) {
					t.Errorf("question lacks %q:\n%s", w, msg)
				}
			}
			for _, n := range c.never {
				if strings.Contains(msg, n) {
					t.Errorf("question holds %q:\n%s", n, msg)
				}
			}
			if got := showJSON(t, h, `{"id":"`+c.id+`"}`); got.Store != "repo" {
				t.Fatalf("an unconfirmed move moved the row to %s", got.Store)
			}
		})
	}
	// a row that does not exist and a bad id answer before anything is asked
	h.Fails(row.OpMove, `{"id":"mem_0e45506e64357c94a87e70ef1c6bb0ff","to":"kb"}`, errs.NotFound)
	h.Fails(row.OpMove, `{"id":"not-an-id","to":"kb"}`, errs.InvalidID) // lookup.BadID (B-ERRS-1)
}

// TestMoveQuestionIntoClientRepo: a move into a client repo says what
// stays back, and names --drop-internal and --with-children when given.
func TestMoveQuestionIntoClientRepo(t *testing.T) {
	h := opstest.WithStores(t, []string{"kb"}, func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return true, nil }
	})
	task := h.ID(h.MustYes("task.add", `{"title":"for the client","to":"kb"}`))
	msg := h.Fails(row.OpMove, `{"id":"`+task+`","to":"repo","drop_internal":true,"with_children":true}`, errs.NeedsConfirm)
	for _, w := range []string{"visible to everyone with access to repo (client)", "kept back:", "--drop-internal", "--with-children", "Move it?"} {
		if !strings.Contains(msg, w) {
			t.Errorf("question lacks %q:\n%s", w, msg)
		}
	}
}

// TestGenericQuestionNamesIDsOnly (G-ENGINE-4): an always-ask operation
// without its own question asks "<noun> <verb> <ids>?" about a row that
// exists, answers E_NOT_FOUND for an id that names no row and E_IN_TRASH
// for a trashed one before asking, and never repeats free text (a --where
// filter, a regex) in its question.
func TestGenericQuestionNamesIDsOnly(t *testing.T) {
	h := opstest.New(t, func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
	})
	live := h.ID(h.Must("memory.add", `{"body":"keep probe-free-text"}`))
	msg := h.Fails("memory.delete", `{"id":"`+live+`"}`, errs.NeedsConfirm)
	if !strings.Contains(msg, "memory delete "+live+"?") {
		t.Fatalf("generic question: %s", msg)
	}
	h.Fails("memory.delete", `{"id":"mem_0e45506e64357c94a87e70ef1c6bb0ff"}`, errs.NotFound)
	gone := h.ID(h.Must("memory.add", `{"body":"to the trash"}`))
	h.MustYes("memory.delete", `{"id":"`+gone+`"}`)
	h.Fails("memory.delete", `{"id":"`+gone+`"}`, errs.InTrash)
	if msg = h.Fails("memory.delete", `{"where":["body~=probe-free-text"]}`, errs.NeedsConfirm); strings.Contains(msg, "probe-free-text") {
		t.Fatalf("the --where text reached the question: %s", msg)
	}
	msg = h.Fails(settings.OpSecretRuleAdd, `{"id":"acme-token","regex":"acme_probe[a-z0-9]{32}"}`, errs.NeedsConfirm)
	if strings.Contains(msg, "acme_probe") || !strings.Contains(msg, "Add secret rule acme-token in repo?") {
		t.Fatalf("secrets rule question: %s", msg)
	}
	msg = h.Fails(settings.OpSecretRuleOff, `{"id":"acme-token","reason":"probe-reason-text"}`, errs.NeedsConfirm)
	if strings.Contains(msg, "probe-reason-text") {
		t.Fatalf("the reason reached the question: %s", msg)
	}
}
