//go:build machines && !windows

package machines

// sweep_cmds.go — the command table of the generic sweep (sweep_test.go):
// every leaf command a user runs, with fixture inputs, grouped in chains
// that run in order on one machine. A leaf command is either in a chain
// or in notUserRun with the reason it is not run; TestSweepCoversEveryCommand
// fails when lore gains a command that is in neither, so a new command
// joins the sweep (or says why not) the day it is added.

import (
	"slices"
	"strings"
)

// Where a step runs.
const (
	inHome = ""       // the home folder (no repo)
	inRepo = RepoLore // a git repo where lore init ran
	inBoth = "*both*" // once in each: commands whose answer depends on the folder
)

// varID is the chain variable a Save step fills and later steps name;
// varPrev is the id saved before it (a step that relates two rows).
const (
	varID   = "{id}"
	varPrev = "{prev}"
)

// sweepStep is one lore run of the sweep.
type sweepStep struct {
	// Cmd is the leaf command path ("rule add"); Args follow it. A literal
	// varID in Args is replaced by the id the chain saved last.
	Cmd  string
	Args []string
	In   string
	// Must: the step is a fixture write the rest of the chain needs, so
	// any failure (even a documented one) fails the sweep.
	Must bool
	// Save reads the "id" of the --json result into varID.
	Save bool
	// NoJSON: the command has no --json flag.
	NoJSON bool
	// Exits are documented non-zero exits that still print the command's
	// report, not an error (doctor: 1 degraded, 2 broken).
	Exits []int
}

// sweepChain is a list of steps run in order on one machine.
type sweepChain struct {
	Name  string
	Steps []sweepStep
}

// knowledgeKind is a row kind with the add → edit → delete → restore life.
type knowledgeKind struct {
	Kind string
	Add  []string // add's fixture flags
	Edit []string // edit's flags
	// Archive: the kind has archive/unarchive.
	Archive bool
	// NoSearch / NoEdit: the kind has no such command.
	NoSearch, NoEdit bool
}

// sampleWord is in every fixture row, so search finds them.
const sampleWord = "sweepsample"

var knowledgeKinds = []knowledgeKind{
	{Kind: "architecturenote", Add: tb("Layering"), Edit: []string{"--title", "Layers"}},
	{Kind: "behaviour", Add: tb("Careful"), Edit: []string{"--body", sampleWord + " edited"}},
	{Kind: "cookbookrecipe", Add: tb("Retry"), Edit: []string{"--title", "Retry twice"}},
	{Kind: "decision", Add: tb("Use files"), Edit: []string{"--title", "Use row files"}, Archive: true},
	{Kind: "hotfix", Add: tb("Flaky CI", "--severity", "high"), Edit: []string{"--summary", "rerun once"}, Archive: true},
	{Kind: "incident", Add: tb("Outage"), Edit: []string{"--title", "Short outage"}},
	{Kind: "memory", Add: []string{"--body", sampleWord + " memory"}, Edit: []string{"--body", sampleWord + " memory edited"}, Archive: true},
	{Kind: "pattern", Add: tb("Retry wrapper"), Edit: []string{"--title", "Retry helper"}, Archive: true},
	{Kind: "playbook", Add: tb("Release"), Edit: []string{"--body", sampleWord + " 1. tag 2. push"}, Archive: true},
	{Kind: "prompt", Add: tb("Review"), Edit: []string{"--body", sampleWord + " review this"}, Archive: true},
	{Kind: "rule", Add: []string{"--body", sampleWord + " never log tokens", "--severity", "must"}, Edit: []string{"--summary", "no tokens in logs"}, Archive: true},
	{Kind: "snapshot", Add: tb("Release 1"), Edit: nil, NoEdit: true, Archive: true},
	{Kind: "suggestion", Add: tb("Cache"), Edit: []string{"--title", "Cache more"}},
	{Kind: "tasklist", Add: tb("Sprint 4"), Edit: []string{"--title", "Sprint 5"}},
	{Kind: "tastepref", Add: []string{"--body", sampleWord + " short names"}, Edit: []string{"--body", sampleWord + " shorter names"}},
	{Kind: "techdoc", Add: []string{"--name", sampleWord + " stdlib", "--base-url", "https://docs.example.test/std"}, NoEdit: true},
	{Kind: "workflow", Add: tb("Ship"), Edit: []string{"--body", sampleWord + " ship it"}},
	{Kind: "workspace", Add: tb("Main desk"), Edit: []string{"--name", "Desk"}},
	{Kind: "plan", Add: tb("Invite flow"), Edit: []string{"--title", "Invite flow v2"}},
	{Kind: "mission", Add: []string{sampleWord + " ship the invite flow", "--target", "2027-12-31"}, Edit: []string{"--target", "2028-01-31"}},
	{Kind: "task", Add: []string{sampleWord + " fix the login redirect", "--priority", "high"}, Edit: []string{"--priority", "urgent"}},
}

