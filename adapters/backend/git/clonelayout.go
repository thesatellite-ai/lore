package git

// clonelayout.go — the one layout of a store clone on this machine (spec
// 6.1) and the scan `lore doctor` uses to find a clone that lost a half.
//
//	<home>/clones/<host>/<owner>/<repo>.git   the bare clone (git data)
//	<home>/clones/<host>/<owner>/<repo>/work  the worktree lore reads and writes
//	<home>/clones/<host>/<owner>/<repo>/lock  the clone lock
//	<home>/clones/<host>/<owner>/<repo>/state the lock folder's journal
//
// The two sibling folders <repo>/ and <repo>.git/ are the two halves of ONE
// clone, not two copies: removing either breaks the store's clone. A clone
// of a pinned branch uses the stem <repo>@<branch>.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ClonePaths is every folder of one store clone, from its stem (CloneBase).
type ClonePaths struct {
	// Base is the stem folder: it holds the worktree, the lock and the
	// lock journal.
	Base string
	// Bare is the bare clone beside it (Base + ".git").
	Bare string
	// Work is the worktree inside Base.
	Work string
	// Lock is the clone lock inside Base.
	Lock string
}

// PathsOf is the layout of the clone whose stem is base. OpenClone, store
// forget and the doctor scan all derive the folders here, so the layout
// has one definition.
func PathsOf(base string) ClonePaths {
	return ClonePaths{Base: base, Bare: base + bareSuffix, Work: filepath.Join(base, workDir), Lock: filepath.Join(base, lockName)}
}

// CloneState is what a clone scan found for one stem.
type CloneState string

// Clone states. Only CloneWhole is a working clone with both halves;
// CloneNoWork is reused (a worktree is added) the next time the store
// opens; the other states cannot be used as they are.
const (
	// CloneWhole: both halves, the worktree linked to the bare clone.
	CloneWhole CloneState = "whole"
	// CloneNoWork: the bare clone without its stem folder or worktree.
	CloneNoWork CloneState = "no_worktree"
	// CloneNoBare: the stem folder without the bare clone beside it; its
	// worktree points at git data that is gone.
	CloneNoBare CloneState = "no_git_data"
	// CloneMislinked: the worktree belongs to another bare clone (the
	// lore home moved, or a folder was copied in by hand).
	CloneMislinked CloneState = "mislinked"
	// CloneOrphanWork: the worktree names git data that no longer exists
	// (the bare clone was replaced by a fresh one).
	CloneOrphanWork CloneState = "orphan_worktree"
)

// CloneFound is one clone stem a scan found.
type CloneFound struct {
	// Norm is the stem relative to the clones folder, slash separated
	// (host/owner/repo, or host/owner/repo@branch).
	Norm  string
	Paths ClonePaths
	State CloneState
	// LinkedTo is the git data the worktree names (CloneMislinked and
	// CloneOrphanWork only).
	LinkedTo string
}

// gitFileName is a linked worktree's pointer file; gitdirPrefix starts its
// one line.
const (
	gitFileName  = ".git"
	gitdirPrefix = "gitdir:"
	headFileName = "HEAD"
)

// ScanClones lists every clone stem under <home>/clones with the state of
// its two halves, in walk order. It only reads. A missing clones folder is
// no clones.
func ScanClones(home string) ([]CloneFound, error) {
	root := filepath.Join(home, ClonesDir)
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []CloneFound
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || p == root {
			return nil
		}
		var base string
		switch {
		case strings.HasSuffix(p, bareSuffix) && isFile(filepath.Join(p, headFileName)):
			base = strings.TrimSuffix(p, bareSuffix)
		case isDir(filepath.Join(p, workDir)) || isFile(filepath.Join(p, lockName)):
			base = p
		default:
			return nil // a host or owner folder: look inside
		}
		if !seen[base] {
			seen[base] = true
			rel, rerr := filepath.Rel(root, base)
			if rerr != nil {
				return rerr
			}
			out = append(out, classify(filepath.ToSlash(rel), PathsOf(base)))
		}
		return filepath.SkipDir
	})
	return out, err
}

// classify decides one stem's state from what is on disk.
func classify(norm string, p ClonePaths) CloneFound {
	f := CloneFound{Norm: norm, Paths: p}
	hasBare := isFile(filepath.Join(p.Bare, headFileName))
	switch {
	case !hasBare:
		f.State = CloneNoBare
	case !isFile(filepath.Join(p.Work, gitFileName)):
		f.State = CloneNoWork
	default:
		linked := worktreeGitdir(p.Work)
		f.LinkedTo = linked
		switch {
		case linked == "" || !isDir(linked):
			f.State = CloneOrphanWork
		case !within(linked, p.Bare):
			f.State = CloneMislinked
		default:
			f.State, f.LinkedTo = CloneWhole, ""
		}
	}
	return f
}

// worktreeGitdir is the git data a linked worktree's .git file names
// ("" when unreadable).
func worktreeGitdir(work string) string {
	b, err := os.ReadFile(filepath.Join(work, gitFileName))
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(b), "\n")
	dir, ok := strings.CutPrefix(strings.TrimSpace(line), gitdirPrefix)
	if !ok {
		return ""
	}
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(work, dir)
	}
	return dir
}

// within reports whether p is dir or inside it, comparing the resolved
// paths when they resolve (a symlinked home spells one folder two ways).
func within(p, dir string) bool {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// StateOf classifies the one clone whose stem is base (norm names it as
// ScanClones would). It only reads.
func StateOf(norm, base string) CloneFound { return classify(norm, PathsOf(base)) }
