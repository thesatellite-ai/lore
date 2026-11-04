package memory

// Input and output structs of the memory operations. Struct tags are the
// op contract (op/fields.go): `json` is the REST and --json name (no
// omitempty = required), `arg`/`flag` the CLI spelling, `desc` the help
// text. Outputs keep v0.1.17's --json envelope keys where scripts and the
// lore skill read them: `data` on show and list, `results` on search.

// AddIn is the input of memory.add.
type AddIn struct {
	// Body is v0.1.17's --body. Text is the same value given positionally
	// (`lore memory add "…"`); exactly one of them must be set.
	Body string `json:"body,omitempty" flag:"body" desc:"the memory text (or give it as the first argument)" ui:"widget=markdown"`
	Text string `json:"text,omitempty" arg:"0" desc:"the memory text, instead of --body"`
	// Kind is one of schema.MemoryKinds; empty means retrieved.
	Kind string `json:"kind,omitempty" flag:"kind" desc:"core | retrieved | episodic | procedural | archival (default retrieved)"`
	// Source is source_kind; empty means manual.
	Source    string `json:"source,omitempty" flag:"source" desc:"source_kind: manual | learn-from | agent-proposal | plugin | imported (default manual)"`
	SourceRef string `json:"source_ref,omitempty" flag:"source-ref" desc:"free-form provenance pointer"`
	// AllowSecrets asks before storing text the secret scanner flags
	// (Confirm.When): with --yes the finding is accepted and audited.
	AllowSecrets bool   `json:"allow_secrets,omitempty" flag:"allow-secrets" desc:"store text the secret scanner flags, after confirming"`
	To           string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
	// Repo scopes the memory to one repo of the project (mount name or
	// rep_ id); empty is the project-wide ("master") scope.
	Repo string `json:"repo,omitempty" flag:"repo" desc:"scope to a repo of the project (mount name or rep_ id; default project-wide)"`
}

// WriteOut is the output of every write operation: the row's id, the store
// that holds it and any warnings the write raised.
type WriteOut struct {
	ID       string    `json:"id"`
	Store    string    `json:"store"`
	Warnings []Warning `json:"warnings,omitempty"`
}

// Warning is one engine warning copied into an output (W_SCAN_DEGRADED).
type Warning struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ListIn is the input of memory.list.
type ListIn struct {
	// Repo scope (v0.1.17 repo_scope.go; op structs cannot embed, so the
	// four fields repeat on SearchIn): no flag = project-wide memories
	// only; --repo adds that repo's; --no-inherit keeps only that repo's.
	Repo       string `json:"repo,omitempty" flag:"repo" desc:"include this repo's memories (mount name or rep_ id)"`
	NoInherit  bool   `json:"no_inherit,omitempty" flag:"no-inherit" desc:"with --repo: only that repo's memories, not the project-wide ones"`
	MasterOnly bool   `json:"master_only,omitempty" flag:"master-only" desc:"only project-wide memories"`
	AllRepos   bool   `json:"all_repos,omitempty" flag:"all-repos" desc:"memories of every repo"`
	Archived   bool   `json:"archived,omitempty" flag:"archived" desc:"include archived memories"`
	Kind       string `json:"kind,omitempty" flag:"kind" desc:"only memories of this kind"`
	Store      string `json:"store,omitempty" flag:"store" desc:"only memories of this store (default every attached store)"`
}

// ListOut is the output of memory.list.
type ListOut struct {
	Data  []Item `json:"data"`
	Count int    `json:"count"`
}

// Item is one memory in a list: the columns a person scans.
type Item struct {
	ID       string `json:"id"`
	Store    string `json:"store"`
	Kind     string `json:"kind"`
	Body     string `json:"body"`
	Archived bool   `json:"archived,omitempty"`
}

// IDIn is the input of the operations that take one memory id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the memory id (mem_…)"`
}

// ShowOut is the output of memory.show.
type ShowOut struct {
	Data Memory `json:"data"`
}

// Memory is one memory with every field a reader needs. Optional fields
// are empty when the row does not set them.
type Memory struct {
	ID         string `json:"id"`
	Store      string `json:"store"`
	Kind       string `json:"kind"`
	Body       string `json:"body"`
	ProjectID  string `json:"project_id"`
	RepoID     string `json:"repo_id,omitempty"`
	SourceKind string `json:"source_kind"`
	SourceRef  string `json:"source_ref,omitempty"`
	ArchivedAt string `json:"archived_at,omitempty"`
	ValidUntil string `json:"valid_until,omitempty"`
	Superseded string `json:"superseded_by_id,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	CreatedBy  string `json:"created_by,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	UpdatedBy  string `json:"updated_by,omitempty"`
	HLC        string `json:"hlc"`
}

// EditIn is the input of memory.edit. An empty string flag leaves its
// field as it is.
type EditIn struct {
	ID           string `json:"id" arg:"0" desc:"the memory id (mem_…)"`
	Body         string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Kind         string `json:"kind,omitempty" flag:"kind" desc:"core | retrieved | episodic | procedural | archival"`
	Source       string `json:"source,omitempty" flag:"source" desc:"new source_kind"`
	SourceRef    string `json:"source_ref,omitempty" flag:"source-ref" desc:"new source-ref"`
	AllowSecrets bool   `json:"allow_secrets,omitempty" flag:"allow-secrets" desc:"store text the secret scanner flags, after confirming"`
}

// DeleteIn is the input of memory.delete.
type DeleteIn struct {
	ID string `json:"id" arg:"0" desc:"the memory id (mem_…)"`
}

// SearchIn is the input of memory.search.
type SearchIn struct {
	// Repo scope (v0.1.17 repo_scope.go; op structs cannot embed, so the
	// four fields repeat on ListIn): no flag = project-wide memories
	// only; --repo adds that repo's; --no-inherit keeps only that repo's.
	Repo       string `json:"repo,omitempty" flag:"repo" desc:"include this repo's memories (mount name or rep_ id)"`
	NoInherit  bool   `json:"no_inherit,omitempty" flag:"no-inherit" desc:"with --repo: only that repo's memories, not the project-wide ones"`
	MasterOnly bool   `json:"master_only,omitempty" flag:"master-only" desc:"only project-wide memories"`
	AllRepos   bool   `json:"all_repos,omitempty" flag:"all-repos" desc:"memories of every repo"`
	Query      string `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	// Limit caps the hits; 0 means DefaultSearchLimit.
	Limit           int  `json:"limit,omitempty" flag:"limit" desc:"at most this many results (default 10)"`
	IncludeArchived bool `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived memories"`
}

// SearchOut is the output of memory.search.
type SearchOut struct {
	Results []Item `json:"results"`
	Count   int    `json:"count"`
}
