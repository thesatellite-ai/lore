// config_check_cmd.go — `lore config check` (LORE_TEAM_SPEC F19, §1):
// validate ~/.lore/config.yaml and print every problem at once, with the
// YAML line for parse errors and unknown keys.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// configCheckOut is `lore config check --json` (CLI_REFERENCE 3.22): the
// file, whether it exists, its org entries and the warnings. An invalid
// file is the E_CONFIG_INVALID error object, its problems in the message.
type configCheckOut struct {
	Path     string   `json:"path"`
	Exists   bool     `json:"exists"`
	Orgs     []string `json:"orgs"`
	Warnings []string `json:"warnings"`
}

func newConfigCheckCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate ~/.lore/config.yaml (orgs, stores, live settings) and list every problem",
		Example: `  lore config check
  HOME=/tmp/test-home lore config check`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := identity.Dir()
			if err != nil {
				return errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
			}
			cfg, loc, err := lorecfg.LoadTeam(cmd.Context(), dir, lorecfg.Options{})
			if !loc.Exists && err == nil {
				if jsonOut {
					printJSON(constants.KindConfigCheck, configCheckOut{Path: loc.Path, Orgs: []string{}, Warnings: []string{}}, 1)
					return nil
				}
				fmt.Printf("%s %s does not exist: no orgs configured (every repo resolves to no org)\n", style.Success("✓"), loc.Path)
				return nil
			}
			var warnings []string
			if raw, rerr := os.ReadFile(loc.Path); rerr == nil {
				warnings = append(warnings, unknownKeyWarnings(raw)...)
			}
			if err != nil && jsonOut {
				return errcodes.New(errcodes.ConfigInvalid, loc.Path+" is not valid: "+strings.Join(splitProblems(err), "; ")).
					WithHint("fix the problems listed; nothing else in lore reads an invalid config")
			}
			if err != nil {
				fmt.Println(style.Error("✗ " + loc.Path))
				for _, p := range splitProblems(err) {
					fmt.Println("  - " + p)
				}
				printWarnings(warnings)
				return errcodes.New(errcodes.ConfigInvalid, loc.Path+" is not valid").
					WithHint("fix the problems listed above; nothing else in lore reads an invalid config")
			}
			warnings = append(warnings, liveKindWarnings(cfg)...)
			if jsonOut {
				if warnings == nil {
					warnings = []string{}
				}
				printJSON(constants.KindConfigCheck, configCheckOut{Path: loc.Path, Exists: true, Orgs: cfg.OrgNames(), Warnings: warnings}, 1)
				return nil
			}
			fmt.Printf("%s %s is valid: %d org entr%s (%s)\n", style.Success("✓"), loc.Path,
				len(cfg.Orgs), plural(len(cfg.Orgs), "y", "ies"), strings.Join(cfg.OrgNames(), ", "))
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func printWarnings(ws []string) {
	for _, w := range ws {
		fmt.Println(style.Warn("  warning: " + w))
	}
}

// splitProblems flattens the joined validation error into one line each.
func splitProblems(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "lorecfg: load") && strings.HasSuffix(line, "problem(s):") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		out = []string{err.Error()}
	}
	return out
}

// unknownKeyWarnings reports keys lore does not read (typos such as
// `org_foldr`), with their line. Lore tolerates them on load, as the TOML
// reader always did, so this is a warning, not an error.
func unknownKeyWarnings(raw []byte) []string {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var c lorecfg.Config
	err := dec.Decode(&c)
	if err == nil {
		return nil
	}
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return nil // syntax errors are reported by the load itself
	}
	var out []string
	for _, e := range te.Errors {
		if strings.Contains(e, "not found in type") {
			out = append(out, "unknown key, ignored: "+e)
		}
	}
	return out
}

// liveKindWarnings flags live.defaults keys that are not synced tables
// (a kind lore will never make live).
func liveKindWarnings(cfg *lorecfg.Config) []string {
	reg, _ := lsync.NewRegistry()
	known := map[string]bool{}
	for _, n := range reg.Names() {
		known[n] = true
	}
	check := func(where string, l *lorecfg.Live) []string {
		var out []string
		for k := range l.Defaults {
			if !known[k] {
				out = append(out, fmt.Sprintf("%s.defaults.%s: not a synced table (use plural table names such as tasks, memories)", where, k))
			}
		}
		sort.Strings(out)
		return out
	}
	out := check("live", &cfg.Live)
	for _, o := range cfg.Orgs {
		if o.Live != nil {
			out = append(out, check("orgs["+o.Name+"].live", o.Live)...)
		}
	}
	return out
}
