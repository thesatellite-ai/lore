package ids

// Tag types: one empty struct per table that code refers to by typed id.
// Add a tag when a package needs ID[T] for another table; the prefix must
// already be in prefixes.go.

// Memory tags ids of memories.
type Memory struct{}

// Prefix implements Tag.
func (Memory) Prefix() Prefix { return PrefixMemory }

// Actor tags ids of actors (people and agents).
type Actor struct{}

// Prefix implements Tag.
func (Actor) Prefix() Prefix { return PrefixActor }

// Store tags ids of stores.
type Store struct{}

// Prefix implements Tag.
func (Store) Prefix() Prefix { return PrefixStore }

// Repo tags ids of repos.
type Repo struct{}

// Prefix implements Tag.
func (Repo) Prefix() Prefix { return PrefixRepo }

// Project tags ids of projects (also folders inside a store).
type Project struct{}

// Prefix implements Tag.
func (Project) Prefix() Prefix { return PrefixProject }

// Machine tags the ROW ids of machines-table rows (mch_ + 32 hex, like
// every row id). It is not the machine's identity: that is MachineID
// (mch_ + the full 64-hex keyed fingerprint, PROV4), stored in the row's
// machine_id field and in every _w stamp. The row id is derived from the
// MachineID (identity.Deterministic), so one machine has one row; the two
// forms are never compared with each other (r3-origin, P2-ORG-1).
type Machine struct{}

// Prefix implements Tag.
func (Machine) Prefix() Prefix { return PrefixMachine }

// Policy tags ids of policies.
type Policy struct{}

// Prefix implements Tag.
func (Policy) Prefix() Prefix { return PrefixPolicy }

// MetaKey tags ids of meta_keys.
type MetaKey struct{}

// Prefix implements Tag.
func (MetaKey) Prefix() Prefix { return PrefixMetaKey }

// MetaValue tags ids of meta_values.
type MetaValue struct{}

// Prefix implements Tag.
func (MetaValue) Prefix() Prefix { return PrefixMetaValue }

// Kind tags ids of kinds.
type Kind struct{}

// Prefix implements Tag.
func (Kind) Prefix() Prefix { return PrefixKind }

// KindField tags ids of kind_fields.
type KindField struct{}

// Prefix implements Tag.
func (KindField) Prefix() Prefix { return PrefixKindField }

// Var tags ids of vars.
type Var struct{}

// Prefix implements Tag.
func (Var) Prefix() Prefix { return PrefixVar }

// SecretRule tags ids of secret_rules.
type SecretRule struct{}

// Prefix implements Tag.
func (SecretRule) Prefix() Prefix { return PrefixSecretRule }

// SecretAllow tags ids of secret_allows.
type SecretAllow struct{}

// Prefix implements Tag.
func (SecretAllow) Prefix() Prefix { return PrefixSecretAllow }

// Resource tags ids of resources.
type Resource struct{}

// Prefix implements Tag.
func (Resource) Prefix() Prefix { return PrefixResource }

// Batch tags ids of bulk actions (XQ1: one bat_ prefix for every bulk
// action; the batch lives on the audit events it wrote, never as a row).
type Batch struct{}

// Prefix implements Tag.
func (Batch) Prefix() Prefix { return PrefixBatch }

// Scope tags ids of directory scopes (scopes table, D12).
type Scope struct{}

// Prefix implements Tag.
func (Scope) Prefix() Prefix { return PrefixScope }

// ActorContact tags ids of actor_contacts (TEAM_PROFILES 2.2).
type ActorContact struct{}

// Prefix implements Tag.
func (ActorContact) Prefix() Prefix { return PrefixActorContact }

// ContactType tags ids of contact_types (TEAM_PROFILES 2.3).
type ContactType struct{}

// Prefix implements Tag.
func (ContactType) Prefix() Prefix { return PrefixContactType }

// Role tags ids of roles (TEAM_PROFILES 2.4).
type Role struct{}

// Prefix implements Tag.
func (Role) Prefix() Prefix { return PrefixRole }

// ActorPeriod tags ids of actor_periods (TEAM_PROFILES 2.5).
type ActorPeriod struct{}

// Prefix implements Tag.
func (ActorPeriod) Prefix() Prefix { return PrefixActorPeriod }

// ProjectMember tags ids of project_members (TEAM_PROFILES 2.6).
type ProjectMember struct{}

// Prefix implements Tag.
func (ProjectMember) Prefix() Prefix { return PrefixProjectMember }

// Invite tags ids of invites (ONBOARDING_DESIGN 4.5).
type Invite struct{}

// Prefix implements Tag.
func (Invite) Prefix() Prefix { return PrefixInvite }

// SetupProgress tags ids of setup_progress (ONBOARDING_DESIGN 3.4).
type SetupProgress struct{}

// Prefix implements Tag.
func (SetupProgress) Prefix() Prefix { return PrefixSetupProgress }

// KnownRepo tags ids of known_repos (ONBOARDING_DESIGN Q11).
type KnownRepo struct{}

// Prefix implements Tag.
func (KnownRepo) Prefix() Prefix { return PrefixKnownRepo }
