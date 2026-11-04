package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Natural ids (ported from saas/pkg/aicoder/ids/deterministic.go, v0.1.17):
// a row whose table declares a unique key gets an id derived from that key,
// so two machines creating "the same" tag, repo or actor write the same
// row and their stores converge instead of holding two rows that clash on
// the key. The bytes are exactly v0.1.17's, so a v0.1.17 row and a 0.1.18
// row with the same key have the same id.
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
)

// NaturalID is the v0.1.17 deterministic id of a row of prefix p whose
// natural key is parts (in the table's unique-index order). Exported so an
// operation can find a natural-key row (a tag by project and name) without
// a lookup.
func NaturalID(p ids.Prefix, parts ...string) (ids.AnyID, error) {
	if len(parts) == 0 {
		return ids.AnyID{}, errs.New(errs.Internal, "engine: a natural id needs at least one key part")
	}
	h := sha256.New()
	h.Write([]byte(strings.Join(parts, naturalKeySeparator)))
	sum := h.Sum(nil)[:uuidByteLen]
	sum[uuidVersionByte] = (sum[uuidVersionByte] & uuidVersionMask) | uuidVersion7
	sum[uuidVariantByte] = (sum[uuidVariantByte] & uuidVariantMask) | uuidVariantRFC4122
	return ids.ParseAny(string(p) + idSep + hex.EncodeToString(sum))
}

// naturalKey returns the first unique index of def and the values of its
// fields in fields; ok is false when the table has none or a key field is
// unset (the row then gets a random id).
func naturalKey(def schema.TableDef, fields map[string]value.Value) ([]string, []string, bool) {
	for _, ix := range def.Indexes {
		if !ix.Unique || len(ix.Fields) == 0 {
			continue
		}
		parts := make([]string, 0, len(ix.Fields))
		for _, f := range ix.Fields {
			v, ok := fields[f]
			if !ok || v.Kind() == value.KindNull || v.String() == "" {
				return nil, nil, false
			}
			parts = append(parts, v.String())
		}
		return ix.Fields, parts, true
	}
	return nil, nil, false
}
