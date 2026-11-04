// Package machine reads this computer's OS machine id and turns it into
// lore's machine id (model/ids.MachineID), the `m` of the `_w` stamp
// (PROVENANCE_DESIGN 6.2).
//
// PROV4 / XQ4: the id is mch_ plus the FULL HMAC-SHA256 of the OS id with
// the fixed lore key "lore-machine/v1", 64 hex, never truncated; the raw OS
// id never leaves this package (not stored, not printed, not in an error),
// because systemd machine-id(5) calls it confidential and asks applications
// for a keyed, application-specific hash. Sources: Linux /etc/machine-id
// (then /var/lib/dbus/machine-id), macOS IOPlatformUUID (read with
// /usr/sbin/ioreg: no pure-Go path exists and CGO is banned), Windows
// HKLM\SOFTWARE\Microsoft\Cryptography MachineGuid.
//
// CFG11: Load caches the fingerprint (never the raw id) in
// ~/.lore/machine.json on every OS, so the macOS ioreg runs once; a
// missing or broken file is recomputed, so deleting it is always safe.
// With no OS id (many containers) Load makes a random id once (id_source
// random); an ephemeral runtime (CI, Lambda, Cloud Run) gets the
// fingerprint of its runtime key instead (6.8), never cached.
//
//	Load(home, getenv)
//	  ├─ RuntimeKey(getenv) != "" → Derive(key), runtime_key
//	  ├─ ~/.lore/machine.json parses → cached id
//	  ├─ ID() (OS id) → os_machine_id, write cache
//	  └─ 32 random bytes → random, write cache
//	Collect(getenv, fsys) → Profile (profile.go, per-OS readers)
package machine

import (
	"crypto/hmac"
	"crypto/sha256"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Key is the fixed, lore-specific HMAC key (PROV4). Changing it changes
// every machine id, so it is versioned in its text.
const Key = "lore-machine/v1"

// Derive turns a raw OS machine id into lore's machine id. Surrounding
// white space is ignored; an empty id is E_NOT_FOUND (a machine with no
// id must not share the fingerprint of "").
func Derive(osID string) (ids.MachineID, error) {
	raw := strings.TrimSpace(osID)
	if raw == "" {
		return ids.MachineID{}, errs.New(errs.NotFound, "machine: the OS machine id is empty")
	}
	mac := hmac.New(sha256.New, []byte(Key))
	mac.Write([]byte(raw)) // hash.Hash.Write never fails
	return ids.MachineFromDigest(mac.Sum(nil))
}

// ID reads this machine's OS id and derives lore's machine id. E_NOT_FOUND
// when the OS has no readable id (a container without /etc/machine-id, an
// unsupported OS); the error never holds the raw id.
func ID() (ids.MachineID, error) {
	raw, err := osID()
	if err != nil {
		return ids.MachineID{}, err
	}
	return Derive(raw)
}
