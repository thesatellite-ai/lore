package rowfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// The repo identity in a checkout's .lore/data/_meta.json (PROV1,
// PROVENANCE_DESIGN 10.1): next to v0.1.17's project_id and _v, the file
// carries the repo's permanent repo_id, its confirmed addresses (urls,
// normalised, newest first), and forked_from / split_from on a repo made by
// `lore repo fork`. Because _meta.json is committed, every clone reads the
// same id. The full URL history with dates and the unconfirmed addresses
// live in the synced repos row, not here: this file only holds what a
// fresh clone compares its remote against.
//
// This file owns only those keys; every other key (project_id, _v, a store
// folder's links, keys a newer lore wrote) is kept byte for byte.

// Keys of the repo identity (file format: they must not change).
const (
	MetaKeyRepoID     = "repo_id"
	MetaKeyURLs       = "urls"
	MetaKeyForkedFrom = "forked_from"
	MetaKeySplitFrom  = "split_from"
	MetaKeyAliases    = "repo_id_aliases"
)

// repoKeys are the keys RepoMeta owns, in one place for the reader, the
// writer and the merge.
var repoKeys = []string{MetaKeyRepoID, MetaKeyURLs, MetaKeyForkedFrom, MetaKeySplitFrom, MetaKeyAliases}

// RepoMeta is the repo identity part of a checkout's _meta.json. The zero
// value is "no identity yet" (a v0.1.17 checkout before its first 0.1.18
// sync).
type RepoMeta struct {
	// RepoID is the permanent random rep_ id (never derived from a name or
	// a URL, PROV1).
	RepoID string `json:"repo_id,omitempty"`
	// URLs are the confirmed addresses, normalised (repourl.Normalize),
	// newest first.
	URLs []string `json:"urls,omitempty"`
	// ForkedFrom is the upstream repo id of a fork (PROV1).
	ForkedFrom string `json:"forked_from,omitempty"`
	// SplitFrom is the repo id a split repo came from (11.5).
	SplitFrom string `json:"split_from,omitempty"`
	// Aliases are repo ids that lost the two-clones race of 11.9 (Q5): the
	// same repo, so rows stamped with one stay correct.
	Aliases []string `json:"repo_id_aliases,omitempty"`
}

// RepoMetaPath is the store-relative path of the checkout's _meta.json.
const RepoMetaPath = LoreDir + "/" + DataDir + "/" + MetaFileName

// ReadRepoMeta reads the repo identity of the checkout whose lore root is
// top (the folder holding .lore). A missing file is the zero RepoMeta and
// exists=false; a file that is not a JSON object, or a repo_id that is not
// a rep_ id, is an error (never silently "no identity", which would mint a
// second id for the repo).
func ReadRepoMeta(top string) (m RepoMeta, exists bool, err error) {
	if cerr := checkChain(top, RepoMetaPath); cerr != nil {
		return RepoMeta{}, false, cerr
	}
	b, rerr := os.ReadFile(filepath.Clean(filepath.Join(top, LoreDir, DataDir, MetaFileName)))
	if errors.Is(rerr, fs.ErrNotExist) {
		return RepoMeta{}, false, nil
	}
	if rerr != nil {
		return RepoMeta{}, false, errs.Wrap(errs.Internal, "rowfs: read "+RepoMetaPath, rerr)
	}
	o, derr := decodeObject(b)
	if derr != nil {
		return RepoMeta{}, true, errs.Wrap(errs.InvalidInput, "rowfs: "+RepoMetaPath+" is not a JSON object", derr)
	}
	m, perr := repoMetaOf(o)
	if perr != nil {
		return RepoMeta{}, true, perr
	}
	return m, true, nil
}

// repoMetaOf reads the identity keys of a decoded _meta.json. A value of
// the wrong shape is ignored except repo_id, which must parse.
func repoMetaOf(o object) (RepoMeta, error) {
	sub := object{}
	for _, k := range repoKeys {
		if v, ok := o[k]; ok {
			sub[k] = v
		}
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		return RepoMeta{}, errs.Wrap(errs.Internal, "rowfs: encode repo identity", err)
	}
	var m RepoMeta
	if json.Unmarshal(raw, &m) != nil {
		// A wrong-shaped list must not hide the id: read it alone.
		var only struct {
			RepoID string `json:"repo_id"`
		}
		if json.Unmarshal(raw, &only) != nil {
			return RepoMeta{}, errs.New(errs.InvalidInput, "rowfs: "+RepoMetaPath+" has an invalid repo_id")
		}
		m = RepoMeta{RepoID: only.RepoID}
	}
	if m.RepoID != "" {
		if _, perr := ids.Parse[ids.Repo](m.RepoID); perr != nil {
			return RepoMeta{}, errs.Wrap(errs.InvalidInput, "rowfs: "+RepoMetaPath+" has an invalid repo_id", perr)
		}
	}
	return m, nil
}

