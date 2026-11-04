package stores

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"saas/pkg/aicoder/gitref"
)

// Fixed commit messages (§6.5 commit metadata: counts and ids only, never
// row text, never a person's name).
const (
	msgInit   = "lore: init store"
	msgLink   = "lore: link %s"
	msgUnlink = "lore: unlink %s"
	msgRows   = "lore: write %d row(s) in %s"
)

// InitResult reports `lore store init`.
type InitResult struct {
	// Already is true when the branch already held a valid marker.
	Already bool
	// Created is true when the branch did not exist and was created with a
	// parentless commit.
	Created bool
	Write   WriteResult
}

// ErrOffline means the store could not be reached for an operation that
// must not run on stale state (init, link).
var ErrOffline = errors.New("stores: store is unreachable")

// Init makes branch of the repo at s a lore store (F12): it refuses when
// the branch — or, for a new branch, the store's default branch — holds
// any file outside the allowed list (TEAM-014: a code repo can never be a
// store), and otherwise writes lore-store.yaml (a parentless first commit
// on a branch that does not exist yet, C2) and pushes it.
func (s *Store) Init(ctx context.Context, branch string, marker Marker, sig gitref.Signature) (InitResult, error) {
	fr, err := s.Fetch(ctx, true)
	if err != nil {
		return InitResult{}, err
	}
	if fr.Offline {
		// %v on purpose: ErrOffline is the identity callers branch on; the
		// transport cause is detail for the message only.
		return InitResult{}, fmt.Errorf("%w: %s: %v", ErrOffline, s.Norm, fr.Err)
	}
	for _, b := range uniqueStrings(branch, s.DefaultBranch()) {
		tip, ok, err := s.Tip(ctx, b)
		if err != nil {
			return InitResult{}, err
		}
		if !ok {
			continue
		}
		paths, err := s.Paths(ctx, tip)
		if err != nil {
			return InitResult{}, err
		}
		if err := CheckTree(s.Norm, b, paths, false); err != nil {
			return InitResult{}, err
		}
	}
	_, exists, err := s.Tip(ctx, branch)
	if err != nil {
		return InitResult{}, err
	}
	body, err := marker.Encode()
	if err != nil {
		return InitResult{}, err
	}
	var res InitResult
	res.Created = !exists
	wr, err := s.Write(ctx, WriteRequest{
		Branch: branch, Create: true, Message: msgInit, Signature: sig,
		Apply: func(ctx context.Context, base Snapshot) (gitref.Changes, error) {
			cur, ok, err := base.ReadFile(ctx, MarkerFile)
			if err != nil {
				return gitref.Changes{}, err
			}
			if ok {
				if _, perr := ParseMarker(cur); perr == nil {
					res.Already = true
					return gitref.Changes{}, nil
				}
			}
			return gitref.Changes{Put: map[string][]byte{MarkerFile: body}}, nil
		},
	})
	res.Write = wr
	if err != nil {
		return res, err
	}
	if wr.Offline {
		return res, fmt.Errorf("%w: %s: the marker is committed in the local clone and is pushed by the next command", ErrOffline, s.Norm)
	}
	return res, nil
}

