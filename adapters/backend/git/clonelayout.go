package git

// clonelayout.go — where a store clone lives on this machine (spec 6.1,
// owner decisions D1 2026-11-12) and the scan `lore doctor`, `store show`
// and `store forget` use to find the clones.
//
//	<home>/stores/<owner>/<repo>/            a normal git clone of the store
//	                                         (.git/ inside, the files checked out)
//	<home>/stores/<owner>/<repo>@<branch>/   a store pinned to another branch
//	<home>/stores/<owner>/<repo>~2/          a second store whose path is the
//	                                         same on another host (D-a)
//	<home>/stores/_local/<repo>/             a git repository on this machine
//	                                         (/srv/git/<repo>.git; B3-21)
//	<clone>/.git/lore/lock                   the one store lock
//	<clone>/.git/lore/state/                 the lock's pending audit journal
//	<clone>/.git/lore/fetch-stamp            when the store was last fetched
//	<home>/state/store-clones.json           clones that are not at their
//	                                         default path (D-b): a person's own
//	                                         clone (`lore store use`) or a
//	                                         "~2" collision
//
// The host never appears in a path. Everything lore keeps about a clone
// lives in its .git/lore/ folder: never in the files a person browses,
// never committed.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/osrename"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Layout names.
const (
	// StoresDir is the folder under the lore home holding every store
	// clone lore makes.
	StoresDir = "stores"
	// gitDirName is a clone's git directory.
	gitDirName = ".git"
	// StateDirName is lore's folder inside a clone's git directory.
	StateDirName = "lore"
	// lockName is the store lock inside StateDirName.
	lockName = "lock"
	// collisionSep starts the suffix of a clone whose default path another
	// store already holds: <repo>~2, <repo>~3, …
	collisionSep = "~"
	// homeStateDir is the machine-local state folder under the lore home.
	homeStateDir = "state"
	// CloneRecordFile is the record of clones not at their default path,
	// under <home>/state.
	CloneRecordFile = "store-clones.json"
	headFileName    = "HEAD"
)

// Why a clone is recorded (CloneEntry.Why).
const (
	// WhyOwn: the person cloned the store themselves and pointed lore at
	// it (`lore store use <path>`). Lore never deletes such a clone and
	// never rewrites its origin.
	WhyOwn = "own"
	// WhyCollision: the default path holds another store's clone (same
	// path on another host), so this one got a "~N" suffix.
	WhyCollision = "collision"
	// WhyMoved: `lore store set-url` kept the store's clone at the folder
	// of its old address, because that clone held commits the store had
	// not got yet (B3-15); its origin names the new address.
	WhyMoved = "moved"
)

// ClonePaths is every path of one store clone, from its folder.
type ClonePaths struct {
	// Dir is the clone: the checked-out files a person browses.
	Dir string
	// GitDir is its git directory (Dir/.git).
	GitDir string
	// StateDir is lore's folder inside GitDir: the lock, the lock's
	// journal, the fetch stamp and the push stamps.
	StateDir string
	// Lock is the store lock.
	Lock string
}

// PathsOf is the layout of the clone at dir. OpenClone, store forget, the
// org link code and the doctor scan all derive the paths here, so the
// layout has one definition.
func PathsOf(dir string) ClonePaths {
	g := filepath.Join(dir, gitDirName)
	s := filepath.Join(g, StateDirName)
	return ClonePaths{Dir: dir, GitDir: g, StateDir: s, Lock: filepath.Join(s, lockName)}
}

// StateDirOf is lore's state folder of the clone at dir (dir/.git/lore).
func StateDirOf(dir string) string { return PathsOf(dir).StateDir }

// LockPathOf is the store lock of the clone at dir: every writer of the
// store (rows, folder edits, org links) takes this one lock.
func LockPathOf(dir string) string { return PathsOf(dir).Lock }

