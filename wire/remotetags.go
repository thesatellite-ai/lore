package wire

// remotetags.go — a commit the remote holds only through a pushed tag is
// history too (issue #51, devco:D-787). The commit and push secret checks
// counted as "on the remote" only what `--remotes` (refs/remotes/*)
// reaches; a release branch whose tags were pushed while the remote's
// branch got the change as one squash commit sits under no
// remote-tracking ref, so a value committed (and passed) long ago blocked
// every later commit and push although the remote has it behind the tag.
//
//	local tags (for-each-ref refs/tags/, in-process)
//	   ─▶ none: nothing to do (no git process, no network)
//	   ─▶ the remote's tags from <git-common-dir>/lore/remote-tags.json
//	      (refreshed by one `git ls-remote --tags <remote>` only when a
//	      local tag is missing from it and the entry is older than
//	      remoteTagsTTL, never per commit; offline: the cached list)
//	   ─▶ the commits of the local tags the remote holds at the same object
//	      ─▶ negated next to --remotes in historyBases, the push range and
//	         the push bases
//
// A local tag that was never pushed does not count: only the remote's own
// listing makes a tag history. Fails closed: any error counts no tag.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// remoteTagsTTL is how long a cached tag listing of a remote answers a
// local tag it does not name before `git ls-remote` asks the remote again
// (a tag pushed from this clone shows up after at most this long; a tag
// that only exists locally costs one ls-remote per TTL, not per commit).
const remoteTagsTTL = 10 * time.Minute

// remoteTagsTimeout bounds the one ls-remote call (an unreachable remote
// must not hang a commit; the cached listing answers instead).
const remoteTagsTimeout = 10 * time.Second

// maxTagRemotes caps the remotes asked about their tags in one check.
const maxTagRemotes = 4

// remoteTagsFile is the cache file under <git-common-dir>/lore/.
const remoteTagsFile = "remote-tags.json"

// tagsRefPrefix is where local and remote tags live.
const tagsRefPrefix = "refs/tags/"

// peeledSuffix marks ls-remote's line for the commit an annotated tag
// points at.
const peeledSuffix = "^{}"

// remoteTagCache is the cache file: one entry per remote name.
type remoteTagCache struct {
	Remotes map[string]remoteTagEntry `json:"remotes"`
}

// remoteTagEntry is one remote's tag listing: tag name → the tag's object
// (as ls-remote prints it), Peeled → the commit an annotated tag points
// at, Checked → when the listing was last asked for (unix seconds; also
// set when the ask failed, so an offline remote is not asked per commit),
// Pushed → tags this clone's pre-push hook saw going out since (the next
// check asks again at once, not after remoteTagsTTL).
type remoteTagEntry struct {
	Tags    map[string]string `json:"tags"`
	Peeled  map[string]string `json:"peeled,omitempty"`
	Checked int64             `json:"checked"`
	Pushed  map[string]bool   `json:"pushed,omitempty"`
}

// RemoteTagCommits returns the commits of the local tags that remotes
// (remote names) hold at the same object: history the remote has though
// no remote-tracking ref reaches it. Nil when there is no local tag (the
// common case: one in-process ref listing), no remote, or on any error.
func RemoteTagCommits(ctx context.Context, top string, remotes []string) []string {
	if top == "" || len(remotes) == 0 {
		return nil
	}
	local := localTags(ctx, top)
	if len(local) == 0 {
		return nil
	}
	common, err := gitRun{dir: top, args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}}.output(ctx)
	if err != nil || strings.TrimSpace(string(common)) == "" {
		return nil
	}
	file := filepath.Join(strings.TrimSpace(string(common)), "lore", remoteTagsFile)
	cache := readRemoteTagCache(file)
	changed := false
	seen := map[string]bool{}
	var out []string
	for i, remote := range remotes {
		if i >= maxTagRemotes {
			break
		}
		e, have := cache.Remotes[remote]
		if needsTagRefresh(e, have, local, time.Now()) {
			e = refreshRemoteTags(ctx, top, remote, e)
			cache.Remotes[remote] = e
			changed = true
		}
		for name, oid := range local {
			if e.Tags[name] != oid {
				continue // not pushed, or the remote's tag of that name is another object
			}
			c := oid
			if p := e.Peeled[name]; p != "" {
				c = p
			}
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	if changed {
		writeRemoteTagCache(file, cache)
	}
	sort.Strings(out) // a stable argument order (gitfast and git answer alike)
	return out
}

// NoteTagPush records that a push to remote sends the tags named by refs
// (the pre-push hook's remote refs; other refs are ignored), so the next
// check lists the remote's tags again instead of waiting remoteTagsTTL.
// Recording is not proof: only the next ls-remote makes a tag history.
func NoteTagPush(ctx context.Context, top, remote string, refs []string) {
	var names []string
	for _, r := range refs {
		if n, ok := strings.CutPrefix(r, tagsRefPrefix); ok && n != "" {
			names = append(names, n)
		}
	}
	if top == "" || remote == "" || len(names) == 0 {
		return
	}
	common, err := gitRun{dir: top, args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}}.output(ctx)
	if err != nil || strings.TrimSpace(string(common)) == "" {
		return
	}
	file := filepath.Join(strings.TrimSpace(string(common)), "lore", remoteTagsFile)
	cache := readRemoteTagCache(file)
	e, have := cache.Remotes[remote]
	if !have {
		return // never listed: the next check asks anyway
	}
	if e.Pushed == nil {
		e.Pushed = map[string]bool{}
	}
	for _, n := range names {
		e.Pushed[n] = true
	}
	cache.Remotes[remote] = e
	writeRemoteTagCache(file, cache)
}

