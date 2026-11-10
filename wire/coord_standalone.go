package wire

// coord_standalone.go — the team coordination of a host that wires none
// (Options.Coord nil: cmd/lore, P3-COORD-3).
//
// The release binary hands wire its v0.1.17 machinery (Options.Coord, and
// an opstask.Team in the command's context). The standalone binary has
// neither yet: it takes no claims, fetches no signals ref and keeps no live
// rows (STORE_SERVER_SPEC "Where 0.1.18 stops"). What it CAN answer from
// the core alone, it answers here; every other verb keeps the
// E_UNSUPPORTED of ops/coord.
//
// Flow:
//
//	App.Coordinator ─► Options.Coord set? ─► that host (release binary)
//	                 └► else standaloneCoord ─► task note: a comment row
//	                                            through the engine (the
//	                                            v0.1.17 plain-repo note)
//	                                         └► every other verb: E_UNSUPPORTED
//	App.TeamContext ─► Options.Coord set? ─► ctx as is (the host's own Team)
//	                 └► else standaloneTeam ─► @handle from the repo roster;
//	                                           claims from the last fetched
//	                                           copy of refs/lore/signals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// standaloneCoord is the opscoord.Host of a binary without coordination
// machinery. It holds the App so its one write (task note) takes the
// engine's write path (scan, policy, audit) like every row.
type standaloneCoord struct{ a *App }

var _ opscoord.Host = standaloneCoord{}

// coordUnsupported is ops/coord's refusal for the verb name.
func coordUnsupported(name string) error { return opscoord.Unsupported(name) }

// fromRepo is the --from value naming this repo's own project.
const fromRepo = "repo"

// TaskNote implements opscoord.Host: v0.1.17's note (saas task_note.go).
//   - the caller holds the task (its claim, or its held record in this
//     session): a fenced write on the signals ref the task is coordinated
//     on (F7, heldNote);
//   - anyone else: a comment row in the store holding the task (a store
//     task's note goes to the store, never into the repo, N2);
//   - where this host coordinates nothing (no git repo, no agent or
//     person) the plain comment row; any other failure to open the
//     coordination world fails closed (closeWorld, B11).
//
// `--to repo` is the deliberate share (§7.6): one unfenced comment row in
// this repo, written as any repo row (in a client repo the engine records
// an agent as its owning person, D8/Q11).
//
// Refused, nothing written: --to any other place, --from another place
// (the note's place is decided by store links, FA-OLDCODE-1).
func (h standaloneCoord) TaskNote(ctx context.Context, in opscoord.NoteIn) (opscoord.Report, error) {
	if (in.ShareTo != "" && in.ShareTo != fromRepo) || (in.From != "" && in.From != fromRepo) {
		return opscoord.Report{}, coordUnsupported(opscoord.OpTaskNote)
	}
	body := strings.TrimSpace(in.Body())
	if body == "" {
		return opscoord.Report{}, errs.New(errs.EmptyBody, "task note: the note is empty")
	}
	tid, err := ids.ParseAny(strings.TrimSpace(in.ID))
	if err == nil && tid.Prefix() != ids.PrefixTask {
		err = fmt.Errorf("prefix %s is not %s", tid.Prefix(), ids.PrefixTask)
	}
	if err != nil {
		return opscoord.Report{}, errs.Wrap(errs.InvalidID, "task note: "+in.ID+" is not a task id (tsk_…)", err).WithHint(errs.InvalidIDHint)
	}
	src, _, err := h.a.Get(ctx, engine.Ref{Table: schema.TableTasks, ID: tid})
	if err != nil {
		return opscoord.Report{}, err
	}
	if in.ShareTo == fromRepo {
		return h.sharedNote(ctx, tid, body)
	}
	if rep, done, cerr := h.clientTaskNote(ctx, src, tid, body); done || cerr != nil {
		return rep, cerr
	}
	w, err := standaloneTeam(h).closeWorld(ctx, tid.String())
	if err != nil {
		return opscoord.Report{}, err
	}
	if w != nil {
		env := w.env(ctx)
		hs, herr := w.heldOf(ctx, env, tid.String())
		if herr != nil {
			return opscoord.Report{}, herr
		}
		if hs.mine || hs.hadClaim {
			return w.heldNote(ctx, env, tid, body)
		}
	}
	fields, err := noteFields(tid, body)
	if err != nil {
		return opscoord.Report{}, err
	}
	wr, err := h.a.Create(ctx, src, schema.TableComments, fields)
	if err != nil {
		return opscoord.Report{}, err
	}
	return opscoord.Report{Lines: []string{"✓ note " + wr.Row.ID.String() + " on " + tid.String()}}, nil
}