// tb is --title t --body "<sampleWord> …" plus extra flags.
func tb(title string, extra ...string) []string {
	return append([]string{"--title", title, "--body", sampleWord + " body of " + strings.ToLower(title)}, extra...)
}

// knowledgeChain is one kind's life: add, read it every way, edit, delete
// to the trash, look at the trash, restore, read it again.
func knowledgeChain(k knowledgeKind) sweepChain {
	s := []sweepStep{
		{Cmd: k.Kind + " add", Args: k.Add, In: inRepo, Must: true, Save: true},
		{Cmd: k.Kind + " show", Args: []string{varID}, In: inRepo, Must: true},
		{Cmd: k.Kind + " list", In: inRepo, Must: true},
	}
	if !k.NoEdit {
		s = append(s, sweepStep{Cmd: k.Kind + " edit", Args: append([]string{varID}, k.Edit...), In: inRepo, Must: true})
	}
	if !k.NoSearch {
		s = append(s, sweepStep{Cmd: k.Kind + " search", Args: []string{sampleWord}, In: inRepo, Must: true})
	}
	if k.Archive {
		s = append(s,
			sweepStep{Cmd: k.Kind + " archive", Args: []string{varID}, In: inRepo, Must: true},
			sweepStep{Cmd: k.Kind + " unarchive", Args: []string{varID}, In: inRepo, Must: true})
	}
	return sweepChain{Name: k.Kind, Steps: append(s,
		sweepStep{Cmd: k.Kind + " delete", Args: []string{varID, "--yes"}, In: inRepo, Must: true},
		sweepStep{Cmd: "trash list", In: inRepo, Must: true},
		sweepStep{Cmd: "trash show", Args: []string{varID}, In: inRepo, Must: true},
		sweepStep{Cmd: "trash restore", Args: []string{varID}, In: inRepo, Must: true},
		sweepStep{Cmd: k.Kind + " show", Args: []string{varID}, In: inRepo, Must: true},
	)}
}

// sweepSetup is the setup the setup chain makes and removes.
const sweepSetup = "sweep-tmp"

