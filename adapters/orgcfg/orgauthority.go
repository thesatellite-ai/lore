package orgcfg

// orgauthority.go — lore-org.yaml is the org's own config (its store
// list, own:/repos: patterns, defaults), and changing it is an admin's
// change (devco R-7, coordinator decision C30). A store is a plain git
// repo every member can push to, so a reader never trusts the file at the
// branch tip as it stands: it replays the file's history and accepts each
// change only from a commit whose git author is an admin of the org in
// the roster at that commit's parent. The changes come from the store
// clone's roster replay, kept on disk and carried on over new commits
// (gitbackend.OrgFileChanges), so an open walks no history (devco:D-999).
//
//	commits touching the org file, in the roster replay's order (a
//	merge counts when its version is neither parent's)
//	  └─ doc at C equals the accepted doc (a move, reformat) ─▶ accepted
//	  └─ first version, or author is an admin at C^          ─▶ accepted
//	  └─ otherwise                                           ─▶ held back
//	                                    (HeldOrgChange: commit, author, why)
//	the accepted version is what the suggestion reads; a held change is
//	never copied into ~/.lore/config.yaml, so lore never clones, reads or
//	pushes to a store only a non-admin added.
//
// Admins at a commit: the actors whose open stint (actor_periods without
// ended_on) has org_role admin in the org folder, matched to the commit
// author by stable key (human:<email>) or a key alias. An org whose roster
// names no admin (made before org roles) counts the creator of the org
// folder's project row; a roster with no actor at all (the org's first
// commits) accepts the change. Git authorship can be forged by anyone who
// can push, so this raises the bar but is not proof; signed changes are
// future work (docs/team/security.md).

import (
	"context"
	"encoding/json"
	"fmt"
	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/schema"
)

// HeldOrgChange is one change to the org file that was not applied: the
// commit, its git author and why it was held back.
type HeldOrgChange struct {
	// Commit is the short commit id; Author / Email its git author.
	Commit, Author, Email string
	// Subject is the commit subject.
	Subject string
	// Path is the org file the commit changed.
	Path string
	// Why says what was wrong ("… is not an admin of the org").
	Why string
}

// String is the one line lore prints for the held change.
func (h HeldOrgChange) String() string {
	return fmt.Sprintf("commit %s by %s <%s> (%q) changed %s: held back, %s", h.Commit, h.Author, h.Email, h.Subject, h.Path, h.Why)
}

// heldOrgWhy is the reason of a change by a non-admin.
const heldOrgWhy = "%s is not an admin of the org at that commit; only an admin changes the org's stores, own: and repos: (an admin makes the change with lore, e.g. lore org store add --org <org> --url <url>, or reverts the commit in the store clone: git revert %s, then lore sync)"

// unreadOrgWhy is why a change to the org file is held back when its
// version is not in this store clone (a partial clone offline, a damaged
// clone): it cannot be judged, so it is not read (devco:D-997).
const unreadOrgWhy = "the version of %s at commit %s is not in this store clone (offline, or the clone lacks it), so it cannot be checked and is held back; run lore sync online to fetch it"

// orgFileCommit is one commit of the org file's history.
type orgFileCommit struct {
	sha, parent, author, email, subject string
	merge                               bool
}

// acceptedOrgFile replays the history of the org file of folder up to tip
// (every place it was kept: orgFilePaths, so a layout move is one file)
// and returns the commit whose version of the file is the accepted one
// and the changes held back. With nothing held back it returns "", nil:
// the tip's file stands. A history git cannot list is read as accepted
// (best effort: never brick a store over a log error).
func acceptedOrgFile(ctx context.Context, dir, tip, folder string) (string, []HeldOrgChange) {
	if at, held, ok := acceptedOrgFileIndexed(ctx, dir, tip, folder); ok {
		return at, held
	}
	return acceptedOrgFileLog(ctx, dir, tip, folder)
}

