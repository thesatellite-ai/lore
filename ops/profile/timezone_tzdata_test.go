package profile

import (
	"os"
	"os/exec"
	"testing"
)

// tzChildEnv marks the child run of TestTimezoneNeedsNoSystemZoneData.
const tzChildEnv = "LORE_TZ_CHILD"

// TestTimezoneNeedsNoSystemZoneData: an IANA name is accepted with no zone
// database on the machine but lore's own. The child runs with GOROOT
// pointing at an empty folder and no ZONEINFO, as on a Windows machine
// without Go (before, "Europe/Istanbul is not an IANA zone name" there).
func TestTimezoneNeedsNoSystemZoneData(t *testing.T) {
	if os.Getenv(tzChildEnv) == "1" {
		if _, err := checkTimezone("Europe/Istanbul"); err != nil {
			t.Fatal(err)
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestTimezoneNeedsNoSystemZoneData$", "-test.count=1")
	cmd.Env = append(os.Environ(), tzChildEnv+"=1", "GOROOT="+t.TempDir(), "ZONEINFO=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
}
