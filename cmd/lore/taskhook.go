package main

// taskhook.go — `lore task hook commit-msg <message-file>`, the hidden entry
// point lore's commit-msg hook block calls (wire/host gitwire.go): adds the
// Lore-Task trailer for this session's in-repo claims (wire
// App.CommitMsgTrailers, F9 / E7). It never fails a commit: a problem is
// one stderr line and the message stays as written.

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// Words of the hidden hook command.
const (
	taskHookGroup     = "hook"
	taskHookCommitMsg = "commit-msg"
)

// mountTaskHook adds `lore task hook commit-msg` under the task group (no
// task group: nothing, the hook block then finds no command and skips).
func (h *host) mountTaskHook(root *cobra.Command) {
	task := find(root, opstask.Noun)
	if task == nil {
		return
	}
	g := &cobra.Command{Use: taskHookGroup, Short: "Git hook entry points for coordination", Hidden: true}
	g.AddCommand(&cobra.Command{
		Use:   taskHookCommitMsg + " <message-file>",
		Short: "Add Lore-Task trailers for the claims this session holds",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := h.app.CommitMsgTrailers(cmd.Context(), args[0]); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "lore: commit-msg: %v (commit not changed)\n", err) // stderr gone: nothing left to report to
			}
			return nil
		},
	})
	task.AddCommand(g)
}