// acceptedOrgFileIndexed is acceptedOrgFile read from the store clone's
// roster replay (gitbackend.OrgFileChanges): the replay already lists
// every change to the org file, in its own order, with whether the author
// was an admin at the commit's parent, and it is kept on disk and carried
// on over new commits only. Why (devco:D-999, store growth item 5): the
// path-limited `git log` of acceptedOrgFileLog walked the whole history
// on every core open, and skipped merges, so a merge commit that brought
// a version of lore-org.yaml neither parent had (a hand-made merge by a
// member) stood unchecked. ok is false when the replay cannot answer for
// tip (tip is not the clone's HEAD: a fetched tip read before lore sync
// merges it).
func acceptedOrgFileIndexed(ctx context.Context, dir, tip, folder string) (string, []HeldOrgChange, bool) {
	changes, ok := gitbackend.OrgFileChanges(ctx, dir, tip)
	if !ok {
		return "", nil, false
	}
	paths := orgFilePaths(folder)
	var held []HeldOrgChange
	accepted := "" // the commit whose file is accepted so far
	var acceptedDoc *OrgFileDoc
	hold := func(c gitbackend.OrgFileChange, unread bool) {
		short := shortCommit(c.Commit)
		name, subject := commitNameSubject(ctx, dir, c.Commit)
		why := fmt.Sprintf(unreadOrgWhy, c.Path, short)
		switch {
		case unread:
		case c.Merge:
			// a merge is reverted against its first parent: the plain
			// `git revert <merge>` git refuses
			why = fmt.Sprintf(heldOrgWhy, name, "-m 1 "+short)
		default:
			why = fmt.Sprintf(heldOrgWhy, name, short)
		}
		held = append(held, HeldOrgChange{Commit: short, Author: name, Email: c.Email, Subject: subject, Path: c.Path, Why: why})
	}
	for _, c := range changes {
		if !slices.Contains(paths, c.Path) || c.Removed {
			continue // another folder's org file, or the commit removed it
		}
		raw, rerr := gitRun(ctx, "-C", dir, "cat-file", "blob", c.Blob)
		if rerr != nil {
			// not in this clone (offline, a damaged clone): held back,
			// never accepted (fail closed, devco:D-997)
			hold(c, true)
			continue
		}
		doc, perr := parseOrgFileDoc(raw)
		if perr != nil {
			continue // unreadable: the tip read names it if it is still there
		}
		admin := c.Admin
		if !c.Decided && c.Parent != "" {
			admin = isOrgAdminAt(ctx, dir, c.Parent, folder, c.Email)
		}
		switch {
		case acceptedDoc != nil && sameOrgDoc(*acceptedDoc, doc):
		case acceptedDoc == nil && c.Parent == "":
		case admin:
		default:
			hold(c, false)
			continue
		}
		d := doc
		accepted, acceptedDoc = c.Commit, &d
	}
	if len(held) == 0 {
		return "", nil, true
	}
	return accepted, held, true
}

// orgLogWalks counts the history walks of acceptedOrgFileLog in this
// process: the tests' proof that an open at a clone's HEAD walks none
// (devco:D-999).
var orgLogWalks atomic.Int64

// shortCommit is the 7-character form of a commit id lore prints.
func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// commitNameSubject is the git author name and the subject of commit
// (both "" when git cannot read it): only a held change prints them.
func commitNameSubject(ctx context.Context, dir, commit string) (name, subject string) {
	out, err := gitRun(ctx, "-C", dir, "show", "-s", "--format=%an%x00%s", commit)
	if err != nil {
		return "", ""
	}
	name, subject, _ = strings.Cut(strings.TrimRight(out, "\n"), "\x00")
	return name, subject
}