// CloneKey is the key of a store clone in the clone record: the remote's
// host/path, plus "@<branch>" for a pinned branch.
func CloneKey(r Remote, branch string) string {
	if branch == "" {
		return r.Norm
	}
	return r.Norm + branchCloneSep + branch
}

// DefaultCloneDir is where lore puts the clone of r (pinned to branch
// when not ""): <home>/stores/<path>[@<branch>].
func DefaultCloneDir(home string, r Remote, branch string) (string, error) {
	base, err := CloneBase(home, r)
	if err != nil {
		return "", err
	}
	if branch != "" {
		base += branchCloneSep + filepath.FromSlash(branch)
	}
	return base, nil
}

// CloneDirFor is the clone folder of r (pinned to branch when not "") on
// this machine: the recorded path (a person's own clone, or a collision
// suffix found earlier), else the default path when it is free or already
// this store's, else the first free "~N" sibling, which is recorded. own
// reports a person's own clone (WhyOwn). It never touches the network and
// creates nothing but the record entry of a new collision.
func CloneDirFor(home string, r Remote, branch string) (dir string, own bool, err error) {
	key := CloneKey(r, branch)
	if e, ok := ReadCloneRecord(home).Clones[key]; ok && e.Path != "" {
		return e.Path, e.Why == WhyOwn, nil
	}
	def, err := DefaultCloneDir(home, r, branch)
	if err != nil {
		return "", false, err
	}
	for n := 1; ; n++ {
		dir = def
		if n > 1 {
			dir = def + collisionSep + strconv.Itoa(n)
		}
		if other := otherStoreAt(dir, r); other {
			continue
		}
		if n > 1 {
			// A lost record only means the next open finds the same "~N"
			// again (the scan above): it is a note for people, not state.
			_ = RecordClone(home, key, CloneEntry{Path: dir, Why: WhyCollision}, time.Now())
		}
		return dir, false, nil
	}
}

// CloneDirOf is CloneDirFor for a store url in any spelling lore accepts
// (scp-like, ssh://, https://): the clone folder of that store, pinned to
// branch when not "". A url that is not a git store url is an error.
func CloneDirOf(home, raw, branch string) (dir string, own bool, err error) {
	r, err := RemoteOfRaw(raw)
	if err != nil {
		return "", false, err
	}
	return CloneDirFor(home, r, branch)
}

// RemoteOfRaw parses a store url in any spelling lore accepts.
func RemoteOfRaw(raw string) (Remote, error) {
	canon, err := CanonicalURL(raw)
	if err != nil {
		return Remote{}, err
	}
	u, err := url.Parse(canon)
	if err != nil {
		return Remote{}, errs.New(errs.StoreScheme, "git: the store url does not parse")
	}
	return RemoteOf(u)
}

// otherStoreAt reports whether dir holds a clone of a store other than r:
// its origin parses as a store url with another host/path. A folder with
// no clone, a clone without an origin yet (a first open cut short) and an
// origin lore cannot parse (a local path) are not another store's.
func otherStoreAt(dir string, r Remote) bool {
	origin := OriginAt(dir)
	if origin == "" {
		return false
	}
	o, err := RemoteOfRaw(origin)
	if err != nil {
		return false
	}
	// A local-path origin under a hosted store (or the reverse) is a
	// mirror or an insteadOf spelling lore cannot compare: not another
	// store's, as before a local path parsed as a store url at all.
	if o.isLocal() != r.isLocal() {
		return false
	}
	return o.Norm != r.Norm && !SameRepoViaAlias(o, r)
}

