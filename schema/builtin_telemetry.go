package schema

// telemetryDefs returns the built-in run, run-step, session, query-log, render-history, audit, activity-archive, intervention-metric, bench and assemble/compress tables. Owned by the phase 5 group 8
// migration; empty until that noun moves onto the new core, so Builtin is
// unchanged for nouns still served by the old binary.
func telemetryDefs() []TableDef { return nil }