// reads are the read commands with no fixture: they answer on any
// machine, here or in a repo (a documented refusal counts as an answer).
var reads = []sweepStep{
	{Cmd: "doctor run", Args: []string{"--all"}, In: inBoth, Exits: doctorExits},
	{Cmd: "doctor run", In: inBoth, Exits: doctorExits},
	// doctor last reads the recorded results only; doctor fix with a key no
	// finding has is refused the documented way (E_NOT_FOUND) and runs nothing.
	{Cmd: "doctor last", In: inBoth},
	{Cmd: "doctor fix", Args: []string{"0000000000000000"}, In: inBoth},
	// lore fix --dry-run changes nothing (outside a repo: E_NOT_A_STORE)
	{Cmd: "fix run", Args: []string{"--dry-run"}, In: inBoth, Exits: doctorExits},
	{Cmd: "version", In: inHome},
	{Cmd: "whoami show", In: inBoth},
	{Cmd: "where show", In: inBoth}, {Cmd: "where show", Args: []string{"--json"}, In: inBoth},
	{Cmd: "identity show", In: inBoth},
	{Cmd: "config check", In: inHome},
	{Cmd: "config list", In: inHome},
	{Cmd: "setup list", In: inBoth},
	{Cmd: "setup discover", In: inHome},
	{Cmd: "setup import-scan", In: inHome},
	{Cmd: "setup aliases", In: inHome},
	{Cmd: "setup folders", In: inHome},
	{Cmd: "setup plan", In: inHome},
	{Cmd: "setup trash list", In: inHome},
	{Cmd: "machine list", In: inHome},
	{Cmd: "machine path list", In: inHome},
	{Cmd: "machine show", Args: []string{"mch_00000000"}, In: inHome},
	{Cmd: "setup browse", In: inHome},
	{Cmd: "store list", In: inBoth},
	{Cmd: "store folder list", In: inBoth},
	{Cmd: "store show", In: inRepo},
	{Cmd: "store doctor", In: inRepo},
	// The code repo is never a push-mode store: both answer E_REFUSED with
	// a hint, and neither changes a file (a real set or compaction runs in
	// the store push tests).
	{Cmd: "store set", Args: []string{"repo", "push=batch"}, In: inRepo},
	{Cmd: "store compact", Args: []string{"repo", "--dry-run"}, In: inRepo},
	// store push: the code repo has no store clone to push (a documented
	// refusal); --restore on a store lore never found reset refuses too
	// (the push and restore paths run in the store-reset journeys and
	// adapters/backend/git TestSyncRefusesAResetRemote).
	{Cmd: "store push", Args: []string{"repo"}, In: inRepo},
	{Cmd: "store push", Args: []string{"repo", "--restore", "--yes"}, In: inRepo},
	{Cmd: "repo list", In: inBoth},
	{Cmd: "project list", In: inBoth},
	{Cmd: "project shared-list", In: inRepo},
	{Cmd: "org use", In: inHome},
	{Cmd: "skill status", In: inBoth},
	{Cmd: "skill install", Args: []string{"--all", "--dry-run"}, In: inBoth},
	{Cmd: "migrate status", In: inBoth},
	{Cmd: "search status", In: inRepo},
	{Cmd: "search run", Args: []string{sampleWord}, In: inRepo},
	{Cmd: "sync status", In: inRepo},
	{Cmd: "sync conflicts", In: inRepo},
	{Cmd: "live show", In: inRepo},
	{Cmd: "live log", In: inRepo},
	{Cmd: "live diff", In: inRepo},
	{Cmd: "render run", Args: []string{"--dry-run"}, In: inRepo},
	{Cmd: "directive show", In: inRepo},
	{Cmd: "why-context show", In: inRepo},
	{Cmd: "inbox show", In: inRepo},
	{Cmd: "lint run", In: inRepo},
	{Cmd: "meta keys", In: inRepo},
	{Cmd: "meta lint", In: inRepo},
	{Cmd: "meta export", In: inRepo},
	{Cmd: "resource kinds", In: inRepo},
	{Cmd: "resource list", In: inRepo},
	{Cmd: "resource lint", In: inRepo},
	{Cmd: "resource stale", In: inRepo},
	{Cmd: "resource report", In: inRepo},
	{Cmd: "resource check", In: inRepo},
	{Cmd: "resource export", In: inRepo},
	{Cmd: "secrets list", In: inRepo},
	{Cmd: "secrets rules", In: inRepo},
	{Cmd: "secrets allow list", In: inRepo},
	{Cmd: "secrets check", In: inRepo},
	{Cmd: "check leaks", In: inRepo},
	{Cmd: "pii-pattern list", In: inRepo},
	{Cmd: "audit log", In: inRepo},
	{Cmd: "audit verify", In: inRepo},
	{Cmd: "batch list", In: inRepo},
	{Cmd: "trash list", In: inBoth},
	{Cmd: "trash stats", In: inRepo},
	{Cmd: "sync trash list", In: inRepo},
	{Cmd: "session list", In: inRepo},
	{Cmd: "actor list", In: inRepo},
	{Cmd: "actor contact list", In: inRepo},
	{Cmd: "role list", In: inRepo},
	{Cmd: "contact-type list", In: inRepo},
	{Cmd: "tag list", In: inRepo},
	{Cmd: "vars list", In: inRepo},
	{Cmd: "link list", In: inRepo},
	{Cmd: "external-source list", In: inRepo},
	{Cmd: "comment list", In: inRepo},
	{Cmd: "comment search", Args: []string{sampleWord}, In: inRepo},
	{Cmd: "policy show", In: inRepo},
	{Cmd: "scope list", In: inRepo},
	{Cmd: "scope show", Args: []string{"."}, In: inRepo},
	{Cmd: "handoff list", In: inRepo},
	{Cmd: "handoff search", Args: []string{sampleWord}, In: inRepo},
	{Cmd: "reminder list", In: inRepo},
	{Cmd: "task board", In: inRepo},
	{Cmd: "task next", In: inRepo},
	{Cmd: "task triage", In: inRepo},
	{Cmd: "task stale", In: inRepo},
	{Cmd: "task deferred", In: inRepo},
	{Cmd: "task someday", In: inRepo},
	{Cmd: "task export", In: inRepo},
	{Cmd: "task tree check", In: inRepo},
	{Cmd: "task view list", In: inRepo},
	{Cmd: "task template list", In: inRepo},
	{Cmd: "mission status", Args: []string{"msn_00000000000000000000000000000000"}, In: inRepo},
	{Cmd: "explain run", Args: []string{"rul_00000000000000000000000000000000"}, In: inRepo},
	{Cmd: "row show", Args: []string{"rul_00000000000000000000000000000000"}, In: inRepo},
	{Cmd: "show", Args: []string{"rul_00000000000000000000000000000000"}, In: inRepo},
	{Cmd: "commit show", Args: []string{"HEAD"}, In: inRepo},
	{Cmd: "setup show", Args: []string{"main"}, In: inHome},
	{Cmd: "setup check", Args: []string{"main"}, In: inHome},
}

