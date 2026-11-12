package host

// mergedriver.go — `lore merge-driver`, the git merge driver lore registers
// as `merge.lore.driver = lore merge-driver %O %A %B %P` (and
// `merge.lore-keep-ours.driver = lore merge-driver --keep-ours …` for the
// rendered files; FORMAT_EVOLUTION, RESOURCES). Ported from saas/cmd/cli
// sync_cmd.go + lsync MergeFiles; in 0.1.18 every table is core-owned, so
// a row file merges through logic/merge.Driver (the core format stays):
//
//	--keep-ours           ─▶ keep %A as is (LORE.md is re-rendered later)
//	_meta.json/_purged.json ─▶ rowfs.MergeFolderFile (set union, identity)
//	a row file            ─▶ merge.Driver (field merge, HLC newer wins,
//	                          markers in text fields = conflict, exit 1)
//	not a row on a side   ─▶ git merge-file (line merge, exit 1 on conflict)
//
// Why a host command and not an operation (P3-MODEB-4): git calls it with
// three temp file paths and reads only %A and the exit status (non-zero =
// conflict left for a person, with no ERROR line); a REST route over the
// server's file paths would make no sense, and an op result has no
// "exit 1 without an error" shape.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// Merge driver words and limits.
const (
	mergeDriverUse     = "merge-driver"
	mergeDriverMinArgs = 3 // %O %A %B
	mergeDriverMaxArgs = 4 // plus %P
	flagKeepOurs       = "keep-ours"
	mergeDriverPrefix  = "lore merge-driver: "
	mergeResultMode    = 0o600
)

// ExitStatus is an exit code a host command chooses without an error line
// (git's merge-driver contract: 1 = conflict left in the file).
type ExitStatus int

// Error implements error.
func (e ExitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// MergeDriverCommand is the hidden `lore merge-driver` command; stderr gets
// the lines git shows during the merge.
func MergeDriverCommand(stderr io.Writer) *cobra.Command {
	var keepOurs bool
	cmd := &cobra.Command{
		Use:    mergeDriverUse + " <base> <ours> <theirs> [path]",
		Short:  "git merge driver for .lore/data row files",
		Hidden: true,
		Args:   cobra.RangeArgs(mergeDriverMinArgs, mergeDriverMaxArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keepOurs {
				return nil
			}
			repoPath := ""
			if len(args) == mergeDriverMaxArgs {
				repoPath = args[mergeDriverMaxArgs-1]
			}
			return runMergeDriver(cmd.Context(), stderr, args[0], args[1], args[2], repoPath)
		},
	}
	cmd.Flags().BoolVar(&keepOurs, flagKeepOurs, false, "keep our side unchanged (generated files)")
	return cmd
}

// runMergeDriver merges base/ours/theirs into ours (see the file comment).
func runMergeDriver(ctx context.Context, stderr io.Writer, basePath, oursPath, theirsPath, repoPath string) error {
	read := func(p string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "merge driver: read "+p, err)
		}
		return b, nil
	}
	base, err := read(basePath)
	if err != nil {
		return err
	}
	ours, err := read(oursPath)
	if err != nil {
		return err
	}
	theirs, err := read(theirsPath)
	if err != nil {
		return err
	}
	if repoPath != "" && rowfs.IsFolderFile(repoPath) {
		out, ferr := rowfs.MergeFolderFile(repoPath, base, ours, theirs)
		if ferr != nil {
			return ferr
		}
		return writeMerged(oursPath, ours, out)
	}
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return errs.Wrap(errs.Internal, "merge driver: core registry", err)
	}
	var res merge.Result
	out, conflict, err := merge.Driver{Registry: reg, Report: func(r merge.Result) { res = r }, OursFirst: true}.Merge(base, ours, theirs)
	if err != nil {
		// Not a core row on some side (a hand edit, a file of another
		// tool): git's line merge, which writes no format of its own.
		return textMerge(ctx, stderr, basePath, oursPath, theirsPath, repoPath)
	}
	if err := writeMerged(oursPath, ours, out); err != nil {
		return err
	}
	if len(res.NewerWins) > 0 {
		// git shows a driver's stderr: say which changes lost, so a
		// teammate's edit never vanishes silently.
		_, _ = fmt.Fprintf(stderr, "%s%s: both sides changed %s; kept the newer edit\n", mergeDriverPrefix, repoPath, strings.Join(res.NewerWins, ", ")) // stderr gone: the merge result stands
	}
	if res.Trash != "" {
		// DELETION 3.7: a delete that lost to an edit (or two deletes) is
		// a decision a person should hear about; `lore trash list
		// --overruled` lists the rows kept alive.
		_, _ = fmt.Fprintf(stderr, "%s%s: %s\n", mergeDriverPrefix, repoPath, res.Trash) // stderr gone: the merge result stands
	}
	if conflict {
		_, _ = fmt.Fprintf(stderr, "%s%s needs a manual merge (markers are inside the JSON values)\n", mergeDriverPrefix, repoPath) // stderr gone: the exit status still tells git
		return ExitStatus(1)
	}
	return nil
}

// writeMerged writes out over ours unless it is unchanged.
func writeMerged(oursPath string, ours, out []byte) error {
	if bytes.Equal(out, ours) {
		return nil
	}
	if err := os.WriteFile(filepath.Clean(oursPath), out, mergeResultMode); err != nil {
		return errs.Wrap(errs.Internal, "merge driver: write the result", err)
	}
	return nil
}

// textMerge is `git merge-file ours base theirs`: exit 1 when it left
// conflict markers.
func textMerge(ctx context.Context, stderr io.Writer, basePath, oursPath, theirsPath, repoPath string) error {
	bin, err := exec.LookPath(gitProgram)
	if err != nil {
		return errs.Wrap(errs.Internal, "merge driver: git not found", err)
	}
	// bin is LookPath's absolute path and each argument its own argv entry
	// (no shell), as gitOutput.
	cmd := &exec.Cmd{Path: bin, Args: []string{bin, "merge-file", "-L", "ours", "-L", "base", "-L", "theirs", oursPath, basePath, theirsPath}, Env: gitenv.Clean(os.Environ())}
	if err = cmd.Start(); err != nil {
		return errs.Wrap(errs.Internal, "merge driver: git merge-file", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() }) // a kill of an exited process has nothing to report
	defer stop()
	err = cmd.Wait()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee) && ee.ExitCode() > 0:
		_, _ = fmt.Fprintf(stderr, "%s%s needs a manual merge (line conflict markers)\n", mergeDriverPrefix, repoPath) // stderr gone: the exit status still tells git
		return ExitStatus(1)
	}
	return errs.Wrap(errs.Internal, "merge driver: git merge-file", err)
}
