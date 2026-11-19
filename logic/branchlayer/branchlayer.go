// Package branchlayer holds the pure rules of branch layers
// (BRANCH_LAYERS_SPEC): which code branch is isolated or shared (section
// 5), the hidden ref a layer lives on (6.1) and the trailers a layer
// commit carries (6.2). It does no I/O: adapters/backend/git moves the
// refs, wire decides when a write goes to a layer.
//
//	Classify(branch, Settings) ─► Mode (isolated | shared) + the step that decided
//	Key{StoreBranch, Folder, Repo, Branch} ─► Ref() refs/lore/layer/<sb>/<folder>/<repo key>/<branch key>
//	                                      ─► TrashRef(stamp) refs/lore/layer-trash/…@<stamp>
//	Facts ─► Trailers() / ParseFacts(commit message)
//
// Layer L1 (logic): imports the standard library only.
package branchlayer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Ref namespaces (BRANCH_LAYERS 6.1). They sit under refs/lore/ so they
// are hidden refs, and beside (never inside) refs/lore/live/<store branch>
// so no store branch name can clash with them.
const (
	// RefPrefix holds every open layer of a store repo.
	RefPrefix = "refs/lore/layer/"
	// TrashPrefix holds every dropped or landed layer.
	TrashPrefix = "refs/lore/layer-trash/"
	// trashStampSep separates a trash ref's branch key from its stamp.
	trashStampSep = "@"
	// StampLayout is the UTC stamp of a trash ref (20261120T091200Z).
	StampLayout = "20060102T150405Z"
	// repoKeyLen is how many hex characters of the remote's SHA-256 name a
	// repo in a ref: 48 bits, plenty for the repos linked to one folder.
	repoKeyLen = 12
)

// Mode is whether a code branch's writes go to its layer.
type Mode string

// Modes.
const (
	Isolated Mode = "isolated"
	Shared   Mode = "shared"
)

// The values of the policy key branches.default_mode.
const (
	PolicyIsolate = "isolate"
	PolicyShare   = "share"
)

// ModeOfPolicy maps a branches.default_mode value to a Mode (anything but
// "share" isolates: the stricter reading).
func ModeOfPolicy(v string) Mode {
	if v == PolicyShare {
		return Shared
	}
	return Isolated
}

// Step names the rule of section 5 that decided a Mode.
type Step int

// Steps, in the order Classify asks them.
const (
	StepOff         Step = iota // branches.layers is false: every branch is shared
	StepDefault                 // the code repo's default branch: always shared
	StepExact                   // branches.isolated / branches.shared name this branch
	StepIsolateGlob             // a branches.isolate pattern matched
	StepMainLike                // a branches.main_like pattern matched
	StepDefaultMode             // branches.default_mode
)

// Settings are the branches.* policy values Classify reads (BRANCH_LAYERS
// 14); the zero value is not the default: use DefaultSettings.
type Settings struct {
	Layers      bool
	DefaultMode Mode
	Isolate     []string // globs
	MainLike    []string // globs
	IsolatedSet []string // exact names (lore branch isolate)
	SharedSet   []string // exact names (lore branch share)
}

// DefaultMainLike are the main_like patterns of a folder that sets none.
func DefaultMainLike() []string { return []string{"dev", "staging", "release/*"} }

// DefaultSettings are the built-in branches.* values (BRANCH_LAYERS 14,
// owner decisions of 2026-11-14).
func DefaultSettings() Settings {
	return Settings{Layers: true, DefaultMode: Isolated, MainLike: DefaultMainLike()}
}

// Decision is Classify's answer.
type Decision struct {
	Mode Mode
	Step Step
	// Rule is the policy value that decided ("release/*", "dev-pat"), "" for
	// the default branch, default_mode and a folder with layers off.
	Rule string
}