// SameRepoViaAlias reports two remotes with the same repository path whose
// hosts are one host under two names: an ssh alias such as
// `git@work-gh:acme/notes.git` for `git@github.com:acme/notes.git`. The
// clone is then the same store's: a clone moved from the old layout, whose
// origin keeps the person's alias spelling, stays at stores/<owner>/<repo>
// when lore later opens the store by its canonical url (never a "~2" copy,
// brief 1.6).
//
// What ssh connects to decides (SetAliasHosts: the person's ~/.ssh/config):
// a host it renames to the other host is the same store, a host it renames
// to a third host is not. Only when the ssh config names neither host, a
// single-label host (no dot) counts as an alias of the other: such a host
// is never a second public git host.
func SameRepoViaAlias(a, b Remote) bool {
	ha, pa, _ := strings.Cut(a.Norm, "/")
	hb, pb, _ := strings.Cut(b.Norm, "/")
	if pa != pb || ha == hb {
		return false
	}
	if f := aliasHosts.Load(); f != nil {
		na, okA := (*f)(ha)
		nb, okB := (*f)(hb)
		switch {
		case okA && strings.EqualFold(na, hb), okB && strings.EqualFold(nb, ha):
			return true
		case okA && okB && strings.EqualFold(na, nb):
			return true // two aliases of one host
		case okA || okB:
			return false // the ssh config sends the alias to another host
		}
	}
	return !strings.Contains(ha, ".") || !strings.Contains(hb, ".")
}

// AliasHostFunc answers the host ssh connects to for host (its HostName);
// ok is false when the ssh config does not rename host.
type AliasHostFunc func(host string) (hostName string, ok bool)

// aliasHosts is the resolver SameRepoViaAlias asks (SetAliasHosts).
var aliasHosts atomic.Pointer[AliasHostFunc]

// SetAliasHosts installs the ssh-config resolver SameRepoViaAlias asks
// before its no-dot fallback (wire installs one reading ~/.ssh/config); nil
// removes it.
func SetAliasHosts(f AliasHostFunc) {
	if f == nil {
		aliasHosts.Store(nil)
		return
	}
	aliasHosts.Store(&f)
}

// OriginAt is the url of the origin remote in the git config of the clone
// at dir, read from the file (no git process); "" when there is none.
func OriginAt(dir string) string {
	f, err := os.Open(filepath.Join(dir, gitDirName, "config"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }() // read only
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = strings.EqualFold(strings.Join(strings.Fields(line), " "), `[remote "`+RemoteName+`"]`)
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "url") {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

// CloneRecord is <home>/state/store-clones.json: every store clone that is
// not at its default path, by CloneKey. Machine-local: it names folders
// of this machine, so it is never synced or shared (D-b).
type CloneRecord struct {
	Clones map[string]CloneEntry `json:"clones"`
}

// CloneEntry is one recorded clone.
type CloneEntry struct {
	// Path is the clone folder (absolute).
	Path string `json:"path"`
	// Why is WhyOwn, WhyCollision or WhyMoved.
	Why string `json:"why"`
	// SetAt is when it was recorded (RFC 3339 UTC).
	SetAt string `json:"set_at"`
}

// CloneRecordPath is <home>/state/store-clones.json.
func CloneRecordPath(home string) string {
	return filepath.Join(home, homeStateDir, CloneRecordFile)
}

// ReadCloneRecord reads the record; a missing or unreadable file is an
// empty record (every clone is then at its default path).
func ReadCloneRecord(home string) CloneRecord {
	r := CloneRecord{Clones: map[string]CloneEntry{}}
	b, err := os.ReadFile(filepath.Clean(CloneRecordPath(home)))
	if err != nil {
		return r
	}
	if json.Unmarshal(b, &r) != nil || r.Clones == nil {
		return CloneRecord{Clones: map[string]CloneEntry{}}
	}
	return r
}

// RecordClone writes key's entry into the record.
func RecordClone(home, key string, e CloneEntry, now time.Time) error {
	r := ReadCloneRecord(home)
	e.SetAt = now.UTC().Format(time.RFC3339)
	r.Clones[key] = e
	return writeCloneRecord(home, r)
}

// ForgetCloneRecord removes key's entry; a key not recorded is no error.
func ForgetCloneRecord(home, key string) error {
	r := ReadCloneRecord(home)
	if _, ok := r.Clones[key]; !ok {
		return nil
	}
	delete(r.Clones, key)
	return writeCloneRecord(home, r)
}

// writeCloneRecord replaces the record atomically (a unique temp file per
// writer: two processes may record at once; the last rename wins, and a
// lost collision entry is found again by CloneDirFor's scan).
func writeCloneRecord(home string, r CloneRecord) error {
	p := CloneRecordPath(home)
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, "git: encode the store clone record", err)
	}
	if err = os.MkdirAll(filepath.Dir(p), cloneDirMode); err != nil {
		return errs.FileIO("create", filepath.Dir(p), err)
	}
	f, err := os.CreateTemp(filepath.Dir(p), CloneRecordFile+".*.tmp")
	if err != nil {
		return errs.FileIO("write", p, err)
	}
	tmp := f.Name()
	_, werr := f.Write(append(b, '\n'))
	if err = errors.Join(werr, f.Close()); err == nil {
		err = osrename.Rename(tmp, p)
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort cleanup of a failed write
		return errs.FileIO("write", p, err)
	}
	return nil
}