func uniqueStrings(xs ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// LinkChange is one `lore org link` / `lore org unlink` on a store branch.
type LinkChange struct {
	Branch string
	Folder string
	Link   LinkRecord
	// Remove unlinks instead of linking.
	Remove bool
	// NewProject returns the files of a fresh lore project for the folder
	// (root-relative to the folder's .lore/data: "_meta.json",
	// "projects/<id>.json", …). Called only when the folder does not exist.
	// The project id it mints must be in the returned _meta.json.
	NewProject func(folder string) (map[string][]byte, error)
	Signature  gitref.Signature
	NoPush     bool
}

// LinkResult reports a link write.
type LinkResult struct {
	// CreatedFolder is true when this run created the folder project.
	CreatedFolder bool
	// Changed is false when the link was already there (or already gone)
	// before this run. A change this run committed stays true even when a
	// lost push race made a later attempt find it already in place.
	Changed bool
	Write   WriteResult
}

// ErrNotAProject: the folder exists in the store but is not a lore project
// (F11 step 2: refuse rather than adopt an unknown folder).
var ErrNotAProject = errors.New("stores: folder exists but is not a lore project")

// ErrFolderMissing: unlink of a folder that does not exist.
var ErrFolderMissing = errors.New("stores: no such project folder in the store")

// WriteLink performs F11 steps 2–5 on this store: check it, create the
// folder project when missing, set the link in its _meta.json (a set; a
// concurrent link by someone else survives the reconcile merge), commit and
// push. The org entry, folder and link were validated by the caller
// (orgresolve.PlanLink) and the store chosen by FolderStore.
func (s *Store) WriteLink(ctx context.Context, ch LinkChange) (LinkResult, error) {
	var res LinkResult
	msg := fmt.Sprintf(msgLink, ch.Link.ProjectID)
	if ch.Remove {
		msg = fmt.Sprintf(msgUnlink, ch.Link.ProjectID)
	}
	wr, err := s.Write(ctx, WriteRequest{
		Branch: ch.Branch, Message: msg, Signature: ch.Signature, NoPush: ch.NoPush,
		// Apply runs once per push attempt. Its decisions are per attempt
		// (created, changed), but the result is sticky across attempts: when
		// a push loses a race, the next attempt builds on a merge that
		// already holds THIS run's commit, so it finds "nothing to do" —
		// and yet this run is what wrote the link (trial F1).
		Apply: func(ctx context.Context, base Snapshot) (gitref.Changes, error) {
			created, changed := false, false
			if err := s.checkSnapshot(ctx, base, ch.Branch); err != nil {
				return gitref.Changes{}, err
			}
			put := map[string][]byte{}
			metaB, ok, err := base.ReadFile(ctx, MetaPath(ch.Folder))
			if err != nil {
				return gitref.Changes{}, err
			}
			var meta StoreMeta
			switch {
			case ok:
				if meta, err = ParseStoreMeta(metaB); err != nil {
					return gitref.Changes{}, err
				}
				if meta.ProjectID == "" {
					return gitref.Changes{}, fmt.Errorf("%w: %s (its _meta.json has no project_id)", ErrNotAProject, ch.Folder)
				}
			case ch.Remove:
				return gitref.Changes{}, fmt.Errorf("%w: %s", ErrFolderMissing, ch.Folder)
			default:
				existing, err := base.ReadTree(ctx, folderTreePrefix(ch.Folder))
				if err != nil {
					return gitref.Changes{}, err
				}
				newFormat, err := newFormatStore(ctx, base)
				if err != nil {
					return gitref.Changes{}, err
				}
				// A 0.1.18 store's folders hold only `_v: 2` row files and
				// the 0.1.18 core makes a folder without a _meta.json (its
				// `--to <store>/<folder> --yes`), so there a folder with
				// rows and no _meta.json is adopted: the link only adds
				// the _meta.json.
				if len(existing) > 0 && !newFormat {
					return gitref.Changes{}, fmt.Errorf("%w: %s", ErrNotAProject, ch.Folder)
				}
				if ch.NewProject == nil {
					return gitref.Changes{}, fmt.Errorf("stores: folder %s does not exist and no project template was given", ch.Folder)
				}
				files, err := ch.NewProject(ch.Folder)
				if err != nil {
					return gitref.Changes{}, err
				}
				for rel, b := range files {
					// Only the _meta.json in a 0.1.18 store: the template's
					// other files are v0.1.17 (`_v: 1`) rows the core
					// would not read.
					if newFormat && rel != metaFileName {
						continue
					}
					put[DataPrefix(ch.Folder)+rel] = b
				}
				nb, ok := files[metaFileName]
				if !ok {
					return gitref.Changes{}, errors.New("stores: new project template has no _meta.json")
				}
				if meta, err = ParseStoreMeta(nb); err != nil {
					return gitref.Changes{}, err
				}
				created = true
			}
			if ch.Remove {
				changed = meta.RemoveLink(ch.Link.ProjectID, ch.Link.Remote)
			} else {
				changed = meta.AddLink(ch.Link)
			}
			res.CreatedFolder = res.CreatedFolder || created
			res.Changed = res.Changed || changed
			if !changed && !created {
				return gitref.Changes{}, nil
			}
			b, err := meta.Encode()
			if err != nil {
				return gitref.Changes{}, err
			}
			put[MetaPath(ch.Folder)] = b
			return gitref.Changes{Put: put}, nil
		},
	})
	res.Write = wr
	return res, err
}

// newFormatStore reports whether the snapshot's lore-store.yaml is a
// 0.1.18 marker (lore_store 2 or later, written by the core's `lore store
// init`). A missing or unreadable marker is not (checkSnapshot refuses
// such a store before this is asked).
func newFormatStore(ctx context.Context, base Snapshot) (bool, error) {
	b, ok, err := base.ReadFile(ctx, MarkerFile)
	if err != nil || !ok {
		return false, err
	}
	m, err := ParseMarker(b)
	if err != nil {
		return false, nil
	}
	return m.LoreStore >= NewFormatMarkerVersion, nil
}

// NewFormatMarkerVersion is the first lore-store.yaml version the 0.1.18
// core writes (adapters/backend/rowfs MarkerVersion); this package still
// writes MarkerVersion 1.
const NewFormatMarkerVersion = 2

// folderTreePrefix is everything belonging to a folder ("" for the root
// project would be the whole store, so the root project's prefix is .lore/).
func folderTreePrefix(folder string) string {
	if folder == RootFolder || folder == "" {
		return loreDir + "/"
	}
	return folder + "/"
}

// checkSnapshot runs the F12 check on a write's base commit, so a write
// never lands in a branch that turned into a non-store meanwhile.
func (s *Store) checkSnapshot(ctx context.Context, base Snapshot, branch string) error {
	if base.Commit == "" {
		return nil
	}
	paths, err := s.Paths(ctx, base.Commit)
	if err != nil {
		return err
	}
	return CheckTree(s.Norm, branch, paths, true)
}

// FolderExists reports whether folder is a project on branch (its
// _meta.json exists at the branch tip).
func (s *Store) FolderExists(ctx context.Context, branch, folder string) (bool, error) {
	tip, ok, err := s.Tip(ctx, branch)
	if err != nil || !ok {
		return false, err
	}
	_, found, err := s.Repo.ReadFile(ctx, tip, MetaPath(folder))
	return found, err
}

// ReadProject returns a store project's row files at the branch tip, keyed
// by their path relative to <folder>/.lore/data/ ("memories/mem_x.json").
// commit is the tip read ("" when the branch does not exist).
func (s *Store) ReadProject(ctx context.Context, branch, folder string) (files map[string][]byte, commit string, err error) {
	tip, ok, err := s.Tip(ctx, branch)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return map[string][]byte{}, "", nil
	}
	all, err := s.Repo.ReadTree(ctx, tip, DataPrefix(folder))
	if err != nil {
		return nil, "", err
	}
	files = make(map[string][]byte, len(all))
	for p, b := range all {
		files[strings.TrimPrefix(p, DataPrefix(folder))] = b
	}
	return files, tip, nil
}

