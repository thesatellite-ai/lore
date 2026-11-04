// Package meta is the pure metadata logic (METADATA_DESIGN 3.1-3.6, 3.13;
// layer L1): key names, the one command-line grammar, value typing, the
// row-file entry shape and the size/depth limits. It reads no store and
// knows no registry; the engine resolves names to key rows (engine/meta.go)
// and calls these functions to check and build each entry.
//
//	--meta k[:type]=v ─► Parse ─► Term ─┐
//	--meta-json k=<json> ─► ParseJSONArg ┤
//	                                     ├─► CheckName / Reserved (E_META_KEY, _RESERVED, _DEPTH)
//	          engine resolves the key ──►├─► Coerce / CoerceJSON (E_META_TYPE, _VALUE, _SHAPE)
//	                                     ├─► Entry.Encode (name, scope, strict, sensitive, v, v_name)
//	                                     └─► Size (E_META_SIZE, the 4 KB warning)
//
// Invariant: nothing is converted silently. Every normalisation (trim, NFC,
// -0 → 0, a datetime to UTC) is reported as a "stored as …" note (R12).
package meta
