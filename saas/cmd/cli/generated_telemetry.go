// generated_telemetry.go — the session, querylog and renderhistory command groups, moved verbatim out of generated_commands.go so each
// phase 5 noun group owns its own file.
package main

import (
	"fmt"
	"saas/pkg/constants"

	"dbent/gen/ent"
	entQueryLog "dbent/gen/ent/querylog"
	entRenderHistory "dbent/gen/ent/renderhistory"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// newSessionGroup lives in session_list.go (AGENT_IDENTITY_DESIGN 6.2 columns).

func newQueryLogGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "querylog", Short: "Inspect querylogs"}
	cmd.AddCommand(newQueryLogListCommand())
	return cmd
}

func newQueryLogListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List querylogs",
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer joinClose(&err, client, "the project DB")
			_ = rctx
			rows, err := client.QueryLog.Query().
				Order(ent.Desc(entQueryLog.FieldID)).
				Limit(50).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list querylogs").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindQueryLogList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no querylogs)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s\n", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newRenderHistoryGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "renderhistory", Short: "Inspect renderhistorys"}
	cmd.AddCommand(newRenderHistoryListCommand())
	return cmd
}

func newRenderHistoryListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List renderhistorys",
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer joinClose(&err, client, "the project DB")
			_ = rctx
			rows, err := client.RenderHistory.Query().
				Order(ent.Desc(entRenderHistory.FieldID)).
				Limit(50).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list renderhistorys").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindRenderList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no renderhistorys)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s\n", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}
