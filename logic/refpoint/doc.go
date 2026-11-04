// Package refpoint applies the expansion-point rules of
// SECRETS_AND_REFERENCES 12 and 13 on top of logic/refs: which schemes a
// reader may fill in, what a secret reference turns into, and the scanner
// pass over the expanded text (13.2).
//
//	text ─▶ rewrite (secret, env per point) ─▶ refs.Expander
//	                                             │ gate (wraps the caller's Resolver):
//	                                             │   var/res/row allowed at this point? (Locate)
//	                                             │   no ─▶ the reference text, as written
//	                                             │   yes ─▶ value, rewritten like the text
//	                                             ▼
//	        scan the expanded text; findings the stored text did not
//	        have came in through a reference (13.2):
//	          show   ─▶ masked ••••••
//	          render ─▶ E_SECRET_DETECTED naming the reference
//
// Points (12):
//
//	show    var res row: yes (personal vars too, SQ23)   env: yes, masked if secret-like   secret: ••••••
//	render  var: shareable, not personal (BATCH 1, 16)   res/row: target reaches this render   env: never   secret: as written (SQ11)
//	client  as render, targets in the client repo only   secret: ${secret:redacted} (SQ11)
//	private var/res/row: any non-personal target (CFG6 private file)   env: never   secret: as written
//
// Layer L1: pure; the caller supplies the Resolver, the Locate lookup,
// the environment and the scanner. Secrets never resolve here (OV13b's
// --reveal does that through a manager).
package refpoint
