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

// Machine tags ids of machine profiles.
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