// setupChain makes a setup step by step, edits it, turns it off and on,
// trashes, restores, trashes and purges it: the setup life a person runs.
var setupChain = sweepChain{Name: "setup", Steps: []sweepStep{
	{Cmd: "setup apply", Args: []string{sweepSetup, "--step", "kind", "--answers", `{"kind":"solo","name":"` + sweepSetup + `","default":false}`}, Must: true},
	{Cmd: "setup apply", Args: []string{sweepSetup, "--step", "identity", "--answers", `{"name":"` + PersonName + `","email":"sweep@example.test"}`}, Must: true},
	{Cmd: "setup show", Args: []string{sweepSetup}, Must: true},
	{Cmd: "setup check", Args: []string{sweepSetup}},
	{Cmd: "setup edit", Args: []string{sweepSetup, "--note", "made by the sweep", "--folder", "~/sweep/**"}, Must: true},
	// rename there and back, so the steps below still name sweepSetup
	{Cmd: "setup rename", Args: []string{sweepSetup, sweepSetup + "-renamed"}, Must: true},
	{Cmd: "setup show", Args: []string{sweepSetup + "-renamed"}, Must: true},
	{Cmd: "setup rename", Args: []string{sweepSetup + "-renamed", sweepSetup}, Must: true},
	{Cmd: "setup disable", Args: []string{sweepSetup}, Must: true},
	{Cmd: "setup enable", Args: []string{sweepSetup}, Must: true},
	{Cmd: "setup list", Must: true},
	{Cmd: "setup remove", Args: []string{sweepSetup}, Must: true},
	{Cmd: "setup trash list", Must: true},
	{Cmd: "setup trash restore", Args: []string{sweepSetup}, Must: true},
	{Cmd: "setup remove", Args: []string{sweepSetup}, Must: true},
	{Cmd: "setup trash purge", Args: []string{sweepSetup, "--confirm"}, Must: true},
}}

// configChain sets a key, reads it, unsets it.
var configChain = sweepChain{Name: "config", Steps: []sweepStep{
	{Cmd: "config set", Args: []string{"lease.ttl", "45m"}, Must: true},
	{Cmd: "config get", Args: []string{"lease.ttl"}, Must: true},
	// fmt rewrites a flow-style file in block style; on the block file set
	// wrote it changes nothing, and the key still reads back after it
	{Cmd: "config fmt", Args: []string{"--dry-run"}, Must: true},
	{Cmd: "config fmt", Must: true},
	{Cmd: "config get", Args: []string{"lease.ttl"}, Must: true},
	{Cmd: "config unset", Args: []string{"lease.ttl"}, Must: true},
	{Cmd: "config check", Must: true},
}}

// r is a fixture step in the repo; rs one that saves its id.
func r(cmd string, args ...string) sweepStep {
	return sweepStep{Cmd: cmd, Args: args, In: inRepo, Must: true}
}
func rs(cmd string, args ...string) sweepStep {
	return sweepStep{Cmd: cmd, Args: args, In: inRepo, Must: true, Save: true}
}