// clientTaskNote is the note on a client repo's own task (D8, §7.6): its
// claim is on the linked store, and its notes are internal, so the
// holder's note is the fenced note staged through clientRows (the comment
// in the linked store) and a note by anyone else is a comment row in the
// linked store folder, never in the client repo. done=false: not a client
// repo's own task, or an org that shares agent details (agent_details:
// client), the usual note applies.
func (h standaloneCoord) clientTaskNote(ctx context.Context, src store.Source, tid ids.AnyID, body string) (opscoord.Report, bool, error) {
	if src != store.SourceRepo {
		return opscoord.Report{}, false, nil
	}
	w, err := h.openWorld(ctx)
	if err != nil || w.sit.Role != orgresolve.RoleClient || len(w.sit.Links) == 0 {
		return opscoord.Report{}, false, nil // closeWorld decides the other cases
	}
	if !clientRowsFor(w).internal {
		return opscoord.Report{}, false, nil
	}
	if err = w.forTask(ctx, tid.String()); err != nil {
		return opscoord.Report{}, true, err
	}
	env := w.env(ctx)
	hs, err := w.heldOf(ctx, env, tid.String())
	if err != nil {
		return opscoord.Report{}, true, err
	}
	if hs.mine || hs.hadClaim {
		rep, nerr := w.heldNote(ctx, env, tid, body)
		return rep, true, nerr
	}
	fields, err := noteFields(tid, body)
	if err != nil {
		return opscoord.Report{}, true, err
	}
	wr, err := h.a.Create(ctx, w.orgStore, schema.TableComments, fields)
	if err != nil {
		return opscoord.Report{}, true, err
	}
	return opscoord.Report{Lines: []string{"✓ note " + wr.Row.ID.String() + " on " + tid.String()}}, true, nil
}

// sharedNote is `task note --to repo`: a comment row in the repo store
// (v0.1.17 sharedNote / agentSharedNote), whatever store holds the task.
func (h standaloneCoord) sharedNote(ctx context.Context, tid ids.AnyID, body string) (opscoord.Report, error) {
	fields, err := noteFields(tid, body)
	if err != nil {
		return opscoord.Report{}, err
	}
	wr, err := h.a.Create(ctx, store.SourceRepo, schema.TableComments, fields)
	if err != nil {
		return opscoord.Report{}, err
	}
	line := "✓ note " + wr.Row.ID.String() + " on " + tid.String()
	if w, werr := h.openWorld(ctx); werr == nil && w.isAgent() && w.sit.Role == orgresolve.RoleClient && clientRowsFor(w).internal {
		line += " (shared with the client repo)" // v0.1.17 agentSharedNote
	}
	return opscoord.Report{Lines: []string{line}}, nil
}

// commentTag types a note's comment id (cmt_…).
type commentTag struct{}

// Prefix implements ids.Tag.
func (commentTag) Prefix() ids.Prefix { return ids.PrefixComment }

// fieldWorkBranch is the task's recorded work branch (§7.4).
const fieldWorkBranch = "work_branch"

// envRideActive marks lore's own git push, so lore's hooks skip it.
const envRideActive = "LORE_RIDE_ACTIVE"

