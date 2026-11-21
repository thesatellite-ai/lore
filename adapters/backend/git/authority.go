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
// R-15, round-6 follow-up; coordinator decision C35):
//
//	  ┌ the writer a row states (updated_by; created_by and
//	  │   created_by_actor_id on a new file) is not the commit's author,
//	  │   not an agent the author runs, and the row is not a version the
//	  │   stated writer committed before, same stamp (_hlc) and same
//	  │   content (a conflict settled with `lore sync resolve`, a partial
//	  │   push or a revert carries another person's edit with its stamp;
//	  │   devco D-329):
//	  │     a roster, membership or project row (authority-gated), or an
//	  │       author not on the roster ─▶ that file is held back
//	  │     any other row (a memory, a rule, a task…) by a member ─▶ read,
//	  │       with the commit's author credited as its writer (updated_by,
//	  │       and created_by on a new file, rewritten on read): a
//	  │       teammate's plain-git hand edit keeps working, and the
//	  │       borrowed name is never shown as the writer
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
// --batch` of every row version, once per clone; the result is kept in
// memory for the process and on disk in the clone's git folder
// (authoritydisk.go), keyed by HEAD, the org roster's HEAD and the lore
// binary. A later process at the same HEAD reads it back; a HEAD that
// moved on by plain commits (a write, a fast-forward pull) replays only
// the new commits; a merge, a rewrite or a new org roster replays it all
// (devco:D-797: a 5,000-row folder paid 0.3 s on every command).
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
	"errors"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
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
	// Severity is a rule's or a hotfix's severity: the role rule (devco
	// D-851) keeps a company must rule or critical/high hotfix an admin's
	Severity string `json:"severity"`
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

// teamInfoTables are rows about a team that say nothing about who is on
// it (its chat channel, …, team contact add): still an admin's change, as
// the local write guard keeps it (profilestore.Classify), so a member's
// hand-made commit cannot point the team's channel elsewhere either.
var teamInfoTables = map[string]bool{schema.TableTeamContacts: true}

// gated reports whether table's rows are authority-gated (C35): a
// stated writer that is not the commit's author holds such a row back,
// where any other row is read with the author credited.
func gated(table string) bool { return rosterTables[table] || wallTables[table] }

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
	parents []string // its parents' commit ids (linearCommitsAfter)
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
	// credited is, per path, the HEAD version whose stated writer is not
	// its commit's author, read with the author credited (C35)
	credited map[string]credit
	// admins is who is an admin after each commit, by the trusted roster
	// (AdminAt): one source of "admin at a commit" for the roster check
	// here and the org file check of adapters/orgcfg
	admins map[string]*adminSet
	// accepted (path ─▶ last accepted blob, "" = absent) and snap (the
	// admins after head) are where the replay stopped: a replay of the
	// commits after head goes on from them (replayOver)
	accepted map[string]string
	snap     *adminSet
}

// credit is one row version read with its commit's author credited as
// the writer, in place of the writer the file states (C35).
type credit struct {
	actor   string // the commit author's actor id, the writer shown
	claimed string // the updated_by the file states
	hlc     string // the version's stamp: only that version is rewritten
	created bool   // the commit added the file: created_by is the author's too
	commit  string
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
	orgHead := b.rosterHead(ctx)
	if a != nil && a.head == head && a.orgHead == orgHead {
		return a, nil
	}
	a, err := b.replayOverHeld(ctx, a, head, orgHead)
	if err != nil {
		return nil, err
	}
	if a == nil {
		a, err = b.replayFromDisk(ctx, head, orgHead)
		if err != nil {
			return nil, err
		}
	}
	if a == nil {
		commits, err := b.replayCommits(ctx, head)
		if err != nil {
			return nil, err
		}
		if a, err = b.replay(ctx, head, commits); err != nil {
			return nil, err
		}
		b.saveAuthority(a)
	}
	authorityCache.Lock()
	authorityCache.byWork[b.work] = a
	authorityCache.Unlock()
	return a, nil
}

