package wire

// resourcefolder.go — list and search reads in an own repo that is not
// linked to a store folder (devco D-556, owner use pass 1 Wrong 8). A read
// by id there already finds its row (targetoutside.go, D-551), but `trash
// list`, `search`, `rule list`, every `<kind> list` and `context` refused
// "lore isn't set up (no .lore folder)" although a resource of the org
// already named the repo by its remote. Such a read now answers from the
// folder holding that resource, with one line saying which folder it read
// and the link that makes it permanent:
//
//	read, own repo, no .lore/, no link ─▶ resourceFolderOf: a resource row
//	    of the org's folders naming one of this repo's remotes
//	  ├─ found ─▶ runStoreOnly on <org>/<folder> + the "read …; link it" line
//	  └─ none  ─▶ the usual refusal (org link --own, --store <org>)

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/resquar"
	"github.com/thesatellite-ai/lore/op"
	contextop "github.com/thesatellite-ai/lore/ops/context"
	"github.com/thesatellite-ai/lore/schema"
)

// resourceFolder is the org-store folder whose resource names this
// repo's remote.
type resourceFolder struct {
	// Org is the org whose store holds the folder; Folder the folder name.
	Org, Folder string
	// Resource is the resource's name (its id when it has none).
	Resource string
	// Holder is the folder the resource row lives in when its project
	// field names another folder (Folder), "" when it is Folder itself.
	Holder string
	// Remote is the normalised remote of this repo the resource names.
	Remote string
}

// target is the `--store` a read of the folder takes.
func (f resourceFolder) target() string { return f.Org + targetSep + f.Folder }

// note is the line printed above the read's answer: which folder it read
// and the one command that links the repo for good.
func (f resourceFolder) note() string {
	res := f.Resource
	if f.Holder != "" {
		res += " (in " + f.Org + targetSep + f.Holder + ")"
	}
	return "read " + f.target() + ": its resource " + res + " names this repo (" + f.Remote + "), which is not linked to a folder; link it: lore org link --org " + f.Org + " --project " + f.Folder + " --own"
}

// resourceProjectField is the resource field naming the folder (project)
// the resource belongs to: a resource kept in the org folder for a repo
// of another project names that project here (devco:D-588).
const resourceProjectField = "project"

// autoStoreKey carries the store a read with no --store reads when a
// resource names the repo (outsideTo takes it as the read's --store).
type autoStoreKey struct{}

// resourceRoutedRead reports the folder a read in an unlinked own repo
// answers from: a read op of rows naming no store of its own, in a git
// repo with no .lore/ that no store link names, whose org is the one this
// repo is an own repo of, and one of whose folders holds a resource
// naming the repo's remote. ok is false otherwise (the usual path and its
// refusal).
func (a *App) resourceRoutedRead(ctx context.Context, e op.Entry, d op.Decoder) (resourceFolder, bool) {
	if !resourceRoutable(e.Spec()) {
		return resourceFolder{}, false
	}
	if UntouchedRepo(ctx, a.opts.RepoDir) == "" || linkedByRemote(ctx, a.opts) != "" {
		return resourceFolder{}, false
	}
	in, err := e.Decode(d)
	if err != nil {
		return resourceFolder{}, false
	}
	if n := scopeOf(in); n.to != "" || n.store != "" {
		return resourceFolder{}, false
	}
	if s, ok := in.RoutedStore(); ok && s != "" {
		return resourceFolder{}, false
	}
	org := ownOrgOf(ctx, a.opts)
	if org == "" {
		return resourceFolder{}, false
	}
	return a.resourceFolderOf(ctx, org)
}

// resourceRoutable reports whether a resource can route the operation
// spec in an unlinked own repo: a read of rows naming no store of its own
// (not a store, live, sync or migrate command), never a write, and no
// Local command but context.
func resourceRoutable(spec op.Spec) bool {
	if spec.Access == op.AccessWrite || !orgReadOp(spec) || !rowRead(spec.Name) {
		return false
	}
	return !spec.Local || spec.Name == contextop.OpFetch
}

// routedField is the top-level --json field of a resource-routed read
// (devco D-623): the <org>/<folder> it read, so an empty answer still says
// where it looked. The text line (note) says the same for a person.
var routedField = op.FieldInfo{
	Name: op.RoutedField,
	Type: op.TypeString,
	Desc: "the <org>/<folder> this read answered from when a resource of the org named this unlinked repo (absent otherwise)",
}

// routedTopFields declares routedField on every operation a resource can
// route (op.Registry.WithTopFields), for the OpenAPI document.
func routedTopFields(spec op.Spec) []op.FieldInfo {
	if !resourceRoutable(spec) {
		return nil
	}
	return []op.FieldInfo{routedField}
}

// resourceFolderOf scans the resource rows of every folder of org's
// stores for one whose field values name a remote of this repo
// (normalised as the org config keys repos, repourl.Normalize).
func (a *App) resourceFolderOf(ctx context.Context, org string) (resourceFolder, bool) {
	remotes, _ := orgcfg.Remotes(ctx, a.opts.RepoDir)
	if len(remotes) == 0 {
		return resourceFolder{}, false
	}
	mine := map[string]bool{}
	for _, r := range remotes {
		mine[r] = true
	}
	scan := a.orgFolders(ctx)
	folders := map[string]bool{}
	byDir := map[string][]string{}
	for _, f := range scan.Folders {
		if f.Org == org || f.Label == org {
			folders[f.Folder] = true
			byDir[f.Dir] = append(byDir[f.Dir], f.Folder)
		}
	}
	for _, f := range scan.Folders {
		if f.Dir == "" || (f.Org != org && f.Label != org) {
			continue
		}
		names := storeValueNames(f.Dir, append([]string{f.Folder}, byDir[f.Dir]...))
		files, _ := filepath.Glob(filepath.Join(f.Dir, filepath.FromSlash(f.Folder), schema.TableResources, "*.json"))
		for _, p := range files {
			name, remote, project, ok := resourceNamingRemote(p, mine, names)
			if !ok {
				continue
			}
			out := resourceFolder{Org: org, Folder: f.Folder, Resource: name, Remote: remote}
			if project != "" && project != f.Folder && folders[project] {
				// devco:D-588: the resource lives in another folder (the org
				// folder) but names its project: that folder is where the
				// repo's rules are, and the link the note suggests
				out.Folder, out.Holder = project, f.Folder
			}
			return out, true
		}
	}
	return resourceFolder{}, false
}