// heldNote is the holder's fenced note (v0.1.17 runTaskNote's held path):
// staged with the claim on its signals ref, the claim's work branch
// recorded on the row and pushed; a holder that lost the claim writes
// nothing (E_LOST_CLAIM).
func (w *coordWorld) heldNote(ctx context.Context, env coord.Env, tid ids.AnyID, body string) (opscoord.Report, error) {
	cid, err := ids.New[commentTag]()
	if err != nil {
		return opscoord.Report{}, errs.Wrap(errs.Internal, "mint note id", err)
	}
	doc := coord.Doc{schema.FieldEntityTable: canonjson.String(string(schema.TableTasks)), schema.FieldEntityID: canonjson.String(tid.String()),
		fieldCommentBody: canonjson.String(body)}
	branch := w.workBranch(ctx)
	if _, err = coord.HolderWrite(ctx, env, tid.String(), false, func(d coord.Doc, _ time.Time) ([]coord.RowWrite, error) {
		if branch != "" {
			d.SetStr(fieldWorkBranch, branch)
		}
		return []coord.RowWrite{{Table: string(schema.TableComments), ID: cid.String(), Doc: doc}}, nil
	}); err != nil {
		return opscoord.Report{}, coordErr(err) // fenced out or offline: nothing written
	}
	lines := []string{"✓ note " + cid.String() + " on " + tid.String()}
	switch {
	case branch != "" && w.pushWorkBranch(ctx, branch):
		lines = append(lines, "  work branch "+branch+" pushed")
	case branch != "":
		lines = append(lines, "  ! work branch "+branch+" NOT pushed (offline or no remote); a replacement cannot fetch it yet")
	}
	return opscoord.Report{Lines: lines}, nil
}

// pushWorkBranch pushes the work branch so a replacement agent can fetch
// it (§7.4 "Work branch"). Best effort: a failed push is reported, never
// fatal.
func (w *coordWorld) pushWorkBranch(ctx context.Context, branch string) bool {
	if w.remote == nil || !w.autopush {
		return false
	}
	_, err := w.repo.Run(ctx, gitref.Invocation{Args: []string{"push", "--quiet", w.remoteName, "refs/heads/" + branch + ":refs/heads/" + branch},
		Env: []string{envRideActive + "=1"}})
	return err == nil
}

// noteFields is the comment row of a note: on the task, with the body.
// The engine stamps the author and times.
func noteFields(task ids.AnyID, body string) (map[string]value.Value, error) {
	tbl, err := value.TextOf(schema.TableTasks)
	if err != nil {
		return nil, err
	}
	ref, err := value.RefOf(task)
	if err != nil {
		return nil, err
	}
	text, err := value.TextOf(body)
	if err != nil {
		return nil, errs.Prefix("task note: body", err)
	}
	return map[string]value.Value{schema.FieldEntityTable: tbl, schema.FieldEntityID: ref, fieldCommentBody: text}, nil
}

// fieldCommentBody is the comments table's text field.
const fieldCommentBody = "body"

// TeamContext returns ctx carrying the team machinery the task and mission
// operations ask for (`--assign @h`, `task list --mine|--claimed-by`, the
// STATE column, the claimed-task fence of group deletes). A host with its
// own coordination (Options.Coord: the release binary, which also puts its
// own Team in every command's context) is left alone, and so is a context
// that already carries a Team (a caller's or a test's fake: App.Run calls
// this on every operation, P3-PERF-2).
func (a *App) TeamContext(ctx context.Context) context.Context {
	if a.opts.Coord != nil || opstask.HasTeam(ctx) {
		return ctx
	}
	return opstask.WithTeam(ctx, standaloneTeam{a: a})
}

// standaloneTeam is the opstask.Team of a host without coordination
// machinery: the repo's own roster and its last fetched claims board.
type standaloneTeam struct{ a *App }

var _ opstask.Team = standaloneTeam{}

// ResolveHandle implements opstask.Team: an active person @h on the repo
// roster, else (allowTeam) an active team @h; v0.1.17's miss texts.
func (t standaloneTeam) ResolveHandle(ctx context.Context, handle string, allowTeam bool) (opstask.Assignee, error) {
	h := strings.TrimPrefix(strings.TrimSpace(handle), rosterHandleMark)
	if h == "" {
		return opstask.Assignee{}, errs.New(errs.InvalidInput, "empty handle")
	}
	// The roster this repo resolves on: the repo's own, or the org
	// folder's when an org applies (repoRoster, P3-CMDLORE-5), with the
	// sync-then-retry on a miss.
	r, err := t.a.coordRoster(ctx)
	if err != nil {
		return opstask.Assignee{}, err
	}
	x, err := t.a.rosterByHandle(ctx, r, h)
	if err == nil {
		return opstask.Assignee{ActorID: x.ID}, nil
	}
	if !allowTeam || !errs.Has(err, errs.InvalidInput) {
		return opstask.Assignee{}, err
	}
	teams, err := t.a.RosterTeams(ctx, r.src)
	if err != nil {
		return opstask.Assignee{}, err
	}
	for _, x := range teams {
		if x.Active && x.Handle == h {
			return opstask.Assignee{TeamID: x.ID}, nil
		}
	}
	// An org roster's miss keeps its sync hint (the store copy may predate
	// a teammate's registration, v0.1.17 rosterMissHint); the repo's own
	// roster has no copy to be stale, so it only says to register.
	hint := "people join with `lore actor register`"
	if r.src != store.SourceRepo {
		hint = r.missHint()
	}
	return opstask.Assignee{}, errs.New(errs.NotFound, "no person or team with handle @"+h).WithHint(hint)
}

