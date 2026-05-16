package constants

// SourceKind is the value stored in every knowledge entity's `source_kind`
// column (per LifecycleMixin). Tracks the provenance of a row.
type SourceKind string

const (
	SourceManual        SourceKind = "manual"
	SourceLearnFrom     SourceKind = "learn-from"
	SourceAgentProposal SourceKind = "agent-proposal"
	SourcePlugin        SourceKind = "plugin"
	SourceImported      SourceKind = "imported"
	SourceMigrated      SourceKind = "migrated"
)

func (s SourceKind) String() string { return string(s) }
