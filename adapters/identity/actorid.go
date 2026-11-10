package identity

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// UUID bits forced into a deterministic id so it parses as a UUIDv7 tail
// (ported from saas/pkg/aicoder/ids/deterministic.go).
const (
	uuidVersion7       = 0x70
	uuidVersionMask    = 0x0f
	uuidVariantRFC4122 = 0x80
	uuidVariantMask    = 0x3f
	uuidByteLen        = 16
	uuidVersionByte    = 6
	uuidVariantByte    = 8
	// keySep joins natural-key parts (a NUL never occurs inside one).
	keySep = "\x00"
	idSep  = "_"
)

// Deterministic is the v0.1.17 natural id: prefix_ + a UUIDv7-shaped hash of
// the natural-key parts, so two machines (and both lore versions) creating
// "the same" row name it with the same id.
func Deterministic(prefix ids.Prefix, parts ...string) (ids.AnyID, error) {
	if len(parts) == 0 {
		return ids.AnyID{}, errs.New(errs.Internal, "identity: a natural id needs at least one part")
	}
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte(keySep))
		}
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)[:uuidByteLen]
	sum[uuidVersionByte] = (sum[uuidVersionByte] & uuidVersionMask) | uuidVersion7
	sum[uuidVariantByte] = (sum[uuidVariantByte] & uuidVariantMask) | uuidVariantRFC4122
	return ids.ParseAny(string(prefix) + idSep + hex.EncodeToString(sum))
}

// ActorID is the act_ id of a stable key (the id v0.1.17 gives the actor row).
func ActorID(stableKey string) (ids.ID[ids.Actor], error) {
	a, err := Deterministic(ids.PrefixActor, stableKey)
	if err != nil {
		return ids.ID[ids.Actor]{}, err
	}
	return ids.As[ids.Actor](a)
}