// coordRoster is the roster @handles resolve on (repoRoster's place,
// without its caller checks: resolving a handle needs no identity, and an
// agent may assign): the org folder store with an org, else the repo.
func (a *App) coordRoster(ctx context.Context) (rosterAt, error) {
	r := newRosterAt(store.SourceRepo, repoRosterWhere)
	if a.opts.Org == nil {
		return r, nil
	}
	org, err := a.opts.Org(ctx)
	if err != nil || org == nil {
		return r, err
	}
	src, folder, ok, err := a.orgRosterSource(ctx, org.Name)
	if err != nil {
		return r, err
	}
	if !ok {
		return r, errs.New(errs.NotImplemented, "org "+org.Name+"'s roster is kept in its org store, which this lore build does not reach for the roster").
			WithHint("set org_folder on one of the org's stores in ~/.lore/config.yaml")
	}
	return newRosterAt(src, org.Name+" "+folder+"/"), nil
}

// Claims implements opstask.Team: the last fetched copy of this repo's
// signals ref, read without network, as v0.1.17's task list read it.
func (t standaloneTeam) Claims(ctx context.Context) (opstask.ClaimBoard, error) {
	return t.a.localClaims(ctx)
}

// Claim timing defaults (LORE_TEAM_SPEC §1 `lease.*`, the v0.1.17
// lorecfg defaults). The config's `lease:` block is not read here yet: it
// lives in the v0.1.17 config reader (P3-COORD-3); a reader only uses the
// lease to tell an expired claim, and expiry also needs an unchanged
// renewal watched for renew_every, so a longer configured TTL is never
// shortened into a steal: at worst this binary reports EXPIRED late.
const (
	defaultLeaseTTL        = 20 * time.Minute
	defaultLeaseRenewEvery = 5 * time.Minute
	defaultLeaseGrace      = 2 * time.Minute
)

// Signals machine state and names (LORE_TEAM_SPEC §1 "Files and folders",
// "Refs"), shared with the release binary so both read one observation file.
const (
	signalsStateDir   = "lore"              // <git dir>/lore
	signalsSeenFile   = "signals-seen.json" // the expiry observations
	signalsRootFolder = "_root"             // a project at the repo root
	defaultRemote     = "origin"            // live.remote default
	envLiveRemote     = "LORE_LIVE_REMOTE"  // the remote override
)

// localClaims reads the claims board of this repo's signals ref from its
// tracking copy (no fetch). Outside a git repo the board is empty: there
// is no signals ref, so nobody can hold anything here.
func (a *App) localClaims(ctx context.Context) (localBoard, error) {
	me, err := a.Actor(ctx)
	if err != nil {
		return localBoard{}, err
	}
	b := localBoard{me: me.String(), folder: signalsRootFolder}
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return b, nil // not a git repo: no signals ref
	}
	repo, err := gitref.Open(ctx, top, nil)
	if err != nil {
		return b, nil // no git: no signals ref
	}
	gitDir, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return localBoard{}, errs.Wrap(errs.Internal, "claims: locate the git dir", err)
	}
	if rel, rerr := filepath.Rel(top, a.projectDir()); rerr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		b.folder = filepath.ToSlash(rel)
	}
	remote := strings.TrimSpace(os.Getenv(envLiveRemote))
	if remote == "" {
		remote = defaultRemote
	}
	st := &signals.Store{
		Repo: repo, RemoteName: remote, Ref: signals.CodeRepoRef,
		Seen:  signals.SeenStore{Path: filepath.Join(strings.TrimSpace(string(gitDir)), signalsStateDir, signalsSeenFile)},
		Clock: a, Lease: signals.Lease{TTL: defaultLeaseTTL, RenewEvery: defaultLeaseRenewEvery, Grace: defaultLeaseGrace},
	}
	v, err := st.Local(ctx)
	if err != nil {
		return localBoard{}, errs.Wrap(errs.Internal, "claims: read the signals ref", err)
	}
	b.view = v
	return b, nil
}