// rowAuthorityOf is what a row read at head needs of the replay
// (heldBlob, creditRow): the replay this process already holds, else the
// row view saved with it (loadRowView, devco:D-826: no decode of the whole
// roster replay for one row), else the replay itself (authorityOf).
func (b *Backend) rowAuthorityOf(ctx context.Context, head string) (*authority, error) {
	if b.mode != modeClone || head == "" {
		return nil, nil
	}
	authorityCache.Lock()
	a := authorityCache.byWork[b.work]
	authorityCache.Unlock()
	orgHead := b.rosterHead(ctx)
	if a != nil && a.head == head && a.orgHead == orgHead {
		return a, nil
	}
	rowViews.Lock()
	v := rowViews.byWork[b.work]
	rowViews.Unlock()
	if v != nil && v.head == head && v.orgHead == orgHead {
		return v, nil
	}
	if v = b.loadRowView(head, orgHead); v != nil {
		rowViews.Lock()
		rowViews.byWork[b.work] = v
		rowViews.Unlock()
		return v, nil
	}
	return b.authorityOf(ctx, head)
}

// rowViews keeps each clone's loaded row view (loadRowView) for the
// process: every folder of a clone and every row read asks for it.
var rowViews = struct {
	sync.Mutex
	byWork map[string]*authority
}{byWork: map[string]*authority{}}

// replayOverHeld carries held (the replay this process already holds for
// the clone) on to head when head only added plain commits on top of it
// and the org head is the same: a copy of held replays those commits, and
// is saved like any replay; nil when held cannot be carried on. Why
// (devco:D-848): a long-lived process (the read server) holds the replay
// across commands, and after each sync replayFromDisk decoded the whole
// saved replay again (~13 ms of each served read after a sync) to rebuild
// what it held already. The copy keeps held as it was for any caller still
// reading it.
func (b *Backend) replayOverHeld(ctx context.Context, held *authority, head, orgHead string) (*authority, error) {
	if held == nil || held.orgHead != orgHead || held.head == "" {
		return nil, nil
	}
	commits, ok := b.linearCommitsAfter(ctx, held.head, head)
	if !ok {
		return nil, nil
	}
	a := held.clone()
	if err := b.replayOver(ctx, a, commits); err != nil {
		return nil, err
	}
	a.head = head
	b.saveAuthority(a)
	return a, nil
}

// clone copies a deep enough for replayOver to carry the copy on without
// changing a: every map replayOver writes is copied (the stamps of each
// file too, whose lists it appends to). The values themselves (row docs,
// admin sets, holds) are never changed in place, so they are shared.
func (a *authority) clone() *authority {
	st := &rosterState{docs: maps.Clone(a.state.docs), stamps: make(map[string]map[string][]stampRec, len(a.state.stamps)), orgless: a.state.orgless}
	for p, byHLC := range a.state.stamps {
		m := make(map[string][]stampRec, len(byHLC))
		for h, recs := range byHLC {
			m[h] = slices.Clip(recs) // an append copies instead of writing past a's end
		}
		st.stamps[p] = m
	}
	return &authority{head: a.head, orgHead: a.orgHead, state: st, held: maps.Clone(a.held), trusted: maps.Clone(a.trusted), refused: maps.Clone(a.refused),
		credited: maps.Clone(a.credited), admins: maps.Clone(a.admins), accepted: maps.Clone(a.accepted), snap: a.snap}
}

// replayFromDisk is the replay at head from the one kept on disk
// (loadAuthority): as is when it was made at head, or carried on over the
// commits after it when head only added plain commits on top
// (linearCommitsAfter); nil when neither (a full replay is due).
func (b *Backend) replayFromDisk(ctx context.Context, head, orgHead string) (*authority, error) {
	a := b.loadAuthority()
	if a == nil || a.orgHead != orgHead {
		return nil, nil
	}
	if a.head == head {
		return a, nil
	}
	commits, ok := b.linearCommitsAfter(ctx, a.head, head)
	if !ok {
		return nil, nil
	}
	if err := b.replayOver(ctx, a, commits); err != nil {
		return nil, err
	}
	a.head = head
	b.saveAuthority(a)
	return a, nil
}