// CloneState is what a clone scan found for one clone folder.
type CloneState string

// Clone states. Only CloneWhole is a clone in use; CloneNotCheckedOut is
// finished the next time the store opens; the others need a person.
const (
	// CloneWhole: a git clone with its files checked out.
	CloneWhole CloneState = "whole"
	// CloneNotCheckedOut: a first clone that was cut short before its
	// files were checked out; lore finishes it on the next open.
	CloneNotCheckedOut CloneState = "not_checked_out"
	// CloneNoGitData: the folder's .git is missing its git data (no
	// HEAD), so lore cannot read it.
	CloneNoGitData CloneState = "no_git_data"
	// CloneMissing: a recorded clone (a person's own, `lore store use`)
	// whose folder is gone.
	CloneMissing CloneState = "missing"
)

// CloneFound is one clone a scan found.
type CloneFound struct {
	// Rel is the folder relative to <home>/stores, slash separated
	// (owner/repo, owner/repo@branch, owner/repo~2); for a recorded clone
	// outside it, the record key.
	Rel   string
	Paths ClonePaths
	State CloneState
	// Origin is the clone's origin url ("" when none).
	Origin string
	// Own is true for a person's own clone (`lore store use`).
	Own bool
}

// PersonalFolder is the folder, directly under <home>/stores, of the
// personal store when it is a plain folder (onboard.LocalStoreFolder; a
// wire test keeps the two equal). GitHub and GitLab owner names cannot
// start with "_", so no clone's owner folder is named like it.
const PersonalFolder = "_me"

// ScanClones lists every clone folder under <home>/stores (a folder with a
// .git inside; owner folders are walked into, never PersonalFolder) and
// every recorded clone outside it, in path order. It only reads. A
// missing stores folder is no clones.
func ScanClones(home string) ([]CloneFound, error) {
	root := filepath.Join(home, StoresDir)
	var out []CloneFound
	seen := map[string]bool{}
	if _, err := os.Stat(root); err == nil {
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() || p == root {
				return nil
			}
			if filepath.Dir(p) == root && d.Name() == PersonalFolder {
				return filepath.SkipDir // lore's own personal folder store: never an owner folder
			}
			if _, serr := os.Lstat(filepath.Join(p, gitDirName)); serr != nil {
				if isFile(filepath.Join(p, rowfs.MarkerFile)) {
					return filepath.SkipDir // a folder store: no clone inside
				}
				return nil // an owner folder: look inside
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return rerr
			}
			seen[p] = true
			out = append(out, classify(filepath.ToSlash(rel), p))
			return filepath.SkipDir
		})
		if err != nil {
			return nil, err
		}
	}
	rec := ReadCloneRecord(home)
	keys := make([]string, 0, len(rec.Clones))
	for k := range rec.Clones {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := rec.Clones[k]
		if e.Path == "" || seen[e.Path] {
			markOwn(out, e)
			continue
		}
		f := classify(k, e.Path)
		if !isDir(e.Path) {
			f.State = CloneMissing
		}
		f.Own = e.Why == WhyOwn
		out = append(out, f)
	}
	return out, nil
}

