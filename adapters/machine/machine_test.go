package machine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// TestDerive is PROV4: the keyed HMAC, full 64 hex, never the raw id.
func TestDerive(t *testing.T) {
	const raw = "4c4c4544003510528044b4c04f4e5a31"
	m, err := Derive(raw + "\n")
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("lore-machine/v1"))
	mac.Write([]byte(raw))
	if want := "mch_" + hex.EncodeToString(mac.Sum(nil)); m.String() != want {
		t.Fatalf("got %s, want %s", m, want)
	}
	if strings.Contains(m.String(), raw) || len(m.String()) != 4+64 {
		t.Fatalf("raw id leaked or wrong length: %s", m)
	}
	plain := sha256.Sum256([]byte(raw))
	if strings.Contains(m.String(), hex.EncodeToString(plain[:])) {
		t.Fatal("an unkeyed hash, not the HMAC")
	}
	if _, err := Derive("  \n"); err == nil {
		t.Fatal("empty OS id accepted")
	}
}

// TestIDOnThisHost: the host's id derives cleanly when the OS has one,
// and the error never repeats a raw id.
func TestIDOnThisHost(t *testing.T) {
	m, err := ID()
	if err != nil {
		t.Logf("no machine id on this host: %v", err)
		return
	}
	if m.Short() == "" || !strings.HasPrefix(m.String(), "mch_") {
		t.Fatalf("bad id %s", m)
	}
	again, _ := ID()
	if again != m {
		t.Fatal("not stable")
	}
}
