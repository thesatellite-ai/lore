package app

// edit.go — `lore edit --id <id>` and `lore path <id>` (alias `lore open`), the
// plain-files commands (D2, D3):
//
//	lore path <id>   ─► row path (shortcut; one absolute path, --json {id, table, path, store, folder})
//	lore edit --id <id>   ─► row markdown (the view) ─► temp file ─► $VISUAL / $EDITOR / vi
//	                 ─► row edit --file <temp> (the normal write path, its prompts and output)
//	                 ─► success / no change: temp file removed
//	                    refused: the error (file:line), the edited text kept, its path printed
//	lore edit --id <id> --file <view.md> ─► row edit --file (no editor)
//
// Why a host command around two operations: the editor needs this
// process's terminal, and the write must be the ordinary row.edit run, so
// its secret menu, confirmation and --json output are the same as for any
// write (an operation that ran the editor itself would open it again on
// every confirmation re-run).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/wire"
)

// Editor environment, in the order lore reads it (git's order).
const (
	envVisual     = "VISUAL"
	envEditor     = "EDITOR"
	defaultEditor = "vi"
	// editTempPattern names the temporary view (os.CreateTemp: * is random).
	editTempPattern = "lore-edit-*.md"
	flagFile        = "file"
	flagID          = "id"
	flagYes         = "yes"
)

// mountEdit adds `lore path` (alias `lore open`) and `lore edit`; a build
// without the row operations adds neither.
func (h *host) mountEdit(root *cobra.Command) {
	if p := find(root, opsrow.Noun, opsrow.PathVerb); p != nil {
		c := shortcutOf(p, opsrow.PathVerb, "Print the file path of any row by id (alias: open)")
		c.Aliases = []string{opsrow.OpenAlias}
		root.AddCommand(c)
	}
	target := find(root, opsrow.Noun, opsrow.EditVerb)
	if target == nil {
		return
	}
	root.AddCommand(h.editCommand(target))
}

// editCommand is `lore edit --id <id> [--file f] [--yes] [--json]`.
func (h *host) editCommand(target *cobra.Command) *cobra.Command {
	var file, id string
	var yes, asJSON bool
	c := &cobra.Command{
		Use:   opsrow.EditVerb + " --" + flagID + " <id>",
		Short: "Edit any row in your editor as Markdown (fields as frontmatter, long text as body)",
		Long: "Opens the row in $VISUAL, else $EDITOR, else vi, as Markdown: every field as YAML frontmatter, the\n" +
			"row's long text (body, content or description) below it. Save and close the editor: lore checks\n" +
			"the row like any write (fields, references, secret scan, policy), stamps it and writes it where the\n" +
			"row lives (commit and push as any write there). Nothing changed: nothing is written.\n" +
			"A refused edit names the file and line, keeps your text in a file and prints its path.\n" +
			"The row file stays JSON: `lore path --id <id>` prints it, and editing it directly works too.",
		Example: "  lore edit --id tsk_01a1cf02b365751bac5520b3d393eef1\n" +
			"  EDITOR=\"code --wait\" lore edit --id mem_01a1cf02b365751bac5520b3d393eef1\n" +
			"  lore edit --id tsk_01a1cf02b365751bac5520b3d393eef1 --file kept.md   # write back a kept or prepared view, no editor\n" +
			"  lore row markdown --id tsk_01a1cf02b365751bac5520b3d393eef1           # the view, to edit with another tool",
		// The id is --id (U-4); one positional id stays a quiet alias.
		Args: func(_ *cobra.Command, args []string) error {
			if (len(args) == 1) != (id != "") {
				return nil
			}
			return errs.New(errs.InvalidInput, fmt.Sprintf("edit: want one row id, got %d", len(args)+min(len(id), 1))).
				WithHint("lore edit --id <id> (e.g. lore edit --id tsk_01a1cf02b365751bac5520b3d393eef1); `lore task list` or `lore search --query <words>` finds an id")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				id = args[0]
			}
			pass := []string{"--" + flagID, id}
			if yes {
				pass = append(pass, "--"+flagYes)
			}
			if asJSON {
				pass = append(pass, "--"+cli.FlagJSON)
			}
			if file != "" {
				return forward(target)(cmd, append(pass, "--"+flagFile, file))
			}
			return h.editInEditor(cmd, target, id, pass)
		},
	}
	c.Flags().StringVar(&id, flagID, "", "the row id (any kind: tsk_, mem_, rul_, …)")
	c.Flags().StringVar(&file, flagFile, "", "write back this Markdown view without opening an editor (a view kept by a refused edit, or one made with lore row markdown)")
	c.Flags().BoolVar(&yes, flagYes, false, "answer yes to a confirmation the write asks without a terminal (a secret finding)")
	c.Flags().BoolVar(&asJSON, cli.FlagJSON, false, "print the result (or the error) as JSON")
	return c
}

