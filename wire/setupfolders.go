package wire

// setupfolders.go — setup.folders' and setup.browse's Prober side
// (opssetup.FolderLister): the facts the Folders step's suggestions come
// from, and a read-only folder browser rooted at the home folder.
//
// The browser lists subfolder NAMES only (never a file, never contents),
// refuses any path that is not the home folder or under it after
// resolving symlinks (so ~/link → /etc is refused, and a symlinked
// subfolder that points outside home is left out), and leaves out hidden
// folders (".config", ".git", …): a setup covers project folders, and
// listing dot-folders would mostly show tool state.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.FolderLister = setupProber{}

// FolderFacts implements opssetup.FolderLister.
func (sp setupProber) FolderFacts(ctx context.Context, in opssetup.FoldersIn) (opssetup.FolderFacts, error) {
	home := sp.p.Home()
	start, err := sp.startFolder()
	if err != nil {
		return opssetup.FolderFacts{}, err
	}
	f := opssetup.FolderFacts{Home: resolvedPath(home), Start: start, Setup: strings.TrimSpace(in.Setup)}
	if top := gitTopLevel(ctx, start); top != "" {
		f.GitRoot = resolvedPath(top)
	}
	for _, inc := range sp.p.IncludeIdentities(ctx) {
		if inc.Folder != "" {
			f.Includes = append(f.Includes, opssetup.IncludeFolder{Folder: inc.Folder, Name: inc.Name, Email: inc.Email})
		}
	}
	var entry onboard.SetupEntry
	if sp.a != nil {
		if cfg, err := ReadSetupsConfig(sp.a.opts.Home); err == nil {
			f.Setups = cfg.Setups
			if e, ok := cfg.Setup(f.Setup); ok {
				entry = e
				entry.Identity = cfg.IdentityOf(e) // a team setup's is in orgs[]
			}
		}
	}
	f.Emails = opssetup.SetupEmails(entry, in.Emails)
	return f, nil
}

// startFolder is the folder `lore setup` was started in (the App's work
// folder, else this process's), symlinks resolved: the Folders step's
// prefill and its first suggestion.
func (sp setupProber) startFolder() (string, error) {
	start := ""
	if sp.a != nil {
		start = sp.a.projectDir()
	}
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", errs.Wrap(errs.Internal, "read the current folder", err)
		}
		start = wd
	}
	return resolvedPath(start), nil
}

// Browse implements opssetup.FolderLister.
func (sp setupProber) Browse(_ context.Context, in opssetup.BrowseIn) (opssetup.BrowseOut, error) {
	return browseFolder(sp.p.Home(), in.Path)
}

// browseFolder lists p's subfolders (names only) when p is home or under
// it after symlinks are resolved; "" is home.
func browseFolder(home, p string) (opssetup.BrowseOut, error) {
	if home == "" {
		return opssetup.BrowseOut{}, errs.New(errs.Unsupported, "no home folder to browse")
	}
	root, err := filepath.EvalSymlinks(home)
	if err != nil {
		return opssetup.BrowseOut{}, errs.Wrap(errs.Internal, "resolve the home folder", err)
	}
	p = strings.TrimSpace(p)
	if strings.ContainsAny(p, "\x00\n\r") {
		return opssetup.BrowseOut{}, errs.New(errs.BadPath, "the folder path holds a control character")
	}
	want := home
	if p != "" && p != "~" {
		want = onboard.ExpandHome(p, home)
		if !filepath.IsAbs(want) {
			return opssetup.BrowseOut{}, errs.New(errs.BadPath, "give a folder under your home folder (~/… or an absolute path)")
		}
	}
	dir, err := filepath.EvalSymlinks(filepath.Clean(want))
	if err != nil {
		return opssetup.BrowseOut{}, errs.New(errs.BadPath, "no folder at "+tildeIn(filepath.Clean(want), home))
	}
	if !within(root, dir) {
		return opssetup.BrowseOut{}, errs.New(errs.BadPath, "only folders under your home folder can be browsed").
			WithHint("paste a glob for a folder outside it")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return opssetup.BrowseOut{}, errs.Wrap(errs.BadPath, "list "+tildeIn(dir, root), err)
	}
	out := opssetup.BrowseOut{Path: tildeIn(dir, root), Dirs: []opssetup.BrowseDir{}}
	if dir != root {
		out.Parent = tildeIn(filepath.Dir(dir), root)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // hidden: tool state, not project folders
		}
		full := filepath.Join(dir, name)
		if e.Type()&os.ModeSymlink != 0 {
			real, rerr := filepath.EvalSymlinks(full)
			st, serr := os.Stat(full)
			if rerr != nil || serr != nil || !st.IsDir() || !within(root, real) {
				continue // a link that leaves home or is not a folder
			}
		} else if !e.IsDir() {
			continue
		}
		if len(out.Dirs) == opssetup.BrowseMax {
			out.Truncated = true
			break
		}
		out.Dirs = append(out.Dirs, opssetup.BrowseDir{Name: name, Path: tildeIn(full, root)})
	}
	slices.SortFunc(out.Dirs, func(a, b opssetup.BrowseDir) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// within reports p is root or under it.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// tildeIn writes p under home as ~/… (home itself is ~).
func tildeIn(p, home string) string {
	if p == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rel)
	}
	return p
}