// valueNamer is the current name of the allowed value vid of the kind
// field fid (OQ-AV: a closed value is stored as its val_ id); ok is false
// when no field row of the store knows the id.
type valueNamer func(fid, vid string) (string, bool)

// storeValueNames is the valueNamer of the store clone dir: it reads the
// kind_fields row of fid in each of folders in turn (the resource's own
// folder first, then the org's other folders: a kind is usually defined
// in the org folder) and maps vid by its value_ids.
func storeValueNames(dir string, folders []string) valueNamer {
	return func(fid, vid string) (string, bool) {
		if fid == "" || strings.ContainsAny(fid, `/\.`) {
			return "", false
		}
		for _, folder := range folders {
			b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(folder), schema.TableKindFields, fid+".json")) //nolint:gosec // a row file of the org's own store clone
			if err != nil {
				continue
			}
			var row struct {
				ValueIDs json.RawMessage `json:"value_ids"`
			}
			if json.Unmarshal(b, &row) != nil {
				continue
			}
			if n, ok := resquar.ValueIDsOf(row.ValueIDs)[vid]; ok && strings.TrimSpace(n) != "" {
				return strings.TrimSpace(n), true
			}
		}
		return "", false
	}
}

// resourceNamingRemote reads one resource row file: its name, the remote
// of mine one of its string values names, and its project field's value
// as text ("" when it has none; a select value resolved to its name by
// names); ok is false when no value names a remote of mine or the file
// is unreadable.
func resourceNamingRemote(path string, mine map[string]bool, names valueNamer) (name, remote, project string, ok bool) {
	b, err := os.ReadFile(path) //nolint:gosec // a row file of the org's own store clone
	if err != nil {
		return "", "", "", false
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		return "", "", "", false
	}
	for _, s := range jsonStrings(b, 0) {
		n, nerr := repourl.Normalize(s)
		if nerr != nil {
			continue
		}
		if c := orgcfg.CanonicalRemote(n); mine[n] || mine[c] { // D-532: an alias spelling names the repo too
			remote = c
			break
		}
	}
	if remote == "" {
		return "", "", "", false
	}
	name = strings.TrimSuffix(filepath.Base(path), ".json")
	if raw, has := top["name"]; has {
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			name = s
		}
	}
	return name, remote, resourceFieldText(top["fields"], resourceProjectField, names), true
}

// resourceFieldText is the text value of the field named name in a
// resource row's fields object ({<fld_ id>: {"name": <field>, "v":
// <value>}}), trimmed; "" when the field is absent or not text. A closed
// (select) value is stored as its val_ id in v with its name in v_name
// (OQ-AV): it is the value's current name by names, else the v_name
// label, never the val_ id (devco:D-643: the owner's project field was a
// select, and the link hint fell back to the folder holding the row).
func resourceFieldText(fields json.RawMessage, name string, names valueNamer) string {
	var entries map[string]struct {
		Name  string          `json:"name"`
		V     json.RawMessage `json:"v"`
		VName json.RawMessage `json:"v_name"`
	}
	if json.Unmarshal(fields, &entries) != nil {
		return ""
	}
	for fid, e := range entries {
		var v string
		if e.Name != name || json.Unmarshal(e.V, &v) != nil {
			continue
		}
		v = strings.TrimSpace(v)
		if len(e.VName) == 0 || string(e.VName) == "null" {
			return v
		}
		if names != nil {
			if n, ok := names(fid, v); ok {
				return n
			}
		}
		var label string
		if json.Unmarshal(e.VName, &label) == nil {
			return strings.TrimSpace(label)
		}
		return ""
	}
	return ""
}

// jsonStrings is every string value in the JSON document b (objects and
// arrays walked, keys skipped), to a small depth: a resource's fields hold
// its values as strings, lists or nested maps.
func jsonStrings(b json.RawMessage, depth int) []string {
	if depth > 6 {
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return []string{s}
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) == nil {
		var out []string
		for _, v := range obj {
			out = append(out, jsonStrings(v, depth+1)...)
		}
		return out
	}
	var arr []json.RawMessage
	if json.Unmarshal(b, &arr) == nil {
		var out []string
		for _, v := range arr {
			out = append(out, jsonStrings(v, depth+1)...)
		}
		return out
	}
	return nil
}

// runResourceRouted runs the read e on the folder f names (runStoreOnly
// with f as its --store) and prints f's note line above its answer.
func (a *App) runResourceRouted(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller, f resourceFolder) (op.Result, bool, error) {
	ctx = context.WithValue(ctx, autoStoreKey{}, f.target())
	res, handled, err := a.runOrgReadAside(ctx, e, d, c)
	if err != nil || !handled {
		return res, handled, err
	}
	note := f.note()
	return res.WithTextEdit(func(s string) string { return note + "\n" + s }).WithTopText(op.RoutedField, f.target()), true, nil
}

// autoStoreOf is the store a resource-routed read reads ("" for any
// other command).
func autoStoreOf(ctx context.Context) string {
	s, _ := ctx.Value(autoStoreKey{}).(string)
	return s
}
