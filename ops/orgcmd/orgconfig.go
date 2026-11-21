package orgcmd

// orgconfig.go — `lore org config get|set` (devco D-453): read and change
// the org's shared lore-org.yaml after it was created. Before it no
// command edited the file: an admin hand-edited it in the store clone,
// committed with git and ran `lore org refresh`.
//
//	org.config.get ─▶ the file as this machine's clone of the org store holds it
//	org.config.set ─▶ admin of the org? ─ no ─▶ E_REFUSED
//	               ─▶ key own|repos, every pattern compiles ─ no ─▶ E_INVALID_INPUT
//	               ─▶ plan (writes nothing) ─▶ not confirmed: E_NEEDS_CONFIRM
//	               ─▶ lore-org.yaml's key replaced (or removed), committed in
//	                  the org store's clone (pushed only with --push)
//	               ─▶ this machine's org entry copies it (the refresh)
//	               ─▶ key min_lore_version (devco:D-735): every folder's
//	                  _meta.json in each of the org's stores gets the floor
//	                  (a lore below it goes read-only there, with its
//	                  upgrade line; never lowered), one commit per store

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// Operation names of org config.
const (
	OpConfigGet = "org.config.get"
	OpConfigSet = "org.config.set"
)

// Keys of lore-org.yaml org config sets: the repo pattern lists. stores
// have their own commands (org store add/remove/audience); defaults are
// moved into the routing.client_repos policy by lore migrate.
const (
	ConfigKeyOwn   = "own"
	ConfigKeyRepos = "repos"
	// ConfigKeyMinLore is the lore version the org's stores need
	// (devco:D-735). It is not a lore-org.yaml key: it is written as
	// min_lore_version into every folder's _meta.json of the org's
	// stores, the floor every lore since 0.1.18 already obeys, so an
	// older client goes read-only there without a new release.
	ConfigKeyMinLore = "min_lore_version"
)

// ConfigKeys are the keys org config set changes, in file order.
func ConfigKeys() []string { return []string{ConfigKeyOwn, ConfigKeyRepos, ConfigKeyMinLore} }

// MinLoreCommand is the command that declares version as the lore version
// org's stores need.
func MinLoreCommand(org, version string) string {
	return "lore org config set --org " + org + " --key " + ConfigKeyMinLore + " --value " + version + " --push --yes"
}

// ConfigGetIn is the input of org.config.get.
type ConfigGetIn struct {
	Org string `json:"org,omitempty" flag:"org" desc:"the org entry (default: the only org entry on this machine, else the active setup's org)"`
	Key string `json:"key,omitempty" flag:"key" desc:"only this key: own, repos, min_lore_version or stores (default every key)"`
}

// ConfigSetIn is the input of org.config.set.
type ConfigSetIn struct {
	Org    string   `json:"org,omitempty" flag:"org" desc:"the org entry (default: the only org entry on this machine, else the active setup's org)"`
	Key    string   `json:"key" flag:"key" desc:"the key to change: own (the org's own repo patterns), repos (client repo patterns) or min_lore_version (the lore version the org's stores need: an older lore goes read-only there)"`
	Values []string `json:"values,omitempty" flag:"value" desc:"the new list, one pattern per --value (repeat; replaces the whole list); host/owner/repo with * inside or as a segment, ** as a segment; for min_lore_version one version like 0.1.20"`
	Add    []string `json:"add,omitempty" flag:"add" desc:"a pattern to add to the list (repeat; keeps the others)"`
	Remove []string `json:"remove,omitempty" flag:"remove" desc:"a pattern to take out of the list (repeat)"`
	Push   bool     `json:"push,omitempty" flag:"push" desc:"push the lore-org.yaml commit to the org store now (default: commit in the store clone only; lore sync, or lore store push --store <org>, sends it)"`
}

// ConfigGetOut is org.config.get's answer: the shared keys of lore-org.yaml.
type ConfigGetOut struct {
	Org   string   `json:"org"`
	File  string   `json:"file"`
	Own   []string `json:"own"`
	Repos []string `json:"repos"`
	// Stores are the store urls the file lists, admins-only ones marked
	// " (admins)".
	Stores []string `json:"stores"`
	// MinLoreVersion is the lore version every folder of the org folder's
	// store declares it needs ("" none; devco:D-735).
	MinLoreVersion string `json:"min_lore_version"`
	// key is the --key asked for ("" every key).
	key string
	// overlaps are orgresolve.OwnRepoOverlaps of Own and Repos, printed
	// under them (devco D-585); text only, the JSON keeps its keys.
	overlaps []string
}

