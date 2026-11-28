package wire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/factstale"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// FactChecker implements ops/lint.FactEnv and ops/ask.FactEnv (devco:D-941):
// a checker that asks git about the code repository this command runs in,
// where fact_evidence paths are relative to. Nil outside a git repository
// (a fact is then never reported stale on a guess).
func (a *App) FactChecker(ctx context.Context) *factstale.Checker {
	top, err := a.knownRepoTop(ctx)
	if err != nil || top == "" {
		return nil
	}
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		return gitRun{dir: top, args: args}.output(ctx)
	}
	// the answers kept for this HEAD (bug hunt H-213): rev-parse is answered
	// in process (gitfast), so a warm call with HEAD unchanged runs no git
	out, err := run(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	head := strings.TrimSpace(string(out))
	if err != nil || head == "" {
		return factstale.NewChecker(run).WithFiles(inTree(top)) // no commit yet: nothing to keep
	}
	return factstale.NewMemoChecker(run, factMemo{path: a.factMemoPath(top), top: top, head: head}).WithFiles(inTree(top))
}

// inTree reports whether the work tree at top holds a repo-relative path,
// so the checker reads evidence written from another folder (`myrepo/a.go`,
// a bare `b.go` after `pkg/a.go`) as the file the repo holds (bug hunt
// H-298). One lstat per distinct item, no git.
func inTree(top string) func(string) bool {
	return func(p string) bool {
		_, err := os.Lstat(filepath.Join(top, filepath.FromSlash(p)))
		return err == nil
	}
}

// factMemoDir is the folder under the lore home's cache that keeps each
// code repo's stale-fact answers (one file per repo, for its last HEAD).
const factMemoDir = "factstale"

// factMemoPath is the memo file of the code repo at top.
func (a *App) factMemoPath(top string) string {
	sum := sha256.Sum256([]byte(top))
	return filepath.Join(writableHome(a.opts.Home), cacheDir, factMemoDir, hex.EncodeToString(sum[:12])+".json")
}

// factMemo is factstale.Memo on a file: the answers of one code repo at
// one HEAD. A file of another repo or HEAD, or one that cannot be read, is
// no answer (the checker asks git and writes it anew).
type factMemo struct {
	path, top, head string
}

// factMemoFile is the memo file's content.
type factMemoFile struct {
	Top  string             `json:"top"`
	Head string             `json:"head"`
	Snap factstale.Snapshot `json:"snapshot"`
}

// Load implements factstale.Memo.
func (m factMemo) Load() (factstale.Snapshot, bool) {
	raw, err := os.ReadFile(m.path)
	if err != nil {
		return factstale.Snapshot{}, false
	}
	var f factMemoFile
	if json.Unmarshal(raw, &f) != nil || f.Top != m.top || f.Head != m.head {
		return factstale.Snapshot{}, false
	}
	return f.Snap, true
}

// Save implements factstale.Memo; a write that fails only costs the next
// command its git log, so the error is dropped.
func (m factMemo) Save(s factstale.Snapshot) {
	raw, err := json.Marshal(factMemoFile{Top: m.top, Head: m.head, Snap: s})
	if err != nil {
		return
	}
	_ = writeFileAtomic(m.path, raw, 0o600)
}

// markStaleHits sets Stale on every hit whose row states a fact about code
// that changed after it was verified (devco:D-941): search prints the mark
// and ranks the hit below a fresh one with the same match.
func (a *App) markStaleHits(ctx context.Context, hits []opssearch.Hit) {
	var claims []factstale.Claim
	for i := range hits {
		if hits[i].Fact != nil && !hits[i].InTrash {
			claims = append(claims, *hits[i].Fact)
		}
	}
	if len(claims) == 0 {
		return
	}
	chk := a.FactChecker(ctx)
	if chk == nil {
		return // no code repo here: nothing said (lint and doctor count it, bug hunt H-298)
	}
	chk.Prime(ctx, claims) // bug hunt H-209: one git log for every hit's evidence
	for i := range hits {
		if hits[i].Fact != nil && !hits[i].InTrash {
			v := chk.Check(ctx, *hits[i].Fact)
			hits[i].Stale, hits[i].Unchecked = v.Note(), v.Unchecked()
		}
	}
}
