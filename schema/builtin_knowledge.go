package schema

// knowledgeDefs returns the built-in rule, decision and hotfix (warning) tables. Owned by the phase 4 group 1
// migration; empty until that noun moves onto the new core, so Builtin is
// unchanged for nouns still served by the old binary.
func knowledgeDefs() []TableDef { return nil }
