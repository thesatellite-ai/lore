package host

// gitwire.go — the per-clone git wiring of a binary built from the root
// module (ported from saas/cmd/cli sync_wire.go ensureGitWiring, P3-MODEB-9):
// the merge drivers in the clone's config, the .gitattributes block of the
// project, and lore's hook blocks, so a fresh clone where only cmd/lore runs
// is wired by its first command (F18) and its commits and pushes get the
// F16 checks (commit-msg: `lore check leaks --staged --commit-msg`,
// pre-push: `lore sync hook pre-push`).
//
//	live run in a project ─▶ fingerprint (wiring version + stat of every
//	   wired file) equals .git/lore/git-wiring? ─▶ nothing to do
//	   else ─▶ config, .gitattributes, hook blocks ─▶ new fingerprint
//
// The values are the old binary's, byte for byte (same markers, same hook
// lines), so the two binaries never rewrite each other's blocks. Blocks are
// chained into the hook folder git uses (core.hooksPath when set), never
// repointing it. Best effort: a failure is a status line, never a refused
// command.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// Git wiring values (v0.1.17 sync_wire.go).
const (
	gitBlockMarker      = "lore-sync"
	mergeDriverLore     = "lore"
	mergeDriverKeepOurs = "lore-keep-ours"
	loreMDRel           = ".lore/LORE.md"
	gitAttributesFile   = ".gitattributes"
	gitAttributesMode   = 0o644
	hookPreCommit       = "pre-commit"
	hookCommitMsg       = "commit-msg"
	hookPostCheckout    = "post-checkout"
	hookPostMerge       = "post-merge"
	hookPostRewrite     = "post-rewrite"
	// gitWiringStamp is the fingerprint file in .git/lore.
	gitWiringStamp = "git-wiring"
	// gitWiringVersion changes whenever what is installed changes.
	gitWiringVersion = "1"
	binaryName       = "lore"
)

// gitAttributeLines are the project's .gitattributes rules: row files LF,
// lore's field merge driver, collapsed in PR diffs; LORE.md keeps ours.
var gitAttributeLines = []string{
	".lore/data/**/*.json text eol=lf merge=" + mergeDriverLore + " linguist-generated=true",
	".lore/data/*.json text eol=lf merge=" + mergeDriverLore + " linguist-generated=true",
	loreMDRel + " text eol=lf merge=" + mergeDriverKeepOurs + " linguist-generated=true",
}

// mergeDriverConfig is the per-clone config the attributes refer to; bin is
// the absolute path of this lore (a merge never falls to an older lore on
// PATH that cannot read `_v: 2` rows).
func mergeDriverConfig(bin string) [][2]string {
	return [][2]string{
		{"merge." + mergeDriverLore + ".name", "lore field-level row merge"},
		{"merge." + mergeDriverLore + ".driver", bin + " merge-driver %O %A %B %P"},
		{"merge." + mergeDriverKeepOurs + ".name", "lore: keep ours, re-render after merge"},
		{"merge." + mergeDriverKeepOurs + ".driver", bin + " merge-driver --keep-ours %O %A %B %P"},
	}
}

// mergeDriverBinary is this executable, single-quoted for git's shell; the
// bare name when it cannot be resolved or holds a quote.
func mergeDriverBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return binaryName
	}
	if r, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = r
	}
	if strings.ContainsRune(exe, '\'') {
		return binaryName
	}
	return "'" + filepath.ToSlash(exe) + "'"
}

