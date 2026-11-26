package formatgate

import (
	"strings"
	"testing"
)

// TestOrgFloorDevBuild (devco:D-735): an org that declares the lore
// version its stores need turns an older stamped lore read-only with the
// version and the upgrade, while a source build of this tree (stamped
// "dev", knowing no feature newer than 0.1.18) still writes there: it
// counts as SourceRelease.
func TestOrgFloorDevBuild(t *testing.T) {
	floor := Declared{Features: []string{HLC, Meta}, MinLoreVersion: SourceRelease}
	for _, bin := range []string{"dev", "", "0.1.0-dev", "v" + SourceRelease + "-3-gabc1234"} {
		if v := Check(bin, floor); !v.Writable {
			t.Errorf("%q: a source build of this tree is refused by the org floor %s: %v", bin, SourceRelease, v.Err())
		}
		if !MayWrite(bin, SourceRelease) {
			t.Errorf("MayWrite(%q, %s) = false", bin, SourceRelease)
		}
	}
	knownBefore(t, VerifiedEpochs) // a stamped 0.1.19: no feature from later releases
	v := Check("0.1.19", Declared{MinLoreVersion: "0.1.20"})
	if v.Writable {
		t.Fatal("lore 0.1.19 may write a folder that needs 0.1.20")
	}
	if msg := v.Err().Error(); !strings.Contains(msg, "needs lore 0.1.20 or later to write (this is 0.1.19)") || !strings.Contains(msg, "Reading works") {
		t.Errorf("refusal = %q", msg)
	}
	if MayWrite("dev", "0.1.99") || MayWrite("0.1.19", "0.1.20") {
		t.Error("MayWrite passes a floor above the binary")
	}
}

// TestValidVersion: only a plain release version may be declared as an
// org floor (an unparseable floor is ignored by every lore).
func TestValidVersion(t *testing.T) {
	for v, want := range map[string]bool{"0.1.20": true, "v0.1.20": true, "1.2": true, "soon": false, "": false, "0.1.20-dev": false, "0.1.x": false} {
		if got := ValidVersion(v); got != want {
			t.Errorf("ValidVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

// knownBefore makes this binary know only the features before name for
// the rest of the test (HideFeaturesFrom).
func knownBefore(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(HideFeaturesFrom(name))
}
