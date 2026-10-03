package ids

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// naturalKeySeparator joins natural-key parts before hashing. A NUL byte can
// never appear inside a validated identifier or name, so ("ab","c") and
// ("a","bc") always hash differently.
const naturalKeySeparator = "\x00"

// uuidVersion7 / uuidVariantRFC4122 are the bit patterns Deterministic forces
// into the hashed bytes so the result passes Validate (which insists on a
// UUIDv7 tail).
const (
	uuidVersion7       = 0x70
	uuidVersionMask    = 0x0f
	uuidVariantRFC4122 = 0x80
	uuidVariantMask    = 0x3f
	// uuidByteLen is the number of hash bytes a UUID tail needs (128 bits).
	uuidByteLen = 16
)

// Deterministic returns an opaque ID whose tail is derived from a hash of the
// row's natural key instead of the clock + RNG.
//
// Why it exists: lore data is shared between developers through git (one
// JSON file per row, see LORE_SYNC_SPEC.md). Rows that carry a natural
// unique key — a tag (project_id, name), a repo (project_id, mount_name), an
// actor (stable_key), … — would otherwise get a DIFFERENT random id on each
// machine that creates "the same" row, and the two files would collide on the
// unique index when merged. With a deterministic id both machines write the
// same path with the same id, and git merges the identical adds cleanly.
//
// Invariants:
//   - Same (prefix, parts) always yields the same id; any change to a part
//     yields a different id.
//   - The tail is shaped as a UUIDv7 (version + variant bits forced), so it
//     passes Validate / ValidateAny. Its embedded "timestamp" is meaningless
//     — never call IDToTime on a deterministic id expecting a creation time;
//     created_at is the source of truth for ordering.
//   - parts must be the natural key in schema order; callers must not mix
//     orders between call sites.
func Deterministic(prefix string, parts ...string) (string, error) {
	if err := validatePrefix(prefix); err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("ids: Deterministic needs at least one natural-key part")
	}
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte(naturalKeySeparator))
		}
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)[:uuidByteLen]
	sum[6] = (sum[6] & uuidVersionMask) | uuidVersion7
	sum[8] = (sum[8] & uuidVariantMask) | uuidVariantRFC4122
	return prefix + "_" + hex.EncodeToString(sum), nil
}
