package ids

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// MachineID is the id of the machine that wrote a row's current content
// (the `m` of the `_w` stamp, PROVENANCE_DESIGN 6.2): "mch_" plus 64
// lowercase hex characters.
//
// PROV4 / XQ4: the id is the FULL keyed fingerprint (HMAC-SHA256 of the OS
// machine id, adapters/machine), never truncated in stored data and never
// the raw OS id, because systemd machine-id(5) says the raw id is
// confidential and must only be used through a keyed, application-specific
// hash. The full length means two machines never share an id, so there is
// no clash rule. It is not an ID[Machine]: row ids are 32-hex UUIDv7s, this is a
// 64-hex digest with no time inside.
//
// Invariant: a non-zero MachineID always holds a valid id. The zero value
// is "no machine" and encodes as the empty string.
type MachineID struct {
	s string
}

// Machine id text form facts.
const (
	// MachinePrefix starts every machine id.
	MachinePrefix = string(PrefixMachine) + "_"
	// machineHexLen is the hex length of the full fingerprint (SHA-256).
	machineHexLen = 64
	// MachineShortHex is how many hex characters the short form shows,
	// and the shortest prefix commands accept (PROV4: like a git short
	// hash, `mch_0cd630f0`).
	MachineShortHex = 8
)

// ParseMachine validates s as a full machine id.
func ParseMachine(s string) (MachineID, error) {
	hex, ok := strings.CutPrefix(s, MachinePrefix)
	if !ok || len(hex) != machineHexLen || !isLowerHex(hex) {
		return MachineID{}, errs.New(errs.InvalidID, "ids: "+quote(s)+" is not mch_<64 lowercase hex>")
	}
	return MachineID{s: s}, nil
}

// MachineFromDigest builds a machine id from a 32-byte digest (the HMAC
// adapters/machine computes). Any other length is E_INVALID_ID.
func MachineFromDigest(sum []byte) (MachineID, error) {
	const hexDigits = "0123456789abcdef"
	if len(sum)*2 != machineHexLen {
		return MachineID{}, errs.New(errs.InvalidID, "ids: a machine digest must be 32 bytes")
	}
	var b strings.Builder
	b.Grow(len(MachinePrefix) + machineHexLen)
	b.WriteString(MachinePrefix)
	for _, c := range sum {
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0x0f])
	}
	return MachineID{s: b.String()}, nil
}

// String returns the full text form ("" for the zero id).
func (m MachineID) String() string { return m.s }

// IsZero reports whether m is "no machine".
func (m MachineID) IsZero() bool { return m.s == "" }

// Short is the display form: mch_ plus the first MachineShortHex hex
// characters (PROV4). Stored data always keeps the full id.
func (m MachineID) Short() string {
	if m.s == "" {
		return ""
	}
	return m.s[:len(MachinePrefix)+MachineShortHex]
}

// MatchesPrefix reports whether p names m: p is mch_ plus at least
// MachineShortHex hex characters and m starts with it (PROV4: commands
// accept any unique prefix of at least 8 characters; uniqueness is the
// caller's check over the machines it knows).
func (m MachineID) MatchesPrefix(p string) bool {
	hex, ok := strings.CutPrefix(p, MachinePrefix)
	return ok && !m.IsZero() && len(hex) >= MachineShortHex && isLowerHex(hex) && strings.HasPrefix(m.s, p)
}

// MarshalText implements encoding.TextMarshaler.
func (m MachineID) MarshalText() ([]byte, error) { return []byte(m.s), nil }

// UnmarshalText implements encoding.TextUnmarshaler; empty text is the
// zero id.
func (m *MachineID) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*m = MachineID{}
		return nil
	}
	got, err := ParseMachine(string(b))
	if err != nil {
		return err
	}
	*m = got
	return nil
}

// isLowerHex reports whether s is non-empty lowercase hex.
func isLowerHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