// acceptedOrgFileLog is acceptedOrgFile from a path-limited `git log` of
// tip's whole history: the way for a tip the clone's roster replay does
// not reach (acceptedOrgFileIndexed).
func acceptedOrgFileLog(ctx context.Context, dir, tip, folder string) (string, []HeldOrgChange) {
	orgLogWalks.Add(1)
	paths := orgFilePaths(folder)
	// merges too: git's path-limited log keeps a merge only when its
	// version is neither parent's (a hand-made merge), the one a member
	// could slip a change in by (devco:D-999)
	args := append([]string{"-C", dir, "log", "--reverse", "--topo-order", "--format=%H%x1f%P%x1f%an%x1f%ae%x1f%s", tip, "--"}, paths...)
	out, err := gitRun(ctx, args...)
	if err != nil {
		return "", nil
	}
	var commits []orgFileCommit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) != 5 {
			continue
		}
		parent, _, merge := strings.Cut(f[1], " ")
		commits = append(commits, orgFileCommit{sha: f[0], parent: parent, author: f[2], email: f[3], subject: f[4], merge: merge})
	}
	var held []HeldOrgChange
	accepted := "" // the commit whose file is accepted so far
	var acceptedDoc *OrgFileDoc
	for _, c := range commits {
		raw, p, ok, rerr := readFirstBlob(ctx, dir, c.sha, paths)
		if rerr != nil {
			// the version is in the history but not in this clone
			// (offline, a damaged clone): a change no one can judge is
			// held back, never accepted (fail closed, devco:D-997)
			short := c.sha
			if len(short) > 7 {
				short = short[:7]
			}
			held = append(held, HeldOrgChange{Commit: short, Author: c.author, Email: c.email, Subject: c.subject, Path: p,
				Why: fmt.Sprintf(unreadOrgWhy, p, short)})
			continue
		}
		if !ok {
			continue // the commit deleted the file: nothing to read from it
		}
		doc, perr := parseOrgFileDoc(raw)
		if perr != nil {
			continue // unreadable: the tip read names it if it is still there
		}
		switch {
		case acceptedDoc != nil && sameOrgDoc(*acceptedDoc, doc):
		case acceptedDoc == nil && c.parent == "":
		case orgAdminAt(ctx, dir, c.parent, folder, c.email):
		default:
			short := c.sha
			if len(short) > 7 {
				short = short[:7]
			}
			revert := short
			if c.merge {
				revert = "-m 1 " + short
			}
			held = append(held, HeldOrgChange{Commit: short, Author: c.author, Email: c.email, Subject: c.subject, Path: p,
				Why: fmt.Sprintf(heldOrgWhy, c.author, revert)})
			continue
		}
		d := doc
		accepted, acceptedDoc = c.sha, &d
	}
	if len(held) == 0 {
		return "", nil
	}
	return accepted, held
}

// acceptedOrgRaw is raw (the org file of folder at tip) or, when changes
// to it were held back, its last accepted version: an admin's own edit
// (a store added or moved) is made on what was accepted, so it never
// carries a held-back change along under the admin's name.
// An accepted version that is not in this clone is an error: the tip's
// file would carry the held-back change along (devco:D-997).
func acceptedOrgRaw(ctx context.Context, dir, tip, folder string, raw []byte) ([]byte, error) {
	at, held := acceptedOrgFile(ctx, dir, tip, folder)
	if len(held) == 0 || at == "" {
		return raw, nil
	}
	b, _, ok, err := readFirstBlob(ctx, dir, at, orgFilePaths(folder))
	if err != nil {
		return nil, err
	}
	if ok {
		return []byte(b), nil
	}
	return raw, nil
}

// readFirstBlob is the first of paths that exists at rev, with its body.
// found=false: no path is in rev's tree. err: a path is in rev's tree but
// its blob is not in this clone (a partial clone offline, a damaged
// clone): never read as "no file" (fail closed, devco:D-997).
func readFirstBlob(ctx context.Context, dir, rev string, paths []string) (body, path string, found bool, err error) {
	for _, p := range paths {
		raw, cerr := gitRun(ctx, "-C", dir, "cat-file", "blob", rev+":"+p)
		if cerr == nil {
			return raw, p, true, nil
		}
		// the tree names the path (rev-parse reads trees only): the blob
		// itself is missing
		if _, rerr := gitRun(ctx, "-C", dir, "rev-parse", "--verify", "--quiet", rev+":"+p); rerr == nil {
			return "", p, false, fmt.Errorf("orgcfg: %s at %s is not in this store clone (run lore sync online to fetch it): %w", p, rev, cerr)
		}
	}
	return "", "", false, nil
}

// sameOrgDoc reports whether two versions of the org file say the same
// (what a move or a reformat leaves).
func sameOrgDoc(a, b OrgFileDoc) bool {
	sa, sb := Suggestion{Doc: a}, Suggestion{Doc: b}
	return a.Name == b.Name && sa.Shared().Equal(sb.Shared())
}