// Why is the human line `lore branch mode` prints after "decided by".
func (d Decision) Why(defaultBranch string) string {
	switch d.Step {
	case StepOff:
		return "branches.layers is false for this project folder: every branch writes to the shared notes"
	case StepDefault:
		return "it is the code repo's default branch (" + defaultBranch + "), which always writes to the shared notes"
	case StepExact:
		if d.Mode == Isolated {
			return "step 1: lore branch isolate chose it (policy branches.isolated)"
		}
		return "step 1: lore branch share chose it (policy branches.shared)"
	case StepIsolateGlob:
		return "step 2: it matches the branches.isolate pattern " + d.Rule
	case StepMainLike:
		return "step 3: it matches the branches.main_like pattern " + d.Rule
	case StepDefaultMode:
		return "step 4: branches.default_mode is " + d.Rule + " (no exact choice, no isolate or main_like pattern matched)"
	}
	return ""
}

// Classify decides whether branch is isolated or shared (BRANCH_LAYERS 5).
// The default branch is shared whatever the settings say: isolating it
// would leave the team no shared notes at all.
func Classify(branch, defaultBranch string, s Settings) Decision {
	if !s.Layers {
		return Decision{Mode: Shared, Step: StepOff}
	}
	if branch == defaultBranch {
		return Decision{Mode: Shared, Step: StepDefault}
	}
	if slices.Contains(s.IsolatedSet, branch) {
		return Decision{Mode: Isolated, Step: StepExact, Rule: branch}
	}
	if slices.Contains(s.SharedSet, branch) {
		return Decision{Mode: Shared, Step: StepExact, Rule: branch}
	}
	for _, g := range s.Isolate {
		if Match(g, branch) {
			return Decision{Mode: Isolated, Step: StepIsolateGlob, Rule: g}
		}
	}
	for _, g := range s.MainLike {
		if Match(g, branch) {
			return Decision{Mode: Shared, Step: StepMainLike, Rule: g}
		}
	}
	if s.DefaultMode == Shared {
		return Decision{Mode: Shared, Step: StepDefaultMode, Rule: PolicyShare}
	}
	return Decision{Mode: Isolated, Step: StepDefaultMode, Rule: PolicyIsolate} // an unknown value is the stricter mode
}