// taskChains are the task, mission and reminder lives.
var taskChains = []sweepChain{
	{Name: "task-life", Steps: []sweepStep{
		rs("task add", sampleWord+" wire the cache"),
		// a --where value holding a space is one term, not two
		r("task list", "--where", "title~=wire the cache"),
		r("task start", varID), r("task note", varID, "half done"),
		r("task checklist add", varID, "write the test"), r("task checklist list", varID),
		r("task checklist tick", varID, "1"), r("task checklist untick", varID, "1"), r("task checklist rm", varID, "1"),
		r("task time log", varID, "15m"), r("task time show", varID), r("task watch", varID), r("task unwatch", varID),
		r("task history", varID), r("task done", varID),
	}},
	{Name: "task-commitment", Steps: []sweepStep{
		rs("task add", sampleWord+" try a cache", "--commitment", "proposed"), r("task accept", varID),
		rs("task add", sampleWord+" try a queue", "--commitment", "proposed"), r("task decline", varID, "--reason", "wont_do"),
		rs("task add", sampleWord+" export page"), r("task split", varID, "--title", "Export query", "--title", "Download button"),
		r("task cancel", varID, "--reason", "obsolete", "--children", "cancel"),
	}},
	{Name: "task-pair", Steps: []sweepStep{
		rs("task add", sampleWord+" first of two"), rs("task add", sampleWord+" second of two"),
		r("task relate", varPrev, "--relates-to", varID), r("task unrelate", varPrev, "--relates-to", varID),
		r("task checklist add", varID, "one"), r("task checklist add", varID, "two"),
		r("task checklist edit", varID, "2", "the second item"), r("task checklist promote", varID, "1"),
		rs("task time log", varID, "15m"), r("task time edit", varID, "--duration", "45m", "--note", "pairing"),
		r("task time rm", varID, "--yes"), r("task time restore", varID),
		r("task view add", "sweep_view2", "--where", "priority>=high"), r("task view edit", "sweep_view2", "--sort", "due_at:asc"),
		r("task view delete", "sweep_view2", "--yes"),
		rs("task add", sampleWord+" weekly sync"), r("task edit", varID, "--recur", "every week"), r("task recur edit", varID, "every! 30 days"), r("task recur show", varID), r("task recur skip", varID),
		r("task recur stop", varID, "--yes"),
	}},
	{Name: "mission-life", Steps: []sweepStep{
		rs("mission add", sampleWord+" launch", "--target", "2027-12-31"), r("mission pause", varID),
		r("mission resume", varID), r("mission status", varID), r("mission done", varID),
	}},
	{Name: "reminder", Steps: []sweepStep{
		rs("reminder add", "--due", "2027-12-01", sampleWord+" weekly review"), r("reminder list"),
		r("reminder done", varID), r("reminder delete", varID, "--yes"),
	}},
}

// rowChains are the smaller row kinds and the commands that work on any
// row (pins, comments, tags, metadata): each makes its own fixture.
var rowChains = []sweepChain{
	{Name: "vars", Steps: []sweepStep{
		r("vars set", "sweep_host", "db.example.test", "--type", "dns"), r("vars show", "sweep_host"), r("vars where", "sweep_host"),
		r("vars rename", "sweep_host", "sweep_db"), r("vars rm", "sweep_db", "--yes"), r("vars list"),
	}},
	{Name: "policy", Steps: []sweepStep{
		r("policy set", "trash.retention_days", "365", "--yes"), r("policy show"), r("policy unset", "trash.retention_days", "--yes"),
	}},
	{Name: "rule-any-row", Steps: []sweepStep{
		r("tag add", "--name=sweep_tag"), r("tag edit", "sweep_tag", "--globs", "src/**"),
		rs("rule add", "--body", sampleWord+" pinned rule"), r("pin set", varID, "--context", "full"), r("pin unset", varID),
		r("tag attach", "--on-table", "rules", "--on-id", varID, "--tag", "sweep_tag"),
		r("tag detach", "--on-table", "rules", "--on-id", varID, "--tag", "sweep_tag"), r("tag list"),
		r("explain run", varID), r("row show", varID), r("show", varID),
		r("row path", varID), r("path", varID), r("row markdown", varID),
		r("meta add", "sweep_key", "--type", "text"), r("meta describe", "sweep_key", "what the sweep keeps"),
		r("meta set", varID, "sweep_key=v"), r("meta get", varID, "sweep_key"), r("meta show", varID),
		r("meta history", varID), r("meta unset", varID, "sweep_key"), r("meta keys"),
	}},
	{Name: "comment", Steps: []sweepStep{
		rs("memory add", "--body", sampleWord+" commented memory"), r("memory invalidate", varID),
		rs("comment add", "--on-table=memories", "--on-id="+varID, "--body=agreed"), r("comment list"),
		r("comment delete", varID, "--yes"),
	}},
	{Name: "resource", Steps: []sweepStep{
		r("resource kind add", "sweephost", "--description", "a server"), r("resource kind show", "sweephost"),
		rs("resource add", "sweephost", "hub1"), r("resource show", varID), r("resource history", varID),
		r("resource graph", varID), r("resource impact", varID), r("resource delete", varID, "--yes"),
	}},
	{Name: "task-view", Steps: []sweepStep{
		r("task view add", "sweep_view", "--where", "priority>=high"), r("task view show", "sweep_view"),
		r("task view delete", "sweep_view", "--yes"),
	}},
	{Name: "external-source", Steps: []sweepStep{
		r("external-source add", "--name", "issues", "--kind", "github"), r("external-source disable", "issues"),
		r("external-source enable", "issues"),
	}},
	{Name: "secrets-rule", Steps: []sweepStep{
		r("secrets rule add", "sweep-token", "--regex", "sweep_[a-z]{8}", "--yes"),
		r("secrets rule off", "sweep-token", "--reason", "fixtures hold sample tokens", "--yes"),
		r("secrets rule on", "sweep-token", "--yes"), r("secrets rule edit", "sweep-token", "--regex", "sweep_[a-z]{9}", "--yes"),
	}},
	{Name: "tools", Steps: []sweepStep{
		{Cmd: "skill install", Args: []string{"--all"}, In: inHome}, {Cmd: "skill status", In: inBoth},
		{Cmd: "directive install", In: inRepo}, {Cmd: "directive show", In: inRepo},
	}},
	{Name: "store-reads", Steps: []sweepStep{
		r("search rebuild"), r("sql run", "select 1"), {Cmd: "project show", Args: []string{"has-lore"}, In: inRepo},
	}},
}