// hookBlocks are the blocks chained into each hook for the project at rel
// (repo-relative, "" or "." for the top level), with their markers: the
// project blocks carry `cd <project>`; post-commit and pre-push are
// repo-wide (one ride commit covers every project) and use rideHookMarker.
func hookBlocks(rel string) map[string]struct {
	marker string
	lines  []string
} {
	cd := ""
	marker := gitBlockMarker
	if rel != "" && rel != "." {
		cd = `cd "` + filepath.ToSlash(rel) + `" && `
		marker = gitBlockMarker + ":" + filepath.ToSlash(rel)
	}
	bn := binaryName
	guard := `command -v ` + bn + ` >/dev/null 2>&1 && `
	refresh := []string{"( " + cd + guard + bn + " sync hook refresh >/dev/null 2>&1 ) || true"}
	probe := bn + " sync hook --help >/dev/null 2>&1"
	tooOld := `echo "` + bn + `: installed ` + bn + ` cannot check .lore/data (no 'sync hook'); upgrade it" >&2`
	coreProbe := bn + " migrate --help >/dev/null 2>&1"
	hasV2 := `grep -rqsE '"_v": *2' .lore/data`
	needsCore := `echo "` + bn + `: .lore/data holds lore 0.1.18 rows but the ` + bn + ` on PATH is older; install lore 0.1.18 or newer to commit" >&2; exit 1`
	absMsg := `m="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"; `
	type block = struct {
		marker string
		lines  []string
	}
	return map[string]block{
		hookPreCommit:    {marker, []string{"( " + cd + "if command -v " + bn + " >/dev/null 2>&1; then if ! " + coreProbe + " && " + hasV2 + "; then " + needsCore + "; elif " + probe + "; then " + bn + " sync hook pre-commit; else " + tooOld + "; fi; fi ) || exit $?"}},
		hookPostCheckout: {marker, refresh},
		hookPostMerge:    {marker, refresh},
		hookPostRewrite:  {marker, refresh},
		hookCommitMsg: {marker, []string{
			"( " + absMsg + cd + guard + bn + ` task hook commit-msg "$m" >/dev/null 2>&1 ) || true`,
			"( " + absMsg + cd + "if command -v " + bn + " >/dev/null 2>&1 && " + bn + " check leaks --help >/dev/null 2>&1; then " + bn + ` check leaks --staged --commit-msg "$m"; fi ) || exit $?`,
		}},
		hookPostCommit: {rideHookMarker, rideHookLines[hookPostCommit]},
		hookPrePush:    {rideHookMarker, rideHookLines[hookPrePush]},
	}
}

// hooksDir is the folder git runs hooks from (core.hooksPath when set).
func (s *liveSession) hooksDir(ctx context.Context) (string, error) {
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}})
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if hp, herr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "core.hooksPath"}}); herr == nil {
		if p := strings.TrimSpace(string(hp)); p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(s.top, p)
			}
			dir = p
		}
	}
	return dir, nil
}

