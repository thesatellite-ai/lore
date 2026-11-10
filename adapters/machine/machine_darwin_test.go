//go:build darwin

package machine

import "testing"

// TestParseIoreg reads the IOPlatformUUID line of ioreg's output.
func TestParseIoreg(t *testing.T) {
	out := []byte("+-o J314sAP  <class IOPlatformExpertDevice>\n    {\n      \"IOPlatformUUID\" = \"0A1B2C3D-0000-1111-2222-333344445555\"\n    }\n")
	if got, err := parseIoreg(out); err != nil || got != "0A1B2C3D-0000-1111-2222-333344445555" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := parseIoreg([]byte("nothing")); err == nil {
		t.Fatal("no uuid accepted")
	}
}