// editInEditor runs the view through the person's editor and writes it back.
func (h *host) editInEditor(cmd *cobra.Command, target *cobra.Command, id string, pass []string) error {
	editor, err := editorCommand(wire.StdinIsTerminal)
	if err != nil {
		return err
	}
	view, err := h.markdownView(cmd, id)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", editTempPattern)
	if err != nil {
		return errs.Wrap(errs.Internal, "edit: create the temporary view file", err).
			WithHint("check that the temp folder (" + os.TempDir() + ") is writable, or set TMPDIR")
	}
	tmp := f.Name()
	_, werr := io.WriteString(f, view.Markdown)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return errs.Wrap(errs.Internal, "edit: write the temporary view "+tmp, werr)
	}
	if err := runEditor(cmd, editor, tmp); err != nil {
		keepNote(cmd, id, tmp)
		return err
	}
	err = forward(target)(cmd, append(pass, "--"+flagFile, tmp))
	if err != nil {
		keepNote(cmd, id, tmp)
		return err
	}
	_ = os.Remove(tmp) // written or no change: the view has done its job
	return nil
}

// markdownView runs row.markdown through the CLI's runner (every host hook:
// the live overlay reads a live row as any command does).
func (h *host) markdownView(cmd *cobra.Command, id string) (opsrow.MarkdownOut, error) {
	e, ok := h.app.Registry().Lookup(opsrow.OpMarkdown)
	if !ok || h.runner == nil {
		return opsrow.MarkdownOut{}, errs.New(errs.Unsupported, "edit: this build has no "+opsrow.OpMarkdown+" operation")
	}
	body, err := json.Marshal(opsrow.MarkdownIn{ID: id})
	if err != nil {
		return opsrow.MarkdownOut{}, errs.Wrap(errs.Internal, "edit: encode the view request", err)
	}
	res, err := h.runner.Run(cmd.Context(), e, op.JSONDecoder{Body: body}, op.Caller{Interactive: wire.StdinIsTerminal()})
	if err != nil {
		return opsrow.MarkdownOut{}, err
	}
	raw, err := res.JSON()
	if err != nil {
		return opsrow.MarkdownOut{}, errs.Wrap(errs.Internal, "edit: read the view", err)
	}
	var out opsrow.MarkdownOut
	if err := json.Unmarshal(raw, &out); err != nil {
		return opsrow.MarkdownOut{}, errs.Wrap(errs.Internal, "edit: read the view", err)
	}
	return out, nil
}

// editorCommand is the editor to run: $VISUAL, else $EDITOR, else vi. vi
// needs a terminal: without one and with neither variable set, the edit is
// refused instead of starting an editor nobody can see.
func editorCommand(terminal func() bool) (string, error) {
	for _, k := range []string{envVisual, envEditor} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, nil
		}
	}
	if !terminal() {
		return "", errs.New(errs.InvalidInput, "edit: no editor: $VISUAL and $EDITOR are not set, and without a terminal "+defaultEditor+" cannot run").
			WithHint("set EDITOR (for example EDITOR=\"code --wait\"), or write a view back without an editor: lore row markdown <id> > view.md, edit it, lore edit --id <id> --file view.md")
	}
	return defaultEditor, nil
}

// runEditor opens path in editor on this process's terminal. editor may
// hold arguments ("code --wait", "emacsclient -t"): on Unix it runs through
// sh as git runs $EDITOR, so quoting in the variable works.
func runEditor(cmd *cobra.Command, editor, path string) error {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		parts := strings.Fields(editor)
		c = exec.CommandContext(cmd.Context(), parts[0], append(parts[1:], path)...)
	} else {
		c = exec.CommandContext(cmd.Context(), "sh", "-c", editor+` "$@"`, editor, path)
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return errs.New(errs.Cancelled, fmt.Sprintf("edit: the editor (%s) exited with status %d; nothing was written", editor, exit.ExitCode())).
				WithHint("run lore edit again, or fix the editor setting ($VISUAL, $EDITOR)")
		}
		return errs.Wrap(errs.InvalidInput, "edit: cannot start the editor "+editor, err).
			WithHint("set EDITOR to an editor on your PATH (for example EDITOR=nano), then run lore edit again")
	}
	return nil
}

// keepNote tells the person where their edited text is, and how to write
// it back once fixed.
func keepNote(cmd *cobra.Command, id, path string) {
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "your edited text is kept in %s\n  fix it there, then write it back: lore edit --id %s --file %s\n", abs, id, abs)
}