// The sweep's four groups, each its own test func (TestSweepReads,
// TestSweepKnowledge, TestSweepRows, TestSweepTasks) on a machine of its own, so each
// group stays under the time budget and the Taskfile can run them as
// separate steps.
const (
	groupReads     = "reads"
	groupKnowledge = "knowledge"
	groupRows      = "rows"
	groupTasks     = "tasks"
)

// afterReads are the reads run again once a group's writes are done: the
// ones whose answer the writes change.
var afterReads = []sweepStep{
	{Cmd: "doctor run", Args: []string{"--all"}, In: inBoth, Exits: doctorExits},
	{Cmd: "trash list", In: inRepo}, {Cmd: "trash stats", In: inRepo},
	{Cmd: "search run", Args: []string{sampleWord}, In: inRepo}, {Cmd: "lint run", In: inRepo},
	{Cmd: "render run", Args: []string{"--dry-run"}, In: inRepo}, {Cmd: "task board", In: inRepo},
	{Cmd: "config check", In: inHome}, {Cmd: "setup list", In: inBoth}, {Cmd: "setup import-scan", In: inHome},
	{Cmd: "setup trash list", In: inHome}, {Cmd: "whoami show", In: inBoth},
}

// sweepChains is one group's table, in run order.
func sweepChains(group string) []sweepChain {
	switch group {
	case groupReads:
		return []sweepChain{{Name: "reads", Steps: reads}}
	case groupKnowledge:
		var cs []sweepChain
		for _, k := range knowledgeKinds {
			cs = append(cs, knowledgeChain(k))
		}
		return append(cs, sweepChain{Name: "reads-after", Steps: afterReads})
	case groupRows:
		cs := append([]sweepChain{configChain, setupChain}, rowChains...)
		return append(cs, sweepChain{Name: "reads-after", Steps: afterReads})
	case groupTasks:
		return append(slices.Clone(taskChains), sweepChain{Name: "reads-after", Steps: afterReads})
	}
	return nil
}

