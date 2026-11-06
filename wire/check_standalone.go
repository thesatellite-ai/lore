package wire

// check_standalone.go — `lore check leaks` and `lore check pr-body` on
// cmd/lore (ops/check.Checker, Options.Local.Leaks; P3-CMDLORE-8), ported
// from the old binary's check_port.go / check_cmd.go onto the core's F16
// check (App.LeakCheckDocs) and logic/leakcheck.CheckCI.
//
//	pr-body ─▶ LeakCheckDocs("PR body", {file: body})
//	leaks   ─▶ --ci: key loosening since the default branch, then every
//	           .lore/data row file must belong to this project (no store)
//	        ─▶ --as-client: CheckAsClient (check_asclient_standalone.go)
//	        ─▶ default: staged files (+ --commit-msg) ─▶ LeakCheckDocs
//	           ("commit"); silent when clean
//
// The check is a Local operation, so the process's App has opened nothing:
// the checker opens its own App from the same Options on first use.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
	opscheck "github.com/thesatellite-ai/lore/ops/check"
)

// check output and names (v0.1.17's).
const (
	checkCIClean        = "✓ lore check leaks --ci: every row in .lore/data belongs to this project"
	checkWhereCommit    = "commit"
	checkWhereCommitMsg = "commit message"
	checkDataPrefix     = ".lore/data/"
)

// standaloneLeaks implements opscheck.Checker for cmd/lore.
type standaloneLeaks struct {
	opts Options
	res  *OrgResolution
	once sync.Once
	app  *App
	err  error
}

var _ opscheck.Checker = (*standaloneLeaks)(nil)

// appOf is the checker's own App (built once from the process's Options).
func (l *standaloneLeaks) appOf() (*App, error) {
	l.once.Do(func() { l.app, l.err = New(l.opts) })
	return l.app, l.err
}

// CheckText implements opscheck.Checker (check.pr_body).
func (l *standaloneLeaks) CheckText(ctx context.Context, in opscheck.TextIn) error {
	a, err := l.appOf()
	if err != nil {
		return err
	}
	return a.LeakCheckDocsExplain(ctx, l.res, in.Where, map[string][]byte{in.Where: in.Body}, in.Explain)
}

// CheckLeaks implements opscheck.Checker.
func (l *standaloneLeaks) CheckLeaks(ctx context.Context, in opscheck.LeaksIn) (opscheck.LeaksOut, error) {
	root, err := os.Getwd()
	if err != nil {
		return opscheck.LeaksOut{}, errs.Wrap(errs.Internal, "getwd", err)
	}
	switch {
	case in.CI:
		if cerr := checkCI(ctx, root); cerr != nil {
			return opscheck.LeaksOut{}, cerr
		}
		return opscheck.LeaksOut{Message: checkCIClean}, nil
	case in.AsClient: // later 515 (check_asclient_standalone.go)
		a, aerr := l.appOf()
		if aerr != nil {
			return opscheck.LeaksOut{}, aerr
		}
		if cerr := a.CheckAsClient(ctx, root, in.Explain); cerr != nil {
			return opscheck.LeaksOut{}, cerr
		}
		return opscheck.LeaksOut{Message: AsClientClean}, nil
	}
	docs := map[string][]byte{}
	if in.CommitMsg != "" {
		b, rerr := os.ReadFile(filepath.Clean(commitMsgFile(ctx, root, in.CommitMsg)))
		if rerr != nil {
			return opscheck.LeaksOut{}, errs.Wrap(errs.InvalidInput, "read the commit message", rerr)
		}
		docs[checkWhereCommitMsg] = stripGitComments(b)
	}
	if in.Staged || in.CommitMsg == "" {
		if serr := stagedFiles(ctx, root, docs); serr != nil {
			return opscheck.LeaksOut{}, serr
		}
	}
	a, err := l.appOf()
	if err != nil {
		return opscheck.LeaksOut{}, err
	}
	// Secrets first, in every repo (hooksecrets.go), then F16 (client repos).
	if serr := a.HookSecrets(ctx, root, checkWhereCommit, docs); serr != nil {
		return opscheck.LeaksOut{}, serr
	}
	return opscheck.LeaksOut{}, a.LeakCheckDocsExplain(ctx, l.res, checkWhereCommit, docs, in.Explain) // silent when clean
}

