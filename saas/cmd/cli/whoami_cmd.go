// whoami_cmd.go — `lore whoami` (LORE_TEAM_SPEC F10, F18): the resolved
// org, what decided it, the identity, the repo's role, the org's stores and
// the personal folders that load here. `--explain` adds every F10 rule with
// hit or miss and why (the same trace `lore explain org` prints).
package main

import (
	"fmt"
	"os"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// flagExplain is whoami's switch for the full rule trace.
const flagExplain = "explain"

// whoamiJSON is `lore whoami --json` data (kind "whoami").
type whoamiJSON struct {
	Org        string                 `json:"org"`
	MatchedBy  string                 `json:"matched_by"`
	Rule       int                    `json:"rule"`
	RepoRole   orgresolve.Role        `json:"repo_role"`
	Identity   whoamiIdentityJSON     `json:"identity"`
	ProjectID  string                 `json:"project_id,omitempty"`
	Remotes    []string               `json:"remotes"`
	Stores     []lorecfg.Store        `json:"stores"`
	Personal   []string               `json:"personal_folders"`
	LinkStatus string                 `json:"link_status"`
	Trace      []orgresolve.RuleTrace `json:"trace"`
	Warnings   []string               `json:"warnings"`
	Error      string                 `json:"error,omitempty"`
}

type whoamiIdentityJSON struct {
	StableKey string `json:"stable_key"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	ActorID   string `json:"actor_id,omitempty"`
	Source    string `json:"source"`
	Session   string `json:"session,omitempty"`
}

func newWhoamiCommand() *cobra.Command {
	var asJSON, explain, share bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show the resolved org, what decided it, your identity and what loads here",
		Example: `  lore whoami
  lore whoami --explain        # every org rule with hit or miss
  LORE_ORG=northwind-stag lore whoami
  lore whoami --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
			}
			s, err := gatherOrgSituation(cmd.Context(), dir)
			if err != nil {
				return err
			}
			id, idErr := s.identity()
			red := newWhoamiRedactor(s, share)
			if asJSON {
				printJSON("whoami", whoamiData(s, id, idErr, red), 0)
				return firstErr(s.ResolveErr, idErr)
			}
			printWhoami(s, id, idErr, explain, red)
			if s.ResolveErr != nil {
				return orgResolveCLIError(s.ResolveErr)
			}
			return idErr
		},
	}
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "machine-readable output")
	cmd.Flags().BoolVar(&explain, flagExplain, false, "print every org rule with hit or miss and why")
	cmd.Flags().BoolVar(&share, flagShare, false, "on a terminal, also print store URLs and internal folders (hidden without a TTY or with CI set)")
	return cmd
}

func firstErr(resolveErr, idErr error) error {
	if resolveErr != nil {
		return orgResolveCLIError(resolveErr)
	}
	return idErr
}

// personalFolders are the personal-store folders that load here (F17).
func personalFolders(org string) []string {
	if org == "" {
		return []string{lorecfg.PersonalEverywhere}
	}
	return []string{org, lorecfg.PersonalEverywhere}
}

func actorIDFor(key string) string {
	if key == "" {
		return ""
	}
	id, err := ids.Deterministic(ids.PrefixActor, key)
	if err != nil {
		return ""
	}
	return id
}