// linearCommitsAfter lists the commits from (exclusive) to to, oldest first,
// when they are one line of plain commits on top of from: each has one
// parent, the one before it (from for the first). Only then is replaying
// them after from's replay the same as replaying to's whole history (git
// lists every commit of from's history first). ok is false otherwise (a
// merge, a rewrite, from not under to, a git failure).
func (b *Backend) linearCommitsAfter(ctx context.Context, from, to string) ([]replayCommit, bool) {
	commits, err := b.replayCommits(ctx, from+".."+to)
	if err != nil || len(commits) == 0 || commits[len(commits)-1].id != to {
		return nil, false
	}
	prev := from
	for _, c := range commits {
		if len(c.parents) != 1 || c.parents[0] != prev {
			return nil, false
		}
		prev = c.id
	}
	return commits, true
}

// commitMark starts each commit's record in replayCommits' log output.
const commitMark = "\x01"

// replayCommits lists the commits of rev (head, or a from..to range)
// oldest first with their parents and the files each changed (blob ids
// on both sides).
func (b *Backend) replayCommits(ctx context.Context, rev string) ([]replayCommit, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "--reverse", "--topo-order", "-c", "--no-renames",
		"--raw", "--no-abbrev", "-z", "--format=" + "%x01%H%x00%P%x00%ae%x00", rev, "--"}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the store's history for the roster check", err)
	}
	var commits []replayCommit
	for _, rec := range strings.Split(string(out), commitMark) {
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x00", 4)
		if len(parts) < 4 {
			continue
		}
		c := replayCommit{id: parts[0], parents: strings.Fields(parts[1]), author: strings.ToLower(parts[2])}
		fields := strings.Split(strings.TrimLeft(parts[3], "\x00\n"), "\x00")
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
	a := &authority{head: head, orgHead: orgHead, state: st, admins: map[string]*adminSet{}, held: map[string]HeldChange{}, trusted: map[string]string{}, refused: map[string]bool{}, credited: map[string]credit{},
		accepted: map[string]string{}, snap: st.adminSnapshot()}
	if err := b.replayOver(ctx, a, commits); err != nil {
		return nil, err
	}
	return a, nil
}

// replayedCommits counts the commits replayOver decided in this process:
// the tests' proof that a read at a known HEAD replays nothing and one
// after a plain commit replays that commit only (devco:D-797).
var replayedCommits atomic.Int64

