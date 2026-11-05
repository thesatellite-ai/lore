package host

// compat.go — v0.1.17's common flags on the core's generated commands
// (CLI parity, phase 3), moved here from the old binary's addCompatFlags so
// cmd/lore answers them the same way:
//
//	--db          refused (E_UNSUPPORTED): rows are row files, not lore.db
//	--project     accepted where the op has none (a row id names its project)
//	--read-only   refuse writes (also LORE_READ_ONLY=1)
//	--confirm     refused, renamed to --yes (a delete's --purge / --confirm
//	              are its op's own input, op.PurgeFlags, bound by the engine
//	              for every surface: P3-DEL-1)
//	--json        printed indented, as v0.1.17's printJSON did
//	LORE_PROJECT_ID / LORE_REPO fill --project / --repo when not given
//
// The running command and its arguments are recorded on the Runner so an
// OnError hook can explain an E_NOT_FOUND of the id argument (F1).

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	opsarchitecturenote "github.com/thesatellite-ai/lore/ops/architecturenote"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
	opsbehaviour "github.com/thesatellite-ai/lore/ops/behaviour"
	opscomment "github.com/thesatellite-ai/lore/ops/comment"
	opscookbookrecipe "github.com/thesatellite-ai/lore/ops/cookbookrecipe"
	opsdecision "github.com/thesatellite-ai/lore/ops/decision"
	opshandoff "github.com/thesatellite-ai/lore/ops/handoff"
	opshotfix "github.com/thesatellite-ai/lore/ops/hotfix"
	opsincident "github.com/thesatellite-ai/lore/ops/incident"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opslink "github.com/thesatellite-ai/lore/ops/link"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmission "github.com/thesatellite-ai/lore/ops/mission"
	opspattern "github.com/thesatellite-ai/lore/ops/pattern"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	opsplaybook "github.com/thesatellite-ai/lore/ops/playbook"
	opsprompt "github.com/thesatellite-ai/lore/ops/prompt"
	opsreminder "github.com/thesatellite-ai/lore/ops/reminder"
	opsrule "github.com/thesatellite-ai/lore/ops/rule"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssnapshot "github.com/thesatellite-ai/lore/ops/snapshot"
	opssuggestion "github.com/thesatellite-ai/lore/ops/suggestion"
	opstag "github.com/thesatellite-ai/lore/ops/tag"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	opstasklist "github.com/thesatellite-ai/lore/ops/tasklist"
	opstastepref "github.com/thesatellite-ai/lore/ops/tastepref"
	opstechdoc "github.com/thesatellite-ai/lore/ops/techdoc"
	opsworkflow "github.com/thesatellite-ai/lore/ops/workflow"
	opsworkspace "github.com/thesatellite-ai/lore/ops/workspace"
	"github.com/thesatellite-ai/lore/surface/cli"
)

// v0.1.17 flag names and environment fallbacks (saas/pkg/constants,
// projresolve).
const (
	FlagDB       = "db"
	FlagProject  = "project"
	FlagRepo     = "repo"
	FlagReadOnly = "read-only"
	FlagConfirm  = "confirm"
	// EnvProjectID fills --project when the caller gave none.
	EnvProjectID = "LORE_PROJECT_ID"
	// EnvRepo fills --repo when the caller gave none.
	EnvRepo = "LORE_REPO"
)

// jsonIndent is v0.1.17's printJSON indent.
const jsonIndent = "  "

// ExternalSourceNoun is the CLI spelling of opsexternalsource.Noun.
const ExternalSourceNoun = "external-source"