// rosterRow is the part of a roster row the admin check reads.
type rosterRow struct {
	ID         string   `json:"id"`
	StableKey  string   `json:"stable_key"`
	KeyAliases []string `json:"key_aliases"`
	Actor      string   `json:"actor_id"`
	EndedOn    string   `json:"ended_on"`
	OrgRole    string   `json:"org_role"`
	CreatedBy  string   `json:"created_by"`
	Deleted    bool     `json:"_deleted"`
	// teams rows (D-727): the handle, its members and whether it left
	Handle  string          `json:"handle"`
	Members json.RawMessage `json:"member_actor_ids"`
	Active  *bool           `json:"active"`
	LeftAt  string          `json:"left_at"`
}

// orgAdminAt is whether email is an admin of the org at commit rev: the
// store clone's trusted roster replay decides (gitbackend.AdminAt, the
// one source the roster check uses too, so a member's forged admin stint
// never counts); only when that replay knows no admin yet (an org from
// before org roles, its first commits) does isOrgAdminAt's own rule apply.
func orgAdminAt(ctx context.Context, dir, rev, folder, email string) bool {
	if admin, decided := gitbackend.AdminAt(ctx, dir, rev, email); decided {
		return admin
	}
	return isOrgAdminAt(ctx, dir, rev, folder, email)
}

// isOrgAdminAt reports whether the git author email is an admin of the
// org (folder) in the roster of commit rev (see the file comment). It
// finds the rows with git grep, so a big roster costs a few git calls,
// not one per row (every lore command reads the org file).
func isOrgAdminAt(ctx context.Context, dir, rev, folder, email string) bool {
	if rev == "" || email == "" {
		return false
	}
	base := rosterBase(folder)
	rowsMatching := func(table, re string) []rosterRow { return rosterRowsAt(ctx, dir, rev, base, table, re) }
	who := actorIDsAt(ctx, dir, rev, base, email)
	if len(who) == 0 {
		if out, err := gitRun(ctx, "-C", dir, "ls-tree", "--name-only", rev, "--", base+schema.TableActors+"/"); err == nil && strings.TrimSpace(out) == "" {
			// the org's first commits: no roster to check against yet. A
			// roster elsewhere in the store means the org file moved to a
			// folder with none (a hand-edited marker): not a first commit.
			return !hasActorsAnywhere(ctx, dir, rev)
		}
		return false
	}
	admins := rowsMatching(schema.TableActorPeriods, `"`+schema.FieldPeriodOrgRole+`"[[:space:]]*:[[:space:]]*"`+orgRoleAdmin+`"`)
	anyAdmin := false
	for _, p := range admins {
		if p.EndedOn != "" || p.OrgRole != orgRoleAdmin {
			continue
		}
		anyAdmin = true
		if slices.Contains(who, p.Actor) {
			return true
		}
	}
	if anyAdmin {
		return false
	}
	// no org roles yet (an org made before them): the org's creator
	for _, pr := range rowsMatching(schema.TableProjects, "") {
		if slices.Contains(who, pr.CreatedBy) {
			return true
		}
	}
	return false
}

// rosterBase is the path prefix of the roster tables of org folder folder
// in a store tree ("" for the store root).
func rosterBase(folder string) string {
	if folder != "" && folder != "." {
		return folder + "/"
	}
	return ""
}

// rosterRowsAt reads the rows of table (under base) at rev whose file
// matches the extended regexp re (every row when re is ""). It finds the
// rows with git grep, so a big roster costs a few git calls, not one per
// row (every lore command reads the org file).
func rosterRowsAt(ctx context.Context, dir, rev, base, table, re string) []rosterRow {
	var files []string
	if re == "" {
		out, err := gitRun(ctx, "-C", dir, "ls-tree", "-r", "-z", "--name-only", rev, "--", base+table)
		if err != nil {
			return nil
		}
		files = strings.Split(out, "\x00")
	} else {
		out, err := gitRun(ctx, "-C", dir, "grep", "-l", "-i", "-E", "-e", re, rev, "--", base+table)
		if err != nil {
			return nil // exit 1: no match
		}
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			files = append(files, strings.TrimPrefix(l, rev+":"))
		}
	}
	var rows []rosterRow
	for _, f := range files {
		if f == "" || path.Ext(f) != ".json" {
			continue
		}
		raw, rerr := gitRun(ctx, "-C", dir, "cat-file", "blob", rev+":"+f)
		if rerr != nil {
			continue
		}
		var r rosterRow
		if json.Unmarshal([]byte(raw), &r) == nil && !r.Deleted {
			rows = append(rows, r)
		}
	}
	return rows
}

