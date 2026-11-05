package wire_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// purgeExempt are the `.delete` operations without op.PurgeFlags, each
// with the reason. Every other delete must embed them, so a new noun's
// delete gets --purge / --confirm on CLI, `lore api` and cmd/lore at once
// (P3-DEL-1 ONE-PATH follow-up).
var purgeExempt = map[string]string{
	"actor.delete":         "always refused: an actor is identity (P3-G-AUDIT), nothing to purge",
	"task.template.delete": "addressed by name with the show input; v0.1.17 had no --purge on it",
	"task.view.delete":     "addressed by name with the show input; v0.1.17 had no --purge on it",
}

// TestDeleteOpsTakePurgeFlags: every registered delete operation takes the
// shared purge and confirm inputs (the CLI flags and the REST fields come
// from them), and no exemption is stale.
func TestDeleteOpsTakePurgeFlags(t *testing.T) {
	h := opstest.New(t)
	for _, e := range h.App.Registry().Entries() {
		name := e.Spec().Name
		if !strings.HasSuffix(name, ".delete") {
			continue
		}
		has := map[string]bool{}
		for _, f := range e.InputFields() {
			has[f.Name+"/"+f.Flag] = true
		}
		got := has["purge/purge"] && has["confirm/confirm"]
		_, exempt := purgeExempt[name]
		switch {
		case !got && !exempt:
			t.Errorf("%s: no purge/confirm input (embed op.PurgeFlags in its input, P3-DEL-1)", name)
		case got && exempt:
			t.Errorf("%s: takes purge/confirm now; drop it from purgeExempt", name)
		}
	}
}

// TestDeletePurgeMatrix is the --purge / --confirm matrix through the
// registry (the path `lore api` and cmd/lore take, no CLI wrapper), ported
// from the old binary's TestResourceMetaDeletePurgeConfirm: purge alone,
// and purge with yes, refuse with E_NEEDS_CONFIRM and leave the row;
// confirm alone is a soft delete with the purge note (the row is in the
// trash); purge with confirm tombstones ("purged for good", the row is not
// in the trash). The bulk form's refusal names "every matching row".
func TestDeletePurgeMatrix(t *testing.T) {
	h := opstest.New(t)
	h.Must("resource.kind.add", `{"kind":"host"}`)
	h.Must("resource.field.add", `{"kind":"host","field":"provider","type":"text"}`)
	for _, n := range []string{"h_soft", "h_gone"} {
		h.Must("resource.add", `{"kind":"host","name":"`+n+`"}`)
	}
	h.Must("resource.add", `{"kind":"host","name":"h_bulk","set":["provider=x"]}`)
	h.Must("meta.add", `{"name":"k_soft","type":"text"}`)
	h.Must("meta.add", `{"name":"k_gone","type":"text"}`)
	memSoft := h.ID(h.Must("memory.add", `{"body":"soft one"}`))
	memGone := h.ID(h.Must("memory.add", `{"body":"gone one"}`))

	cases := []struct{ op, soft, gone string }{
		{"memory.delete", memSoft, memGone},
		{"resource.delete", "h_soft", "h_gone"},
		{"meta.delete", "k_soft", "k_gone"},
	}
	for _, c := range cases {
		purgeOnly := `{"id":"` + c.gone + `","purge":true}`
		msg := h.Fails(c.op, purgeOnly, errs.NeedsConfirm)
		if want := "--purge removes " + c.gone + " for good, without the trash"; !strings.Contains(msg, want) {
			t.Errorf("%s purge alone: %q lacks %q", c.op, msg, want)
		}
		_, err := h.RunYes(c.op, purgeOnly)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.NeedsConfirm || !strings.Contains(e.Hint, "--confirm") {
			t.Errorf("%s purge + yes: %v, want E_NEEDS_CONFIRM with a hint naming --confirm (DQ2: --yes never purges)", c.op, err)
		}
		// confirm alone, without yes: answers the question (DQ8), soft.
		soft := opstest.Text(h.Must(c.op, `{"id":"`+c.soft+`","confirm":true}`))
		if !strings.Contains(soft, "moved to the trash") || !strings.Contains(soft, "lore trash purge <id> --confirm") {
			t.Errorf("%s confirm: want a soft delete with the purge note, got %q", c.op, soft)
		}
		res := h.Must(c.op, `{"id":"`+c.gone+`","purge":true,"confirm":true}`)
		if got := opstest.Text(res); !strings.Contains(got, "✓ purged for good: ") || strings.Contains(got, "moved to the trash") {
			t.Errorf("%s purge + confirm: want the purged line, got %q", c.op, got)
		}
		if strings.Contains(h.JSON(res), "purged for good") {
			t.Errorf("%s purge + confirm: the JSON form was edited: %s", c.op, h.JSON(res))
		}
		// A plain delete's line is untouched.
		if c.op == "memory.delete" {
			plain := h.ID(h.Must("memory.add", `{"body":"plain one"}`))
			got := opstest.Text(h.MustYes(c.op, `{"id":"`+plain+`"}`))
			if strings.Contains(got, "note:") || strings.Contains(got, "purged") {
				t.Errorf("plain delete changed: %q", got)
			}
		}
	}
	msg := h.Fails("resource.delete", `{"kind":"host","where":["provider=x"],"purge":true}`, errs.NeedsConfirm)
	if !strings.Contains(msg, "every matching row") {
		t.Errorf("bulk purge alone: %q lacks every matching row", msg)
	}
	if got := opstest.Text(h.Must("resource.delete", `{"kind":"host","where":["provider=x"],"purge":true,"confirm":true}`)); !strings.Contains(got, "purged for good") {
		t.Errorf("bulk purge + confirm: %q", got)
	}
	list := h.JSON(h.Must(trash.OpList, `{}`))
	for _, want := range []string{memSoft, "h_soft", "k_soft"} {
		if !strings.Contains(list, want) {
			t.Errorf("trash lacks the soft-deleted %s:\n%s", want, list)
		}
	}
	for _, gone := range []string{memGone, "h_gone", "k_gone", "h_bulk"} {
		if strings.Contains(list, gone) {
			t.Errorf("purged %s is still in the trash:\n%s", gone, list)
		}
	}
}