// CompatNouns are the command groups whose generated commands keep
// v0.1.17's common flags (the old binary's coreNouns). A migrated noun
// that existed in v0.1.17 adds its name here.
var CompatNouns = []string{
	opsmemory.Noun,
	opspattern.Noun, opsplaybook.Noun, opsprompt.Noun,
	opsrule.Noun, opsdecision.Noun, opshotfix.Noun,
	opstag.Noun, opscomment.Noun, opslink.Noun, opslink.CommitNoun,
	opstask.Noun, opstasklist.Noun, opsmission.Noun, opsplan.Noun,
	opsreminder.Noun,
	opsaudit.Noun, // P3-AUDIT-2: --db/--project refused with a reason, as on every core noun
	opsinfra.NounActor, opsinfra.NounProject, opsinfra.NounRepo, opsinfra.NounMachine,
	opsarchitecturenote.Noun, opsbehaviour.Noun, opscookbookrecipe.Noun, opsincident.Noun,
	opssuggestion.Noun, opstastepref.Noun, opsworkflow.Noun, opsworkspace.Noun,
	opshandoff.Noun, opssnapshot.Noun, opstechdoc.Noun, ExternalSourceNoun,
	opssearch.Noun,
}

// CompatOptions are the binary-specific parts of the compat flags.
type CompatOptions struct {
	// Nouns are the command groups that get the flags; nil = CompatNouns.
	Nouns []string
	// Printed is called when a generated command ran or failed its flag
	// parse (surface/cli printed the error itself). Nil = nothing.
	Printed func()
	// BeforeCommand runs after the flags are applied, before the op (the
	// old binary's lore.db sync pass). Nil = nothing.
	BeforeCommand func(cmd *cobra.Command, readOnly bool)
	// Filed returns the "filed:" line of a routed write (where it went and
	// why, v0.1.17 printRoute), "" for none. Nil = never.
	Filed func() string
	// Wrap, when set, adjusts each command after the flags were added
	// (the old binary's task list merged check).
	Wrap func(c *cobra.Command)
}

// CompatFlagsHook gives every generated command of the compat nouns
// v0.1.17's common flags and indented --json (see the file comment).
func CompatFlagsHook(o CompatOptions) Hook {
	nouns := o.Nouns
	if nouns == nil {
		nouns = CompatNouns
	}
	return Hook{Name: HookCompat, Command: func(root *cobra.Command, r *Runner) {
		for _, noun := range nouns {
			g, _, err := root.Find([]string{noun})
			if err != nil || g.Name() != noun {
				continue
			}
			for _, c := range g.Commands() {
				// Only the surface's operation commands: a host command
				// under a core noun prints its own errors, and a group
				// has no RunE to wrap.
				if !cli.Generated(c) || c.RunE == nil {
					continue
				}
				addCompatFlags(c, r, o)
				recordCommand(c, r)
				if o.Wrap != nil {
					o.Wrap(c)
				}
			}
		}
	}}
}

// recordCommand records the running command and its arguments on the
// runner (the F1 not-found explanation reads them).
func recordCommand(c *cobra.Command, r *Runner) {
	run := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r.State.Cmd, r.State.Args = cmd, args
		return run(cmd, args)
	}
}

