package orgcmd

// list.go — `lore org list` (devco D-570): this machine's org entries,
// each with its stores, the setups that use it and the role each setup
// plays there. Before, `lore org list` was an unknown command and the
// only ways to see the entries were whoami's hint or `lore setup list`
// (which names an entry's org but none of its stores).
//
//	org.list ─▶ Env upgraded to Lister? ─ no ─▶ E_UNSUPPORTED
//	         ─▶ ~/.lore/config.yaml orgs: + setups: ─▶ ListOut
//
// Read-only and Local: it opens no store, so it works from the home folder.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/logic/textwidth"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpList lists this machine's org entries.
const OpList = "org.list"

// ListIn is the input of org.list (no flags).
type ListIn struct {
	// --limit / --page: the shared paging flags of every list (op.PageFlags, T-4).
	op.PageFlags
}

// ListOut is org.list's output: one row per org entry, in config order.
type ListOut struct {
	Orgs []ListOrg `json:"orgs" page:"rows"`
}

// ListOrg is one org entry of ~/.lore/config.yaml.
type ListOrg struct {
	Name string `json:"name"`
	// ID is the org's permanent id; empty for an entry written before ids.
	ID string `json:"id,omitempty"`
	// Key is your identity key in this org (human:<email>).
	Key    string      `json:"key,omitempty"`
	Stores []ListStore `json:"stores"`
	// Setups are the setups whose org is this entry, with their kind
	// (solo, member, admin): the role this machine plays in the org.
	Setups []ListSetup `json:"setups"`
	Own    []string    `json:"own,omitempty"`
	Repos  []string    `json:"repos,omitempty"`
}

// ListStore is one store of an org entry.
type ListStore struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// OrgFolder is set on the store holding the roster and company rules.
	OrgFolder string `json:"org_folder,omitempty"`
	// Audience is "admins" for an admins-only store, empty for everyone.
	Audience string `json:"audience,omitempty"`
}

// ListSetup is one setup that uses an org entry.
type ListSetup struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// Lister is the optional Env upgrade behind org.list (*wire.App).
type Lister interface {
	OrgList(ctx context.Context) (ListOut, error)
}

// Text implements op.Texter: one block per entry, aligned store rows, and
// a count line like every other list.
func (o ListOut) Text() string {
	if len(o.Orgs) == 0 {
		return "no org entries on this machine (lore org import --url <url> joins one; lore org create --org <name> --store <store> starts one)\n"
	}
	var b strings.Builder
	for _, g := range o.Orgs {
		b.WriteString(g.Name)
		if g.ID != "" {
			b.WriteString("  " + g.ID)
		}
		b.WriteString("\n")
		if g.Key != "" {
			b.WriteString("  you      " + g.Key + "\n")
		}
		setups := make([]string, 0, len(g.Setups))
		for _, s := range g.Setups {
			setups = append(setups, s.Name+" ("+s.Role+")")
		}
		if len(setups) == 0 {
			setups = []string{"none (lore setup list shows the setups)"}
		}
		b.WriteString("  setups   " + strings.Join(setups, ", ") + "\n")
		if len(g.Own) > 0 {
			b.WriteString("  own      " + strings.Join(g.Own, ", ") + "\n")
		}
		if len(g.Repos) > 0 {
			b.WriteString("  repos    " + strings.Join(g.Repos, ", ") + "\n")
		}
		if len(g.Stores) == 0 {
			b.WriteString("  stores   none\n")
			continue
		}
		rows := make([][]string, 0, len(g.Stores))
		for _, s := range g.Stores {
			var notes []string
			if s.OrgFolder != "" {
				notes = append(notes, "org folder "+s.OrgFolder)
			}
			if s.Audience != "" {
				notes = append(notes, s.Audience+" only")
			}
			rows = append(rows, []string{s.Name, s.URL, strings.Join(notes, ", ")})
		}
		ws := textwidth.Columns(rows)
		for i, r := range rows {
			lead := "  stores   "
			if i > 0 {
				lead = "           "
			}
			line := lead + textwidth.PadRight(r[0], ws[0]) + "  " + textwidth.PadRight(r[1], ws[1]) + "  " + r[2]
			b.WriteString(strings.TrimRight(line, " ") + "\n")
		}
	}
	b.WriteString(plural.Of(len(o.Orgs), "org entry", "org entries") + " (lore store show --store <org> shows a store's clone and folders)\n")
	return b.String()
}

// listOp is org.list bound to o.
func listOp(o ops) op.Entry {
	return op.Define(op.Spec{Name: OpList, Summary: "List this machine's org entries: their stores, the setups that use them and each setup's role",
		Permission: op.PermRead, Access: op.AccessRead, Local: true,
		Errors:   []errs.Code{errs.ConfigInvalid, errs.Unsupported, errs.Internal},
		Examples: []op.Example{{Title: "every org entry", CLI: "lore org list", Want: ""}}},
		func(ctx context.Context, _ ListIn) (ListOut, error) {
			l, ok := upgrade.As[Lister](o.env)
			if !ok {
				return ListOut{}, errs.New(errs.Unsupported, "lore org list needs this machine's config, which this binary cannot read").
					WithHint("lore setup list names each setup's org")
			}
			return l.OrgList(ctx)
		})
}
