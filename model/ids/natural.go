package ids

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// Natural ids (ported from saas/pkg/aicoder/ids/deterministic.go, v0.1.17):
// an id derived from a key instead of the clock and randomness, so two
// machines creating "the same" thing (a tag, an actor, a store folder's
// project) write the same id and converge. The bytes are exactly
// v0.1.17's.
const (
	// naturalKeySeparator joins key parts before hashing (a NUL can never
	// be inside a name, so ("ab","c") and ("a","bc") differ).
	naturalKeySeparator = "\x00"
	uuidVersion7        = 0x70
	uuidVersionMask     = 0x0f
	uuidVariantRFC4122  = 0x80
	uuidVariantMask     = 0x3f
	uuidByteLen         = 16
	uuidVersionByte     = 6
	uuidVariantByte     = 8
	// naturalSep joins the prefix and the hex digits.
	naturalSep = "_"
)

// errNoKeyParts: a natural id needs at least one key part.
var errNoKeyParts = errors.New("ids: a natural id needs at least one key part")

// Natural is the v0.1.17 deterministic id of prefix p for the key parts:
// sha256 of the parts joined by NUL, cut to 16 bytes with the UUIDv7
// version and RFC 4122 variant bits set.
func Natural(p Prefix, parts ...string) (AnyID, error) {
	if len(parts) == 0 {
		return AnyID{}, errNoKeyParts
	}
	h := sha256.New()
	h.Write([]byte(strings.Join(parts, naturalKeySeparator)))
	sum := h.Sum(nil)[:uuidByteLen]
	sum[uuidVersionByte] = (sum[uuidVersionByte] & uuidVersionMask) | uuidVersion7
	sum[uuidVariantByte] = (sum[uuidVariantByte] & uuidVariantMask) | uuidVariantRFC4122
	return ParseAny(string(p) + naturalSep + hex.EncodeToString(sum))
}
