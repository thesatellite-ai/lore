package wire

// gitignoreprivate.go — devco:D-856: a private project (git ignores its
// .lore/ folder, privateproject.go) needs none of lore's .gitignore lines.
//
// Why: a dev listed .lore/ in .git/info/exclude (the project is private to
// that clone). MissingGitignoreLines did not count info/exclude (it reaches
// nobody else), so `lore init`, `lore fix` and `lore doctor --fix` appended
// lore's block to the tracked .gitignore and added it again after every
// revert: git status was never clean, against lore's own promise that a
// private project gets no line in the repo's tracked files. When git
// ignores the whole .lore/ folder every path in the block is ignored
// already, whichever rule does it, so nothing is missing anywhere.
//
//	private project (LoreDirIgnored)
//	  ─► MissingGitignoreLines: none (no command adds the block, .gitignore
//	     or info/exclude)
//	  ─► a "# lore" block an older lore left in .gitignore: `lore doctor`
//	     names it, `lore doctor --fix` (any install step) takes it out
//	     (DropPrivateGitignoreBlock)

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// gitignoreBlockHeader is the line lore's appended .gitignore block starts
// with (wire/host gitignoreHeader writes it after a blank line).
const gitignoreBlockHeader = "# lore"

// PrivateGitignoreBlock lists the lore lines of the "# lore" block(s) in
// root's .gitignore when the project at root is private (LoreDirIgnored):
// lines no clone needs, since git ignores the whole .lore/ folder. Empty
// for a shared project, a .gitignore without the block, or one git cannot
// read.
func PrivateGitignoreBlock(ctx context.Context, root string) []string {
	if !LoreDirIgnored(ctx, root) {
		return nil
	}
	_, removed := withoutLoreBlock(readGitignore(root))
	return removed
}

// DropPrivateGitignoreBlock takes the "# lore" block(s) out of the tracked
// .gitignore of the private project at root and returns the lore lines it
// removed. Only lore's own lines go: a line the person wrote under the
// header stays (and keeps the header). A shared project, or a .gitignore
// without the block, is not touched. The file is read and written through
// an os.Root, as the append is: a .gitignore linked out of the repository
// is left as it is.
func DropPrivateGitignoreBlock(ctx context.Context, root string) (removed []string, err error) {
	if !LoreDirIgnored(ctx, root) {
		return nil, nil
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	b, err := dir.ReadFile(gitignoreFile)
	if err != nil {
		return nil, nil // no .gitignore (or a link out of the repo): no block to take out
	}
	kept, removed := withoutLoreBlock(string(b))
	if len(removed) == 0 {
		return nil, nil
	}
	fi, err := dir.Stat(gitignoreFile)
	if err != nil {
		return nil, err
	}
	f, err := dir.OpenFile(gitignoreFile, os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return nil, err
	}
	_, werr := f.WriteString(kept)
	if err = errors.Join(werr, f.Close()); err != nil {
		return nil, err
	}
	return removed, nil
}

// readGitignore is root's .gitignore ("" when missing or linked out of the
// repository).
func readGitignore(root string) string {
	r, err := os.OpenRoot(root)
	if err != nil {
		return ""
	}
	defer func() { _ = r.Close() }() // read-only handle
	b, err := r.ReadFile(gitignoreFile)
	if err != nil {
		return ""
	}
	return string(b)
}

// withoutLoreBlock returns text without lore's lines under each "# lore"
// header (and the header plus the blank line lore put before it, when the
// block held nothing else), and the lore lines it took out.
func withoutLoreBlock(text string) (string, []string) {
	lore := make(map[string]bool, len(GitignoreLines))
	for _, l := range GitignoreLines {
		lore[l] = true
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	var removed []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != gitignoreBlockHeader {
			out = append(out, lines[i])
			continue
		}
		j := i + 1
		var own, other []string
		for ; j < len(lines) && strings.TrimSpace(lines[j]) != ""; j++ {
			if lore[strings.TrimSpace(lines[j])] {
				own = append(own, strings.TrimSpace(lines[j]))
			} else {
				other = append(other, lines[j])
			}
		}
		if len(own) == 0 {
			out = append(out, lines[i]) // a "# lore" the person wrote: not lore's block
			continue
		}
		removed = append(removed, own...)
		if len(other) > 0 {
			out = append(out, lines[i])
			out = append(out, other...)
		} else if n := len(out); n > 0 && out[n-1] == "" {
			out = out[:n-1] // the blank line lore wrote before its header
		}
		i = j - 1
	}
	return strings.Join(out, "\n"), removed
}

// doctorPrivateGitignoreNote is the doctor line of a private project whose
// tracked .gitignore still holds lore's block (devco:D-856).
func doctorPrivateGitignoreNote(root string, lines []string) string {
	return filepath.Join(root, gitignoreFile) + " holds lore's block (" + strings.Join(lines, ", ") + ") although git ignores .lore/ here, so the lines only keep .gitignore changed; `lore doctor --fix` takes them out (then commit .gitignore if the block was committed)"
}

// doctorPrivateDataNote replaces the data-ignored warning in a private
// project (devco:D-856): the ignore is the person's choice, not a fault.
const doctorPrivateDataNote = "git ignores .lore/ here, so this project is private to this machine: its notes stay in this clone and reach no teammate (to share them, stop ignoring .lore/ and commit .lore/data)"
