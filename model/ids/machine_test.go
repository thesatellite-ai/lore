package ids

import (
	"strings"
	"testing"
)

// TestMachineID is PROV4: mch_ + 64 lowercase hex, Short is 8 hex, a
// prefix needs at least 8 hex.
func TestMachineID(t *testing.T) {
	full := "mch_0cd630f071ab154c3b6a0f44e9366337ec0fb87c4e688d19fc5e84f1bf8dd980"
	m, err := ParseMachine(full)
	if err != nil || m.String() != full || m.Short() != "mch_0cd630f0" {
		t.Fatalf("%v %q %q", err, m, m.Short())
	}
	for _, bad := range []string{"", "mch_", full[:len(full)-1], full + "0", strings.ToUpper(full), "mac_" + full[4:], "mch_" + strings.Repeat("g", 64)} {
		if _, perr := ParseMachine(bad); perr == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for p, want := range map[string]bool{"mch_0cd630f0": true, full: true, "mch_0cd630f": false, "mch_0cd630f1": false, "0cd630f0": false, "mch_0CD630F0": false} {
		if got := m.MatchesPrefix(p); got != want {
			t.Errorf("MatchesPrefix(%q) = %v", p, got)
		}
	}
	var z MachineID
	if !z.IsZero() || z.Short() != "" || z.MatchesPrefix("mch_00000000") {
		t.Fatal("zero id")
	}
	var back MachineID
	if uerr := back.UnmarshalText([]byte(full)); uerr != nil || back != m {
		t.Fatal("text round trip")
	}
	if _, derr := MachineFromDigest(make([]byte, 31)); derr == nil {
		t.Fatal("short digest accepted")
	}
	d, err := MachineFromDigest(make([]byte, 32))
	if err != nil || d.String() != "mch_"+strings.Repeat("0", 64) {
		t.Fatalf("%v %s", err, d)
	}
}