// WriteRepoMeta sets the identity keys of the checkout's _meta.json to m
// (an empty field removes its key) and keeps every other key. The file
// must exist: it is created by `lore init` with the project id, and a
// folder without one is not a lore checkout yet, so writing an identity
// there would make it look like one. Writing the same bytes again is a
// no-op, so a repeated sync leaves git quiet.
func WriteRepoMeta(top string, m RepoMeta) error {
	if cerr := checkChain(top, RepoMetaPath); cerr != nil {
		return cerr
	}
	full := filepath.Clean(filepath.Join(top, LoreDir, DataDir, MetaFileName))
	cur, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errs.New(errs.NotFound, "rowfs: "+RepoMetaPath+" does not exist").
				WithHint("run `lore init` in the repository first")
		}
		return errs.Wrap(errs.Internal, "rowfs: read "+RepoMetaPath, err)
	}
	o, err := decodeObject(cur)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "rowfs: "+RepoMetaPath+" is not a JSON object", err)
	}
	if serr := setRepoMeta(o, m); serr != nil {
		return serr
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode "+RepoMetaPath, err)
	}
	doc, err := canon.Document(raw)
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode "+RepoMetaPath, err)
	}
	if bytes.Equal(doc, cur) {
		return nil
	}
	return writeAtomic(full, doc, sharedModes) // a repo's _meta.json is committed
}

// setRepoMeta replaces the identity keys of o with m's.
func setRepoMeta(o object, m RepoMeta) error {
	for _, k := range repoKeys {
		delete(o, k)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode repo identity", err)
	}
	sub := object{}
	if err := json.Unmarshal(raw, &sub); err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode repo identity", err)
	}
	for k, v := range sub {
		o[k] = v
	}
	return nil
}

// ResolveRepoIdentity is the merge rule of the identity keys when two
// branches of one repo meet (Q5, PROVENANCE_DESIGN 10.6 and 11.9). The
// common ancestor never decides an identity (both sides are newer than it),
// so it is not an input.
//
//   - Same repo_id on both sides (or one side has none): that id, the urls
//     as a union (ours' order first: newest first stays true for ours, the
//     side whose content continues), aliases as a union.
//   - Different ids and either side has forked_from or split_from: a fork
//     (or split) is being merged with its upstream. Q5: keep ours (%A,
//     the branch being merged into) for every identity key, because the
//     target repo is the one that continues; rows made in the fork keep
//     their own _o.repo (true about where they were made), and `lore
//     doctor` flags a clone whose repo_id disagrees with its remote.
//   - Different ids and neither side has forked_from or split_from: the
//     race of 11.9 (two clones minted an id for one repo before either
//     pushed). The older UUIDv7, which sorts first, wins; the other id is
//     kept in repo_id_aliases so rows already stamped with it stay
//     correct. Every clone picks the same winner.
func ResolveRepoIdentity(ours, theirs RepoMeta) RepoMeta {
	switch {
	case theirs.RepoID == "" || theirs.RepoID == ours.RepoID:
		return unionRepoMeta(ours, theirs, ours.RepoID)
	case ours.RepoID == "":
		return unionRepoMeta(theirs, ours, theirs.RepoID)
	case forked(ours) || forked(theirs):
		// Q5: keep ours.
		return ours
	}
	win, lose := ours, theirs
	if theirs.RepoID < ours.RepoID {
		win, lose = theirs, ours
	}
	out := unionRepoMeta(win, lose, win.RepoID)
	out.Aliases = unionStrings(out.Aliases, []string{lose.RepoID})
	return out
}

// forked reports whether m is the record of a fork or a split.
func forked(m RepoMeta) bool { return m.ForkedFrom != "" || m.SplitFrom != "" }

// unionRepoMeta is first's identity with second's urls and aliases added.
func unionRepoMeta(first, second RepoMeta, id string) RepoMeta {
	out := first
	out.RepoID = id
	out.URLs = unionStrings(first.URLs, second.URLs)
	out.Aliases = unionStrings(first.Aliases, second.Aliases)
	out.Aliases = slices.DeleteFunc(out.Aliases, func(a string) bool { return a == id })
	if out.ForkedFrom == "" {
		out.ForkedFrom = second.ForkedFrom
	}
	if out.SplitFrom == "" {
		out.SplitFrom = second.SplitFrom
	}
	if len(out.Aliases) == 0 {
		out.Aliases = nil
	}
	return out
}

// unionStrings is a followed by b's entries a lacks (order kept).
func unionStrings(a, b []string) []string {
	out := slices.Clone(a)
	for _, s := range b {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// mergeRepoMeta applies ResolveRepoIdentity to a merged folder file (the
// store folders' _meta.json merge, MergeFolderFile). A file without any
// identity key on any side is left as mergeKeys made it.
func mergeRepoMeta(out, o, t object) error {
	has := false
	for _, k := range repoKeys {
		_, inO := o[k]
		_, inT := t[k]
		has = has || inO || inT
	}
	if !has {
		return nil
	}
	om, oerr := repoMetaOf(o)
	tm, terr := repoMetaOf(t)
	if oerr != nil || terr != nil {
		return nil // an unreadable id stays as mergeKeys left it; doctor reports it
	}
	return setRepoMeta(out, ResolveRepoIdentity(om, tm))
}
