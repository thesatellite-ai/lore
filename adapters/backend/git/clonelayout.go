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
//	                                         clone (`lore store use`), a "~2"
//	                                         collision or a set-url move; a
//	                                         path under the home is kept
//	                                         relative to it (devco D-534)
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
	"github.com/thesatellite-ai/lore/logic/repourl"
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
	if e, ok := recordedClone(ReadCloneRecord(home), r, branch); ok {
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

// recordedClone is rec's entry for r (pinned to branch when not ""): the
// one under CloneKey, else one recorded under the key of an ssh alias of
// r's host (a record written before Norm resolved aliases, D-493: the
// same repository path on a host SameRepoViaAlias calls the same).
func recordedClone(rec CloneRecord, r Remote, branch string) (CloneEntry, bool) {
	if e, ok := rec.Clones[CloneKey(r, branch)]; ok && e.Path != "" {
		return e, true
	}
	if r.isLocal() {
		return CloneEntry{}, false
	}
	_, path, _ := strings.Cut(r.Norm, "/")
	keys := make([]string, 0, len(rec.Clones))
	for k := range rec.Clones {
		keys = append(keys, k)
	}
	sort.Strings(keys) // one answer when several old keys match
	for _, k := range keys {
		e := rec.Clones[k]
		norm, br := k, ""
		if i := strings.Index(k, branchCloneSep); i >= 0 {
			norm, br = k[:i], k[i+len(branchCloneSep):]
		}
		h, p, _ := strings.Cut(norm, "/")
		if e.Path == "" || br != branch || p != path || strings.HasPrefix(norm, LocalHost+"/") {
			continue
		}
		if identityHost(h) != h && SameRepoViaAlias(Remote{Norm: norm}, r) {
			return e, true
		}
	}
	return CloneEntry{}, false
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
	if pa != pb {
		return false
	}
	// a Norm recorded before D-630 may still name a provider's port-443
	// route host (ssh.github.com): it is the provider's
	ha, hb = repourl.ProviderSSHHost(ha), repourl.ProviderSSHHost(hb)
	if ha == hb {
		return true // one host: Norm already resolved both aliases (identityHost)
	}
	// renamedHost, the rule identityHost uses: a host the ssh config
	// sends only to its own port-443 route (github.com → ssh.github.com) is
	// not renamed (devco D-628: that line made every unnamed alias, such as
	// one git reaches by url.<base>.insteadOf, "another host").
	na, okA := renamedHost(ha)
	nb, okB := renamedHost(hb)
	switch {
	case okA && na == hb, okB && nb == ha:
		return true
	case okA && okB && na == nb:
		return true // two aliases of one host
	case okA || okB:
		return false // the ssh config sends the alias to another host
	}
	return !strings.Contains(ha, ".") || !strings.Contains(hb, ".")
}

// identityHost is the host of a store's identity (Remote.Norm) for an ssh
// url whose host is host: the HostName the person's ssh config renames it
// to (SetAliasHosts), so `git@work-gh:acme/notes.git` (Host work-gh /
// HostName github.com) is the same store as `git@github.com:acme/notes.git`
// and `https://github.com/acme/notes` for every comparison lore makes
// (org of a clone, clone folder, setup and link matching; D-493). Without
// a resolver, or when the config does not rename host, host is kept. A
// HostName that is a subdomain of host (github.com → ssh.github.com, the
// port-443 route) is a route to the same host, not an alias, and is not
// taken: the https url of that store names host.
func identityHost(host string) string {
	if n, ok := renamedHost(host); ok {
		return n
	}
	// devco D-630: a url typed on a provider's port-443 route host
	// (git@ssh.github.com:o/r.git) is the provider's repository
	return repourl.ProviderSSHHost(host)
}

// renamedHost is the other host the person's ssh config sends host to
// (SetAliasHosts), lower case; ok is false without a resolver, when the
// config does not rename host, renames it to itself or to a name that is
// no host, or only to a subdomain of it (github.com → ssh.github.com, the
// port-443 route: the same host, not an alias); a known provider's route
// host is the provider's (repourl.ProviderSSHHost). identityHost and
// SameRepoViaAlias share it, so a store's identity and the same-store test
// of a clone's origin read one ssh config line the same way (devco D-628).
func renamedHost(host string) (string, bool) {
	f := aliasHosts.Load()
	if f == nil {
		return "", false
	}
	n, ok := (*f)(host)
	// a provider's port-443 route is the provider's host (devco D-629:
	// Host work-gh / HostName ssh.github.com was "another host")
	n = repourl.ProviderSSHHost(n)
	if !ok || n == "" || n == host || strings.HasSuffix(n, "."+host) || !hostPattern.MatchString(n) {
		return "", false
	}
	return n, true
}

// IdentityHost is identityHost for callers outside the backend: the host
// a code repo's ssh remote is known by once the person's ssh alias is
// resolved (SetAliasHosts). A code repo's remote and a store url must name
// one host the same way, or an org's `repos: github.com/<owner>/*` never
// matches a repo cloned as git@<alias>:<owner>/<repo>.git (devco D-531).
func IdentityHost(host string) string { return identityHost(strings.ToLower(strings.TrimSpace(host))) }

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
//
// DECISION (devco D-534): a path under the home is written relative to it
// and a clone at its default path is never recorded, so a lore home that
// is copied or moved (a preview home made from a copy of ~/.lore) reaches
// its own clones and never the old home's. Before, set-url wrote
// `why: moved` with the absolute default path, and every read of the
// copied home fetched in the original home's clone.
type CloneRecord struct {
	Clones map[string]CloneEntry `json:"clones"`
	// Foreign holds the entries ReadCloneRecord left out: an absolute path
	// that is another lore home's default place for the same store (a
	// record copied with its home). Never written back; New warns once.
	Foreign []ForeignClone `json:"-"`
}

// ForeignClone is a record entry naming another lore home's clone.
type ForeignClone struct {
	Key  string
	Path string
}

// CloneEntry is one recorded clone.
type CloneEntry struct {
	// Path is the clone folder: absolute in memory; in the file, relative
	// to the home when it is under it.
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
//
// A relative path is joined to home. An absolute path outside home that is
// another home's default place for its key (…/stores/<owner>/<repo>, the
// layout DefaultCloneDir gives under any home) is left out and listed in
// Foreign: it is the record of the home this one was copied from (D-534).
// A person's own clone (WhyOwn, `lore store use`) is kept wherever it is.
func ReadCloneRecord(home string) CloneRecord {
	r := CloneRecord{Clones: map[string]CloneEntry{}}
	b, err := os.ReadFile(filepath.Clean(CloneRecordPath(home)))
	if err != nil {
		return r
	}
	if json.Unmarshal(b, &r) != nil || r.Clones == nil {
		return CloneRecord{Clones: map[string]CloneEntry{}}
	}
	for k, e := range r.Clones {
		if e.Path == "" {
			continue
		}
		if !filepath.IsAbs(filepath.FromSlash(e.Path)) {
			e.Path = filepath.Join(home, filepath.FromSlash(e.Path))
			r.Clones[k] = e
			continue
		}
		if e.Why != WhyOwn && foreignDefault(home, k, e.Path) {
			r.Foreign = append(r.Foreign, ForeignClone{Key: k, Path: e.Path})
			delete(r.Clones, k)
		}
	}
	sort.Slice(r.Foreign, func(i, j int) bool { return r.Foreign[i].Key < r.Foreign[j].Key })
	return r
}

// DefaultCloneDirOfKey is the default clone folder under home of the
// store a record key (CloneKey) names; ok is false for a key it cannot
// read.
func DefaultCloneDirOfKey(home, key string) (string, bool) {
	norm, branch := key, ""
	if i := strings.Index(key, branchCloneSep); i >= 0 {
		norm, branch = key[:i], key[i+len(branchCloneSep):]
	}
	if !strings.Contains(norm, "/") {
		return "", false
	}
	dir, err := DefaultCloneDir(home, Remote{Norm: norm}, branch)
	if err != nil {
		return "", false
	}
	return dir, true
}

// foreignDefault reports whether p, an absolute path outside home, is the
// default place of key's clone under some other lore home: it ends with
// the same stores/<…> tail DefaultCloneDirOfKey gives under home.
func foreignDefault(home, key, p string) bool {
	p = filepath.Clean(p)
	if _, inside := relUnder(home, p); inside {
		return false
	}
	def, ok := DefaultCloneDirOfKey(home, key)
	if !ok {
		return false
	}
	tail, inside := relUnder(home, def)
	if !inside {
		return false
	}
	return strings.HasSuffix(p, string(filepath.Separator)+tail)
}

// relUnder is p relative to dir, and whether p is inside dir (not dir).
func relUnder(dir, p string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// RecordClone writes key's entry into the record. A clone that is not a
// person's own and sits at the default path of key is not recorded (its
// entry is removed): CloneDirFor finds it there with no record (D-534).
func RecordClone(home, key string, e CloneEntry, now time.Time) error {
	r := ReadCloneRecord(home)
	if def, ok := DefaultCloneDirOfKey(home, key); ok && e.Why != WhyOwn && filepath.Clean(e.Path) == def {
		if _, had := r.Clones[key]; !had {
			return nil
		}
		delete(r.Clones, key)
		return writeCloneRecord(home, r)
	}
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
	out := CloneRecord{Clones: make(map[string]CloneEntry, len(r.Clones))}
	for k, e := range r.Clones {
		if rel, inside := relUnder(home, e.Path); inside {
			e.Path = filepath.ToSlash(rel) // relative: the record moves with its home (D-534)
		}
		out.Clones[k] = e
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, "git: encode the store clone record", err)
	}
	if err = os.MkdirAll(filepath.Dir(p), cloneDirMode); err != nil {
		return errs.FileIO("create the state folder", filepath.Dir(p), err)
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
		return errs.FileIO("create the store clones folder", filepath.Dir(dir), err)
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

// DropForeignClones removes from the record every entry ReadCloneRecord
// leaves out as another home's clone (CloneRecord.Foreign) and returns
// them, so the caller says so once (D-534). Nothing is written when there
// is none.
func DropForeignClones(home string) ([]ForeignClone, error) {
	r := ReadCloneRecord(home)
	if len(r.Foreign) == 0 {
		return nil, nil
	}
	return r.Foreign, writeCloneRecord(home, r)
}
