package host

// compat.go — v0.1.17's common flags on the core's generated commands
// (CLI parity, phase 3), moved here from the old binary's addCompatFlags so
// cmd/lore answers them the same way:
//
//	--db          refused (E_UNSUPPORTED): rows are row files, not lore.db
//	--project     accepted where the op has none (a row id names its project)
//	--read-only   refuse writes (also LORE_READ_ONLY=1)
//	--confirm     on a delete: confirms --purge, else v0.1.17's --yes (DQ8,
//	              FA-DEL-1); elsewhere refused, renamed to --yes
//	--purge       on a delete: skip the trash (needs the literal --confirm)
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
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsarchitecturenote "github.com/thesatellite-ai/lore/ops/architecturenote"
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
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
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
	FlagPurge    = "purge"
	// EnvProjectID fills --project when the caller gave none.
	EnvProjectID = "LORE_PROJECT_ID"
	// EnvRepo fills --repo when the caller gave none.
	EnvRepo = "LORE_REPO"
)

// jsonIndent is v0.1.17's printJSON indent.
const jsonIndent = "  "

// trashLineMark is the phrase of every noun delete's success line
// (ops/internal/trashline).
const trashLineMark = "moved to the trash"

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

// PurgeOnlyDeletes are the delete operations outside CompatNouns that get
// only the delete half of the compat flags, --purge / --confirm (PASS3:
// like every core delete; FA-DEL-1). resource and meta never had v0.1.17's
// --db / --project / --read-only.
var PurgeOnlyDeletes = []string{opssettings.OpResDelete, opssettings.OpMetaDelete}

// DeletePurgeHook gives each PurgeOnlyDeletes command --purge / --confirm
// with the same refusal and output rewrite as a compat-noun delete. A
// command not mounted in this build is skipped.
func DeletePurgeHook(o CompatOptions) Hook {
	return Hook{Name: HookDeletePurge, Command: func(root *cobra.Command, r *Runner) {
		for _, name := range PurgeOnlyDeletes {
			noun, verb, ok := strings.Cut(name, ".")
			if !ok || verb != op.VerbDelete {
				continue
			}
			c, _, err := root.Find([]string{noun, verb})
			if err != nil || c.Name() != verb || !cli.Generated(c) || c.RunE == nil {
				continue
			}
			addFlags(c, r, o, false)
		}
	}}
}

// addCompatFlags adds the flags to one generated command c.
func addCompatFlags(c *cobra.Command, r *Runner, o CompatOptions) { addFlags(c, r, o, true) }

// addFlags adds the delete half (--purge / --confirm on a delete) and,
// when full, v0.1.17's --db / --project / --read-only, the --confirm
// refusal elsewhere, the env defaults and indented --json.
func addFlags(c *cobra.Command, r *Runner, o CompatOptions, full bool) {
	var db, project string
	var readOnly, confirm, purge bool
	isDelete := c.Name() == op.VerbDelete
	if full && c.Flags().Lookup(FlagDB) == nil {
		c.Flags().StringVar(&db, FlagDB, "", "v0.1.17 lore.db path: not used by this command (refused)")
	}
	if full && c.Flags().Lookup(FlagProject) == nil {
		c.Flags().StringVar(&project, FlagProject, "", "accepted for v0.1.17 scripts; a row id already names its project")
	}
	if full && c.Flags().Lookup(FlagReadOnly) == nil {
		c.Flags().BoolVar(&readOnly, FlagReadOnly, false, "refuse writes (also "+EnvReadOnly+"=1)")
	}
	if (full || isDelete) && c.Flags().Lookup(cli.FlagYes) != nil && c.Flags().Lookup(FlagConfirm) == nil {
		desc := "v0.1.17 spelling: refused, use --yes"
		if isDelete { // DQ8 / FA-DEL-1: accepted on a delete
			desc = "confirms a --purge (or a delete under policy trash.purge_on_delete); without them, v0.1.17's spelling of --yes"
		}
		c.Flags().BoolVar(&confirm, FlagConfirm, false, desc)
	}
	if isDelete && c.Flags().Lookup(FlagPurge) == nil {
		c.Flags().BoolVar(&purge, FlagPurge, false, "skip the trash: remove for good, only a tombstone stays (needs --confirm)")
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
		if isDelete {
			r.State.Purge = &engine.PurgeRequest{Asked: purge, Confirmed: confirm}
		}
		r.State.Pending = compatRefusal(cmd, args, isDelete, purge, confirm)
		if !full {
			return runDeleteOnly(cmd, args, inner, r.State.Purge, confirm)
		}
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
		if err == nil && r.State.Purge != nil {
			b = purgedLines(b, r.State.Purge, confirm)
		}
		if err == nil && o.Filed != nil {
			if f := o.Filed(); f != "" {
				b = insertAfterFirstLine(b, "  filed: "+f+"\n")
			}
		}
		_, werr := out.Write(b)
		return errors.Join(err, werr)
	}
}

