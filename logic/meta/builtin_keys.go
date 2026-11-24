package meta

import "github.com/thesatellite-ai/lore/model/value"

// Metadata keys lore itself reads (devco:D-941, bug hunt H-190). A row that
// states a fact about code names its evidence and the day a person or
// agent last checked it; lint, doctor, search and ask then flag the row as
// stale when an evidence file changed after that day (logic/factstale).
const (
	// KeyFactEvidence lists where the fact can be checked: repo-relative
	// `<path>:<line>` or `<path>` items, or a commit sha whose files the
	// fact describes (--meta fact_evidence=billing/refund.go:42,3f9c2ab).
	KeyFactEvidence = "fact_evidence"
	// KeyFactVerifiedAt is the day the fact was last checked against its
	// evidence (--meta fact_verified_at=2026-11-22).
	KeyFactVerifiedAt = "fact_verified_at"
	// KeyFactState says whether the fact holds (live, fixed, planned,
	// rejected …; --meta fact_state=live): lore ask ranks a live or fixed
	// fact above a planned or rejected one (devco D-963). No type is
	// registered for it, so any value a team uses is kept.
	KeyFactState = "fact_state"
)

// builtinTypes are the types the keys lore reads get when a write
// registers them on first use. Why: an auto-registered key is typed from
// its first value, which a bare --meta reads as text, so fact_verified_at
// was a text key and a typo in its date passed silently.
var builtinTypes = map[string]Type{
	KeyFactEvidence:   {Kind: value.KindList, Elem: value.KindText},
	KeyFactVerifiedAt: {Kind: value.KindDate},
}

// BuiltinType is the type a key lore reads gets on first use (false for
// any other name, which keeps the first value's type).
func BuiltinType(name string) (Type, bool) {
	t, ok := builtinTypes[name]
	return t, ok
}