// ensureGitWiring wires the clone for the project at root (see the file
// comment).
func (s *liveSession) ensureGitWiring(ctx context.Context, root string) {
	if os.Getenv(EnvSyncGit) == envOff {
		return
	}
	dir, err := s.hooksDir(ctx)
	if err != nil {
		s.warn("git wiring: " + err.Error())
		return
	}
	cfgPath, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "config"}})
	if err != nil {
		s.warn("git wiring: " + err.Error())
		return
	}
	rel, rerr := filepath.Rel(s.top, root)
	if rerr != nil || strings.HasPrefix(rel, "..") {
		rel = ""
	}
	bin := mergeDriverBinary()
	blocks := hookBlocks(rel)
	paths := []string{strings.TrimSpace(string(cfgPath)), filepath.Join(root, gitAttributesFile), filepath.Join(root, gitignoreName)}
	names := make([]string, 0, len(blocks))
	for n := range blocks {
		names = append(names, n)
		paths = append(paths, filepath.Join(dir, n))
	}
	sort.Strings(names)
	fp := wiringFingerprint(s.h.cfg.Version, bin, rel, paths)
	stamp := filepath.Join(s.stateDir, gitWiringStamp)
	if b, rerr := os.ReadFile(filepath.Clean(stamp)); rerr == nil && strings.TrimSpace(string(b)) == fp {
		return
	}
	// The config, attributes and hook files (not .gitignore, which has its
	// own notice): any byte changed means this command wired the clone.
	wired := make([]string, 0, len(paths)-1)
	wired = append(wired, paths[:2]...)
	wired = append(wired, paths[3:]...)
	before := wiredContent(wired)
	for _, kv := range mergeDriverConfig(bin) {
		cur, gerr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--local", "--get", kv[0]}})
		if gerr == nil && strings.TrimSpace(string(cur)) == kv[1] {
			continue
		}
		if _, serr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--local", kv[0], kv[1]}}); serr != nil {
			s.warn("git wiring: " + serr.Error())
			return
		}
	}
	if err := ensureFileBlock(filepath.Join(root, gitAttributesFile), gitBlockMarker, gitAttributeLines); err != nil {
		s.warn("git wiring: " + err.Error())
		return
	}
	for _, n := range names {
		b := blocks[n]
		if err := ensureHookBlock(dir, n, b.marker, b.lines); err != nil {
			s.warn("git wiring: " + err.Error())
			return
		}
	}
	if wiredContent(wired) != before { // v0.1.17 reportGitWiring, same text and condition
		s.syncNotice("configured git for lore data (merge driver, .gitattributes, hooks in " + dir + ")")
	}
	// Row files git would not commit (OWNERQ-9): re-include them, once
	// per state of .gitignore; `lore sync` refuses while some stay hidden.
	switch changed, still, err := EnsureDataNotIgnored(ctx, root); {
	case err != nil:
		s.warn("git wiring: " + err.Error())
		return
	case changed:
		s.syncNotice("a .gitignore rule hid lore files; re-included " + dataRel + " in .gitignore — commit it")
	case len(still) > 0:
		s.warn("git ignores lore data, so teammates will not receive it (`lore sync` refuses until it is fixed):\n  " + strings.Join(still, "\n  "))
	}
	if err := os.MkdirAll(s.stateDir, liveStateMode); err == nil {
		_ = os.WriteFile(stamp, []byte(wiringFingerprint(s.h.cfg.Version, bin, rel, paths)+"\n"), liveNoteMode) // a lost stamp only means the next command checks again
	}
}

// decimal is the base the fingerprint prints sizes and times in.
const decimal = 10

// wiringFingerprint hashes what is installed, the running lore's version
// and the size and mtime of every wired file: equal fingerprints mean
// nobody touched them since this lore last wired, so no git process is
// spawned. The version is in it (E-FATES-2, v0.1.17 wiringVersion): a
// binary replaced at the same path re-wires the clone once rather than
// keep hooks and driver config an older lore wrote (re-wiring is
// idempotent, so a no-op upgrade costs one check).
func wiringFingerprint(loreVersion, bin, rel string, paths []string) string {
	var b strings.Builder // a Builder never fails a write; the hash is taken once
	b.WriteString(gitWiringVersion + "\n" + loreVersion + "\n" + bin + "\n" + rel + "\n")
	for _, l := range gitAttributeLines {
		b.WriteString(l + "\n")
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for _, p := range sorted {
		info, err := os.Stat(p)
		if err != nil {
			b.WriteString(p + ":missing\n")
			continue
		}
		b.WriteString(p + ":" + strconv.FormatInt(info.Size(), decimal) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), decimal) + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ensureFileBlock makes path hold exactly one block under marker: replaced
// in place, else appended. Content outside the block is never touched.
func ensureFileBlock(path, marker string, lines []string) error {
	// Through a root at the file's folder, so the write stays in it.
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // read-and-write errors are returned below; a close error adds nothing
	name := filepath.Base(path)
	cur, err := root.ReadFile(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	start, end := "# >>> "+marker+" >>>", "# <<< "+marker+" <<<"
	block := start + "\n" + strings.Join(lines, "\n") + "\n" + end + "\n"
	content := string(cur)
	next := content
	if i := strings.Index(content, start); i >= 0 {
		if j := strings.Index(content[i:], end); j >= 0 {
			k := i + j + len(end)
			if k < len(content) && content[k] == '\n' {
				k++
			}
			next = content[:i] + block + content[k:]
		}
	}
	if !strings.Contains(content, start) {
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += block
	}
	if next == content {
		return nil
	}
	return root.WriteFile(name, []byte(next), gitAttributesMode)
}
