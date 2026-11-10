package settings_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaMoveLevelMatrix (later 7, OV58-2 / FB-META-5): meta move goes
// strictly outward in the level order (repo -> folder -> org -> store
// root): repo->store root and folder->org move; org->folder, a folder to
// another store's folder, and store root->org are E_INVALID_INPUT; a name
// clash in the target is E_META_TAKEN before the level check.
func TestMetaMoveLevelMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, from, to string
		clash          bool
		code           errs.Code // "" = moved
	}{
		{"repo to store root", "repo", "kb", false, ""},
		{"folder to org", "kb/web", "kb/org", false, ""},
		{"org to folder", "kb/org", "kb/web", false, errs.InvalidInput},
		{"folder to another folder", "kb/web", "agency", false, errs.InvalidInput},
		{"store root to org", "kb", "kb/org", false, errs.InvalidInput},
		{"folder to folder with a clash", "kb/web", "agency", true, errs.MetaTaken},
		{"org to folder with a clash", "kb/org", "kb/web", true, errs.MetaTaken},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := multiStore(t)
			key := "mk_" + strconv.Itoa(i)
			id := h.ID(h.MustYes(settings.OpMetaAdd, `{"name":"`+key+`","type":"text","to":"`+c.from+`"}`))
			if c.clash {
				h.MustYes(settings.OpMetaAdd, `{"name":"`+key+`","type":"text","to":"`+c.to+`"}`)
			}
			in := `{"id":"` + id + `","to":"` + c.to + `"}`
			if c.code != "" {
				failsYes(t, h, settings.OpMetaMove, in, c.code)
				return
			}
			h.MustYes(settings.OpMetaMove, in)
			keys := h.JSON(h.Must(settings.OpMetaKeys, `{"keys":["`+key+`"]}`))
			if !strings.Contains(keys, id) || !strings.Contains(keys, `"store":"`+c.to+`"`) {
				t.Fatalf("after move %s", keys)
			}
		})
	}
}
