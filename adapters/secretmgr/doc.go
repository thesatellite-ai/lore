// Package secretmgr is lore's built-in secret managers (layer L3,
// SECRETS_AND_REFERENCES 6, 10.2; SQ14, D20): gopass, op (1Password),
// bitwarden, vault, aws-secrets, gcp-secrets, azure-keyvault and env.
//
//	ops/settings secrets show --reveal / secrets check
//	   └► Set.Get(scheme) ─► Manager.ValidateLocator (no I/O)
//	                       └► Revealer.Reveal(ctx, Locator)
//	                             └► run: LookPath ─► exec with timeout (kill on expiry)
//	                                  stdout ─► Secret (never a command line, log or error)
//	                                  stderr ─► dropped; a fixed message + exit code
//
// Rules (section 6): one named entry per call, never a listing; no cache on
// disk or in memory (D20); the value lives only in a Secret, whose String,
// GoString, Format and MarshalJSON print a mask. Only the --reveal printer
// calls Expose.
package secretmgr
