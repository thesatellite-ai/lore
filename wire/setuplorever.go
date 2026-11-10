package wire

// setuplorever.go — the release lore on PATH versus this binary, for the
// tools.skill_* rows of the setup prober. A skill teaches agents the
// commands of the lore that installed it; agents run the `lore` they find
// on PATH. When that lore is an older release than the binary serving the
// setup page (owner case: lore 0.1.17 installed, the page is 0.1.18-dev),
// installing this binary's skill now hands agents commands the installed
// lore lacks, so the row says "upgrade lore first" at info level instead
// of urging an install.
//
//	exec.LookPath("lore") ─▶ same file as os.Executable? ─▶ yes: nothing to compare
//	                      └▶ `<it> version` (first line "lore X") ─▶ pathLoreOlder(X, ours)

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/ops/skill"
)

// loreProgram is the name agents run.
const loreProgram = "lore"

// pathLoreTimeout bounds `lore version` of the PATH lore (a release binary
// answers in milliseconds; a hung one must not hold the check).
const pathLoreTimeout = 5 * time.Second

// pathLore is the first lore on PATH that is not this binary.
type pathLore struct {
	Path    string
	Version string
}

// findPathLore is the PATH lore and its version; false when there is none,
// it is this binary, or its version could not be read. It writes nothing:
// `lore version` runs in the temp folder, never in a repo.
func findPathLore(ctx context.Context) (pathLore, bool) {
	p, err := exec.LookPath(loreProgram)
	if err != nil {
		return pathLore{}, false
	}
	if self, err := os.Executable(); err == nil && sameFile(self, p) {
		return pathLore{}, false
	}
	v, ok := binaryLoreVersion(ctx, p)
	if !ok {
		return pathLore{}, false
	}
	return pathLore{Path: p, Version: v}, true
}

// binaryLoreVersion is X of `<p> version` ("lore X"), run in the temp
// folder (never a repo) within pathLoreTimeout; false when it fails.
func binaryLoreVersion(ctx context.Context, p string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, pathLoreTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p, "version") // p is a lore on PATH; the argv is fixed
	cmd.Dir = versionRunDir()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return parseLoreVersionOutput(string(out))
}

// sameFile reports whether a and b are the same file after symlinks (a
// Homebrew lore is a symlink into the Cellar).
func sameFile(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	if ra == rb {
		return true
	}
	fa, errA := os.Stat(ra)
	fb, errB := os.Stat(rb)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// parseLoreVersionOutput is X of the first line "lore X" that `lore
// version` prints (every release since 0.1.x prints it that way).
func parseLoreVersionOutput(out string) (string, bool) {
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	name, v, ok := strings.Cut(strings.TrimSpace(first), " ")
	if !ok || name != loreProgram {
		return "", false
	}
	v = strings.TrimSpace(v)
	return v, v != ""
}

// versionShape is how a version string relates to its X.Y.Z core.
type versionShape int

const (
	// shapeRelease is exactly X.Y.Z.
	shapeRelease versionShape = iota
	// shapePre is before X.Y.Z: "0.1.18-dev", "0.1.18-rc1", a goreleaser
	// snapshot (its core is the next version).
	shapePre
	// shapePost is after the X.Y.Z tag: `git describe` past it
	// ("0.1.17-34-gabc1234") or a dirty tree at it ("0.1.18-dirty").
	shapePost
)

// coreRe is the leading X.Y.Z; describeRe a `git describe` suffix past a tag.
var (
	coreRe     = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)
	describeRe = regexp.MustCompile(`^-\d+-g[0-9a-f]{4,40}`)
)

// parseLoreVersion is a version's core and shape; false for anything with
// no X.Y.Z core ("dev", a bare commit hash, "").
func parseLoreVersion(v string) ([3]int, versionShape, bool) {
	var core [3]int
	v = strings.TrimSpace(v)
	m := coreRe.FindStringSubmatch(v)
	if m == nil {
		return core, shapeRelease, false
	}
	for i := range core {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return core, shapeRelease, false
		}
		core[i] = n
	}
	rest := v[len(m[0]):]
	switch {
	case rest == "":
		return core, shapeRelease, true
	case describeRe.MatchString(rest) || strings.HasPrefix(rest, "-dirty"):
		return core, shapePost, true
	case strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "+"):
		return core, shapePre, true
	}
	return core, shapeRelease, false // "0.1.18x": not a version
}

// pathLoreOlder reports whether the installed lore (a release; anything
// else is not compared) is older than this binary: its core is below
// ours, or equal while ours is past that tag. A pre-release of the same
// core ("0.1.18-dev" against an installed 0.1.18) is not newer. An
// unreadable version on either side is false: no claim without evidence.
func pathLoreOlder(installed, binary string) bool {
	ic, ishape, ok := parseLoreVersion(installed)
	if !ok || ishape != shapeRelease {
		return false
	}
	bc, bshape, ok := parseLoreVersion(binary)
	if !ok {
		return false
	}
	for i := range ic {
		switch {
		case ic[i] < bc[i]:
			return true
		case ic[i] > bc[i]:
			return false
		}
	}
	return bshape == shapePost
}

// OlderPathLore is skill.PathLoreFinder: the release lore on PATH when it
// is older than binary, for the tools.skill_* rows of both the doctor and
// the setup page (one verdict: ops/skill CheckRow).
func (a *App) OlderPathLore(ctx context.Context, binary string) (skill.PathLore, bool) {
	pl, ok := findPathLore(ctx)
	if !ok || !pathLoreOlder(pl.Version, binary) {
		return skill.PathLore{}, false
	}
	return skill.PathLore{Path: pl.Path, Version: pl.Version}, true
}

var _ skill.PathLoreFinder = (*App)(nil)

// versionRunDir is the folder `lore version` of the PATH lore runs in: the
// temp folder (outside any repo), or the root folder when TMPDIR names a
// folder that is gone; a missing folder made the run fail and the older
// PATH lore went unreported.
func versionRunDir() string {
	if info, err := os.Stat(os.TempDir()); err == nil && info.IsDir() {
		return os.TempDir()
	}
	return string(filepath.Separator)
}
