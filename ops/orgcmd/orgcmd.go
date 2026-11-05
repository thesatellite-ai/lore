// Package orgcmd defines the `lore org` operations (layer L5,
// CLI_REFERENCE 2.26, CONFIG_GUIDE 6.4, LORE_TEAM_SPEC F10, F11, PROV
// 11.5): create, import, link, rename, set and use. They edit this
// machine's ~/.lore/config.yaml, the committed .lore/org.yaml, and the org
// store's lore-org.yaml and link files, never the repo's rows.
//
//	org.<verb> ─▶ Env.OrgHost ─▶ Host.<Verb>(in, confirmed)
//	                              ├─ validate, build the plan (writes nothing)
//	                              ├─ asks and not confirmed: Report{Lines: plan, Ask}
//	                              │     ─▶ E_NEEDS_CONFIRM (plan + question; the surface asks, re-runs with yes)
//	                              └─ confirmed: write ─▶ Report{Lines: plan + results}
//
// PASS3 ONE-PATH: each verb is an operation (CLI, `lore api`, docs from
// one definition); ONE-PATH overrides CLI_REFERENCE 2.26's "v0.1.17-style
// command, not in the operation registry". The machinery (store clones,
// the org resolution, the roster registration) is the host's: the binary
// that owns it supplies Host, and a binary without one answers
// E_UNSUPPORTED (P3-ORG-G-2). The verbs are Local: they run without
// opening the repo's stores, so `org import` and `org rename` work outside
// a repo; the host finds the repo itself where a verb needs one.
// v0.1.17's `org unlink` is not in the design and is dropped.
package orgcmd

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names.
const (
	OpCreate = "org.create"
	OpImport = "org.import"
	OpLink   = "org.link"
	OpRename = "org.rename"
	OpSet    = "org.set"
	OpUse    = "org.use"
)

// DefaultOrgFolder is the store folder for company rules and the roster
// when --org-folder is not given (CLI_REFERENCE 2.26, CFG1).
const DefaultOrgFolder = "org"

// Host is the machinery behind `lore org`, supplied by the binary.
//
// Invariant: a verb that asks (create, import, link) writes nothing when
// confirmed is false and it returns a Report with Ask set; every other
// return is the finished run.
type Host interface {
	Create(ctx context.Context, in CreateIn, confirmed bool) (Report, error)
	Import(ctx context.Context, in ImportIn, confirmed bool) (Report, error)
	Link(ctx context.Context, in LinkIn, confirmed bool) (Report, error)
	Rename(ctx context.Context, in RenameIn) (RenameOut, error)
	Set(ctx context.Context, in SetIn) (Report, error)
	Use(ctx context.Context, in UseIn) (Report, error)
}

// Env is what the org operations need; *wire.App implements it.
type Env interface {
	// OrgHost is the binary's Host; ok is false on a binary without one.
	OrgHost(ctx context.Context) (h Host, ok bool)
}

// Report is a verb's output: the lines v0.1.17 printed, in order.
type Report struct {
	Lines []string `json:"lines"`
	// Ask is the question of a verb that stopped before writing because
	// it was not confirmed ("Create it? [y/N]" without the suffix); empty
	// on a finished run.
	Ask string `json:"-"`
}

// Text implements op.Texter.
func (r Report) Text() string { return joinLines(r.Lines) }

// joinLines is lines as terminal text, each ending in a newline.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// CreateIn is the input of org.create (CLI_REFERENCE 2.26).
type CreateIn struct {
	Org       string   `json:"org" arg:"0" desc:"the new org entry's name"`
	Store     string   `json:"store,omitempty" flag:"store" desc:"the org's store: a name in this repo's .lore/stores.yaml, or a store url (required)"`
	OrgFolder string   `json:"org_folder,omitempty" flag:"org-folder" desc:"the store folder for company rules and the roster (default org)"`
	Own       []string `json:"own,omitempty" flag:"own" desc:"pattern of the org's own repos, e.g. github.com/agency/* (repeatable)"`
	Repos     []string `json:"repos,omitempty" flag:"repos" desc:"pattern of client repos the org works in (repeatable)"`
	Key       string   `json:"key,omitempty" flag:"key" desc:"your identity key in this org: human:<your email there> (required)"`
	Name      string   `json:"name,omitempty" flag:"name" desc:"your display name (required)"`
	Handle    string   `json:"handle,omitempty" flag:"handle" desc:"your @handle on the roster"`
}

