package wire

// writestore.go — a write naming its row's store with op.StoreFlag
// (`--store <org>[/<folder>]`, alias --to) inside a code repo (devco
// D-616). Outside a repo such a write runs on the store it names alone
// (targetoutside.go routedWriteStore). Inside a repo it keeps the repo's
// own routing (owner call: the write finds its row in the stores the repo
// attaches), so a --store naming a store or folder the repo does not
// attach used to be ignored: `resource check --id acme_app --store
// acme/sync` in a repo attaching acme/web and acme/org wrote the row of
// acme/org. Now that write is refused before anything opens for writing,
// naming what the repo attaches and the same command from the home folder
// (where --store routes) or from the store clone's folder.
//
//	write --store <name> in a code repo ─▶ <name> attached here ─▶ runs
//	                                    └▶ refused (E_NOT_FOUND): attaches
//	                                       <list>; run it from the home
//	                                       folder: lore <cmd> … --store <name>

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// refuseUnattachedWriteStore is the D-616 step (see the file comment). It
// does nothing for a read, a write naming no store or the repo, inside a
// store clone (its own folder routing) or when the engine cannot open
// (the operation reports that itself).
func (a *App) refuseUnattachedWriteStore(ctx context.Context, e op.Entry, d op.Decoder) error {
	if e.Spec().Access != op.AccessWrite {
		return nil
	}
	if _, in := storeHere(a.opts.RepoDir); in {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	name := routedWriteStore(in)
	if name == "" {
		return nil
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil
	}
	a.mu.Lock()
	var folders map[store.Source]string
	if a.st != nil {
		folders = maps.Clone(a.st.folders)
	}
	a.mu.Unlock()
	attached := attachedNames(eng.Stores(), folders)
	if slices.Contains(eng.Stores(), eng.StoreName(store.Source(name))) || storeNameAttached(name, attached) {
		return nil
	}
	shown := make([]store.Source, 0, len(eng.Stores()))
	for _, src := range eng.Stores() {
		// a store at a folder is shown as <store>/<folder>
		if f := strings.Trim(folders[src], "/"); f != "" && !strings.Contains(string(src), targetSep) {
			src = store.Source(string(src) + targetSep + f)
		}
		shown = append(shown, src)
	}
	return errUnattachedWriteStore(e, in, name, shown)
}

// attachedNames is every name a write's --store may give for the attached
// sources: each source, and for a store attached at a folder (a linked
// repo's own store "acme" at folder web) also "<source>/<folder>".
func attachedNames(srcs []store.Source, folders map[store.Source]string) []store.Source {
	var out []store.Source
	for _, s := range srcs {
		out = append(out, s)
		if f := strings.Trim(folders[s], "/"); f != "" && !strings.Contains(string(s), targetSep) {
			out = append(out, store.Source(string(s)+targetSep+f))
		}
	}
	return out
}

// storeNameAttached reports whether the store name a write's --store gives
// is one of the attached names: the name itself, or an org whose folders
// (<org>/<folder>) are attached.
func storeNameAttached(name string, attached []store.Source) bool {
	return slices.ContainsFunc(attached, func(s store.Source) bool {
		return string(s) == name || strings.HasPrefix(string(s), name+targetSep)
	})
}

// errUnattachedWriteStore is D-616's refusal: what --store named, what
// this repo attaches, and the command that writes there.
func errUnattachedWriteStore(e op.Entry, in op.Input, name string, attached []store.Source) error {
	var places []string
	for _, s := range attached {
		if s != store.SourceRepo {
			places = append(places, string(s))
		}
	}
	sort.Strings(places)
	here := "no store"
	if len(places) > 0 {
		here = strings.Join(places, ", ")
	}
	cmd := homeCommand(e, in, name)
	return errs.New(errs.NotFound, "--store "+name+" is not attached in this repo (it attaches "+here+"), and a write here never goes anywhere but what --store names, so nothing was written").
		WithHint("run it from the home folder, where --store picks the store: cd ~ && " + cmd + "; or from that folder of the store's clone (lore store list --setup <setup> shows the clones)")
}

// homeCommand is the command e with the text flags in carries and
// --store name: what a person types in the home folder.
func homeCommand(e op.Entry, in op.Input, name string) string {
	flags := map[string]string{}
	for _, f := range e.InputFields() {
		if f.Flag != "" && f.ArgOf == "" {
			flags[f.Name] = f.Flag
		}
	}
	parts := append([]string{"lore"}, op.CLIPath(e.Spec().Name)...)
	for _, tv := range in.Texts() {
		f, ok := flags[tv.Field]
		if !ok || tv.Text == "" || tv.Field == readStoreField {
			continue
		}
		parts = append(parts, "--"+f, shellWord(tv.Text))
	}
	return strings.Join(append(parts, "--store", name), " ")
}