// checkCI is F16's CI mode over the checked-out tree's .lore/data.
func checkCI(ctx context.Context, root string) error {
	if found, err := KeyLooseningsSince(ctx, root, ""); err == nil && len(found) > 0 {
		return keyLoosening(found)
	}
	pid := orgcfg.ProjectID(root)
	if pid == "" {
		return errs.New(errs.InvalidInput, "no .lore/data/_meta.json here; run from the project root")
	}
	dataDir := filepath.Join(root, rowfs.LoreDir, rowfs.DataDir)
	tree, err := readDataTree(dataDir)
	if err != nil {
		return errs.Wrap(errs.Internal, "read .lore/data", err)
	}
	fs := leakcheck.CheckCI(tree, pid)
	if len(fs) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "lore check leaks --ci: %d row file(s) must not be in this repo:\n", len(fs))
	for _, f := range fs {
		fmt.Fprintf(&b, "  %s: %s\n", f.Where, f.Detail)
	}
	b.WriteString("ask the author to remove them (and run `lore check leaks` locally, which names them)")
	return errs.New(errs.LeakBlocked, b.String())
}

// keyLoosening joins KeyLooseningsSince findings into one E_LEAK_BLOCKED.
func keyLoosening(found []error) error {
	lines := make([]string, 0, len(found))
	hints := make([]string, 0, len(found))
	for _, f := range found {
		var e *errs.Error
		if errors.As(f, &e) {
			lines, hints = append(lines, e.Message), append(hints, e.Hint)
			continue
		}
		lines = append(lines, f.Error())
	}
	return errs.New(errs.LeakBlocked, strings.Join(lines, "; ")).WithHint(strings.Join(hints, "; "))
}

// gitTop is the repo top of dir ("" when not a git checkout).
func gitTop(ctx context.Context, dir string) string {
	out, err := gitRun{dir: dir, args: []string{"rev-parse", "--show-toplevel"}}.output(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// stagedFiles adds every staged file (added, copied, modified, renamed),
// read from the index: what is checked is what is committed.
func stagedFiles(ctx context.Context, root string, docs map[string][]byte) error {
	top := gitTop(ctx, root)
	if top == "" {
		return nil // not a git checkout: nothing is staged
	}
	out, err := gitRun{dir: top, args: []string{"diff", "--cached", "--name-only", "-z", "--diff-filter=ACMR"}}.output(ctx)
	if err != nil {
		return errs.Wrap(errs.Internal, "list staged files", err)
	}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		content, serr := gitRun{dir: top, args: []string{"show", ":" + p}}.output(ctx)
		if serr != nil {
			continue // a submodule or an unreadable blob: nothing to read
		}
		docs[p] = content
	}
	return nil
}

// commitMsgFile resolves the commit-msg hook's $1 (relative to the repo
// top; a monorepo project's hook runs from the project folder).
func commitMsgFile(ctx context.Context, root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(filepath.Join(root, p)); err == nil {
		return filepath.Join(root, p)
	}
	if top := gitTop(ctx, root); top != "" {
		return filepath.Join(top, p)
	}
	return p
}

// stripGitComments drops git's "#" comment lines; every other line,
// trailers included, is checked (coordinator Q13).
func stripGitComments(b []byte) []byte {
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(ln, "#") {
			out = append(out, ln)
		}
	}
	return []byte(strings.Join(out, "\n"))
}

// readDataTree reads every regular file under dataDir through an os.Root
// (no symlink leaves the folder), keyed ".lore/data/<rel>".
func readDataTree(dataDir string) (map[string][]byte, error) {
	r, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }() // read-only: nothing to lose
	tree := map[string][]byte{}
	err = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || !d.Type().IsRegular() {
			return werr
		}
		b, rerr := r.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		tree[checkDataPrefix+p] = b
		return nil
	})
	return tree, err
}
