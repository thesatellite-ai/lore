package git

// authority.go — every reader checks who may change the roster (devco
// R-5, R-6, R-18; coordinator decision C30). A store clone is plain git:
// anyone with push access can commit any file, with or without lore. The
// writer's own lore refuses a member's roster change and a leaver's
// write, but a hand-made commit skipped that check and every teammate's
// lore believed it (a member made himself admin; a member offboarded a
// teammate; a person already offboarded kept adding company notes).
//
//	read of a clone ─▶ authorityOf(HEAD): replay the store's commits
//	  oldest first (merges skipped: they only join two lines of commits),
//	  keeping the roster as the accepted commits left it
//	  ┌ commit author (git author email ─▶ actor via stable_key/contacts)
//	  │   left at that point (offboarded, no open stint) ─▶ every row the
//	  │     commit changes is held back
//	  │   not an admin, and the commit changes a stint (actor_periods),
//	  │     another person's actor or contact row, or an invite not
//	  │     theirs ─▶ held back
//	  │   not an admin nor a lead of the project, and the commit changes
//	  │     who is on a project or team (project_members, teams; their
//	  │     own row too) beyond what their pending invite names ─▶ held
//	  │     back (devco D-328)
//	  └ otherwise accepted: its files become the trusted version
//	─▶ readCommits / Get serve a held file as its last accepted version
//	   (or not at all when no commit of it was accepted): not in lists,
//	   not rendered, not in the agent's context
//	─▶ Held() names each one (file, commit, author, why) for sync and
//	   doctor; an accepted commit that touches the file later (an admin
//	   making the change themselves, or reverting it) ends the hold
//
// The same replay checks every row file a commit changes (devco R-4,
// R-15, round-6 follow-up):
//
//	  ┌ the writer a row states (updated_by; created_by and
//	  │   created_by_actor_id on a new file) is not the commit's author,
//	  │   not an agent the author runs, and the row is not a version the
//	  │   stated writer committed before, same stamp (_hlc) and same
//	  │   content (a conflict settled with `lore sync resolve`, a partial
//	  │   push or a revert carries another person's edit with its stamp;
//	  │   devco D-329) ─▶ that file is held back
//	  └ the author's open stint is a contractor's and the file is neither
//	      one of their own roster rows nor in a folder that holds one of
//	      their projects ─▶ that file is held back
//
// A merge commit is checked for the files whose merged version neither
// parent had (git log -c): lore's own field merge keeps the larger
// side's stamp, so a merged row whose stamp an earlier accepted version
// carries passes; anything else is judged as the merger's own change.
// A store with no org folder (a client store) is checked against the
// roster of its org's store (SetRosterSource): the contractor wall and
// the stated writer, with that roster as it is now.
//
// Cost: one `git log` of the whole history and one `git cat-file
// --batch` of every row version, per clone, per new HEAD, cached in
// memory for the process. A store with a long history pays it on the
// first read of each command after a sync.
//
// While the roster has no admin yet (a personal store, or an org's first
// commits) every change is accepted. Git authorship can be forged by
// anyone who can push: this raises the bar, it is not proof (signed
// changes are a separate feature).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// HeldChange is one row file a reader holds back because the commit that
// last changed it had no authority to (Backend.Held).
type HeldChange struct {
	Path   string // root-relative file path
	Table  string // the row's table (folder name)
	ID     string // the row id (file name without .json)
	Commit string // the refused commit (full id)
	Author string // its git author email
	Reason string // why, in plain words
}

// Short is the refused commit's abbreviated id.
func (h HeldChange) Short() string {
	if len(h.Commit) > shortCommitLen {
		return h.Commit[:shortCommitLen]
	}
	return h.Commit
}

// shortCommitLen is how many hex digits a printed commit id keeps.
const shortCommitLen = 7

// orgRoleAdmin is the org_role value of an admin stint (ops/team
// OrgRoleAdmin; an L3 adapter cannot import ops).
const orgRoleAdmin = "admin"

// humanKeyPrefix starts a person's actors.stable_key ("human:<email>").
const humanKeyPrefix = "human:"

// zeroBlob is git's all-zero object id: the file is absent on that side.
func zeroBlob(sha string) bool { return strings.Trim(sha, "0") == "" }

// rosterDoc is the part of a roster row file the check reads.
type rosterDoc struct {
	ID        string          `json:"id"`
	Deleted   json.RawMessage `json:"_deleted,omitempty"`
	ActorID   string          `json:"actor_id"`
	Type      string          `json:"type"`
	OrgRole   string          `json:"org_role"`
	StartedOn string          `json:"started_on"`
	EndedOn   string          `json:"ended_on"`
	Status    string          `json:"status"`
	StableKey string          `json:"stable_key"`
	Owner     string          `json:"owner_actor_id"`
	ValueNorm string          `json:"value_norm"`
	Email     string          `json:"email"`
	// the row-author check (R-4) and the contractor wall (R-15)
	DisplayName string `json:"display_name"`
	UpdatedBy   string `json:"updated_by"`
	CreatedBy   string `json:"created_by"`
	CreatedByID string `json:"created_by_actor_id"`
	HLC         string `json:"_hlc"`
	ProjectID   string `json:"project_id"`
	// project and team membership (devco D-328): a membership's roles and
	// stint, a team's handle and actors, an invite's projects and teams
	Roles    strList         `json:"roles"`
	Members  strList         `json:"member_actor_ids"`
	Projects strList         `json:"projects"`
	Teams    strList         `json:"teams"`
	Handle   string          `json:"handle"`
	Active   json.RawMessage `json:"active,omitempty"`
	LeftAt   string          `json:"left_at"`
	// content is the row's content key (contentKey), not a file field:
	// the stamp carry is accepted only for content the stated writer
	// committed (devco D-329)
	content string
}