// Match reports whether a branch name matches a pattern: `*` and `?` and
// classes within one /-separated segment (path.Match), `**` across any
// number of segments. A malformed pattern matches nothing.
func Match(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		ok, err := path.Match(pattern, name)
		return err == nil && ok
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// matchSegments matches split pattern and name, `**` taking zero or more
// segments.
func matchSegments(p, n []string) bool {
	if len(p) == 0 {
		return len(n) == 0
	}
	if p[0] == "**" {
		for i := 0; i <= len(n); i++ {
			if matchSegments(p[1:], n[i:]) {
				return true
			}
		}
		return false
	}
	if len(n) == 0 {
		return false
	}
	ok, err := path.Match(p[0], n[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(p[1:], n[1:])
}

// RepoKey is the ref segment naming a code repo: the first 12 hex
// characters of the SHA-256 of its normalized remote ("github.com/acme/app").
func RepoKey(normalizedRemote string) string {
	sum := sha256.Sum256([]byte(normalizedRemote))
	return hex.EncodeToString(sum[:])[:repoKeyLen]
}

// BranchKey is a code branch as one flat ref segment: `%` becomes `%25`
// and `/` becomes `%2F`, so `feat` and `feat/x` never clash as a ref and
// a folder of refs (git's directory-or-file rule).
func BranchKey(branch string) string {
	return strings.ReplaceAll(strings.ReplaceAll(branch, "%", "%25"), "/", "%2F")
}

// BranchFromKey undoes BranchKey.
func BranchFromKey(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "%2F", "/"), "%25", "%")
}

// Key names one layer: a code branch of one code repo in one project
// folder of one store branch.
type Key struct {
	StoreBranch string
	Folder      string
	// RepoKey is RepoKey(remote).
	RepoKey string
	Branch  string
}

// errBadKey is a Key missing a part or holding a `/` where one may not be.
var errBadKey = errors.New("branchlayer: a layer key needs a store branch, a folder and a repo key without '/', and a branch")

// RootFolderSeg is the ref segment of a store's root folder (".", which a
// ref may not hold as a segment).
const RootFolderSeg = "_root"

// folderSeg is the ref segment of folder.
func folderSeg(folder string) string {
	if folder == "." {
		return RootFolderSeg
	}
	return folder
}

// folderOfSeg undoes folderSeg.
func folderOfSeg(seg string) string {
	if seg == RootFolderSeg {
		return "."
	}
	return seg
}

// Check validates k's parts for a ref.
func (k Key) Check() error {
	for _, part := range []string{k.StoreBranch, folderSeg(k.Folder), k.RepoKey} {
		if part == "" || strings.Contains(part, "/") || strings.HasPrefix(part, ".") {
			return errBadKey
		}
	}
	if k.Branch == "" {
		return errBadKey
	}
	return nil
}

// dir is the ref path below a namespace without the branch key.
func (k Key) dir() string { return k.StoreBranch + "/" + folderSeg(k.Folder) + "/" + k.RepoKey + "/" }

// Ref is the layer's open ref.
func (k Key) Ref() string { return RefPrefix + k.dir() + BranchKey(k.Branch) }

// TrashRef is the layer's trash ref for the moment at.
func (k Key) TrashRef(at time.Time) string {
	return k.TrashPrefixOf() + trashStampSep + at.UTC().Format(StampLayout)
}

// trashSeqSep separates a trash stamp from its sequence number when two
// trash moves of one layer share a second (…@20261120T091200Z-2).
const trashSeqSep = "-"

// TrashRefN is TrashRef with sequence number n (n < 2 is TrashRef itself).
func (k Key) TrashRefN(at time.Time, n int) string {
	if n < 2 {
		return k.TrashRef(at)
	}
	return k.TrashRef(at) + trashSeqSep + strconv.Itoa(n)
}

// TrashPrefixOf is the common start of every trash ref of this layer.
func (k Key) TrashPrefixOf() string { return TrashPrefix + k.dir() + BranchKey(k.Branch) }

// FolderPrefix is the ref prefix of every open layer of one folder of one
// store branch (all repos).
func FolderPrefix(storeBranch, folder string) string {
	return RefPrefix + storeBranch + "/" + folderSeg(folder) + "/"
}

// FolderSeg is the ref segment of a folder (the root "." is "_root").
func FolderSeg(folder string) string { return folderSeg(folder) }

// ParseRef reads an open layer ref (or its tracking copy, given the same
// prefix as namespace) back into a Key.
func ParseRef(namespace, ref string) (Key, bool) {
	rest, ok := strings.CutPrefix(ref, namespace)
	if !ok {
		return Key{}, false
	}
	parts := strings.Split(rest, "/")
	const want = 4
	if len(parts) != want || parts[3] == "" {
		return Key{}, false
	}
	k := Key{StoreBranch: parts[0], Folder: folderOfSeg(parts[1]), RepoKey: parts[2], Branch: BranchFromKey(parts[3])}
	return k, k.Check() == nil
}

// ParseTrashRef reads a trash ref back into its Key and stamp.
func ParseTrashRef(namespace, ref string) (Key, time.Time, bool) {
	rest, ok := strings.CutPrefix(ref, namespace)
	if !ok {
		return Key{}, time.Time{}, false
	}
	i := strings.LastIndex(rest, trashStampSep)
	if i < 0 {
		return Key{}, time.Time{}, false
	}
	stamp := rest[i+1:]
	if base, n, found := strings.Cut(stamp, trashSeqSep); found {
		if _, err := strconv.Atoi(n); err != nil {
			return Key{}, time.Time{}, false
		}
		stamp = base
	}
	at, err := time.Parse(StampLayout, stamp)
	if err != nil {
		return Key{}, time.Time{}, false
	}
	k, ok := ParseRef("", rest[:i])
	return k, at, ok
}

// State of a layer in the trash (Lore-Layer-State).
type State string

// States.
const (
	StateOpen    State = "open"
	StateLanded  State = "landed"
	StateDropped State = "dropped"
)

// Trailer keys of a layer commit (BRANCH_LAYERS 6.2; file format: never
// renamed).
const (
	TrailerLayer      = "Lore-Layer"
	TrailerRepo       = "Lore-Layer-Repo"
	TrailerFolder     = "Lore-Layer-Folder"
	TrailerBranch     = "Lore-Layer-Branch"
	TrailerBaseBranch = "Lore-Layer-Base-Branch"
	TrailerSplit      = "Lore-Layer-Split"
	TrailerCodeHead   = "Lore-Layer-Code-Head"
	TrailerStarted    = "Lore-Layer-Started"
	TrailerBy         = "Lore-Layer-By"
	TrailerState      = "Lore-Layer-State"
	TrailerLanded     = "Lore-Layer-Landed"
	TrailerParent     = "Lore-Layer-Parent"
	// TrailerRestored marks the commit `lore branch restore` adds on top of
	// a layer brought back from the trash (devco V-8): lore sync never moves
	// such a layer to the trash on its own; the next write drops the mark
	// and the orphan clock starts from that write.
	TrailerRestored = "Lore-Layer-Restored"
	// layerVersion is TrailerLayer's value.
	layerVersion = "1"
)

// Facts are what a layer commit records about its layer.
type Facts struct {
	Repo       string // normalized code repo remote
	Folder     string
	Branch     string
	BaseBranch string // the code repo's default branch when the layer began
	Split      string // code commit where the branch left the default branch
	CodeHead   string // code HEAD at the write
	Started    time.Time
	By         string // actor id of the first writer
	State      State  // "" on an open layer's commits
	Landed     string // store commit of the landing (trash after a land)
	// Parent is the code branch whose layer this one is stacked on (it
	// started from that layer's tip); "" for a layer on the shared notes.
	Parent string
	// Restored is when a person brought the layer back from the trash, set
	// only on that restore commit (TrailerRestored); zero otherwise.
	Restored time.Time
}

// Trailers renders f as git trailer lines (one "Key: value" per line).
func (f Facts) Trailers() string {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, oneLine(v))
		}
	}
	line(TrailerLayer, layerVersion)
	line(TrailerRepo, f.Repo)
	line(TrailerFolder, f.Folder)
	line(TrailerBranch, f.Branch)
	line(TrailerBaseBranch, f.BaseBranch)
	line(TrailerSplit, f.Split)
	line(TrailerCodeHead, f.CodeHead)
	if !f.Started.IsZero() {
		line(TrailerStarted, f.Started.UTC().Format(time.RFC3339))
	}
	line(TrailerBy, f.By)
	line(TrailerState, string(f.State))
	line(TrailerLanded, f.Landed)
	line(TrailerParent, f.Parent)
	if !f.Restored.IsZero() {
		line(TrailerRestored, f.Restored.UTC().Format(time.RFC3339))
	}
	return b.String()
}

// oneLine keeps a trailer value on one line.
func oneLine(v string) string { return strings.Join(strings.Fields(v), " ") }

// ParseFacts reads the trailers of a layer commit message; ok is false when
// the message is no layer commit (no Lore-Layer trailer).
func ParseFacts(message string) (Facts, bool) {
	var f Facts
	seen := false
	for line := range strings.SplitSeq(message, "\n") {
		k, v, found := strings.Cut(line, ": ")
		if !found {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case TrailerLayer:
			seen = true
		case TrailerRepo:
			f.Repo = v
		case TrailerFolder:
			f.Folder = v
		case TrailerBranch:
			f.Branch = v
		case TrailerBaseBranch:
			f.BaseBranch = v
		case TrailerSplit:
			f.Split = v
		case TrailerCodeHead:
			f.CodeHead = v
		case TrailerStarted:
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				f.Started = t
			}
		case TrailerBy:
			f.By = v
		case TrailerState:
			f.State = State(v)
		case TrailerLanded:
			f.Landed = v
		case TrailerParent:
			f.Parent = v
		case TrailerRestored:
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				f.Restored = t
			}
		}
	}
	return f, seen
}
