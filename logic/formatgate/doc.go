// Package formatgate decides whether this lore binary may write a project,
// from the features and version floor the project's _meta.json declares
// (FORMAT_EVOLUTION_DESIGN rule 2). Pure: stdlib and model only.
//
// Flow:
//
//	_meta.json ──(rowfs.ReadFormat)──► Declared
//	Declared + binary version ──Check──► Verdict ──Err──► E_OLD_LORE (writes refused, reads work)
//	row about to be written ──Needs──► features ──Raise──► Declared' (+ added names)
//	two sides of a merged _meta.json ──Merge──► features ∪, floor = max (version-aware)
//	.github/workflows/*.yml text ──CIPins──► pins ──CIPinsBelow(floor)──► doctor warnings
package formatgate
