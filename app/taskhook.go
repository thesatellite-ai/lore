package app

// taskhook.go — `lore task hook commit-msg <message-file>`, the hidden entry
// point lore's commit-msg hook block calls (wire/host gitwire.go): adds the
// Lore-Task trailer for this session's in-repo claims (wire
// App.CommitMsgTrailers, F9 / E7). The trailer never fails a commit: a
// problem is one stderr line and the message stays as written.
//
// --check-leaks then runs `lore check leaks --staged --commit-msg <file>`
// in the same process, and its result is the command's (D-16: the block
// started three lore processes per commit, a trailer, a --help probe and
// the check, about 50 ms each before any work).

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// Words of the hidden hook command.
const (
	taskHookGroup     = "hook"
	taskHookCommitMsg = "commit-msg"
	// taskHookCheckLeaks is the flag that also runs the staged leak check.
	taskHookCheckLeaks = "check-leaks"
)

// Path and flags of the leak check --check-leaks runs (ops/check).
var (
	leakCheckPath        = []string{"check", "leaks"}
	leakCheckFlagStaged  = "staged"
	leakCheckFlagMessage = "commit-msg"
)

// mountTaskHook adds `lore task hook commit-msg` under the task group (no
// task group: nothing, the hook block then finds no command and skips).
func (h *host) mountTaskHook(root *cobra.Command) {
	task := find(root, opstask.Noun)
	if task == nil {
		return
	}
	g := &cobra.Command{Use: taskHookGroup, Short: "Git hook entry points for coordination", Hidden: true}
	var checkLeaks bool
	c := &cobra.Command{
		Use:   taskHookCommitMsg + " <message-file>",
		Short: "Add Lore-Task trailers for the claims this session holds",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := h.app.CommitMsgTrailers(cmd.Context(), args[0]); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "lore: commit-msg: %v (commit not changed)\n", err) // stderr gone: nothing left to report to
			}
			if !checkLeaks {
				return nil
			}
			return runStagedLeakCheck(cmd, root, args[0])
		},
	}
	c.Flags().BoolVar(&checkLeaks, taskHookCheckLeaks, false, "then check the staged files and this message for leaks (lore check leaks --staged --commit-msg)")
	g.AddCommand(c)
	task.AddCommand(g)
}

// runStagedLeakCheck runs the `lore check leaks` command of root on the
// staged files and the message file msg, as the commit-msg block's own
// call would, and returns its result. A build without that command is
// E_INVALID_INPUT, so the hook block falls back to its separate calls.
func runStagedLeakCheck(cmd *cobra.Command, root *cobra.Command, msg string) error {
	leaks := find(root, leakCheckPath...)
	if leaks == nil || leaks.RunE == nil {
		return errs.New(errs.InvalidInput, "this lore has no `lore check leaks`")
	}
	for name, v := range map[string]string{leakCheckFlagStaged: "true", leakCheckFlagMessage: msg} {
		if err := leaks.Flags().Set(name, v); err != nil {
			return errs.Wrap(errs.InvalidInput, "lore check leaks --"+name, err)
		}
	}
	leaks.SetContext(cmd.Context())
	return leaks.RunE(leaks, nil)
}
