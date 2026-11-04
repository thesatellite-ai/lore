package schema

// infraDefs returns the built-in team, user, identity-profile, mount-alias, project-config, pii-pattern, trusted-plugin, task-view and config tables beyond coreDefs. Owned by the phase 5 group 9
// migration; empty until that noun moves onto the new core, so Builtin is
// unchanged for nouns still served by the old binary.
func infraDefs() []TableDef { return nil }