// actorIDsAt is the ids of the roster actors (under base, at rev) whose
// stable key or a key alias is human:<email>.
func actorIDsAt(ctx context.Context, dir, rev, base, email string) []string {
	key := "human:" + strings.ToLower(strings.TrimSpace(email))
	var who []string
	for _, a := range rosterRowsAt(ctx, dir, rev, base, schema.TableActors, regexp.QuoteMeta(key)) {
		if strings.EqualFold(a.StableKey, key) || slices.ContainsFunc(a.KeyAliases, func(k string) bool { return strings.EqualFold(k, key) }) {
			who = append(who, a.ID)
		}
	}
	return who
}

// teamsAt is the handles (no @) of the active roster teams of org folder
// folder, at rev, that list the person with email as a member (devco
// D-727: a team store is that team's). A team that left (active false or
// left_at set) counts for nobody.
func teamsAt(ctx context.Context, dir, rev, folder, email string) []string {
	if rev == "" || email == "" {
		return nil
	}
	base := rosterBase(folder)
	who := actorIDsAt(ctx, dir, rev, base, email)
	if len(who) == 0 {
		return nil
	}
	var out []string
	for _, t := range rosterRowsAt(ctx, dir, rev, base, schema.TableTeams, "") {
		if t.Handle == "" || t.LeftAt != "" || (t.Active != nil && !*t.Active) {
			continue
		}
		if slices.ContainsFunc(t.memberIDs(), func(id string) bool { return slices.Contains(who, id) }) && !slices.Contains(out, t.Handle) {
			out = append(out, t.Handle)
		}
	}
	return out
}

// TeamHandles is the handles (no @) of the active roster teams of org
// folder folder in this machine's clone of the store at storeURL, at its
// HEAD: what a team audience may name (D-727). Empty when the clone or
// its roster has none.
func TeamHandles(ctx context.Context, home, storeURL, folder string) []string {
	norm, err := repourl.NormalizeStore(storeURL)
	if err != nil {
		return nil
	}
	dir, _ := cloneDir(home, norm)
	return teamHandlesIn(ctx, dir, folder)
}

// teamHandlesIn is TeamHandles of the clone dir.
func teamHandlesIn(ctx context.Context, dir, folder string) []string {
	var out []string
	for _, t := range rosterRowsAt(ctx, dir, "HEAD", rosterBase(folder), schema.TableTeams, "") {
		if t.Handle != "" && t.LeftAt == "" && (t.Active == nil || *t.Active) && !slices.Contains(out, t.Handle) {
			out = append(out, t.Handle)
		}
	}
	slices.Sort(out)
	return out
}

// memberIDs is a teams row's member_actor_ids: a JSON list of act_ ids,
// or (v0.1.17 rows) a string holding that JSON list.
func (r rosterRow) memberIDs() []string {
	if len(r.Members) == 0 {
		return nil
	}
	var ids []string
	if json.Unmarshal(r.Members, &ids) == nil {
		return ids
	}
	var s string
	if json.Unmarshal(r.Members, &s) == nil && json.Unmarshal([]byte(s), &ids) == nil {
		return ids
	}
	return nil
}

// hasActorsAnywhere reports whether the tree at rev holds an actor row in
// any folder.
func hasActorsAnywhere(ctx context.Context, dir, rev string) bool {
	out, err := gitRun(ctx, "-C", dir, "ls-tree", "-r", "-z", "--name-only", rev)
	if err != nil {
		return false
	}
	for _, f := range strings.Split(out, "\x00") {
		if d := path.Base(path.Dir(f)); d == schema.TableActors && path.Ext(f) == ".json" {
			return true
		}
	}
	return false
}

// orgRoleAdmin is actor_periods.org_role of an org admin (ops/team
// OrgRoleAdmin; an adapter cannot import ops).
const orgRoleAdmin = "admin"