// runDeleteOnly runs a PurgeOnlyDeletes command: JSON as the op prints it
// (it carries the ids), text with the trash line rewritten.
func runDeleteOnly(cmd *cobra.Command, args []string, inner func(*cobra.Command, []string) error, pr *engine.PurgeRequest, confirm bool) error {
	if asJSON, _ := cmd.Flags().GetBool(cli.FlagJSON); asJSON {
		return inner(cmd, args)
	}
	var buf bytes.Buffer
	out := cmd.OutOrStdout()
	cmd.SetOut(&buf)
	err := inner(cmd, args)
	cmd.SetOut(out)
	b := buf.Bytes()
	if err == nil {
		b = purgedLines(b, pr, confirm)
	}
	_, werr := out.Write(b)
	return errors.Join(err, werr)
}

// compatRefusal is the refusal a compat flag sets for this run (nil = none).
// On a delete given --confirm without --purge it also sets --yes (DQ8).
func compatRefusal(cmd *cobra.Command, args []string, isDelete, purge, confirm bool) error {
	switch {
	case isDelete && purge && !confirm: // DQ2/DEL-R4: a purge never prompts, it needs the literal --confirm
		return errs.New(errs.NeedsConfirm, "--purge removes "+purgeTarget(args)+" for good, without the trash (only a tombstone stays)").
			WithHint("ask the person, then rerun with --confirm (--yes does not purge)")
	case isDelete && cmd.Flags().Changed(FlagConfirm):
		// DQ8 (FA-DEL-1): v0.1.17's delete --confirm is accepted; a delete
		// is soft, so it answers the question like --yes.
		if fl := cmd.Flags().Lookup(cli.FlagYes); fl != nil {
			_ = fl.Value.Set("true") // a bool flag accepts "true"
		}
		return nil
	case cmd.Flags().Changed(FlagConfirm):
		return errs.New(errs.InvalidInput, "--confirm was renamed: confirm with --yes").
			WithHint("rerun with --yes instead of --confirm")
	case cmd.Flags().Changed(FlagDB):
		return errs.New(errs.Unsupported, "--db is not supported by this command: its rows are row files read from the repository, not lore.db").
			WithHint("run the command inside the repository (or name a store with --store / --to)")
	}
	return nil
}

// purgeTarget names what a refused --purge would have removed: the named
// row, or the bulk form's matches (resource delete --kind --where).
func purgeTarget(args []string) string {
	if len(args) == 0 {
		return "every matching row"
	}
	return strings.Join(args, " ")
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

// purgedLines rewrites a delete's "moved to the trash" line when the
// engine purged instead (--purge, or policy trash.purge_on_delete), and
// adds DQ8's hint to a plain delete given v0.1.17's --confirm.
func purgedLines(b []byte, pr *engine.PurgeRequest, confirm bool) []byte {
	if len(pr.Purged) == 0 {
		if confirm && bytes.Contains(b, []byte(trashLineMark)) {
			b = append(b, []byte("  note: a delete is soft now (restorable); to remove content for good: lore trash purge <id> --confirm\n")...)
		}
		return b
	}
	names := make([]string, 0, len(pr.Purged))
	for _, id := range pr.Purged {
		names = append(names, id.String())
	}
	lines := strings.SplitAfter(string(b), "\n")
	for i, l := range lines {
		if strings.Contains(l, trashLineMark) {
			lines[i] = "✓ purged for good: " + strings.Join(names, ", ") + " (tombstones written; old content is still in git history)\n"
		}
	}
	return []byte(strings.Join(lines, ""))
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
