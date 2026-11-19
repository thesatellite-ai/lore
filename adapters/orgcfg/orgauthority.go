package orgcfg

// orgauthority.go — lore-org.yaml is the org's own config (its store
// list, own:/repos: patterns, defaults), and changing it is an admin's
// change (devco R-7, coordinator decision C30). A store is a plain git
// repo every member can push to, so a reader never trusts the file at the
// branch tip as it stands: it replays the file's history and accepts each
// change only from a commit whose git author is an admin of the org in
// the roster at that commit's parent.
//
//	commits touching the org file, oldest first (no merges)
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

// orgFileCommit is one commit of the org file's history.
type orgFileCommit struct {
	sha, parent, author, email, subject string
}

// acceptedOrgFile replays the history of the org file of folder up to tip
// (every place it was kept: orgFilePaths, so a layout move is one file)
// and returns the commit whose version of the file is the accepted one
// and the changes held back. With nothing held back it returns "", nil:
// the tip's file stands. A history git cannot list is read as accepted
// (best effort: never brick a store over a log error).
func acceptedOrgFile(ctx context.Context, dir, tip, folder string) (string, []HeldOrgChange) {
	paths := orgFilePaths(folder)
	args := append([]string{"-C", dir, "log", "--reverse", "--topo-order", "--no-merges", "--format=%H%x1f%P%x1f%an%x1f%ae%x1f%s", tip, "--"}, paths...)
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
		parent, _, _ := strings.Cut(f[1], " ")
		commits = append(commits, orgFileCommit{sha: f[0], parent: parent, author: f[2], email: f[3], subject: f[4]})
	}
	var held []HeldOrgChange
	accepted := "" // the commit whose file is accepted so far
	var acceptedDoc *OrgFileDoc
	for _, c := range commits {
		raw, p, ok := readFirstBlob(ctx, dir, c.sha, paths)
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
			held = append(held, HeldOrgChange{Commit: short, Author: c.author, Email: c.email, Subject: c.subject, Path: p,
				Why: fmt.Sprintf(heldOrgWhy, c.author, short)})
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
func acceptedOrgRaw(ctx context.Context, dir, tip, folder string, raw []byte) []byte {
	at, held := acceptedOrgFile(ctx, dir, tip, folder)
	if len(held) == 0 || at == "" {
		return raw
	}
	if b, _, ok := readFirstBlob(ctx, dir, at, orgFilePaths(folder)); ok {
		return []byte(b)
	}
	return raw
}

// readFirstBlob is the first of paths that exists at rev, with its body.
func readFirstBlob(ctx context.Context, dir, rev string, paths []string) (string, string, bool) {
	for _, p := range paths {
		if raw, err := gitRun(ctx, "-C", dir, "cat-file", "blob", rev+":"+p); err == nil {
			return raw, p, true
		}
	}
	return "", "", false
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
