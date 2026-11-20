package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestUnknownAudienceSaidLoudly (devco D-734): a store of an audience this
// lore does not know is a doctor warning naming the store, its audience,
// this lore's version and the upgrade, never folded into the admins-only
// note (lore 0.1.18 read team:@web as admins-only and said nothing).
func TestUnknownAudienceSaidLoudly(t *testing.T) {
	t.Parallel()
	skipped := []orgconf.Store{
		{URL: "git@example.invalid:acme/ops.git", Audience: orgconf.AudienceAdmins},
		{URL: "git@example.invalid:acme/lore-web.git", Audience: "group:@web"},
	}
	got := unknownAudienceFindings("acme", "0.1.20", skipped)
	if len(got) != 1 {
		t.Fatalf("findings %+v; want one for lore-web", got)
	}
	f := got[0]
	for _, want := range []string{"acme's store lore-web is for group:@web", "this lore 0.1.20 cannot read that audience", "lore upgrade", "lore sync"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q lacks %q", f.Message, want)
		}
	}
	if f.Level != DoctorWarning || f.Fix != "lore upgrade" {
		t.Errorf("level %v fix %q; want a warning fixed by lore upgrade", f.Level, f.Fix)
	}
	if n := skippedStoresNote("acme", knownSkippedStores(skipped)); strings.Contains(n, "lore-web") || !strings.Contains(n, "ops") {
		t.Errorf("admins note %q: want ops only, never the unknown-audience store", n)
	}
	if fx := onboard.FixFor(onboard.CheckOrgSource, "", onboard.FixUnknownAudience); strings.Join(fx.Command, " ") != "lore upgrade" {
		t.Errorf("org.source fix for an unknown audience = %q; want lore upgrade", fx.Command)
	}
}
