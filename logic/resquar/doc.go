// Package resquar decides whether one stored resource value is quarantined
// (RESOURCES 3.15, R10): it breaks its field's type or allowed values. A
// quarantined resource stays visible and editable, but is left out of
// res_<kind>, search and render, and a move refuses it
// (E_RESOURCE_QUARANTINED).
//
//	fields[fld_id] = {name, v}  ─► Check(field def, v)
//	   ref / secret_ref / unknown type ─► never (a dangling ref is not a reason, 3.4)
//	   v does not parse as the type      ─► "does not fit <type>"
//	   values set and v (each item) not in it ─► "not one of a, b"
//
// The caller decides about a field id it has no definition for (ops call
// it "unknown field"; the engine fails closed on it as a leak).
//
// Layer L1: pure, imports model only. Shared by ops/settings (show, list,
// lint), engine (move) and the sqlite cache (projection), so every place
// gives the same answer.
package resquar