// addCompatFlags adds v0.1.17's --db / --project / --read-only, the
// --confirm refusal (only where the op has no confirm input of its own: a
// delete's is op.PurgeFlags), the env defaults and indented --json to one
// generated command c.
func addCompatFlags(c *cobra.Command, r *Runner, o CompatOptions) {
	var db, project string
	var readOnly, confirm bool
	if c.Flags().Lookup(FlagDB) == nil {
		c.Flags().StringVar(&db, FlagDB, "", "v0.1.17 lore.db path: not used by this command (refused)")
	}
	if c.Flags().Lookup(FlagProject) == nil {
		c.Flags().StringVar(&project, FlagProject, "", "accepted for v0.1.17 scripts; a row id already names its project")
	}
	if c.Flags().Lookup(FlagReadOnly) == nil {
		c.Flags().BoolVar(&readOnly, FlagReadOnly, false, "refuse writes (also "+EnvReadOnly+"=1)")
	}
	ownConfirm := c.Flags().Lookup(cli.FlagYes) != nil && c.Flags().Lookup(FlagConfirm) == nil
	if ownConfirm {
		c.Flags().BoolVar(&confirm, FlagConfirm, false, "v0.1.17 spelling: refused, use --yes")
	}
	printed := func() {
		if o.Printed != nil {
			o.Printed()
		}
	}
	flagErr := c.FlagErrorFunc()
	c.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		printed() // the surface prints cobra's usage error itself
		return flagErr(cmd, err)
	})
	inner := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		defer printed() // every error a generated command returns was printed by surface/cli
		r.State.ReadOnly = readOnly
		r.State.Pending = compatRefusal(cmd, ownConfirm)
		applyEnvDefaults(cmd)
		if o.BeforeCommand != nil {
			o.BeforeCommand(cmd, readOnly)
		}
		var buf bytes.Buffer
		out := cmd.OutOrStdout()
		cmd.SetOut(&buf)
		err := inner(cmd, args)
		cmd.SetOut(out)
		if asJSON, _ := cmd.Flags().GetBool(cli.FlagJSON); asJSON {
			return errors.Join(err, writeIndented(out, buf.Bytes()))
		}
		// Buffered so the "filed:" line (where a routed write went and
		// why) goes right under the success line, before the op's own
		// detail lines. Prompts go to stderr, so nothing waits behind it.
		b := buf.Bytes()
		if err == nil && o.Filed != nil {
			if f := o.Filed(); f != "" {
				b = insertAfterFirstLine(b, "  filed: "+f+"\n")
			}
		}
		_, werr := out.Write(b)
		return errors.Join(err, werr)
	}
}

// compatRefusal is the refusal a compat flag sets for this run (nil =
// none); ownConfirm is whether the compat hook added --confirm itself.
func compatRefusal(cmd *cobra.Command, ownConfirm bool) error {
	switch {
	case ownConfirm && cmd.Flags().Changed(FlagConfirm):
		return errs.New(errs.InvalidInput, "--confirm was renamed: confirm with --yes").
			WithHint("rerun with --yes instead of --confirm")
	case cmd.Flags().Changed(FlagDB):
		return errs.New(errs.Unsupported, "--db is not supported by this command: its rows are row files read from the repository, not lore.db").
			WithHint("run the command inside the repository (or name a store with --store / --to)")
	}
	return nil
}

// envFlagDefaults are v0.1.17's environment fallbacks for a row command's
// scope flags (flag > env). LORE_DB is not one: --db is refused.
var envFlagDefaults = [][2]string{
	{FlagProject, EnvProjectID},
	{FlagRepo, EnvRepo},
}

// applyEnvDefaults sets each scope flag the command has, and the caller did
// not give, from its environment variable when that is set.
func applyEnvDefaults(cmd *cobra.Command) {
	for _, fe := range envFlagDefaults {
		fl := cmd.Flags().Lookup(fe[0])
		v := os.Getenv(fe[1])
		if fl == nil || fl.Changed || v == "" {
			continue
		}
		_ = cmd.Flags().Set(fe[0], v) // a string flag accepts any value; the op validates it
	}
}

// insertAfterFirstLine returns b with line inserted after its first line
// (at the end when b has no newline; just line when b is empty).
func insertAfterFirstLine(b []byte, line string) []byte {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		if len(b) > 0 {
			return append(append(append([]byte{}, b...), '\n'), line...)
		}
		return []byte(line)
	}
	out := make([]byte, 0, len(b)+len(line))
	out = append(out, b[:i+1]...)
	out = append(out, line...)
	return append(out, b[i+1:]...)
}

// writeIndented prints JSON the way v0.1.17's printJSON did
// (json.MarshalIndent with two spaces, one trailing newline); anything that
// is not one JSON value is written unchanged.
func writeIndented(w io.Writer, b []byte) error {
	var ind bytes.Buffer
	if err := json.Indent(&ind, bytes.TrimSpace(b), "", jsonIndent); err != nil {
		_, werr := w.Write(b)
		return werr
	}
	ind.WriteByte('\n')
	_, err := w.Write(ind.Bytes())
	return err
}