// RowChanges are row-file changes for one store project, relative to its
// .lore/data folder.
type RowChanges struct {
	Put    map[string][]byte
	Delete []string
}

// WriteRows commits row changes into a store project on its branch and
// pushes them (§3.2: "the store branch, committed and pushed at once").
// _meta.json is never taken from Put when the store already has one: the
// store's links and flags are kept and only a missing project_id is filled,
// because a mirror's engine rewrites _meta.json without links. Apply-time
// re-reading makes a concurrent writer's rows survive (each row is its own
// file).
func (s *Store) WriteRows(ctx context.Context, branch, folder string, rc RowChanges, sig gitref.Signature, noPush bool) (WriteResult, error) {
	n := len(rc.Put) + len(rc.Delete)
	return s.Write(ctx, WriteRequest{
		Branch: branch, Signature: sig, NoPush: noPush,
		Message: fmt.Sprintf(msgRows, n, folderLabel(folder)),
		Apply: func(ctx context.Context, base Snapshot) (gitref.Changes, error) {
			if err := s.checkSnapshot(ctx, base, branch); err != nil {
				return gitref.Changes{}, err
			}
			ch := gitref.Changes{Put: map[string][]byte{}}
			for rel, b := range rc.Put {
				full := DataPrefix(folder) + rel
				if rel == metaFileName {
					cur, ok, err := base.ReadFile(ctx, full)
					if err != nil {
						return gitref.Changes{}, err
					}
					if ok {
						merged, err := keepStoreMeta(cur, b)
						if err != nil {
							return gitref.Changes{}, err
						}
						b = merged
					}
				}
				ch.Put[full] = b
			}
			for _, rel := range rc.Delete {
				if rel == metaFileName {
					continue // a project's identity is never deleted by a row write
				}
				ch.Delete = append(ch.Delete, DataPrefix(folder)+rel)
			}
			return ch, nil
		},
	})
}

// keepStoreMeta keeps the store's _meta.json, filling only a missing
// project_id from the mirror's version.
func keepStoreMeta(store, mirror []byte) ([]byte, error) {
	sm, err := ParseStoreMeta(store)
	if err != nil {
		return nil, err
	}
	if sm.ProjectID != "" {
		return store, nil
	}
	mm, err := ParseStoreMeta(mirror)
	if err != nil {
		return nil, err
	}
	sm.ProjectID = mm.ProjectID
	return sm.Encode()
}

// folderLabel is how a folder appears in a commit message.
func folderLabel(folder string) string {
	if folder == RootFolder || folder == "" {
		return "the root project"
	}
	return folder
}
