package schema

// settingsDefs returns the built-in resource-kind, var and settings tables beyond coreDefs. Owned by the phase 4 group 5
// migration; empty until that noun moves onto the new core, so Builtin is
// unchanged for nouns still served by the old binary.
func settingsDefs() []TableDef { return nil }
