package settings_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestSettingsInTrashMatrix (later 498, DELETION 3.2): every settings
// read or remove that names a trashed row answers E_IN_TRASH, never
// E_NOT_FOUND: meta show of a trashed row,
// meta get / set / unset on a trashed row, secrets allow rm of a trashed
// allow entry; a name that never existed stays E_NOT_FOUND.
func TestSettingsInTrashMatrix(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
	mem := h.ID(h.MustYes("memory.add", `{"body":"x","meta":["client=acme"]}`))
	allow := h.ID(h.MustYes(settings.OpSecretAllow, `{"row_id":"`+mem+`","rule":"acme-rule","field":"body","match_hash":7,"reason":"fixture"}`))
	h.MustYes(settings.OpSecretAllowRm, `{"id":"`+allow+`"}`)
	h.MustYes("memory.delete", `{"id":"`+mem+`"}`)

	cases := []struct {
		name, op, input string
		yes             bool
		code            errs.Code
	}{
		{"meta show trashed row", settings.OpMetaShow, `{"id":"` + mem + `"}`, false, errs.InTrash},
		{"meta get trashed row", settings.OpMetaGet, `{"id":"` + mem + `","key":"client"}`, false, errs.InTrash},
		{"meta unset trashed row", settings.OpMetaUnset, `{"id":"` + mem + `","key":"client"}`, true, errs.InTrash},
		{"secrets allow rm trashed", settings.OpSecretAllowRm, `{"id":"` + allow + `"}`, true, errs.InTrash},
		{"meta show never", settings.OpMetaShow, `{"id":"never_was"}`, false, errs.NotFound},
	}
	for _, c := range cases {
		if c.yes {
			failsYes(t, h, c.op, c.input, c.code)
			continue
		}
		h.Fails(c.op, c.input, c.code)
	}
}
