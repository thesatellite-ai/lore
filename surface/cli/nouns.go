package cli

// nouns.go — the help text of the noun groups Mount creates.
//
// An operation's Spec carries the help of its leaf command (`lore task add`),
// but nothing in the registry describes the group above it (`lore task`), so
// group() used to print the placeholder "lore task commands" in `lore
// --help`, the group's own --help and the generated command index. The
// table below gives every group a real one-line Short (and a Long where the
// design page has an intro), worded after the CLI design reference; where the
// design is silent the v0.1.17 group help is kept (DESIGN-ONLY rule,
// P3-NOUNHELP-1).

// NounDoc is the help text of one noun group.
type NounDoc struct {
	// Short is the one line shown in the parent's command list.
	Short string
	// Long is the paragraph above the group's own command list; empty
	// means cobra prints Short there.
	Long string
}

// genericGroupSuffix ends the placeholder Short of a group missing from
// nounDocs ("lore x commands"), so a test can find such groups.
const genericGroupSuffix = " commands"

// nounDoc returns the help of the group at path (the words after `lore`,
// space separated, e.g. "task time"), and false when the table has none.
func nounDoc(path string) (NounDoc, bool) {
	d, ok := nounDocs()[path]
	return d, ok
}

// nounDocs is the table, keyed by group path. A function, not a package
// variable, so no caller can change it at run time.
func nounDocs() map[string]NounDoc {
	return map[string]NounDoc{
		"actor":            {Short: "The org roster: register, list, show and deactivate people and agents"},
		"actor contact":    {Short: "A person's contacts: emails, GitHub/GitLab logins, phones, social handles (add, edit, remove, list)"},
		"actor period":     {Short: "A person's company stints (list, add, edit); offboard and rejoin end and start them"},
		"contact-type":     {Short: "Contact types: the built-ins plus the org's own (list, add, edit, retire)"},
		"role":             {Short: "Project roles: the built-ins plus the org's own (list, add, edit, retire)"},
		"team":             {Short: "The org's teams (@handle names a group: add, list, show, remove, member add|remove) and invites (lore team invite)"},
		"team member":      {Short: "Who is on a team: add or take people off"},
		"team invite":      {Short: "Org invites for outsiders: send one (a lore-invite line holding no secret), list, revoke, resend; your own staff: lore member add"},
		"member":           {Short: "The org's people: add your staff straight to the roster (role, teams, projects; one or a CSV), list, show, edit, remove; they join with lore setup join <org>"},
		"setup trash":      {Short: "Setups lore setup remove moved aside: list, restore (with their repo pins), purge for good (never a repo)"},
		"machine path":     {Short: "Where lore keeps its own files on this machine: lore home, config.yaml, this repo's .lore"},
		"setup leftovers":  {Short: "The identity or personal.store of ~/.lore/config.yaml no setup uses any more: clear one (lore setup list shows them)"},
		"agent":            {Short: "Agent sessions (one actor per session label)"},
		"architecturenote": {Short: "Manage architecture notes"},
		"audit":            {Short: "Inspect and verify the hash-chained audit log of knowledge changes"},
		"batch": {
			Short: "Bulk changes: list, show and undo a batch",
			Long:  "One noun for every bulk change: task bulk edits, metadata bulk set/unset and imports, bulk and group deletes, resource bulk edits and imports, lore secrets move. lore batch undo replaces every per-noun undo.",
		},
		"behaviour":       {Short: "Manage behaviours"},
		"cache":           {Short: "Maintain lore's caches outside the checkout (~/.lore/cache, ~/.lore/render)"},
		"check":           {Short: "Leak checks: internal or personal content must never reach the client"},
		"comment":         {Short: "Manage comments on entities"},
		"commit":          {Short: "Show the entities linked to a git commit"},
		"config":          {Short: "Get/set keys of ~/.lore/config.yaml"},
		"cookbookrecipe":  {Short: "Manage cookbook recipes"},
		"decision":        {Short: "Manage decisions (architectural records)"},
		"directive":       {Short: "Install or show the lore agent-directive block in agent-loaded markdown"},
		"external-source": {Short: "Register external knowledge sources (add, enable, disable, list)"},
		"gate":            {Short: "Human gates: stop agents until a person approves"},
		"handoff":         {Short: "Manage handoffs"},
		"hold":            {Short: "Hold a shared resource (a test suite, a staging slot) with a lease"},
		"hotfix":          {Short: "Manage hotfixes (loud recurring warnings)"},
		"identity":        {Short: "Manage persisted identity (identity: in ~/.lore/config.yaml)"},
		"incident":        {Short: "Manage incidents"},
		"link":            {Short: "Link git commits to any entity (task / run / mission / decision / …)"},
		"live":            {Short: "Inspect and maintain the live ref (refs/lore/live)"},
		"memory":          {Short: "Manage memories (free-form learned knowledge)"},
		"meta": {
			Short: "Metadata on any row: define keys, set and unset values, rename, move, export",
			Long:  "Metadata commands work on the place the command runs in (repo, project folder, the store's org/ folder, or a store root that holds rows) and see key definitions along the chain repo → project → org (client repos see the repo only).",
		},
		"meta value": {Short: "Allowed values of a metadata key (add, rename)"},
		"migrate": {
			Short: "Bring a project's data to the current format (status, run, rollback, enable, unlock)",
			Long:  "Every subcommand works on the current project: its branch, its live ref and its store folders.",
		},
		"mission":     {Short: "Manage missions (containers for tasks)"},
		"org":         {Short: "Choose and inspect which org a repo belongs to"},
		"pattern":     {Short: "Manage patterns"},
		"pii-pattern": {Short: "Old spelling of lore secrets rule (custom secret detection patterns)"},
		"pin":         {Short: "Pin how a must rule or critical/high hotfix appears in LORE.md"},
		"plan":        {Short: "Manage plans"},
		"playbook":    {Short: "Manage playbooks"},
		"policy": {
			Short: "Team rules: set, show and unset policies (lower levels may only tighten)",
			Long:  "One command for every team rule. Each rule set at a level is one row of the policies table at that level: the store's org/ folder, the project folder, or the repo. Lower levels may only tighten what a higher level set.",
		},
		"project":        {Short: "Manage projects"},
		"project member": {Short: "Who works on a project, with which roles, since when (add, edit, remove, list)"},
		"prompt":         {Short: "Manage prompts"},
		"reminder":       {Short: "Manage time-based reminders"},
		"repo":           {Short: "Manage repos within a project"},
		"resource": {
			Short: "Infrastructure facts: servers, domains, accounts and the refs between them",
			Long:  "A resource is a row of a kind (server, domain, …) with typed fields. <name> accepts a bare name, a qualified name <place>:<kind>/<name> (<place> is repo, org, a project folder or a store's name, as resource add prints it), the short form <kind>/<name>, or an id (res_…). Resources are branch-only: there is no --live.",
		},
		"resource field": {Short: "Fields of a resource kind (add, edit, rename, retype, remove)"},
		"resource kind":  {Short: "Resource kinds (add, edit, rename, show)"},
		"row":            {Short: "Any row by id: show it, or move it to another store"},
		"rule":           {Short: "Manage rules (hard constraints)"},
		"run":            {Short: "Log agent runs (start, end, cancel)"},
		"scope":          {Short: "Directory scopes for rules and hotfixes (ids are kept when a folder is renamed)"},
		"secrets":        {Short: "Secret references and who holds them (list, check, move, offboarding)"},
		"secrets rule":   {Short: "Secret detection rules on top of the defaults (add, edit, off, on)"},
		"session":        {Short: "Inspect this machine's agent sessions (local only, never synced)"},
		"skill":          {Short: "Meta-tools for the skill bundle"},
		"snapshot":       {Short: "Manage snapshots (point-in-time knowledge captures)"},
		"store":          {Short: "Manage lore stores: lore-only repos holding internal and personal projects"},
		"store folder":   {Short: "Project folders inside a store"},
		"suggestion":     {Short: "Manage suggestions"},
		"sync trash":     {Short: "Moved: lore trash --local (this spelling goes away in the next release)"},
		"tag":            {Short: "Manage tags + entity-tag bindings"},
		"task":           {Short: "Manage tasks (discrete work items)"},
		"task checklist": {Short: "Checklist items inside a task (add, tick, untick, promote to a sub-task)"},
		"task recur":     {Short: "A repeating task's schedule (show, edit, skip, stop)"},
		"task template":  {Short: "Task templates (add, edit, rename, show, delete)"},
		"task time": {
			Short: "Time spent on a task (show, log, edit, stop)",
			Long:  "Simple time tracking per task for people and agents. Time comes from status spans, claim spans, runs and manual entries; overlaps count once; holds never count. Entries travel inside the task row.",
		},
		"task tree": {Short: "Check a task's sub-task tree"},
		"task view": {Short: "Saved task list views (add, edit, show, delete)"},
		"tasklist":  {Short: "Manage tasklists"},
		"tastepref": {Short: "Manage taste preferences"},
		"techdoc":   {Short: "Manage external documentation references"},
		"vars": {
			Short: "Project variables, used as ${var:<name>} in any text field",
			Long:  "Variables are data: one row per variable, referenced by name and resolved to its permanent var_ id. Variables never hold secrets.",
		},
		"workflow":  {Short: "Manage workflows"},
		"workspace": {Short: "Manage workspaces"},
	}
}