// sweptCommands is every leaf command some chain runs.
func sweptCommands() []string {
	var out []string
	var all []sweepChain
	for _, g := range []string{groupReads, groupKnowledge, groupRows, groupTasks} {
		all = append(all, sweepChains(g)...)
	}
	for _, c := range all {
		for _, s := range c.Steps {
			if !slices.Contains(out, s.Cmd) {
				out = append(out, s.Cmd)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Reasons a leaf command is not in the sweep (a closed set).
const (
	whyInteractive = "interactive: needs a terminal, a browser or the file manager"
	whyOutward     = "reaches the network, a remote store, GitHub or a model"
	whyServer      = "changes the local API server's token"
	whyDestructive = "removes data for good or rewrites shared history; its own lifecycle test runs it"
	whyHook        = "run by git hooks, CI or an agent harness (claims, leases, runs, gates), not by a person at a prompt"
	whyMigration   = "a one-time migration of a v0.1.17 install; the migration tests run it"
	whyTeam        = "needs an org, a second person or an own store; the team and profile tests run it"
	whyEdit        = "opens the person's editor ($EDITOR) or reads an edited Markdown view file; TestJourneyFoldersEditRows runs both with a script as EDITOR"
	whyFixtureFile = "reads a fixture file (import, apply, template); the area's scenario tests run it"
	whyResource    = "needs resource fields and links; the resource scenario tests run it"
	whyMeta        = "rewrites metadata keys across rows; the meta scenario tests run it"
	whySecrets     = "needs a stored secret entry in a vault backend; the secrets tests run it"
	whyGenerator   = "prints lore's own reference or shell code; no machine state"
	whyRetired     = "a v0.1.17 spelling kept only to refuse with its replacement"
	whyLifecycle   = "a setup step that needs a failing check, a proposal or a leftover; the setup lifecycle tests drive it"
	whyStoreMove   = "moves rows to a second attached store, which these shapes do not attach; the move tests run it"
	whyRepoAdmin   = "changes which repos and projects a store knows; TestInitInEveryFolder and the repo tests run it"
	whyTaskFixture = "needs another person, a running timer or a merged branch; the task scenario tests run it"
	whyBatch       = "needs a bulk action's batch id; the trash and batch tests run it"
	whyNoCommit    = "needs a commit; the shapes' repos have none"
	whyIdentity    = "replaces who the person is on this machine; the identity lifecycle tests run it"
	whyStoreAdmin  = "makes or changes a store (a git repo of its own); the store lifecycle tests run it"
	whyStoreAdopt  = "copies a whole repo into a store folder made first (a store of its own, a folder, a repo with rows); the journey TestJourneyFoldersAdopt and wire TestStoreAdopt run it"
	whyStoreUse    = "points lore at a person's own git clone of a store, made with git clone first; wire TestStoreUse runs it"
	whySyncFixture = "needs a conflict, a second branch or a live remote; the sync tests run it"
	whySupport     = "writes a support bundle to send; the support-bundle tests run it"
	whySourceCopy  = "rewrites copies from their one source of truth (SOURCE_OF_TRUTH), which needs a stale copy, a leftover or an org store; the sot_* tests and the doctor fixes in the env, repos and foreign tests run it"
)

// notUserRun are the leaf commands the sweep does not run, each with why.
var notUserRun = map[string]string{
	"tui": whyInteractive, "edit": whyEdit, "onboard": whyInteractive, "setup web": whyInteractive, "setup run": whyInteractive, "machine reveal": whyInteractive,
	"context fetch": whyOutward, "org import": whyOutward, "setup join": whyOutward, "store add": whyOutward, "store open": whyOutward,
	"store forget": whyOutward, "store set-url": whyOutward, "sync run": whyOutward, "skill compile": whyOutward, "upgrade run": whyOutward,
	"repo fork": whyOutward, "repo set-url": whyOutward, "sync install-action": whyOutward, "live restore": whyOutward,
	"api token rotate": whyServer,
	"live purge":       whyDestructive, "live compact": whyDestructive, "sync purge": whyDestructive, "trash purge": whyDestructive,
	"migrate rollback": whyDestructive, "project delete": whyDestructive, "repo delete": whyDestructive, "store remove": whyDestructive,
	"cache prune": whyDestructive,
	"agent start": whyHook, "sync ci-merge": whyHook, "check pr-body": whyHook, "task heartbeat": whyHook, "task claim": whyHook,
	"task release": whyHook, "task revoke": whyHook, "run start": whyHook, "run end": whyHook, "run cancel": whyHook,
	"hold claim": whyHook, "hold release": whyHook, "gate request": whyHook, "gate approve": whyHook,
	"migrate run": whyMigration, "migrate enable": whyMigration, "migrate unlock": whyMigration,
	"actor contact add": whyTeam, "actor contact edit": whyTeam, "actor contact remove": whyTeam, "actor deactivate": whyTeam,
	"actor delete": whyTeam, "actor edit": whyTeam, "actor merge": whyTeam, "actor offboard": whyTeam, "actor period add": whyTeam, "actor period edit": whyTeam,
	"actor period list": whyTeam, "actor register": whyTeam, "actor rejoin": whyTeam, "actor show": whyTeam, "actor status": whyTeam,
	"contact-type add": whyTeam, "contact-type edit": whyTeam, "role add": whyTeam, "role edit": whyTeam,
	"org create": whyTeam, "org link": whyTeam, "org unlink": whyTeam, "org rename": whyTeam, "org set": whyTeam,
	"project member add": whyTeam, "project member edit": whyTeam, "project member list": whyTeam, "project member remove": whyTeam,
	"project shared-init": whyTeam, "team invite list": whyTeam, "team invite resend": whyTeam, "team invite revoke": whyTeam,
	"team add": whyTeam, "team list": whyTeam, "team show": whyTeam, "team remove": whyTeam, "team member add": whyTeam, "team member remove": whyTeam,
	"member add": whyTeam, "member list": whyTeam, "member show": whyTeam, "member edit": whyTeam, "member remove": whyTeam, // journey TestJourneyStaff runs them
	"handoff add": whyTeam, "handoff ack": whyTeam, "handoff show": whyTeam, "handoff delete": whyTeam, "machine label": whyTeam,
	"setup suggest-again": whyTeam,
	"mission apply":       whyFixtureFile, "row edit": whyEdit, "task import": whyFixtureFile, "meta import": whyFixtureFile, "resource import": whyFixtureFile,
	"task checklist import": whyFixtureFile, "task template add": whyFixtureFile, "task template delete": whyFixtureFile,
	"task template edit": whyFixtureFile, "task template rename": whyFixtureFile, "task template show": whyFixtureFile,
	"resource adopt": whyResource, "resource archive": whyResource, "resource clone": whyResource, "resource edit": whyResource,
	"resource field add": whyResource, "resource field edit": whyResource, "resource field remove": whyResource,
	"resource field rename": whyResource, "resource field retype": whyResource, "resource kind edit": whyResource,
	"resource kind rename": whyResource, "resource merge": whyResource, "resource rename": whyResource, "resource repoint": whyResource,
	"meta adopt": whyMeta, "meta delete": whyMeta, "meta edit": whyMeta, "meta forget": whyMeta, "meta rebuild": whyMeta,
	"meta rename": whyMeta, "meta value add": whyMeta, "meta value rename": whyMeta,
	"secrets allow rm": whySecrets, "secrets link": whySecrets, "secrets relabel": whySecrets, "secrets rename-entry": whySecrets,
	"secrets show": whySecrets,
	"completion":   whyGenerator, "spec docs": whyGenerator, "spec openapi": whyGenerator,
	"pii-pattern add": whyRetired, "pii-pattern disable": whyRetired, "pii-pattern enable": whyRetired,
	"setup fix": whyLifecycle, "setup repair": whyLifecycle, "setup import": whyLifecycle, "setup import-dismiss": whyLifecycle,
	"setup leftovers clear": whyLifecycle, "setup use": whyLifecycle,
	"move": whyStoreMove, "row move": whyStoreMove, "memory move": whyStoreMove, "meta move": whyStoreMove, "meta rehome": whyStoreMove,
	"resource rehome": whyStoreMove, "secrets move": whyStoreMove, "scope move": whyStoreMove,
	"init": whyRepoAdmin, "repo init": whyRepoAdmin, "repo add": whyRepoAdmin, "repo archive": whyRepoAdmin, "repo unarchive": whyRepoAdmin,
	"repo merge-ids": whyRepoAdmin, "project archive": whyRepoAdmin, "project unarchive": whyRepoAdmin,
	"task merged":   whyTaskFixture,
	"task reassign": whyTaskFixture,

	"task time stop": whyTaskFixture,
	"batch show":     whyBatch, "batch undo": whyBatch,
	"link add": whyNoCommit, "link remove": whyNoCommit, "land run": whyNoCommit,
	"identity set": whyIdentity, "identity unset": whyIdentity,
	"store init": whyStoreAdmin, "store folder add": whyStoreAdmin, "store folder remove": whyStoreAdmin, "store adopt": whyStoreAdopt, "store visibility": whyStoreAdmin, "store use": whyStoreUse,
	"sync fix-projects": whySyncFixture, "sync merge-rows": whySyncFixture, "sync promote": whySyncFixture, "sync resolve": whySyncFixture,
	"sync trash restore": whySyncFixture, "sync peek": whySyncFixture,

	"support-bundle run": whySupport,

	"actor refresh": whySourceCopy, "identity migrate": whySourceCopy, "org refresh": whySourceCopy,
}
