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

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/repourl"
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
)

// ConfigKeys are the keys org config set changes, in file order.
func ConfigKeys() []string { return []string{ConfigKeyOwn, ConfigKeyRepos} }

// ConfigGetIn is the input of org.config.get.
type ConfigGetIn struct {
	Org string `json:"org,omitempty" flag:"org" desc:"the org entry (default: the only org entry on this machine, else the active setup's org)"`
	Key string `json:"key,omitempty" flag:"key" desc:"only this key: own or repos (default every key)"`
}

// ConfigSetIn is the input of org.config.set.
type ConfigSetIn struct {
	Org    string   `json:"org,omitempty" flag:"org" desc:"the org entry (default: the only org entry on this machine, else the active setup's org)"`
	Key    string   `json:"key" flag:"key" desc:"the key to change: own (the org's own repo patterns) or repos (client repo patterns)"`
	Values []string `json:"values,omitempty" flag:"value" desc:"the new list, one pattern per --value (repeat; replaces the whole list); host/owner/repo with * inside or as a segment, ** as a segment"`
	Add    []string `json:"add,omitempty" flag:"add" desc:"a pattern to add to the list (repeat; keeps the others)"`
	Remove []string `json:"remove,omitempty" flag:"remove" desc:"a pattern to take out of the list (repeat)"`
	Push   bool     `json:"push,omitempty" flag:"push" desc:"push the lore-org.yaml commit to the org store now (default: commit in the store clone only; the next write to that store sends it)"`
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
	// key is the --key asked for ("" every key).
	key string
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
	line("stores", o.Stores)
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
	if !slices.Contains(ConfigKeys(), in.Key) {
		return errs.New(errs.InvalidInput, "org config set: --key "+strconvQuote(in.Key)+" is not one of "+strings.Join(ConfigKeys(), ", ")).
			WithHint("lore org config set --org <org> --key repos --add 'github.com/agency/client-*' --yes")
	}
	if len(in.Values) == 0 && len(in.Add) == 0 && len(in.Remove) == 0 {
		return errs.New(errs.InvalidInput, "org config set: nothing to change").
			WithHint("give --value (the whole list, repeat), --add or --remove, e.g. lore org config set --org <org> --key " + in.Key + " --add 'github.com/agency/client-*' --yes")
	}
	for _, p := range append(slices.Clone(in.Values), in.Add...) {
		if _, err := repourl.Compile(p); err != nil {
			return errs.Wrap(errs.InvalidInput, "org config set --key "+in.Key+": "+strings.TrimPrefix(err.Error(), "repourl: "), err).
				WithHint("write host/owner/repo with ** as a whole segment and * as a segment or inside one, like github.com/agency/client-*")
		}
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
	get := spec(OpConfigGet, "Show the org's shared lore-org.yaml: own and repos patterns, stores", codes, false,
		op.Example{Title: "the org's repo patterns", CLI: "lore org config get --org agency", Want: "repos:"})
	get.Permission, get.Access = op.PermRead, op.AccessRead
	return []op.Entry{
		op.Define(get, func(ctx context.Context, in ConfigGetIn) (ConfigGetOut, error) {
			if in.Key != "" && !slices.Contains(ConfigKeys(), in.Key) && in.Key != "stores" {
				return ConfigGetOut{}, errs.New(errs.InvalidInput, "org config get: --key "+strconvQuote(in.Key)+" is not one of own, repos, stores").
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
		op.Define(spec(OpConfigSet, "Change the org's shared lore-org.yaml (admin only): own or repos patterns; committed in the org store and copied to this machine's org entry", append([]errs.Code{errs.NeedsConfirm, errs.Refused}, codes...), true,
			op.Example{Title: "add a client repo pattern", CLI: "lore org config set --org agency --key repos --add 'github.com/agency/client-*' --push --yes", Want: "repos"}),
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
