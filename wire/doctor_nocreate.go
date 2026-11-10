package wire

// doctor_nocreate.go — a READ never sets a repo up (decided 2026-11-08):
// an operation that only reads (Spec.OnlyReads: list, show, search,
// status, whoami, store list, sync status, doctor, …) run in a git repo
// with no .lore/ creates nothing in it: no .lore/, no cache db, no
// .gitattributes, no merge-driver config, no hooks. It answers from what
// exists, or says lore is not set up there (lore init). Only a write keeps
// the implicit setup of the repo open (open.go creates .lore/ and its
// cache on first use, and the live hook then wires git). Before, a
// `lore memory list` or `lore sync status` in any clone left
// .lore/lore-cache.db, .gitattributes and seven hooks behind, and the next
// command saw a half-made project.
//
//	host.Runner.Run / App.runLocal (Spec.OnlyReads) ─▶ ctx carries noCreate
//	open(ctx) in a git repo with no .lore/ ─▶ E_NOT_A_STORE, nothing written
//	App.state / configState ─▶ the refusal is not memoised on the App (a
//	                           later command on the same App, e.g. `lore
//	                           api`, opens normally), only for the rest of
//	                           this run (noCreateRun): doctor's ~25 checks
//	                           each asked the App, and each failed open ran
//	                           git rev-parse, the setup resolution and the
//	                           bare-repo probe again (doctor --all in ~ ran
//	                           git 100 times for one answer)
//	DoctorChecks ─▶ one note instead of one "could not run" per check

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/thesatellite-ai/lore/model/errs"
)

// noCreateKey marks a context whose repo open must not create .lore/.
type noCreateKey struct{}

// noCreateRun is one read run's memory of the App's refused opens. A read
// creates nothing, so within one run a refused open stays refused: the
// next ask gets the same error without running git again. It lives in the
// run's ctx, not on the App, so the next run (a long-lived `lore api`)
// opens afresh. Its fields are read and written under App.mu.
type noCreateRun struct {
	stateErr  error // App.state's refusal in this run
	configErr error // App.configState's refusal in this run

	// tops memoises gitTopLevel per folder for the run: the setup
	// resolution, the read-only notice and doctor's checks each asked git
	// for the same folder's top. A read creates no repo, so the answer
	// cannot change within the run.
	topsMu sync.Mutex
	tops   map[string]string
}

// topLevel is gitTopLevel(dir) asked once per read run (ask runs git).
func (r *noCreateRun) topLevel(dir string, ask func() string) string {
	r.topsMu.Lock()
	defer r.topsMu.Unlock()
	if top, ok := r.tops[dir]; ok {
		return top
	}
	top := ask()
	if r.tops == nil {
		r.tops = map[string]string{}
	}
	r.tops[dir] = top
	return top
}

// WithNoCreate is ctx for a run that only reads (Spec.OnlyReads). The host's
// Runner applies it to every read before its hooks run, so the hooks that
// read the project after the op (the Context section) are covered too.
// A ctx that is already a read run keeps its memory (one run, one answer).
func WithNoCreate(ctx context.Context) context.Context {
	if NoCreate(ctx) {
		return ctx
	}
	return context.WithValue(ctx, noCreateKey{}, &noCreateRun{})
}

// NoCreate reports whether ctx forbids creating .lore/ (WithNoCreate).
func NoCreate(ctx context.Context) bool {
	return noCreateOf(ctx) != nil
}

// noCreateOf is ctx's read-run memory, nil outside a read run.
func noCreateOf(ctx context.Context) *noCreateRun {
	v, _ := ctx.Value(noCreateKey{}).(*noCreateRun)
	return v
}

// loreFolderName is the project folder a repo open creates (RepoCacheFile
// lives in it).
const loreFolderName = ".lore"

// loreFolderAt reports whether top already holds a .lore/ folder. Any
// .lore/ counts (an older lore's layout, a half-run init): doctor then
// checks it, and only a repo with none at all is left alone.
func loreFolderAt(top string) bool {
	fi, err := os.Stat(filepath.Join(top, loreFolderName))
	return err == nil && fi.IsDir()
}

// noProjectErr is open's refusal under noCreate: top is a git repo with no
// .lore/.
func noProjectErr(top string) error {
	return errs.New(errs.NotAStore, "lore isn't set up in "+top+" (no .lore folder); a read creates nothing there").
		WithHint("lore init sets this repo up (or the This folder step of `lore setup`)")
}

// doctorNoProjectNote is DoctorChecks' one line for a git repo with no
// lore project (instead of every store check failing to run).
const doctorNoProjectNote = "no lore project in this repo, so the store checks were skipped (lore init sets one up)"

// gitRepoWithoutLore reports whether the command's folder is in a git work
// tree whose top has no .lore/ (the case doctor must leave untouched).
func (a *App) gitRepoWithoutLore(ctx context.Context) bool {
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return false
	}
	return !loreFolderAt(a.loreRootFrom(top))
}

// loreRootFrom is the folder whose .lore/ counts for the command's folder:
// the nearest folder from projectDir up to top holding .lore/ (a monorepo
// subproject), else top.
func (a *App) loreRootFrom(top string) string {
	dir, err := filepath.Abs(a.projectDir())
	if err != nil {
		return top
	}
	if r, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		dir = r
	}
	for d := dir; len(d) >= len(top); d = filepath.Dir(d) {
		if loreFolderAt(d) {
			return d
		}
		if d == top || filepath.Dir(d) == d {
			break
		}
	}
	return top
}