// needsTagRefresh reports whether a remote's cached listing must be asked
// again: never asked, or a local tag it does not name at the same object
// and the listing is older than remoteTagsTTL.
func needsTagRefresh(e remoteTagEntry, have bool, local map[string]string, now time.Time) bool {
	if !have {
		return true
	}
	for name := range local {
		if e.Pushed[name] {
			return true // pushed from here since the last listing
		}
	}
	if now.Sub(time.Unix(e.Checked, 0)) < remoteTagsTTL {
		return false
	}
	for name, oid := range local {
		if e.Tags[name] != oid {
			return true
		}
	}
	return false
}

// localTags is tag name → object id of every local tag (refs/tags/*).
func localTags(ctx context.Context, top string) map[string]string {
	out, err := gitRun{dir: top, args: []string{"for-each-ref", "--format=%(objectname) %(refname)", tagsRefPrefix}}.output(ctx)
	if err != nil {
		return nil
	}
	tags := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		oid, ref, ok := strings.Cut(strings.TrimSpace(l), " ")
		if name, isTag := strings.CutPrefix(ref, tagsRefPrefix); ok && isTag && oid != "" && name != "" {
			tags[name] = oid
		}
	}
	return tags
}

// refreshRemoteTags asks remote for its tags (one `git ls-remote --tags`,
// no prompt, bounded by remoteTagsTimeout). On failure (offline, no such
// remote, LORE_OFFLINE) the old listing stays, stamped as asked now.
func refreshRemoteTags(ctx context.Context, top, remote string, old remoteTagEntry) remoteTagEntry {
	tctx, cancel := context.WithTimeout(ctx, remoteTagsTimeout)
	defer cancel()
	out, err := gitRun{dir: top, env: []string{envTerminalPrompt + "=0"}, args: []string{"ls-remote", "--tags", remote}}.output(tctx)
	now := time.Now().Unix()
	if err != nil {
		old.Checked, old.Pushed = now, nil // asked again after remoteTagsTTL
		return old
	}
	e := parseLsRemoteTags(string(out))
	e.Checked = now
	return e
}

// parseLsRemoteTags reads `git ls-remote --tags` output ("<oid>\t<ref>",
// an annotated tag's commit on a second "<ref>^{}" line).
func parseLsRemoteTags(out string) remoteTagEntry {
	e := remoteTagEntry{Tags: map[string]string{}, Peeled: map[string]string{}}
	for _, l := range strings.Split(out, "\n") {
		oid, ref, ok := strings.Cut(strings.TrimSpace(l), "\t")
		name, isTag := strings.CutPrefix(ref, tagsRefPrefix)
		if !ok || !isTag || oid == "" || name == "" {
			continue
		}
		if base, peeled := strings.CutSuffix(name, peeledSuffix); peeled {
			e.Peeled[base] = oid
			continue
		}
		e.Tags[name] = oid
	}
	return e
}

// readRemoteTagCache reads the cache file (an empty cache when missing or
// unreadable: every remote is asked once).
func readRemoteTagCache(file string) remoteTagCache {
	c := remoteTagCache{}
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &c) // a damaged file is a cold cache
	}
	if c.Remotes == nil {
		c.Remotes = map[string]remoteTagEntry{}
	}
	return c
}

// writeRemoteTagCache saves c (best effort: a failed write only means the
// next check asks the remote again).
func writeRemoteTagCache(file string, c remoteTagCache) {
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), remoteTagsFile+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		_ = os.Remove(tmp.Name())
	}
}

// remoteNames are the remotes that have remote-tracking refs
// (refs/remotes/<name>/…), in name order.
func remoteNames(ctx context.Context, top string) []string {
	out, err := gitRun{dir: top, args: []string{"for-each-ref", "--format=%(refname)", remotesRefPrefix}}.output(ctx)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, l := range strings.Split(string(out), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(l), remotesRefPrefix)
		name, _, slash := strings.Cut(rest, "/")
		if ok && slash && name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// remotesRefPrefix holds every remote-tracking ref.
const remotesRefPrefix = "refs/remotes/"

// envTerminalPrompt=0 keeps git from asking for a password on a terminal
// (a hook has none to answer with).
const envTerminalPrompt = "GIT_TERMINAL_PROMPT"