// WithOverlaps is o with the own/repos overlap lines it prints.
func (o ConfigGetOut) WithOverlaps(lines []string) ConfigGetOut {
	o.overlaps = lines
	return o
}

// Text prints one key per line, lists comma-separated.
func (o ConfigGetOut) Text() string {
	var b strings.Builder
	line := func(k string, v []string) {
		if o.key == "" || o.key == k {
			b.WriteString(k + ": " + orNone(strings.Join(v, ", ")) + "\n")
		}
	}
	if o.key == "" {
		b.WriteString("org " + o.Org + " (" + o.File + ")\n")
	}
	line(ConfigKeyOwn, o.Own)
	line(ConfigKeyRepos, o.Repos)
	if o.key == "" || o.key == ConfigKeyOwn || o.key == ConfigKeyRepos {
		for _, l := range o.overlaps {
			b.WriteString("note: " + l + "\n")
		}
	}
	line("stores", o.Stores)
	if o.key == "" || o.key == ConfigKeyMinLore {
		b.WriteString(ConfigKeyMinLore + ": " + orNone(o.MinLoreVersion) + "\n")
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// ConfigEditor is the optional Host upgrade behind org config.
type ConfigEditor interface {
	OrgConfigGet(ctx context.Context, in ConfigGetIn) (ConfigGetOut, error)
	OrgConfigSet(ctx context.Context, in ConfigSetIn, confirmed bool) (Report, error)
}

// ConfigSetCommand is the command a hint names to change key.
func ConfigSetCommand(org, key string) string {
	return "lore org config set --org " + org + " --key " + key
}

// CheckConfigSet refuses an org config set that cannot be applied: an
// unknown key, no change named, or a pattern repourl.Compile refuses.
func CheckConfigSet(in ConfigSetIn) error {
	if in.Key == ConfigKeyMinLore {
		return checkMinLore(in)
	}
	if !slices.Contains(ConfigKeys(), in.Key) {
		return errs.New(errs.InvalidInput, "org config set: --key "+strconvQuote(in.Key)+" is not one of "+strings.Join(ConfigKeys(), ", ")).
			WithHint("lore org config set --org <org> --key repos --add 'github.com/agency/client-*' --yes")
	}
	if len(in.Values) == 0 && len(in.Add) == 0 && len(in.Remove) == 0 {
		return errs.New(errs.InvalidInput, "org config set: nothing to change").
			WithHint("give --value (the whole list, repeat), --add or --remove, e.g. lore org config set --org <org> --key " + in.Key + " --add 'github.com/agency/client-*' --yes")
	}
	vals := append(slices.Clone(in.Values), in.Add...)
	return checkPatterns("org config set --key "+in.Key, vals, vals, func(bad, fixed string) string {
		return configSetRerun(in, bad, fixed)
	})
}

// configSetRerun is the org config set the person typed with fixed in
// place of bad (--org only when they gave it: the default org applies
// again), confirmed with --yes.
func configSetRerun(in ConfigSetIn, bad, fixed string) string {
	cmd := "lore org config set"
	if strings.TrimSpace(in.Org) != "" {
		cmd += " --org " + shellQuote(in.Org)
	}
	cmd += " --key " + in.Key + flagArgs("value", replaced(in.Values, bad, fixed)) + flagArgs("add", replaced(in.Add, bad, fixed)) + flagArgs("remove", in.Remove)
	if in.Push {
		cmd += " --push"
	}
	return cmd + " --yes"
}

// checkMinLore refuses a min_lore_version change that is not one release
// version given with --value: --add/--remove make no sense for one
// version, and a floor lore cannot parse is ignored by every lore.
func checkMinLore(in ConfigSetIn) error {
	hint := "lore org config set --org <org> --key " + ConfigKeyMinLore + " --value 0.1.20 --push --yes"
	if len(in.Add) > 0 || len(in.Remove) > 0 || len(in.Values) != 1 {
		return errs.New(errs.InvalidInput, "org config set --key "+ConfigKeyMinLore+" takes one version with --value (no --add or --remove)").WithHint(hint)
	}
	if !formatgate.ValidVersion(in.Values[0]) {
		return errs.New(errs.InvalidInput, "org config set --key "+ConfigKeyMinLore+": "+strconvQuote(in.Values[0])+" is not a release version (major.minor.patch, like 0.1.20)").WithHint(hint)
	}
	return nil
}

// NewList is cur changed by in: --value replaces it, then --add appends
// what is missing and --remove takes entries out; order is kept.
func NewList(cur []string, in ConfigSetIn) []string {
	out := slices.Clone(cur)
	if len(in.Values) > 0 {
		out = nil
		for _, v := range in.Values {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	for _, v := range in.Add {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return slices.DeleteFunc(out, func(v string) bool { return slices.Contains(in.Remove, v) })
}

func strconvQuote(s string) string { return `"` + s + `"` }

// configOps are org.config.get and org.config.set bound to o.
func configOps(o ops, spec func(name, summary string, errCodes []errs.Code, mayAsk bool, ex ...op.Example) op.Spec, codes []errs.Code) []op.Entry {
	editor := func(ctx context.Context, name string, h Host) (ConfigEditor, error) {
		e, ok := upgrade.As[ConfigEditor](h)
		if !ok {
			return nil, errs.New(errs.Unsupported, "lore "+strings.ReplaceAll(name, ".", " ")+" needs the org store machinery, which this binary does not have yet").
				WithHint("run it with the release binary")
		}
		return e, nil
	}
	get := spec(OpConfigGet, "Show the org's shared lore-org.yaml: own and repos patterns, stores; and min_lore_version, the lore version its stores need", codes, false,
		op.Example{Title: "the org's repo patterns", CLI: "lore org config get --org agency", Want: "repos:"})
	get.Permission, get.Access = op.PermRead, op.AccessRead
	return []op.Entry{
		op.Define(get, func(ctx context.Context, in ConfigGetIn) (ConfigGetOut, error) {
			if in.Key != "" && !slices.Contains(ConfigKeys(), in.Key) && in.Key != "stores" {
				return ConfigGetOut{}, errs.New(errs.InvalidInput, "org config get: --key "+strconvQuote(in.Key)+" is not one of own, repos, stores, min_lore_version").
					WithHint("lore org config get --org <org> --key repos")
			}
			h, err := o.host(ctx, OpConfigGet)
			if err != nil {
				return ConfigGetOut{}, err
			}
			e, err := editor(ctx, OpConfigGet, h)
			if err != nil {
				return ConfigGetOut{}, err
			}
			var out ConfigGetOut
			_, err = withDefaultOrg(ctx, h, OpConfigGet, &in.Org, func() (Report, error) {
				var gerr error
				out, gerr = e.OrgConfigGet(ctx, in)
				return Report{}, gerr
			})
			out.key = in.Key
			return out, err
		}),
		op.Define(spec(OpConfigSet, "Change the org's shared lore-org.yaml (admin only): own or repos patterns, committed in the org store and copied to this machine's org entry; or min_lore_version, the lore version the org's stores need (an older lore goes read-only there)", append([]errs.Code{errs.NeedsConfirm, errs.Refused}, codes...), true,
			op.Example{Title: "add a client repo pattern", CLI: "lore org config set --org agency --key repos --add 'github.com/agency/client-*' --push --yes", Want: "repos"},
			op.Example{Title: "require lore 0.1.20 on every store of the org", CLI: "lore org config set --org agency --key min_lore_version --value 0.1.20 --push --yes", Want: "min_lore_version"}),
			func(ctx context.Context, in ConfigSetIn) (Report, error) {
				if err := CheckConfigSet(in); err != nil {
					return Report{}, err
				}
				return o.ask(ctx, OpConfigSet, func(h Host, yes bool) (Report, error) {
					e, err := editor(ctx, OpConfigSet, h)
					if err != nil {
						return Report{}, err
					}
					return withDefaultOrg(ctx, h, OpConfigSet, &in.Org, func() (Report, error) { return e.OrgConfigSet(ctx, in, yes) })
				})
			}),
	}
}