// ImportIn is the input of org.import (CONFIG_GUIDE 6.4).
type ImportIn struct {
	URL    string `json:"url" arg:"0" desc:"the org's store url"`
	Key    string `json:"key,omitempty" flag:"key" desc:"your identity key in this org: human:<your email there> (required)"`
	Name   string `json:"name,omitempty" flag:"name" desc:"your display name (required)"`
	Handle string `json:"handle,omitempty" flag:"handle" desc:"your @handle on the roster"`
	As     string `json:"as,omitempty" flag:"as" desc:"entry name, when the suggested one is taken"`
	Branch string `json:"branch,omitempty" flag:"branch" desc:"store branch to read the suggestion from (default: the store's default branch)"`
}

// LinkIn is the input of org.link (F11).
type LinkIn struct {
	Org     string `json:"org" arg:"0" desc:"the org entry"`
	Project string `json:"project,omitempty" flag:"project" desc:"the internal project folder in the store (required)"`
	Own     bool   `json:"own,omitempty" flag:"own" desc:"this is one of the org's own repos (only for own: patterns)"`
	Default bool   `json:"default,omitempty" flag:"default" desc:"the link --to <org> uses when this repo has several"`
	Store   string `json:"store,omitempty" flag:"store" desc:"which of the org's stores (default: the first)"`
	Remote  string `json:"remote,omitempty" flag:"remote" desc:"which remote, when the repo has several"`
	DryRun  bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"validate and show the link, write nothing"`
}

// RenameIn is the input of org.rename (PROV 11.5).
type RenameIn struct {
	Old string `json:"old" arg:"0" desc:"the org entry's current name"`
	New string `json:"new" arg:"1" desc:"its new name"`
}

// RenameOut is the output of org.rename (v0.1.17 `--json` shape).
type RenameOut struct {
	From       string `json:"from"`
	To         string `json:"to"`
	ID         string `json:"id,omitempty"`
	IDRecorded bool   `json:"id_recorded"`
	RepoOrgs   int    `json:"repo_orgs"`
	Defaults   int    `json:"defaults"`
	Config     string `json:"config"`
	// Lines is the human output, built by the host.
	Lines []string `json:"-"`
}

// Text implements op.Texter.
func (o RenameOut) Text() string { return joinLines(o.Lines) }

// SetIn is the input of org.set (F10 rule 3).
type SetIn struct {
	Org     string `json:"org" arg:"0" desc:"the org the committed .lore/org.yaml names"`
	Commit  bool   `json:"commit,omitempty" flag:"commit" desc:"confirm writing the committed .lore/org.yaml"`
	Project string `json:"project,omitempty" flag:"project" desc:"the internal project folder (optional)"`
}

// UseIn is the input of org.use (F10 rule 2).
type UseIn struct {
	Entry string `json:"entry,omitempty" arg:"0" desc:"the org entry to use for this project on this machine"`
	Clear bool   `json:"clear,omitempty" flag:"clear" desc:"forget the choice for this project"`
}

// Ops returns every org operation bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	codes := []errs.Code{errs.InvalidInput, errs.NotFound, errs.NotProjectRoot, errs.ConfigInvalid, errs.NotAStore,
		errs.Offline, errs.StoreUnreachable, errs.OrgAmbiguous, errs.OrgDisagrees, errs.Unsupported, errs.NotImplemented, errs.Internal}
	asking := append([]errs.Code{errs.NeedsConfirm}, codes...)
	spec := func(name, summary string, errCodes []errs.Code, mayAsk bool, ex op.Example) op.Spec {
		s := op.Spec{Name: name, Summary: summary, Permission: op.PermWrite, Access: op.AccessRead, Local: true,
			Errors: errCodes, Examples: []op.Example{ex}}
		// MayAsk: the verb lists its plan and asks before writing
		// (CLI_REFERENCE 2.26 "It asks before writing").
		s.Confirm.MayAsk = mayAsk
		return s
	}
	return []op.Entry{
		op.Define(spec(OpCreate, "Start a new org on a lore store: suggest it to teammates, add it with your identity, join its roster", asking, true,
			op.Example{Title: "create an org", CLI: "lore org create agency --store agency --own 'github.com/agency/*' --key human:mira@agency.example --name Mira --yes", Want: "added org entry"}),
			func(ctx context.Context, in CreateIn) (Report, error) {
				if strings.TrimSpace(in.Store) == "" {
					return Report{}, errs.New(errs.InvalidInput, "name the org's store with --store <name in .lore/stores.yaml | url>").
						WithHint("make one first with `lore store init <name> <url>`")
				}
				if in.OrgFolder == "" {
					in.OrgFolder = DefaultOrgFolder
				}
				return o.ask(ctx, OpCreate, func(h Host, yes bool) (Report, error) { return h.Create(ctx, in, yes) })
			}),
		op.Define(spec(OpImport, "Adopt the org entry a team suggests in its store, with your identity, and join its roster", asking, true,
			op.Example{Title: "join an org", CLI: "lore org import git@github.com:agency/notes --key human:me@agency.example --name Me --yes", Want: "added org entry"}),
			func(ctx context.Context, in ImportIn) (Report, error) {
				return o.ask(ctx, OpImport, func(h Host, yes bool) (Report, error) { return h.Import(ctx, in, yes) })
			}),
		op.Define(spec(OpLink, "Link this repo to an internal project in the org's store", asking, true,
			op.Example{Title: "check a link", CLI: "lore org link agency --project client --dry-run", Want: "nothing written"}),
			func(ctx context.Context, in LinkIn) (Report, error) {
				if strings.TrimSpace(in.Project) == "" {
					return Report{}, errs.New(errs.InvalidInput, "name the internal project folder with --project <folder>")
				}
				return o.ask(ctx, OpLink, func(h Host, yes bool) (Report, error) { return h.Link(ctx, in, yes || in.DryRun) })
			}),
		op.Define(spec(OpRename, "Rename an org entry on this machine (a label change; the org's id and every row stay)", codes, false,
			op.Example{Title: "rename", CLI: "lore org rename agency northwind", Want: "is now"}),
			func(ctx context.Context, in RenameIn) (RenameOut, error) {
				h, err := o.host(ctx, OpRename)
				if err != nil {
					return RenameOut{}, err
				}
				return h.Rename(ctx, in)
			}),
		op.Define(spec(OpSet, "Write the committed .lore/org.yaml (only in the org's own repos)", codes, false,
			op.Example{Title: "name the org for every teammate", CLI: "lore org set agency --commit", Want: "wrote"}),
			func(ctx context.Context, in SetIn) (Report, error) {
				if !in.Commit {
					return Report{}, errs.New(errs.InvalidInput, "lore org set writes a COMMITTED file; pass --commit to confirm").
						WithHint("for a machine-only choice use `lore org use " + in.Org + "`")
				}
				h, err := o.host(ctx, OpSet)
				if err != nil {
					return Report{}, err
				}
				return h.Set(ctx, in)
			}),
		op.Define(spec(OpUse, "Choose the org entry for this project on this machine (not committed)", codes, false,
			op.Example{Title: "back to automatic resolution", CLI: "lore org use --clear", Want: ""}),
			func(ctx context.Context, in UseIn) (Report, error) {
				if in.Clear && in.Entry != "" {
					return Report{}, errs.New(errs.InvalidInput, "pass an entry or --clear, not both")
				}
				h, err := o.host(ctx, OpUse)
				if err != nil {
					return Report{}, err
				}
				return h.Use(ctx, in)
			}),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

// host returns the Host, or E_UNSUPPORTED naming the command.
func (o ops) host(ctx context.Context, name string) (Host, error) {
	h, ok := o.env.OrgHost(ctx)
	if !ok || h == nil {
		return nil, errs.New(errs.Unsupported, "lore "+strings.ReplaceAll(name, ".", " ")+" needs the org store machinery, which this binary does not have yet").
			WithHint("run it with the release binary")
	}
	return h, nil
}

// ask runs a verb that asks first: an unconfirmed run that stopped before
// writing becomes E_NEEDS_CONFIRM carrying the plan and the question, so
// the CLI asks on a terminal and re-runs confirmed, and every other caller
// (no terminal, lore api) is told to pass --yes / "yes". P3-ORG-G-2: this
// replaces v0.1.17's E_INVALID_INPUT "confirm with --yes when there is no
// terminal" with the core's confirm path.
func (o ops) ask(ctx context.Context, name string, run func(h Host, yes bool) (Report, error)) (Report, error) {
	h, err := o.host(ctx, name)
	if err != nil {
		return Report{}, err
	}
	r, err := run(h, op.Confirmed(ctx))
	if err != nil || r.Ask == "" {
		return r, err
	}
	msg := r.Ask
	if len(r.Lines) > 0 {
		msg = strings.Join(r.Lines, "\n") + "\n" + r.Ask
	}
	return Report{}, errs.New(errs.NeedsConfirm, msg).WithQuestion(r.Ask).
		WithHint("nothing written; confirm with --yes (or \"yes\": true over lore api)")
}