// strList is a list of text in a row file: a JSON array, or (v0.1.17
// rows) a string holding one.
type strList []string

// UnmarshalJSON reads either shape; anything else is an empty list.
func (l *strList) UnmarshalJSON(b []byte) error {
	var list []string
	if json.Unmarshal(b, &list) == nil {
		*l = list
		return nil
	}
	var inner string
	if json.Unmarshal(b, &inner) == nil && json.Unmarshal([]byte(inner), &list) == nil {
		*l = list
		return nil
	}
	*l = nil
	return nil
}

// has reports whether v is in l.
func (l strList) has(v string) bool { return v != "" && slices.Contains(l, v) }

// contentFormatKey is the row file key a format migration rewrites
// without changing the row ("_v"): content keys leave it out.
const contentFormatKey = "_v"

// contentKey is a digest of a row file's content: its keys sorted, each
// value compacted, the format version left out. Two files with the same
// row content (a revert, a partial push, a conflict settled with the
// other side's version, re-encoded by another lore) have the same key.
func contentKey(raw []byte) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(raw), &obj) != nil {
		return ""
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if k != contentFormatKey {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	h := sha256.New()
	var buf bytes.Buffer
	for _, k := range keys {
		buf.Reset()
		if json.Compact(&buf, obj[k]) != nil {
			buf.Write(obj[k])
		}
		h.Write([]byte(strconv.Quote(k)))
		h.Write([]byte{0})
		h.Write(buf.Bytes())
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// deleted reports a tombstone.
func (d rosterDoc) deleted() bool {
	s := strings.TrimSpace(string(d.Deleted))
	return s != "" && s != "null" && s != "false"
}

// stintFields are a stint's fields only an admin may change.
func (d rosterDoc) stintFields() [6]string {
	return [6]string{d.ActorID, d.Type, d.OrgRole, d.StartedOn, d.EndedOn, string(d.Deleted)}
}

// rosterTables are the tables whose rows the replay keeps.
var rosterTables = map[string]bool{
	schema.TableActorPeriods: true, schema.TableActors: true,
	schema.TableActorContacts: true, schema.TableInvites: true,
}

// wallTables are the further tables the replay keeps for the contractor
// wall: who is a member of which project, and which folder holds which
// project.
var wallTables = map[string]bool{schema.TableProjectMembers: true, schema.TableProjects: true, schema.TableTeams: true}

// membershipTables are the tables that say who is on a project or a
// team: only an admin, or a lead of that project, changes them, whoever's
// row it is (devco D-328). The one exception is a person taking up what
// a pending invite of theirs names (lore team join).
var membershipTables = map[string]bool{schema.TableProjectMembers: true, schema.TableTeams: true}

// kept reports whether the replay keeps table's rows in its state.
func kept(table string) bool { return rosterTables[table] || wallTables[table] }

// rosterState is the roster as the accepted commits left it.
type rosterState struct {
	docs map[string]rosterDoc // path ─▶ row (kept tables only)
	// stamps maps each row file to the _hlc stamps of its accepted
	// versions: the actors whose commits wrote them and the content each
	// version had
	stamps map[string]map[string][]stampRec
	// orgless is set for a store with no org folder checked against its
	// org's roster (SetRosterSource): no admin-only roster rules apply
	orgless bool
}

// newRosterState is an empty state.
func newRosterState() *rosterState {
	return &rosterState{docs: map[string]rosterDoc{}, stamps: map[string]map[string][]stampRec{}}
}

// stampRec is one accepted version of a row file with a given stamp: who
// committed it and its content key.
type stampRec struct{ actor, content string }

// stamp records that actor's accepted commit wrote p with stamp hlc and
// content key content.
func (s *rosterState) stamp(p, hlc, actor, content string) {
	if hlc == "" {
		return
	}
	if s.stamps[p] == nil {
		s.stamps[p] = map[string][]stampRec{}
	}
	s.stamps[p][hlc] = append(s.stamps[p][hlc], stampRec{actor: actor, content: content})
}

// stampKnown reports whether an accepted version of p carries hlc.
func (s *rosterState) stampKnown(p, hlc string) bool {
	_, ok := s.stamps[p][hlc]
	return ok && hlc != ""
}

// stampBy reports whether an accepted version of p with stamp hlc and
// the same content was committed by claimed, or by the person who runs
// the agent claimed. The content must match, not only the stamp: an old
// stamp of the stated writer on changed content is a forgery (devco
// D-329).
func (s *rosterState) stampBy(p, hlc, claimed, content string) bool {
	for _, r := range s.stamps[p][hlc] {
		if r.actor != "" && content != "" && r.content == content && (r.actor == claimed || s.owns(r.actor, claimed)) {
			return true
		}
	}
	return false
}

// leads reports whether actor has an open membership of project with the
// lead role.
func (s *rosterState) leads(actor, project string) bool {
	if actor == "" || project == "" {
		return false
	}
	for _, d := range s.byTable(schema.TableProjectMembers) {
		if d.ActorID == actor && d.ProjectID == project && d.EndedOn == "" && d.Roles.has(teamprofile.RoleLead) {
			return true
		}
	}
	return false
}

// pendingInvite is the pending invite for email, if any.
func (s *rosterState) pendingInvite(email string) (rosterDoc, bool) {
	for _, d := range s.byTable(schema.TableInvites) {
		if email != "" && strings.EqualFold(d.Email, email) && d.Status == schema.InviteStatusPending {
			return d, true
		}
	}
	return rosterDoc{}, false
}

// name is actor's display name, else the id.
func (s *rosterState) name(actor string) string {
	for _, d := range s.byTable(schema.TableActors) {
		if d.ID == actor && d.DisplayName != "" {
			return d.DisplayName
		}
	}
	return actor
}

// known reports whether actor is on the roster.
func (s *rosterState) known(actor string) bool {
	for _, d := range s.byTable(schema.TableActors) {
		if d.ID == actor {
			return true
		}
	}
	return false
}

// contractor reports whether actor's open stint is a contractor's.
func (s *rosterState) contractor(actor string) bool {
	for _, d := range s.byTable(schema.TableActorPeriods) {
		if d.ActorID == actor && d.EndedOn == "" && d.Type == schema.PeriodTypeContractor {
			return true
		}
	}
	return false
}

// folderHoldsProjectOf reports whether folder holds a projects row of a
// project actor is a member of.
func (s *rosterState) folderHoldsProjectOf(folder, actor string) bool {
	member := map[string]bool{}
	for _, d := range s.byTable(schema.TableProjectMembers) {
		if d.ActorID == actor && d.ProjectID != "" {
			member[d.ProjectID] = true
		}
	}
	for p, d := range s.docs {
		if tableOfPath(p) == schema.TableProjects && !d.deleted() && member[d.ID] {
			if f, _, ok := rowPlace(p); ok && f == folder {
				return true
			}
		}
	}
	return false
}

// byTable lists the live rows of table.
func (s *rosterState) byTable(table string) []rosterDoc {
	var out []rosterDoc
	for p, d := range s.docs {
		if tableOfPath(p) == table && !d.deleted() {
			out = append(out, d)
		}
	}
	return out
}

// hasAdmin reports whether any open stint is an admin's.
func (s *rosterState) hasAdmin() bool {
	for _, d := range s.byTable(schema.TableActorPeriods) {
		if d.EndedOn == "" && d.OrgRole == orgRoleAdmin {
			return true
		}
	}
	return false
}

// actorOf is the actor id whose stable key or email contact is email.
func (s *rosterState) actorOf(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	for _, d := range s.byTable(schema.TableActors) {
		if strings.EqualFold(d.StableKey, humanKeyPrefix+email) {
			return d.ID
		}
	}
	for _, d := range s.byTable(schema.TableActorContacts) {
		if strings.EqualFold(d.ValueNorm, email) && d.ActorID != "" {
			return d.ActorID
		}
	}
	return ""
}

// isAdmin reports whether actor has an open admin stint.
func (s *rosterState) isAdmin(actor string) bool {
	for _, d := range s.byTable(schema.TableActorPeriods) {
		if d.ActorID == actor && d.EndedOn == "" && d.OrgRole == orgRoleAdmin {
			return true
		}
	}
	return false
}

// left reports whether actor is marked left: status left, or stints on
// the roster and none of them open.
func (s *rosterState) left(actor string) bool {
	for _, d := range s.byTable(schema.TableActors) {
		if d.ID == actor && d.Status == schema.ActorStatusLeft {
			return true
		}
	}
	stints, open := 0, false
	for _, d := range s.byTable(schema.TableActorPeriods) {
		if d.ActorID == actor {
			stints++
			open = open || d.EndedOn == ""
		}
	}
	return stints > 0 && !open
}

// stints counts actor's stints.
func (s *rosterState) stints(actor string) int {
	n := 0
	for _, d := range s.byTable(schema.TableActorPeriods) {
		if d.ActorID == actor {
			n++
		}
	}
	return n
}

// invited reports whether an invite for email exists.
func (s *rosterState) invited(email string) bool {
	for _, d := range s.byTable(schema.TableInvites) {
		if email != "" && strings.EqualFold(d.Email, email) {
			return true
		}
	}
	return false
}

// tableOfPath is the table folder of a row file path ("org/actors/act_x.json" ─▶ "actors").
func tableOfPath(p string) string { return path.Base(path.Dir(p)) }

// isRowFile reports whether p looks like a row file (<…>/<table>/<id>.json).
func isRowFile(p string) bool {
	return strings.HasSuffix(p, ".json") && strings.Contains(p, "/") && !strings.HasPrefix(path.Base(p), "_")
}

// fileChange is one file a commit changed.
type fileChange struct {
	path    string
	oldBlob string
	newBlob string
}

// replayCommit is one non-merge commit of the replay.
type replayCommit struct {
	id      string
	author  string
	changes []fileChange
	// merge marks a merge commit: changes are then only the files whose
	// merged version neither parent had (git log -c)
	merge bool
}

// authority is the replay's result at one HEAD.
type authority struct {
	head    string
	orgHead string                // the org store's HEAD a roster-sourced replay used
	state   *rosterState          // the roster as the accepted commits left it
	held    map[string]HeldChange // path ─▶ hold
	trusted map[string]string     // held path ─▶ last accepted blob ("" = none)
	refused map[string]bool       // every commit the replay refused
	// admins is who is an admin after each commit, by the trusted roster
	// (AdminAt): one source of "admin at a commit" for the roster check
	// here and the org file check of adapters/orgcfg
	admins map[string]*adminSet
}

// adminSet is the admins of the trusted roster at one point of the replay.
type adminSet struct {
	emails   map[string]bool // every email of an admin (stable key, contacts)
	anyAdmin bool
}

// admins is the admin set of s now.
func (s *rosterState) adminSnapshot() *adminSet {
	out := &adminSet{emails: map[string]bool{}}
	admin := map[string]bool{}
	for _, d := range s.byTable(schema.TableActorPeriods) {
		if d.EndedOn == "" && d.OrgRole == orgRoleAdmin && d.ActorID != "" {
			admin[d.ActorID], out.anyAdmin = true, true
		}
	}
	for _, d := range s.byTable(schema.TableActors) {
		if e, ok := strings.CutPrefix(strings.ToLower(d.StableKey), humanKeyPrefix); ok && admin[d.ID] {
			out.emails[e] = true
		}
	}
	for _, d := range s.byTable(schema.TableActorContacts) {
		if admin[d.ActorID] && d.ValueNorm != "" {
			out.emails[strings.ToLower(d.ValueNorm)] = true
		}
	}
	return out
}

// AdminAt reports whether email is an admin of the org after commit rev
// of the store clone at dir, by the trusted roster of the replay (a
// member's forged admin stint never counts). decided is false when the
// replay does not reach rev or its roster names no admin yet (an org from
// before org roles, or its first commits): the caller applies its own
// rule then. Why: the org file check (adapters/orgcfg) and the roster
// check must agree on who is admin at a commit (devco R-5, R-7).
func AdminAt(ctx context.Context, dir, rev, email string) (admin, decided bool) {
	if dir == "" || rev == "" || email == "" {
		return false, false
	}
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		return false, false
	}
	b := &Backend{git: runner{bin: bin}, work: dir, mode: modeClone}
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return false, false
	}
	a, err := b.authorityOf(ctx, h)
	if err != nil || a == nil {
		return false, false
	}
	set, ok := a.admins[rev]
	if !ok || !set.anyAdmin {
		return false, false
	}
	return set.emails[strings.ToLower(strings.TrimSpace(email))], true
}

// authorityCache keeps the last replay per clone (folders of one clone share it).
var authorityCache = struct {
	sync.Mutex
	byWork map[string]*authority
}{byWork: map[string]*authority{}}

// authorityOf replays the clone's history up to head. A failure of git
// holds nothing back (the read goes on as before) and is returned for
// the caller to report.
func (b *Backend) authorityOf(ctx context.Context, head string) (*authority, error) {
	if b.mode != modeClone || head == "" {
		return nil, nil
	}
	authorityCache.Lock()
	a := authorityCache.byWork[b.work]
	authorityCache.Unlock()
	if a != nil && a.head == head && a.orgHead == b.rosterHead(ctx) {
		return a, nil
	}
	commits, err := b.replayCommits(ctx, head)
	if err != nil {
		return nil, err
	}
	a, err = b.replay(ctx, head, commits)
	if err != nil {
		return nil, err
	}
	authorityCache.Lock()
	authorityCache.byWork[b.work] = a
	authorityCache.Unlock()
	return a, nil
}

// commitMark starts each commit's record in replayCommits' log output.
const commitMark = "\x01"

// replayCommits lists head's non-merge commits oldest first with the
// files each changed (blob ids on both sides).
func (b *Backend) replayCommits(ctx context.Context, head string) ([]replayCommit, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "--reverse", "--topo-order", "-c", "--no-renames",
		"--raw", "--no-abbrev", "-z", "--format=" + "%x01%H%x00%ae%x00", head, "--"}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the store's history for the roster check", err)
	}
	var commits []replayCommit
	for _, rec := range strings.Split(string(out), commitMark) {
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x00", 3)
		if len(parts) < 3 {
			continue
		}
		c := replayCommit{id: parts[0], author: strings.ToLower(parts[1])}
		fields := strings.Split(strings.TrimLeft(parts[2], "\x00\n"), "\x00")
		for i := 0; i+1 < len(fields); i += 2 {
			// ":<modes…> <blobs…> <status>": one colon per parent, then
			// parents+1 modes and parents+1 blobs (the last is the result)
			raw := strings.TrimSpace(fields[i])
			n := len(raw) - len(strings.TrimLeft(raw, ":"))
			meta := strings.Fields(strings.TrimLeft(raw, ":"))
			if n < 1 || len(meta) < 2*n+3 {
				continue
			}
			c.merge = c.merge || n > 1
			c.changes = append(c.changes, fileChange{path: fields[i+1], oldBlob: meta[n+1], newBlob: meta[2*n+1]})
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// replay walks commits and decides each one.
func (b *Backend) replay(ctx context.Context, head string, commits []replayCommit) (*authority, error) {
	// read every roster blob once, up front
	var names []string
	seen := map[string]bool{}
	for _, c := range commits {
		for _, f := range c.changes {
			if isRowFile(f.path) && !zeroBlob(f.newBlob) && !seen[f.newBlob] {
				seen[f.newBlob] = true
				names = append(names, f.newBlob)
			}
		}
	}
	raws, err := b.readBlobs(ctx, names)
	if err != nil {
		return nil, err
	}
	parsed := make(map[string]rosterDoc, len(raws))
	for sha, raw := range raws {
		var d rosterDoc
		if json.Unmarshal(bytes.TrimSpace(raw), &d) == nil {
			d.content = contentKey(raw)
			parsed[sha] = d
		}
	}
	raws = nil // only the fields the check reads stay in memory
	docOf := func(sha string) (rosterDoc, bool) {
		d, ok := parsed[sha]
		return d, ok
	}
	st := newRosterState()
	orgHead := ""
	if src := b.rosterSource(); src != nil {
		// a store with no org folder: its org's roster, as it is now
		base, h, serr := src(ctx)
		if serr != nil {
			return nil, serr
		}
		if base != nil {
			st, orgHead = base.forOrgless(), h
		}
	}
	a := &authority{head: head, orgHead: orgHead, state: st, admins: map[string]*adminSet{}, held: map[string]HeldChange{}, trusted: map[string]string{}, refused: map[string]bool{}}
	accepted := map[string]string{} // path ─▶ last accepted blob ("" = absent)
	snap := st.adminSnapshot()
	for _, c := range commits {
		rosterMoved := false
		reason := ""
		if !c.merge && !st.orgless {
			reason = st.refusal(c, docOf)
		}
		actor := st.authorActor(c, docOf)
		for _, f := range c.changes {
			if !isRowFile(f.path) {
				continue
			}
			reason := reason
			if reason == "" {
				reason = st.fileRefusal(c, actor, f, docOf)
			}
			if reason != "" {
				a.refused[c.id] = true
			}
			if reason != "" && f.newBlob == accepted[f.path] {
				// back to the accepted content (a revert of a held change):
				// nothing to hold
				delete(a.held, f.path)
				delete(a.trusted, f.path)
				continue
			}
			if reason != "" {
				if _, ok := a.held[f.path]; !ok {
					prev, known := accepted[f.path]
					if !known && !zeroBlob(f.oldBlob) {
						prev = f.oldBlob
					}
					a.trusted[f.path] = prev
				}
				a.held[f.path] = HeldChange{Path: f.path, Table: tableOfPath(f.path), ID: strings.TrimSuffix(path.Base(f.path), ".json"),
					Commit: c.id, Author: c.author, Reason: reason}
				continue
			}
			delete(a.held, f.path)
			delete(a.trusted, f.path)
			rosterMoved = rosterMoved || rosterTables[tableOfPath(f.path)]
			if zeroBlob(f.newBlob) {
				accepted[f.path] = ""
				delete(st.docs, f.path)
				continue
			}
			accepted[f.path] = f.newBlob
			if d, ok := docOf(f.newBlob); ok {
				st.stamp(f.path, d.HLC, actor, d.content)
				if t := tableOfPath(f.path); kept(t) && (!st.orgless || t == schema.TableProjects) {
					st.docs[f.path] = d
				}
			}
		}
		if rosterMoved {
			snap = st.adminSnapshot()
		}
		a.admins[c.id] = snap
	}
	return a, nil
}

// refusal is why commit c has no authority over what it changes ("" =
// accepted), judged on the roster before it.
func (s *rosterState) refusal(c replayCommit, docOf func(string) (rosterDoc, bool)) string {
	if !s.hasAdmin() {
		return "" // no admin yet: a personal store, or the org's first commits
	}
	actor := s.authorActor(c, docOf)
	if actor != "" && s.isAdmin(actor) {
		return ""
	}
	invited := s.invited(c.author)
	if actor != "" && s.left(actor) && !invited {
		return "written by " + c.author + " after they were marked as left"
	}
	for _, f := range c.changes {
		table := tableOfPath(f.path)
		if (!rosterTables[table] && !membershipTables[table]) || !isRowFile(f.path) {
			continue
		}
		old, hadOld := s.docs[f.path]
		next, hasNext := docOf(f.newBlob)
		if zeroBlob(f.newBlob) {
			hasNext = false
		}
		switch table {
		case schema.TableActorPeriods:
			if why := s.stintRefusal(actor, invited, old, hadOld, next, hasNext); why != "" {
				return why
			}
		case schema.TableActors:
			subject := next
			if !hasNext {
				subject = old
			}
			if actor == "" || (subject.ID != actor && subject.Owner != actor) {
				return "changes another person's roster entry and " + c.author + " is not an admin"
			}
			if hadOld && hasNext && old.Status != next.Status && !invited && (gone(old.Status) || gone(next.Status)) {
				return "changes a roster status and " + c.author + " is not an admin"
			}
		case schema.TableActorContacts:
			subject := next
			if !hasNext {
				subject = old
			}
			if actor == "" || (subject.ActorID != actor && !s.owns(actor, subject.ActorID)) {
				return "changes another person's contacts and " + c.author + " is not an admin"
			}
		case schema.TableInvites:
			subject := next
			if !hasNext {
				subject = old
			}
			if !hadOld || !strings.EqualFold(subject.Email, c.author) {
				return "changes an invite and " + c.author + " is not an admin"
			}
		case schema.TableProjectMembers, schema.TableTeams:
			if why := s.membershipRefusal(c.author, actor, table, old, hadOld, next, hasNext); why != "" {
				return why
			}
		}
	}
	return ""
}

// membershipRefusal judges a non-admin's change of one project_members
// or teams row (devco D-328): a lead of the row's project may make it;
// otherwise only a person taking up their own pending invite (their own
// membership of a project the invite names, with roles it or the stint
// already gave; or adding only themselves to a team it names). Anything
// else, a person's own row included, is held back: a hand-made
// membership row would otherwise open another project's folders to a
// contractor.
func (s *rosterState) membershipRefusal(author, actor, table string, old rosterDoc, hadOld bool, next rosterDoc, hasNext bool) string {
	what := "who is on a project"
	if table == schema.TableTeams {
		what = "who is on a team"
	}
	why := "changes " + what + " and " + author + " is neither an admin nor a lead of that project"
	if actor == "" {
		return why
	}
	if (hadOld || hasNext) && (!hadOld || s.leads(actor, old.ProjectID)) && (!hasNext || s.leads(actor, next.ProjectID)) {
		return ""
	}
	inv, ok := s.pendingInvite(author)
	if !ok || !hasNext || next.deleted() {
		return why
	}
	if table == schema.TableProjectMembers {
		own := next.ActorID == actor && (!hadOld || (old.ActorID == actor && old.ProjectID == next.ProjectID && !old.deleted()))
		if !own || next.EndedOn != "" || !inv.Projects.has(next.ProjectID) {
			return why
		}
		for _, r := range next.Roles {
			if !inv.Roles.has(r) && !(hadOld && old.Roles.has(r)) {
				return why
			}
		}
		return ""
	}
	// a team: the joiner adds only themselves to a team the invite names
	if !hadOld || old.deleted() || !inv.Teams.has(old.Handle) || !sameTeam(old, next) {
		return why
	}
	for _, m := range next.Members {
		if m != actor && !old.Members.has(m) {
			return why
		}
	}
	for _, m := range old.Members {
		if !next.Members.has(m) {
			return why
		}
	}
	return ""
}

// sameTeam reports whether two versions of a team row differ at most in
// their actors.
func sameTeam(a, b rosterDoc) bool {
	return a.Handle == b.Handle && a.ProjectID == b.ProjectID && a.DisplayName == b.DisplayName &&
		string(a.Active) == string(b.Active) && a.LeftAt == b.LeftAt && string(a.Deleted) == string(b.Deleted)
}

// authorActor is the actor of commit c's author: by the roster before
// it, or, for a first join, by the actors row the commit itself adds.
func (s *rosterState) authorActor(c replayCommit, docOf func(string) (rosterDoc, bool)) string {
	if actor := s.actorOf(c.author); actor != "" {
		return actor
	}
	for _, f := range c.changes {
		if tableOfPath(f.path) != schema.TableActors || zeroBlob(f.newBlob) {
			continue
		}
		if d, ok := docOf(f.newBlob); ok && strings.EqualFold(d.StableKey, humanKeyPrefix+c.author) {
			return d.ID
		}
	}
	return ""
}

// fileRefusal is why one file of commit c may not be read ("" = it may):
// the writer the row states is not the author (R-4), or the author is a
// contractor writing outside their own roster rows and projects (R-15).
func (s *rosterState) fileRefusal(c replayCommit, actor string, f fileChange, docOf func(string) (rosterDoc, bool)) string {
	if zeroBlob(f.newBlob) || (!s.orgless && !s.hasAdmin()) {
		return "" // a removed file (rowfs reports a missing row), or no org yet
	}
	d, ok := docOf(f.newBlob)
	if !ok {
		return "" // not a row: rowfs reports the file
	}
	if c.merge && s.stampKnown(f.path, d.HLC) {
		return "" // lore's field merge keeps an accepted side's stamp
	}
	if actor != "" && s.isAdmin(actor) {
		return ""
	}
	claimed := []string{d.UpdatedBy}
	if zeroBlob(f.oldBlob) {
		claimed = append(claimed, d.CreatedBy, d.CreatedByID)
	}
	for _, w := range claimed {
		if w == "" || w == actor || (actor != "" && s.owns(actor, w)) || s.stampBy(f.path, d.HLC, w, d.content) {
			continue
		}
		if actor == "" && !s.known(w) {
			continue // neither side is on the roster: nothing to compare
		}
		return "claims to be written by " + s.name(w) + ", but the commit's author is " + c.author
	}
	if actor == "" || !s.contractor(actor) {
		return ""
	}
	switch tableOfPath(f.path) {
	case schema.TableActors:
		if d.ID == actor || d.Owner == actor {
			return ""
		}
	case schema.TableActorContacts, schema.TableActorPeriods:
		if d.ActorID == actor {
			return ""
		}
	case schema.TableProjectMembers, schema.TableTeams:
		// refusal already allowed it (a lead's change, or their own invite
		// taken up); a membership row lives in the org folder
		if d.ActorID == actor || d.Members.has(actor) || s.leads(actor, d.ProjectID) {
			return ""
		}
	case schema.TableInvites:
		if strings.EqualFold(d.Email, c.author) {
			return ""
		}
	}
	if folder, _, ok := rowPlace(f.path); ok && s.folderHoldsProjectOf(folder, actor) {
		return ""
	}
	return "written by " + c.author + ", a contractor in this org, outside their projects' folders"
}

// forOrgless is a copy of s's roster for checking a store with no org
// folder: roster rows only, no stamps, orgless set.
func (s *rosterState) forOrgless() *rosterState {
	out := newRosterState()
	out.orgless = true
	for p, d := range s.docs {
		if t := tableOfPath(p); rosterTables[t] || t == schema.TableProjectMembers {
			out.docs[p] = d
		}
	}
	return out
}

// rosterFunc gives the org's trusted roster and the org store's HEAD.
type rosterFunc func(ctx context.Context) (*rosterState, string, error)

// SetRosterSource makes org (the clone of the store that holds the org
// folder) the roster this clone's rows are checked against: a client
// store has no roster of its own, and without one a contractor's hand
// commit there was read by everyone (devco R-15). Nil, or a backend that
// is not a clone with an org folder, removes the source. Call it once
// after open, before the first read.
func (b *Backend) SetRosterSource(org *Backend) {
	b.rosterMu.Lock()
	defer b.rosterMu.Unlock()
	if org == nil || org == b || org.mode != modeClone || org.marker.OrgFolder == "" || b.marker.OrgFolder != "" {
		b.rosterFrom = nil
		return
	}
	b.rosterFrom = func(ctx context.Context) (*rosterState, string, error) {
		h, err := org.head(ctx)
		if err != nil || h == "" {
			return nil, "", err
		}
		a, err := org.authorityOf(ctx, h)
		if err != nil || a == nil {
			return nil, h, err
		}
		return a.state, h, nil
	}
}

// rosterSource is the SetRosterSource function, nil when none.
func (b *Backend) rosterSource() rosterFunc {
	b.rosterMu.Lock()
	defer b.rosterMu.Unlock()
	return b.rosterFrom
}

// rosterHead is the roster source's HEAD ("" without one), so a cached
// replay is redone when the org's roster moved.
func (b *Backend) rosterHead(ctx context.Context) string {
	src := b.rosterSource()
	if src == nil {
		return ""
	}
	_, h, _ := src(ctx)
	return h
}

// HasOrgFolder reports whether this clone's store holds the org folder
// (lore-store.yaml org_folder).
func (b *Backend) HasOrgFolder() bool { return b.mode == modeClone && b.marker.OrgFolder != "" }

// stintRefusal judges a non-admin's change of one actor_periods row.
func (s *rosterState) stintRefusal(actor string, invited bool, old rosterDoc, hadOld bool, next rosterDoc, hasNext bool) string {
	const who = "changes the roster (a stint) and the author is not an admin"
	switch {
	case actor == "":
		return who
	case !hasNext: // a removed file
		return who
	case next.ActorID != actor:
		return "changes another person's stint and the author is not an admin"
	case next.OrgRole == orgRoleAdmin && (!hadOld || old.OrgRole != orgRoleAdmin):
		return "makes its author an admin and the author is not an admin"
	case !hadOld:
		// a person's own first stint (a join), or a rejoin an admin invited
		if s.stints(actor) == 0 || invited {
			return ""
		}
		return who
	case old.stintFields() != next.stintFields() && !invited:
		return who
	}
	return ""
}

// gone reports a status only an admin sets or lifts (left, archived);
// a person may mark their own leave.
func gone(status string) bool {
	return status == schema.ActorStatusLeft || status == schema.ActorStatusArchived
}

// owns reports whether actor owns agent (actors.owner_actor_id).
func (s *rosterState) owns(actor, agent string) bool {
	for _, d := range s.byTable(schema.TableActors) {
		if d.ID == agent && d.Owner == actor {
			return true
		}
	}
	return false
}

// Held lists the row files this clone's reads hold back, by path, in
// every folder of the clone. Best
// effort: a history that cannot be read holds nothing back and lists
// nothing.
func (b *Backend) Held(ctx context.Context) ([]HeldChange, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	h, err := b.head(ctx)
	if err != nil {
		return nil, err
	}
	a, err := b.authorityOf(ctx, h)
	if err != nil || a == nil {
		return nil, err
	}
	out := make([]HeldChange, 0, len(a.held))
	for _, hc := range a.held {
		out = append(out, hc) // every folder of the clone: one store, one list
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// heldBlob is what a read serves for path at head: the last accepted
// blob id ("" = nothing) and true when the path is held back.
func (a *authority) heldBlob(p string) (string, bool) {
	if a == nil {
		return "", false
	}
	if _, ok := a.held[p]; !ok {
		return "", false
	}
	return a.trusted[p], true
}

// headIfHeld is HEAD when the clone's last replay holds path back or no
// replay ran yet, "" otherwise: Get skips the git call for the common
// case of a file nobody's hold names.
func (b *Backend) headIfHeld(ctx context.Context, p string) (string, error) {
	authorityCache.Lock()
	a := authorityCache.byWork[b.work]
	authorityCache.Unlock()
	if a != nil {
		if _, ok := a.held[p]; !ok && len(a.held) == 0 {
			return "", nil
		}
	}
	return b.head(ctx)
}

// AuthoredCommit is one commit of the clone a person authored
// (CommitsBy): the audit view of `lore member show --history`.
type AuthoredCommit struct {
	Commit  string
	At      string // author date, YYYY-MM-DD HH:MM (UTC as git wrote it)
	Subject string
	Files   int
	Held    bool // every reader holds back what it changed
}

// maxAuthoredCommits caps the commits CommitsBy lists per clone.
const maxAuthoredCommits = 200

// CommitsBy lists the clone's commits authored by any of emails, newest
// first (merges skipped), each marked when the roster check holds it
// back. Why: the store's git log is the only trail of what a person did
// in it, and what a reader held back is part of that trail (devco R-18).
func (b *Backend) CommitsBy(ctx context.Context, emails []string) ([]AuthoredCommit, error) {
	if err := b.usable(ctx); err != nil || b.mode != modeClone || len(emails) == 0 {
		return nil, err
	}
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return nil, err
	}
	args := []string{"log", "--no-merges", "--fixed-strings", "--regexp-ignore-case", "-z", "--name-only",
		"--max-count=" + strconv.Itoa(maxAuthoredCommits), "--date=format-local:%Y-%m-%d %H:%M", "--format=" + "%x01%H%x00%ad%x00%s%x00"}
	for _, e := range emails {
		args = append(args, "--author=<"+strings.TrimSpace(e)+">")
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: append(args, h, "--"), env: []string{"TZ=UTC"}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the store's history", err)
	}
	held := map[string]bool{}
	if a, aerr := b.authorityOf(ctx, h); aerr == nil && a != nil {
		held = a.refused
	}
	var list []AuthoredCommit
	for _, rec := range strings.Split(string(out), commitMark) {
		parts := strings.Split(rec, "\x00")
		if len(parts) < 3 {
			continue
		}
		c := AuthoredCommit{Commit: parts[0], At: parts[1], Subject: parts[2]}
		for _, f := range parts[3:] {
			if f = strings.TrimSpace(f); f != "" {
				c.Files++
			}
		}
		c.Held = held[c.Commit]
		list = append(list, c)
	}
	return list, nil
}

// heldSeenFile is where a folder's last re-served hold set is kept (in
// the clone's git dir), so heldRecheck re-serves only when it changes.
const heldSeenFile = "lore-held-seen-"

// heldRecheck lists, for an incremental read, the held files of this
// folder the read did not already cover (changed is the read's paths):
// those with an accepted version as blob names to read (name ─▶ folder
// path), and those with none as tombstones that hide the row in the
// cache. Why: a cache filled before the check existed, or before a later
// roster change made a hold, still has the held version; it must never
// stay readable. Runs only when the folder's hold set changed since the
// last time it ran (one small file per folder in the git dir).
func (b *Backend) heldRecheck(ctx context.Context, a *authority, head string, changed []string) (map[string]string, []store.Row) {
	if a == nil {
		return nil, nil
	}
	covered := make(map[string]bool, len(changed))
	for _, p := range changed {
		covered[p] = true
	}
	var keys []string
	for p, hc := range a.held {
		if _, ok := b.folder.FileRel(p); ok {
			keys = append(keys, p+"="+hc.Commit)
		}
	}
	sort.Strings(keys)
	sig := strings.Join(keys, "\n")
	gd := b.gitDirOf(ctx, b.work)
	if gd == "" {
		return nil, nil
	}
	seen := filepath.Join(gd, heldSeenFile+strings.NewReplacer("/", "_", ".", "_root").Replace(b.folder.Name()))
	if old, err := os.ReadFile(seen); (err == nil && string(old) == sig) || (err != nil && sig == "") {
		return nil, nil
	}
	names := map[string]string{}
	var tombs []store.Row
	for p := range a.held {
		rel, ok := b.folder.FileRel(p)
		if !ok || covered[p] {
			continue
		}
		if blob := a.trusted[p]; blob != "" {
			names[blob] = rel
			continue
		}
		docs, err := b.readBlobs(ctx, []string{head + ":" + p})
		if err != nil {
			continue
		}
		r, err := rowfs.DecodeRowFile(rel, docs[head+":"+p])
		if err != nil {
			continue
		}
		t, err := store.FromRow(row.Row{Table: r.Table, ID: r.ID, HLC: r.HLC, Deleted: true})
		if err != nil {
			continue
		}
		tombs = append(tombs, t)
	}
	_ = os.WriteFile(seen, []byte(sig), 0o600) // best effort: the worst case is serving the holds again
	return names, tombs
}