func whoamiData(s *orgSituation, id identity.Resolved, idErr error, red whoamiRedactor) whoamiJSON {
	r := s.Result
	out := whoamiJSON{
		Org: r.OrgName(), MatchedBy: red.str(r.MatchedBy), Rule: int(r.Step), RepoRole: r.Role,
		ProjectID: s.ProjectID, Remotes: nonNil(s.Remotes), Personal: personalFolders(r.OrgName()),
		LinkStatus: r.LinkStatus.String(), Trace: red.trace(r.Trace), Warnings: nonNil(red.strs(whoamiWarnings(s, id))),
		Stores: []lorecfg.Store{},
	}
	if r.Org != nil {
		out.Stores = red.stores(r.Org.Stores)
	}
	if idErr == nil {
		out.Identity = whoamiIdentityJSON{StableKey: id.StableKey, Name: id.DisplayName, Kind: id.Kind,
			ActorID: actorIDFor(id.StableKey), Source: id.Step.String(), Session: id.Session}
	}
	if err := firstErr(s.ResolveErr, idErr); err != nil {
		out.Error = red.str(err.Error())
	}
	return out
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func whoamiWarnings(s *orgSituation, id identity.Resolved) []string {
	var w []string
	w = append(w, s.Result.Warnings...)
	w = append(w, id.Notes...)
	w = append(w, s.RemoteNotes...)
	if s.CommittedErr != nil {
		w = append(w, fmt.Sprintf(".lore/org.yaml ignored: %v", s.CommittedErr))
	}
	return w
}

// whoamiLabelWidth aligns the two-column output.
const whoamiLabelWidth = 10

// printWhoamiRow prints one aligned "label value" line of `lore whoami`.
func printWhoamiRow(label, value string) {
	fmt.Printf("%-*s %s\n", whoamiLabelWidth, label, value)
}

// orgUseHint tells a user in a repo no rule matched how to pick an org
// (spec F10 example: "hint: lore org use betalabs"). Staging entries are left
// out because staging is chosen only on purpose (Q-a); "" without entries.
func orgUseHint(cfg *lorecfg.Config) string {
	if cfg == nil {
		return ""
	}
	var names []string
	for i := range cfg.Orgs {
		if !orgresolve.IsStaging(&cfg.Orgs[i], nil) {
			names = append(names, cfg.Orgs[i].Name)
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "lore org use " + names[0]
	default:
		return "lore org use <entry> (" + strings.Join(names, ", ") + ")"
	}
}

func printWhoami(s *orgSituation, id identity.Resolved, idErr error, explain bool, red whoamiRedactor) {
	r := s.Result
	switch {
	case s.ResolveErr != nil:
		printWhoamiRow("org", style.Error("refused")+"  "+style.Muted("("+red.str(s.ResolveErr.Error())+")"))
	case r.Org == nil:
		v := "none" + "  " + style.Muted("(matched by: "+red.str(r.MatchedBy)+")")
		if h := orgUseHint(s.Config); h != "" {
			v += "   " + style.Muted("hint: "+h)
		}
		printWhoamiRow("org", v)
	default:
		printWhoamiRow("org", style.Bold(r.Org.Name)+"  "+style.Muted("(matched by: "+red.str(r.MatchedBy)+")"))
	}

	if s.ResolveErr != nil {
		printWhoamiRow("identity", style.Muted("(not resolved: the org is refused above)"))
	} else if idErr != nil {
		printWhoamiRow("identity", style.Error("missing")+"  "+style.Muted(idErr.Error()))
	} else {
		v := fmt.Sprintf("%s <%s>   %s", id.DisplayName, id.StableKey, actorIDFor(id.StableKey))
		if id.Handle != "" {
			v += "  @" + id.Handle
		}
		if id.Session != "" {
			v += "  session " + id.Session
		}
		printWhoamiRow("identity", v+"  "+style.Muted("(from: "+id.Step.String()+")"))
	}

	if s.ResolveErr == nil {
		switch r.Role {
		case orgresolve.RoleClient:
			printWhoamiRow("repo", "client repo  "+style.Muted("(guards on)"))
		case orgresolve.RoleOwn:
			printWhoamiRow("repo", "own repo  "+style.Muted("(no client guards)"))
		default:
			printWhoamiRow("repo", "no org  "+style.Muted("(fallback identity, personal everywhere/ notes only)"))
		}
	}
	project := s.ProjectID
	if project == "" {
		project = style.Muted("(no .lore/data/_meta.json here)")
	}
	printWhoamiRow("project", project)
	if len(s.Remotes) > 0 {
		printWhoamiRow("remotes", strings.Join(s.Remotes, ", "))
	} else {
		printWhoamiRow("remotes", style.Muted("(none)"))
	}

	if r.Org != nil {
		if len(r.Org.Stores) == 0 {
			printWhoamiRow("stores", style.Muted("(none listed for "+r.Org.Name+")"))
		}
		for i, st := range r.Org.Stores {
			label := ""
			if i == 0 {
				label = "stores"
			}
			v := red.str(st.URL) + " on branch " + st.EffectiveBranch()
			if st.OrgFolder != "" {
				v += "  (org folder " + red.folder(st.OrgFolder) + ")"
			}
			printWhoamiRow(label, v)
		}
		if r.Org.OrgFolderStore() == nil {
			printWhoamiRow("global", style.Muted("(no store sets org_folder: no roster or company rules)"))
		}
		for _, l := range r.Links {
			v := red.str(l.StoreURL) + " " + red.folder(l.Folder) + "/"
			if l.Default {
				v += " (default link)"
			}
			printWhoamiRow("internal", v)
		}
	}
	printWhoamiRow("links", r.LinkStatus.String()+"  "+style.Muted(red.str(r.LinkDetail)))

	personal := personalFolders(r.OrgName())
	store := s.Config.Personal.Store
	if store == "" {
		store = lorecfg.PersonalStoreLocal
	}
	printWhoamiRow("personal", store+": "+strings.Join(appendSlash(personal), ", "))
	if !s.HasConfig {
		printWhoamiRow("config", style.Muted(s.ConfigPath+" does not exist (no orgs configured)"))
	}

	for _, w := range whoamiWarnings(s, id) {
		fmt.Println(style.Warn("warning: " + red.str(w)))
	}

	if explain {
		fmt.Println()
		fmt.Println(style.Bold("org resolution (first rule that answers wins):"))
		printOrgTrace(red.trace(r.Trace))
	}
}

func appendSlash(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x + "/"
	}
	return out
}

// printOrgTrace prints the F10 trace; `lore explain org` reuses it.
func printOrgTrace(trace []orgresolve.RuleTrace) {
	for _, t := range trace {
		mark := style.Muted("·")
		switch t.Outcome {
		case orgresolve.OutcomeHit:
			mark = style.Success("✓")
		case orgresolve.OutcomeMiss:
			mark = style.Muted("✗")
		}
		fmt.Printf("  %s %d. %-16s %s\n", mark, int(t.Step), t.Step.String(), t.Reason)
	}
}