// replayOver decides commits, the commits right after where a's replay
// stopped, and carries a's state on over them.
func (b *Backend) replayOver(ctx context.Context, a *authority, commits []replayCommit) error {
	replayedCommits.Add(int64(len(commits)))
	// read every roster blob once, up front
	var names []string
	seen := map[string]bool{}
	want := func(sha string) {
		if !zeroBlob(sha) && sha != "" && !seen[sha] {
			seen[sha] = true
			names = append(names, sha)
		}
	}
	for _, c := range commits {
		for _, f := range c.changes {
			if !isRowFile(f.path) {
				continue
			}
			want(f.newBlob)
			if pinnable(tableOfPath(f.path)) {
				// the role rule judges the version a commit replaced
				// (devco D-851): an incremental replay has not read it
				want(f.oldBlob)
				want(a.accepted[f.path])
			}
		}
	}
	raws, err := b.readBlobs(ctx, names)
	if err != nil {
		return err
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
	st, accepted, snap := a.state, a.accepted, a.snap
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
			credited := false
			if reason == "" {
				reason, credited = st.fileRefusal(c, actor, f, docOf)
			}
			if reason == "" {
				reason = st.roleRefusal(c, actor, f, accepted[f.path], docOf)
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
			delete(a.credited, f.path) // a held change keeps serving the accepted version, credit and all
			rosterMoved = rosterMoved || rosterTables[tableOfPath(f.path)]
			if zeroBlob(f.newBlob) {
				accepted[f.path] = ""
				delete(st.docs, f.path)
				continue
			}
			accepted[f.path] = f.newBlob
			if d, ok := docOf(f.newBlob); ok {
				if credited {
					a.credited[f.path] = credit{actor: actor, claimed: d.UpdatedBy, hlc: d.HLC, created: zeroBlob(f.oldBlob), commit: c.id}
				}
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
	a.snap = snap
	return nil
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
	if why := s.addsPerson(c, actor, docOf); why != "" {
		return why
	}
	for _, f := range c.changes {
		table := tableOfPath(f.path)
		if (!rosterTables[table] && !membershipTables[table] && !teamInfoTables[table]) || !isRowFile(f.path) {
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
			// naming oneself the owner of an entry someone else had is a
			// takeover, not an own change (devco U-1 class)
			if hadOld && old.ID != actor && old.Owner != actor {
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
		case schema.TableTeamContacts:
			return "changes a team's contacts and " + c.author + " is not an admin (only an admin changes the teams)"
		}
	}
	return ""
}

// addsPerson names what commit c does when it puts a new person on the
// roster (a new actor row that is neither the author's own nor owned by
// them), with an admin stint when one of its new stints has org_role
// admin: "" when it adds nobody. Why (devco T-18): the refusal named the
// first row file it met ("changes another person's contacts") for each of
// the three rows a member add writes, never what the commit did.
func (s *rosterState) addsPerson(c replayCommit, actor string, docOf func(string) (rosterDoc, bool)) string {
	who, admin := "", false
	for _, f := range c.changes {
		if !isRowFile(f.path) || zeroBlob(f.newBlob) {
			continue
		}
		if _, hadOld := s.docs[f.path]; hadOld {
			continue
		}
		next, ok := docOf(f.newBlob)
		if !ok {
			continue
		}
		switch tableOfPath(f.path) {
		case schema.TableActors:
			if next.ID != actor && (actor == "" || next.Owner != actor) {
				who = next.ID
				if next.DisplayName != "" {
					who = next.DisplayName + " (" + next.ID + ")"
				}
			}
		case schema.TableActorPeriods:
			if next.OrgRole == orgRoleAdmin && next.ActorID != actor {
				admin = true
			}
		}
	}
	switch {
	case who == "":
		return ""
	case admin:
		return "adds a person, " + who + ", to the roster with an admin stint and " + c.author + " is not an admin"
	}
	return "adds a person, " + who + ", to the roster and " + c.author + " is not an admin"
}

// membershipRefusal judges a non-admin's change of one project_members
// or teams row (devco D-328): a lead of the row's project may change who
// is on that project; a team row is an admin's change only (devco D-694:
// who is on a team decides in which company folder they add rules and
// notes, the same rule the local write guard keeps, so a lead's team
// change refused on their machine is not taken from a hand-made commit
// at sync). Past that only a person taking up their own pending invite
// is read (their own membership of a project the invite names, with
// roles it or the stint already gave; or adding only themselves to a
// team it names). Anything else, a person's own row included, is held
// back: a hand-made membership row would otherwise open another
// project's folders to a contractor.
func (s *rosterState) membershipRefusal(author, actor, table string, old rosterDoc, hadOld bool, next rosterDoc, hasNext bool) string {
	why := "changes who is on a project and " + author + " is neither an admin nor a lead of that project"
	if table == schema.TableTeams {
		why = "changes who is on a team and " + author + " is not an admin (only an admin changes the teams)"
	}
	if actor == "" {
		return why
	}
	if table == schema.TableProjectMembers && (hadOld || hasNext) && (!hadOld || s.leads(actor, old.ProjectID)) && (!hasNext || s.leads(actor, next.ProjectID)) {
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
// the writer an authority-gated row states is not the author (R-4), or
// the author is a contractor writing outside their own roster rows and
// projects (R-15). credit is true when the file is read with the author
// credited as its writer: an ordinary row whose stated writer is someone
// else, committed by a member (C35).
func (s *rosterState) fileRefusal(c replayCommit, actor string, f fileChange, docOf func(string) (rosterDoc, bool)) (reason string, credit bool) {
	if zeroBlob(f.newBlob) || (!s.orgless && !s.hasAdmin()) {
		return "", false // a removed file (rowfs reports a missing row), or no org yet
	}
	d, ok := docOf(f.newBlob)
	if !ok {
		return "", false // not a row: rowfs reports the file
	}
	if c.merge && s.stampKnown(f.path, d.HLC) {
		return "", false // lore's field merge keeps an accepted side's stamp
	}
	if actor != "" && s.isAdmin(actor) {
		return "", false
	}
	claimed := []string{d.UpdatedBy}
	if zeroBlob(f.oldBlob) {
		claimed = append(claimed, d.CreatedBy, d.CreatedByID)
	}
	for _, w := range claimed {
		if w == "" || w == actor || (actor != "" && s.ownsIn(c, actor, w, docOf)) || s.stampBy(f.path, d.HLC, w, d.content) {
			continue
		}
		if actor == "" && !s.known(w) {
			continue // neither side is on the roster: nothing to compare
		}
		if actor == "" || gated(tableOfPath(f.path)) {
			return "claims to be written by " + s.name(w) + ", but the commit's author is " + c.author, false
		}
		credit = true // C35: read, with the author named as the writer
	}
	if actor == "" || !s.contractor(actor) {
		return "", credit
	}
	switch tableOfPath(f.path) {
	case schema.TableActors:
		if d.ID == actor || d.Owner == actor {
			return "", credit
		}
	case schema.TableActorContacts, schema.TableActorPeriods:
		if d.ActorID == actor {
			return "", credit
		}
	case schema.TableProjectMembers, schema.TableTeams:
		// refusal already allowed it (a lead's change, or their own invite
		// taken up); a membership row lives in the org folder
		if d.ActorID == actor || d.Members.has(actor) || s.leads(actor, d.ProjectID) {
			return "", credit
		}
	case schema.TableInvites:
		if strings.EqualFold(d.Email, c.author) {
			return "", credit
		}
	}
	if folder, _, ok := rowPlace(f.path); ok && s.folderHoldsProjectOf(folder, actor) {
		return "", credit
	}
	return "written by " + c.author + ", a contractor in this org, outside their projects' folders", false
}

// pinnable reports whether table's rows can be company pinned (a must
// rule, a critical/high hotfix: profilestore.CompanyPinned).
func pinnable(table string) bool {
	return table == schema.TableRules || table == schema.TableHotfixes
}

// pinned reports whether d, a row file of table, is company pinned: a
// live must rule or critical/high hotfix every repo reading its place
// gets in full (profilestore.CompanyPinned; an L3 adapter cannot import
// ops).
func (d rosterDoc) pinned(table string) bool {
	if d.deleted() {
		return false
	}
	switch table {
	case schema.TableRules:
		return d.Severity == schema.RuleSeverityMust
	case schema.TableHotfixes:
		return d.Severity == schema.HotfixSeverityCritical || d.Severity == schema.HotfixSeverityHigh
	}
	return false
}

// roleRefusal is the role rule of the write guard (ops/profilestore
// writeguard.go) applied to a commit made without lore (devco D-851): in
// a store that holds the org folder every folder is company ground, so a
// commit whose author is not an admin (or not on the roster at all) may
// not change a policy, change or remove a company pinned row, or add one
// (or raise a row to pinned) outside a folder of a team the author is on.
// Without it a member's plain `git commit` of a must rule, with any
// created_by it liked, reached every teammate's context as a company
// must. The author is judged, never the writer the row states: a forged
// created_by cannot make a row an admin's. acceptedBlob is the version
// the replay accepted last ("" none); a version whose stamp and content
// an admin committed before (a conflict settled with --take other, a
// revert) and a rewrite that leaves the content as it was (a format
// migration) pass. "" = allowed.
func (s *rosterState) roleRefusal(c replayCommit, actor string, f fileChange, acceptedBlob string, docOf func(string) (rosterDoc, bool)) string {
	if s.orgless || !s.hasAdmin() || (actor != "" && s.isAdmin(actor)) {
		return ""
	}
	table := tableOfPath(f.path)
	if table != schema.TablePolicies && !pinnable(table) {
		return ""
	}
	next, hasNext := docOf(f.newBlob)
	if zeroBlob(f.newBlob) {
		hasNext = false
	}
	if hasNext && next.UpdatedBy != "" && s.isAdmin(next.UpdatedBy) && s.stampBy(f.path, next.HLC, next.UpdatedBy, next.content) {
		return "" // an admin's accepted version, carried as it was
	}
	who := c.author + " is not an admin"
	if actor == "" {
		who = c.author + " is not on the roster"
	}
	if table == schema.TablePolicies {
		return "changes a company policy without lore and " + who
	}
	var prevs []rosterDoc
	for _, sha := range []string{f.oldBlob, acceptedBlob} {
		if d, ok := docOf(sha); ok && !zeroBlob(sha) {
			prevs = append(prevs, d)
		}
	}
	for _, p := range prevs {
		if !p.pinned(table) {
			continue
		}
		if hasNext && next.content == p.content {
			return "" // the same row, rewritten (a format migration)
		}
		return "changes " + pinnedName(table) + " of the company and " + who
	}
	if !hasNext || !next.pinned(table) {
		return ""
	}
	if folder, _, ok := rowPlace(f.path); ok && actor != "" && s.onTeamOf(actor, folder) {
		return "" // a member adds in the folder of a team they are on
	}
	return "adds " + pinnedName(table) + " to the company folder, outside a folder of a team the author is on, and " + who
}

// pinnedName names a pinned row of table in a hold's reason.
func pinnedName(table string) string {
	if table == schema.TableHotfixes {
		return "a critical/high hotfix"
	}
	return "a must rule"
}

// onTeamOf reports whether actor is on an active team whose handle is
// folder (a member's add right, profilestore callerTeams).
func (s *rosterState) onTeamOf(actor, folder string) bool {
	for _, d := range s.byTable(schema.TableTeams) {
		if strings.TrimPrefix(d.Handle, "@") != folder || d.LeftAt != "" || string(d.Active) == "false" {
			continue
		}
		if d.Members.has(actor) {
			return true
		}
	}
	return false
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

// ownsIn reports whether actor owns agent by the roster before commit
// c, or because c itself adds agent's actors row with actor as its owner
// (devco U-1): a member's first write through a coding agent registers
// the agent in the same commit as the row it writes, and asking only the
// roster before c held the member's own agent back for every reader.
// refusal has already judged that actors row (a non-admin may add only
// rows they own, never take over another person's), so trusting it here
// gives no one a name they could not write themselves.
func (s *rosterState) ownsIn(c replayCommit, actor, agent string, docOf func(string) (rosterDoc, bool)) bool {
	if s.owns(actor, agent) {
		return true
	}
	for _, d := range s.byTable(schema.TableActors) {
		if d.ID == agent {
			return false // an existing actor changes owner only through an admin
		}
	}
	for _, f := range c.changes {
		if tableOfPath(f.path) != schema.TableActors || zeroBlob(f.newBlob) || !zeroBlob(f.oldBlob) {
			continue
		}
		if d, ok := docOf(f.newBlob); ok && d.ID == agent && d.Owner == actor && d.ID != actor {
			return true
		}
	}
	return false
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

// heldWriteErr turns a stale write of a row the clone's reads hold back
// into a refusal that names the hold (devco U-1): reads serve the
// accepted version of a held row while the file holds the refused one,
// so every write of it failed the version check with a bare "row changed
// since it was read" and no way out. Any other error is returned as is.
func (b *Backend) heldWriteErr(changes []store.Change, err error) error {
	if err == nil || b.mode != modeClone || !errors.Is(err, store.ErrStale) {
		return err
	}
	authorityCache.Lock()
	a := authorityCache.byWork[b.work]
	authorityCache.Unlock()
	if a == nil {
		return err
	}
	for _, c := range changes {
		h, ok := a.held[b.folder.StorePath(c.Row.Table, c.Row.ID)]
		if !ok {
			continue
		}
		return errs.New(errs.PolicyDenied, "git: "+h.Table+" "+h.ID+" is held back in this store (commit "+h.Short()+" by "+h.Author+": "+h.Reason+"), so it cannot be written; nothing was changed").
			WithHint("an admin who agrees makes that change with lore (or reverts the commit) and the hold ends; `git -C " + b.work + " show " + h.Short() + "` shows what it changed")
	}
	return err
}

// headIfHeld is HEAD when the clone's last replay holds path back,
// credits its writer (C35) or no replay ran yet, "" otherwise: Get skips
// the git call for the common case of a file nobody's hold names.
func (b *Backend) headIfHeld(ctx context.Context, p string) (string, error) {
	authorityCache.Lock()
	a := authorityCache.byWork[b.work]
	authorityCache.Unlock()
	if a != nil {
		_, held := a.held[p]
		_, credited := a.credited[p]
		if !held && !credited && len(a.held) == 0 {
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

// heldSeenFile is where a folder's last re-served hold set is kept (next
// to the replay file, heldSeenPath), so heldRecheck re-serves only when it
// changes.
const heldSeenFile = "lore-held-seen-"

// heldRecheck lists, for an incremental read, the held and credited
// files of this folder the read did not already cover (changed is the
// read's paths): held ones with an accepted version, and credited ones
// (C35, read at head for creditRow), as blob names to read (name ─▶
// root-relative path), and held ones with none as tombstones that hide
// the row in the cache. Why: a cache filled before the check existed, or before a later
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
	for p, c := range a.credited {
		if _, ok := b.folder.FileRel(p); ok {
			keys = append(keys, p+"=credit:"+c.commit)
		}
	}
	sort.Strings(keys)
	sig := strings.Join(keys, "\n")
	seen := b.heldSeenPath(strings.NewReplacer("/", "_", ".", "_root").Replace(b.folder.Name()))
	if seen == "" {
		return nil, nil
	}
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
			names[blob] = p
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
	for p := range a.credited {
		if _, held := a.held[p]; held || covered[p] {
			continue
		}
		if _, ok := b.folder.FileRel(p); ok {
			names[head+":"+p] = p // a cache that took the stated writer gets the committer
		}
	}
	if os.MkdirAll(filepath.Dir(seen), authorityDirMode) == nil {
		_ = os.WriteFile(seen, []byte(sig), 0o600) // best effort: the worst case is serving the holds again
	}
	return names, tombs
}

// fieldCreatedByActorID is the content tables' creator field (schema
// builtin_content.go contentCreatedBy).
const fieldCreatedByActorID = "created_by_actor_id"

// creditRow is r (the row file at root-relative path p) with the commit
// author credited as its writer when the replay credited that version
// (C35): updated_by, and on a version that added the file created_by
// and created_by_actor_id, name the person who committed it, never the
// writer the file states. Any other row, or a version other than the
// credited one (a later write of lore's own), is returned as it is.
func (a *authority) creditRow(p string, r store.Row) store.Row {
	if a == nil || r.Deleted {
		return r
	}
	c, ok := a.credited[p]
	if !ok {
		return r
	}
	var stated struct {
		HLC       string `json:"_hlc"`
		UpdatedBy string `json:"updated_by"`
	}
	if json.Unmarshal(r.Doc, &stated) != nil || stated.HLC != c.hlc || stated.UpdatedBy != c.claimed {
		return r
	}
	writer, err := ids.Parse[ids.Actor](c.actor)
	if err != nil {
		return r
	}
	m, err := r.Decode()
	if err != nil {
		return r
	}
	m.UpdatedBy = writer
	if c.created {
		m.CreatedBy = writer
		if _, has := m.Fields[fieldCreatedByActorID]; has {
			if v, terr := value.TextOf(c.actor); terr == nil {
				m.Fields[fieldCreatedByActorID] = v
			}
		}
	}
	out, err := store.FromRow(m)
	if err != nil {
		return r
	}
	return out
}