// projectDir is the folder the command runs for (WorkDir, else RepoDir).
func (a *App) projectDir() string {
	if a.opts.WorkDir != "" {
		return a.opts.WorkDir
	}
	return a.opts.RepoDir
}

// localBoard is an opstask.ClaimBoard over one signals view (an empty
// view: no claims).
type localBoard struct {
	view   signals.View
	folder string
	me     string
}

var (
	_ opstask.ClaimBoard   = localBoard{}
	_ opstask.ClaimSpanner = localBoard{}
)

// Me implements opstask.ClaimBoard.
func (b localBoard) Me() string { return b.me }

// Claim implements opstask.ClaimBoard.
func (b localBoard) Claim(taskID string) (string, bool, bool) {
	p := signals.ClaimPath(b.folder, taskID)
	sig, claimed := b.view.Board[p]
	_, active := b.view.Active(p)
	return sig.Holder, claimed, active
}

// stateClock is the STATE column's time of day (v0.1.17 clockLayout).
const stateClock = "15:04"

// State implements opstask.ClaimBoard: v0.1.17's taskState (§3.5 STATE).
func (b localBoard) State(t opstask.View) string {
	p := signals.ClaimPath(b.folder, t.ID)
	sig, claimed := b.view.Board[p]
	_, active := b.view.Active(p)
	switch {
	case t.Status == schema.TaskStatusDone && !t.MergedAt.IsZero():
		return "✓ done · merged " + t.MergedAt.UTC().Format(stateClock)
	case t.Status == schema.TaskStatusDone && (t.DonePR != "" || t.DoneCommit != ""):
		return "✓ done · not merged yet"
	case t.Status == schema.TaskStatusDone:
		return ""
	case claimed && active:
		return fmt.Sprintf("▶ working: %s (until %s)", holderName(sig), sig.Expires.UTC().Format(stateClock))
	case claimed:
		return fmt.Sprintf("▶ EXPIRED %s ago (%s)", agoText(b.view.Now.Sub(sig.Expires)), holderName(sig))
	case t.Gate != "":
		return "⏸ gate: " + gateState(t)
	}
	return ""
}

// OpenClaim implements opstask.ClaimSpanner (TIME_TRACKING 5.1 rule 1).
func (b localBoard) OpenClaim(taskID string) (opstask.OpenClaim, bool) {
	p := signals.ClaimPath(b.folder, taskID)
	sig, ok := b.view.Board[p]
	if !ok || sig.Kind != signals.KindClaim || sig.Since.IsZero() {
		return opstask.OpenClaim{}, false
	}
	_, active := b.view.Active(p)
	end := b.view.Now
	if !active {
		end, _ = sig.LastHeartbeat(b.view.Lease.TTL)
	}
	return opstask.OpenClaim{Holder: sig.Holder, Session: sig.Session, Since: sig.Since, End: end, Expired: !active}, true
}

// agentLabelPrefix starts an agent's holder label ("agent:frontend-1").
const agentLabelPrefix = "agent:"

// holderName is how the STATE column names a holder (v0.1.17 holderLabel).
func holderName(s signals.Signal) string {
	if s.HolderLabel != "" {
		return strings.TrimPrefix(s.HolderLabel, agentLabelPrefix)
	}
	return s.Holder
}

// agoText is a duration as "N min" / "N h" (v0.1.17 agoString).
func agoText(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h", int(d.Hours()))
}

// gateState describes a task's gate (v0.1.17 coord.GateState). The
// approval hash is sha256 over title, NUL, body (coord.GateHash), the
// value every binary writes to approved_hash.
func gateState(t opstask.View) string {
	switch t.Gate {
	case "":
		return ""
	case schema.TaskGateApproved:
		sum := sha256.Sum256([]byte(t.Title + "\x00" + t.Body))
		if t.ApprovedHash == hex.EncodeToString(sum[:]) {
			return "approved"
		}
		return "approval voided by an edit"
	case schema.TaskGateRequired:
		return "needs approval"
	}
	return "approval requested"
}