// markOwn flags the scanned clone at e.Path as a person's own one.
func markOwn(found []CloneFound, e CloneEntry) {
	for i := range found {
		if found[i].Paths.Dir == e.Path && e.Why == WhyOwn {
			found[i].Own = true
		}
	}
}

// classify decides one clone folder's state from what is on disk.
func classify(rel, dir string) CloneFound {
	p := PathsOf(dir)
	f := CloneFound{Rel: rel, Paths: p, Origin: OriginAt(dir)}
	head, err := os.ReadFile(filepath.Join(p.GitDir, headFileName))
	switch {
	case err != nil:
		f.State = CloneNoGitData
	case strings.TrimSpace(string(head)) == "ref: "+headsPrefix+pendingBranch:
		f.State = CloneNotCheckedOut
	default:
		f.State = CloneWhole
	}
	return f
}

// StateOf classifies the one clone at dir (rel names it as ScanClones
// would). It only reads.
func StateOf(rel, dir string) CloneFound { return classify(rel, dir) }

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// legacyLocalCloneDir is where a clone of a local store (a git repository
// on this machine) lived before B3-21: <home>/stores/<its whole absolute
// path without .git>[@<branch>].
func legacyLocalCloneDir(home string, r Remote, branch string) string {
	dir := filepath.Join(home, StoresDir, filepath.FromSlash(strings.TrimPrefix(r.Norm, LocalHost+"/")))
	if branch != "" {
		dir += branchCloneSep + filepath.FromSlash(branch)
	}
	return dir
}

// moveLegacyLocalClone moves the clone of the local store r from its
// pre-B3-21 folder (legacyLocalCloneDir) to dir, the folder it has now
// (<home>/stores/_local/<repo>), on the first open after the upgrade. The
// whole clone moves by one rename, so its unpushed commits, lock and
// stamps go with it; it runs under the layout-move lock (MoveLock), so two
// lore processes never move it at once. Nothing happens when dir already
// exists, when the old folder is missing, or when it holds no clone of r
// (another store, or a person's own files there are left alone). Emptied
// parent folders up to <home>/stores are removed.
func moveLegacyLocalClone(ctx context.Context, home string, r Remote, branch, dir string) error {
	old := legacyLocalCloneDir(home, r, branch)
	if old == dir || !legacyCloneOf(old, r) {
		return nil
	}
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	lock, err := MoveLock(ctx, home)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }() // the rename below already landed or failed
	if _, err = os.Stat(dir); err == nil || !legacyCloneOf(old, r) {
		return nil // another process moved it while this one waited
	}
	if err = os.MkdirAll(filepath.Dir(dir), cloneDirMode); err != nil {
		return errs.FileIO("create", filepath.Dir(dir), err)
	}
	if err = osrename.Rename(old, dir); err != nil {
		return errs.FileIO("move the store clone to", dir, err)
	}
	stores := filepath.Join(home, StoresDir)
	for p := filepath.Dir(old); p != stores && strings.HasPrefix(p, stores+string(filepath.Separator)); p = filepath.Dir(p) {
		if os.Remove(p) != nil {
			break // not empty (another store's clone, a person's file): kept
		}
	}
	return nil
}

// legacyCloneOf reports whether dir is a git clone whose origin is the
// local store r.
func legacyCloneOf(dir string, r Remote) bool {
	if _, err := os.Stat(filepath.Join(dir, gitDirName)); err != nil {
		return false
	}
	o, err := RemoteOfRaw(OriginAt(dir))
	return err == nil && o.Norm == r.Norm
}
