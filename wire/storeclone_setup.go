package wire

// storeclone_setup.go — the commands that set up the code repo they run in
// refuse inside a store clone (use pass 4, devco D-682).
//
// A store clone (~/.lore/stores/<owner>/<repo>, a person's own clone, a
// folder store's folder) is never a code repo (storehere.go). `lore init`
// run there opened the store folder it stood in, "adopted the shared lore
// project", committed a context.targets policy and actor rows into that
// folder (published to every teammate by the next sync), wrote pointer
// files and a .gitignore into the clone and installed lore's hooks and
// merge driver there. A plain render there committed into the store too,
// and org link/unlink write a code repo's own file (its link). Each is
// refused before anything is opened, so nothing at all is written. A
// read-only render (--dry-run, --check, --report, --trend) stays: in a
// store folder it previews that folder (D-432).
//
//	repo.init, render.run (writing), org.link, org.unlink in a store clone
//	  ─▶ E_UNSUPPORTED "this is the clone of store <org> (folder <f>);
//	     lore <verb> is for code repos: nothing written"
//	     hint: cd into the code repo; a code repo reads this folder through
//	     `lore org link --org <org> --project <f>`; here lore reads and
//	     writes the folder itself (`lore task list`)

import (
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsorg "github.com/thesatellite-ai/lore/ops/orgcmd"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opsrepoinit "github.com/thesatellite-ai/lore/ops/repoinit"
)

// storeCloneSetUpOps are the repo set-up operations refused in a store
// clone, by the command a person types for each (what the refusal names).
var storeCloneSetUpOps = map[string]string{
	opsrepoinit.OpRepoInit: "lore init",
	opsrender.OpRun:        "lore render",
	opsorg.OpLink:          "lore org link",
	opsorg.OpUnlink:        "lore org unlink",
}

// storeCloneReadModes are the input fields that make one of those
// operations read only (opsrender.RunIn.ReadOnly): set, it runs as before.
var storeCloneReadModes = map[string][]string{
	opsrender.OpRun: opsrender.ReadModes,
}

// storeCloneSetUp is storeCloneSetUpRefusal for an operation of the
// registry, which reads the input only for an operation that has a read
// only mode, and only inside a store clone.
func (a *App) storeCloneSetUp(e op.Entry, d op.Decoder) error {
	name, dir := e.Spec().Name, a.projectDir()
	if _, ok := storeCloneSetUpOps[name]; !ok || !inStore(dir) {
		return nil
	}
	if modes := storeCloneReadModes[name]; len(modes) > 0 {
		if in, err := e.Decode(d); err == nil {
			for _, f := range modes {
				if on, _ := in.Bool(f); on {
					return nil
				}
			}
		}
	}
	return storeCloneSetUpRefusal(a.opts.Home, dir, name)
}

// storeCloneSetUpRefusal refuses the repo set-up operation name when dir is
// inside a store clone; nil for any other operation or folder.
func storeCloneSetUpRefusal(home, dir, name string) error {
	verb, ok := storeCloneSetUpOps[name]
	if !ok {
		return nil
	}
	sc, in := storeHere(dir)
	if !in {
		return nil
	}
	return errStoreCloneSetUp(home, sc, verb)
}

// errStoreCloneSetUp is the refusal of verb in the store clone sc: which
// store and folder this is, that verb is for code repos, and the two ways
// on (the code repo, or this folder read and written in place).
func errStoreCloneSetUp(home string, sc storeCheckout, verb string) error {
	name, folder := storeCloneName(home, sc), sc.folder
	where := "this is the clone of store " + name
	if folder != rowfs.FolderRoot {
		where += " (folder " + folder + ")"
	} else {
		folder = "<folder>"
	}
	link := "lore org link --org " + name + " --project " + folder
	return errs.New(errs.Unsupported, where+" at "+sc.shown+", not a code repo; "+verb+" is for code repos: nothing written").
		WithHint("cd into the code repo you want set up and run " + verb + " there (a code repo reads this store folder after `" + link + "` in it); inside the store clone lore reads and writes the folder you stand in (`lore task list`, `lore memory list`)")
}

// StoreCloneSetUpRefusal is storeCloneSetUpRefusal for a command the CLI
// runs outside the registry (`lore init` runs repoinit.Run itself): the
// same refusal in the same words, before anything is opened or written.
func (a *App) StoreCloneSetUpRefusal(dir, name string) error {
	return storeCloneSetUpRefusal(a.opts.Home, dir, name)
}

// storeCloneName is the name of the store sc is a checkout of on this
// machine (its org entry, me), else the clone folder's name; never the
// folder part storeHereEntry appends.
func storeCloneName(home string, sc storeCheckout) string {
	ent, err := storeHereEntry(home, sc)
	if err != nil {
		return filepath.Base(sc.top)
	}
	name, _, _ := strings.Cut(string(ent.Name), targetSep)
	return name
}
