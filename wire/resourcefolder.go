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
	// Remote is the normalised remote of this repo the resource names.
	Remote string
}

// target is the `--store` a read of the folder takes.
func (f resourceFolder) target() string { return f.Org + targetSep + f.Folder }

// note is the line printed above the read's answer: which folder it read
// and the one command that links the repo for good.
func (f resourceFolder) note() string {
	return "read " + f.target() + ": its resource " + f.Resource + " names this repo (" + f.Remote + "), which is not linked to a folder; link it: lore org link --org " + f.Org + " --project " + f.Folder + " --own"
}

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
	spec := e.Spec()
	if spec.Access == op.AccessWrite || !orgReadOp(spec) || !rowRead(spec.Name) {
		return resourceFolder{}, false
	}
	if spec.Local && spec.Name != contextop.OpFetch {
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
	for _, f := range a.orgFolders(ctx).Folders {
		if f.Dir == "" || (f.Org != org && f.Label != org) {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(f.Dir, filepath.FromSlash(f.Folder), schema.TableResources, "*.json"))
		for _, p := range files {
			name, remote, ok := resourceNamingRemote(p, mine)
			if ok {
				return resourceFolder{Org: org, Folder: f.Folder, Resource: name, Remote: remote}, true
			}
		}
	}
	return resourceFolder{}, false
}

// resourceNamingRemote reads one resource row file: its name and the
// remote of mine one of its string values names; ok is false when none
// does or the file is unreadable.
func resourceNamingRemote(path string, mine map[string]bool) (name, remote string, ok bool) {
	b, err := os.ReadFile(path) //nolint:gosec // a row file of the org's own store clone
	if err != nil {
		return "", "", false
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		return "", "", false
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
		return "", "", false
	}
	name = strings.TrimSuffix(filepath.Base(path), ".json")
	if raw, has := top["name"]; has {
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			name = s
		}
	}
	return name, remote, true
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
	return res.WithTextEdit(func(s string) string { return note + "\n" + s }), true, nil
}

// autoStoreOf is the store a resource-routed read reads ("" for any
// other command).
func autoStoreOf(ctx context.Context) string {
	s, _ := ctx.Value(autoStoreKey{}).(string)
	return s
}
