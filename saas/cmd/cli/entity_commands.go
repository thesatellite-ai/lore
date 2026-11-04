// entity_commands.go — final pass of CLI groups for schemas that had no
// surface verbs. Each entity gets the minimum useful set of subcommands
// (list/show/add or list/get/set, depending on shape). Heavily inferred
// from the ent generated setters — kept compact, one block per entity
//
// Coverage added in this file:
//
//	actor          — list / show
//	snapshot       — add / list / show
//	plugin         — list / trust / untrust       (TrustedPlugin)
//	pii-pattern    — list / enable / disable / add
//	task-view      — add / list / show
//	external-source— list / enable / disable / add
//	techdoc        — add / list / show
//	mount-alias    — list                          (read-only — written by repo-rename)
//	config         — get / set / list             (dbconfig table)
package main

import (
	"encoding/hex"
	"fmt"
	"saas/pkg/constants"
	"time"

	"dbent/gen/ent"
	entMountAlias "dbent/gen/ent/mountalias"
	entPIIPattern "dbent/gen/ent/piipattern"
	entTaskView "dbent/gen/ent/taskview"
	entTrustedPlugin "dbent/gen/ent/trustedplugin"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// ── actor ────────────────────────────────────────────────────────────────

// newActorCommand is the `lore actor` group: list and show are the
// core's (ops/infra), register and deactivate the roster's
// (attachRosterCommands). A pure group, so cli.Mount reuses it.
func newActorCommand() *cobra.Command {
	return &cobra.Command{Use: "actor", Short: "Inspect actors (humans, agents, hooks, plugins)"}
}

// ── snapshot ─────────────────────────────────────────────────────────────

func newPluginCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "plugin", Short: "Manage trusted-plugin allowlist"}
	cmd.AddCommand(newPluginListCommand())
	cmd.AddCommand(newPluginTrustCommand())
	cmd.AddCommand(newPluginUntrustCommand())
	return cmd
}

func newPluginListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List trusted plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			rows, err := client.TrustedPlugin.Query().
				Order(ent.Asc(entTrustedPlugin.FieldTrustedAt)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list plugins").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindPluginList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no trusted plugins)"))
				return nil
			}
			for _, p := range rows {
				fmt.Printf("%s %s  sha256:%s\n", style.Code(p.ID), p.Name, shortFingerprint(p.Sha256))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// sha256HexLen is the length of a hex-encoded SHA-256 digest.
const sha256HexLen = 64

// pluginFingerprintShown is how many hex digits of a plugin's sha256 the
// list and trust confirmations print.
const pluginFingerprintShown = 12

// validPluginSHA256 checks a --sha256 value before it is stored: a trusted
// plugin row is compared against the plugin file's digest, so anything but
// a full 64-digit hex SHA-256 can never match and used to crash the list.
func validPluginSHA256(s string) error {
	if _, err := hex.DecodeString(s); err != nil || len(s) != sha256HexLen {
		return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("--sha256 must be a %d-digit hex SHA-256 digest, got %q", sha256HexLen, s)).
			WithHint("compute it with: sha256sum <plugin file>")
	}
	return nil
}

// shortFingerprint is the printed prefix of a stored digest. It never
// slices past the end: rows saved before validPluginSHA256 existed may hold
// a short value, and listing them must not crash.
func shortFingerprint(s string) string {
	if len(s) > pluginFingerprintShown {
		return s[:pluginFingerprintShown]
	}
	return s
}

func newPluginTrustCommand() *cobra.Command {
	var f commonFlags
	var name, sha string
	cmd := &cobra.Command{
		Use: "trust", Short: "Trust a plugin by name + sha256",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			if err := validPluginSHA256(sha); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			create := client.TrustedPlugin.Create().
				SetName(name).
				SetSha256(sha).
				SetTrustedAt(cliClock().Now())
			actorID, err := resolveCurrentActorID(cmd.Context(), client)
			if err != nil {
				return err
			}
			create.SetTrustedByActorID(actorID)
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "trust plugin").WithCause(err)
			}
			fmt.Printf("%s plugin trusted: %s (sha256:%s)\n", style.Success("✓"), r.Name, shortFingerprint(r.Sha256))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "plugin name (required)")
	cmd.Flags().StringVar(&sha, "sha256", "", "plugin file sha256 (required)")
	_ = cmd.MarkFlagRequired(constants.FlagName)
	_ = cmd.MarkFlagRequired("sha256")
	return cmd
}

func newPluginUntrustCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use: "untrust <id>", Short: "Remove a trusted-plugin entry",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.TrustedPlugin.DeleteOneID(args[0]).Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "untrust plugin").WithCause(err)
			}
			fmt.Printf("%s plugin %s untrusted\n", style.Success("✓"), args[0])
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// ── pii-pattern ──────────────────────────────────────────────────────────

func newPIIPatternCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "pii-pattern", Short: "Manage custom PII/secret detection patterns"}
	cmd.AddCommand(newPIIPatternListCommand())
	cmd.AddCommand(newPIIPatternAddCommand())
	cmd.AddCommand(newPIIPatternEnableCommand(true))
	cmd.AddCommand(newPIIPatternEnableCommand(false))
	return cmd
}

func newPIIPatternListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List PII patterns",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			rows, err := client.PiiPattern.Query().
				Order(ent.Asc(entPIIPattern.FieldName)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list pii-patterns").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindPiiPatternList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no patterns)"))
				return nil
			}
			for _, p := range rows {
				state := "off"
				if p.Enabled {
					state = "on"
				}
				fmt.Printf("[%s] %s  (%s)\n", state, p.Name, p.SourceKind)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// errPIIPatternMoved is the answer of `lore pii-pattern add|enable|disable`:
// custom secret patterns are settings rows now (SETTINGS ARE DATA,
// FORMAT_EVOLUTION step 17), written by `lore secrets rule add|on|off`.
// The v0.1.17 pii_patterns table was machine-local and no scanner ever
// read it, so nothing that worked before stops working; `pii-pattern list`
// still shows what is left in it.
func errPIIPatternMoved(verb string) error {
	repl := map[string]string{"add": "lore secrets rule add", "enable": "lore secrets rule on", "disable": "lore secrets rule off"}[verb]
	return errcodes.New(errcodes.InvalidInput, "lore pii-pattern "+verb+" was replaced by "+repl+" (secret rules are settings rows)").
		WithHint("use `" + repl + "`; `lore secrets rules` lists every rule")
}

func newPIIPatternAddCommand() *cobra.Command {
	var name, regex, source string
	cmd := &cobra.Command{
		Use: "add", Short: "Replaced by `lore secrets rule add`",
		RunE: func(cmd *cobra.Command, args []string) error { return errPIIPatternMoved("add") },
	}
	cmd.Flags().StringVar(&name, constants.FlagName, "", "pattern name")
	cmd.Flags().StringVar(&regex, "regex", "", "regex pattern")
	cmd.Flags().StringVar(&source, constants.FlagSource, "user", "source_kind")
	return cmd
}

func newPIIPatternEnableCommand(enable bool) *cobra.Command {
	verb := "enable"
	repl := "lore secrets rule on"
	if !enable {
		verb, repl = "disable", "lore secrets rule off"
	}
	return &cobra.Command{
		Use: verb + " <id>", Short: "Replaced by `" + repl + "`",
		RunE: func(cmd *cobra.Command, args []string) error { return errPIIPatternMoved(verb) },
	}
}

// ── task-view ────────────────────────────────────────────────────────────

func newTaskViewCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "task-view", Short: "Manage saved task-list filter views"}
	cmd.AddCommand(newTaskViewAddCommand())
	cmd.AddCommand(newTaskViewListCommand())
	cmd.AddCommand(newTaskViewDeleteCommand())
	return cmd
}

func newTaskViewAddCommand() *cobra.Command {
	var f commonFlags
	var name, filter string
	cmd := &cobra.Command{
		Use: "add", Short: "Save a task filter as a named view",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			create := client.TaskView.Create().SetProjectID(projectID).SetName(name)
			if filter != "" {
				create.SetFilterJSON(filter)
			}
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create task-view").WithCause(err)
			}
			fmt.Printf("%s task-view %s saved\n", style.Success("✓"), r.Name)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "view name (required)")
	cmd.Flags().StringVar(&filter, constants.FlagFilter, "", "JSON filter spec")
	_ = cmd.MarkFlagRequired(constants.FlagName)
	return cmd
}

func newTaskViewListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List saved task views",
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			rows, err := client.TaskView.Query().Where(entTaskView.ProjectID(projectID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list task-views").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindTaskViewList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no task-views)"))
				return nil
			}
			for _, v := range rows {
				fmt.Printf("%s %s\n", style.Code(v.ID), v.Name)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newTaskViewDeleteCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use: "delete <id>", Short: "Delete a saved task view",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.TaskView.DeleteOneID(args[0]).Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "delete task-view").WithCause(err)
			}
			fmt.Printf("%s task-view %s deleted\n", style.Success("✓"), args[0])
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// ── external-source ──────────────────────────────────────────────────────

func newMountAliasCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "mount-alias", Short: "Inspect repo rename-redirect aliases"}
	cmd.AddCommand(newMountAliasListCommand())
	return cmd
}

func newMountAliasListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut, expiringOnly bool
	cmd := &cobra.Command{
		Use: "list", Short: "List mount aliases (rename redirects)",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			q := client.MountAlias.Query()
			if expiringOnly {
				q = q.Where(entMountAlias.ExpiresAtLT(time.Now().Add(7 * 24 * time.Hour)))
			}
			rows, err := q.Order(ent.Asc(entMountAlias.FieldExpiresAt)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list mount-aliases").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindMountAliasList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no mount aliases)"))
				return nil
			}
			for _, a := range rows {
				fmt.Printf("%s (repo:%s) → expires %s\n",
					a.OldName, a.RepoID, a.ExpiresAt.Format("2006-01-02"))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	cmd.Flags().BoolVar(&expiringOnly, constants.FlagExpiring, false, "show only aliases expiring within 7 days")
	return cmd
}

// ── config (~/.lore/config.yaml, CFG12) ─────────────────────────────────

// newConfigCommand is `lore config`: get, set and list edit only
// ~/.lore/config.yaml (CFG12, config_yaml_cmd.go); check validates it.
// The DB-level dbconfig table is the cache's own bookkeeping and has no
// user command any more.
func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Get/set keys of ~/.lore/config.yaml"}
	cmd.AddCommand(newConfigGetCommand())
	cmd.AddCommand(newConfigSetCommand())
	cmd.AddCommand(newConfigListCommand())
	cmd.AddCommand(newConfigCheckCommand())
	return cmd
}

// ── shared add-command helpers ───────────────────────────────────────────
