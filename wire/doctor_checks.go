package wire

// doctor_checks.go — five `lore doctor` checks of the 0.1.18 design
// (CLI_REFERENCE 3.7, TASK_SYSTEM_DESIGN 14.4 and 18.3), each one method
// returning typed findings so the old binary's doctor and a later doctor
// operation call the same code (P3-DOC-1):
//
//	DoctorTaskLeads        a task lead missing from its assignees (14.4); fix writes it back
//	DoctorTriageOwner      tasks.triage_owner names no known actor (18.3, FB-TASK-3)
//	DoctorStoreLocks       the holder of a held store lock (SPEC 6.2, E_STORE_LOCKED hint)
//	DoctorEditsAfterPurge  a git commit with an edit newer than the row's tombstone (SPEC 10/11 rule 3, DQ4)
//	DoctorInternalOff      commits that dropped a key's internal flag without the record (META 3.8 RX7)
//
// Every check reads only, except DoctorTaskLeads with fix. A check that
// cannot run (no store open, not a git checkout) returns no findings: the
// other doctor lines already report a broken store.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheck names the check a finding comes from (the JSON "check" key).
type DoctorCheck string

// The checks of this file.
const (
	DoctorCheckTaskLead       DoctorCheck = "task_lead"
	DoctorCheckTriageOwner    DoctorCheck = "triage_owner"
	DoctorCheckStoreLock      DoctorCheck = "store_lock"
	DoctorCheckEditAfterPurge DoctorCheck = "edit_after_purge"
	DoctorCheckInternalOff    DoctorCheck = "internal_off"
)

// DoctorLevel is how a finding counts toward doctor's status: a warning
// degrades it (exit 1), a note does not.
type DoctorLevel string

// The levels.
const (
	DoctorWarning DoctorLevel = "warning"
	DoctorNote    DoctorLevel = "note"
)

// DoctorFinding is one doctor line.
type DoctorFinding struct {
	Check DoctorCheck  `json:"check"`
	Level DoctorLevel  `json:"level"`
	Store store.Source `json:"store,omitempty"`
	// Message is the human line (without the check name).
	Message string `json:"message"`
	// Fix is the command or step that repairs it ("" when none).
	Fix string `json:"fix,omitempty"`
	// Fixed is true when doctor --fix repaired it in this run.
	Fixed bool `json:"fixed,omitempty"`
}

// Line is the finding as one text line: "<check>: <message>[; fix: …]".
func (f DoctorFinding) Line() string {
	s := string(f.Check) + ": " + f.Message
	if f.Fixed {
		return s + " (fixed)"
	}
	if f.Fix != "" {
		s += "; fix: " + f.Fix
	}
	return s
}

// fieldTaskLead is the lead column (kept under its v0.1.17 name for old
// readers, TSQ5).
const fieldTaskLead = "assigned_to_actor_id"

// DoctorTaskLeads finds live tasks whose lead is missing from assignees
// (an old binary wrote only assigned_to_actor_id, or a merge removed it;
// TASK 14.4). Readers already count the lead as an assignee; with fix the
// lead is written back at the front of the list (the 14.2 order), one
// engine write per task, so the next reader of any binary sees it.
// DECISION P3-DOC-2: the write-back is `lore doctor --fix` (doctor's one
// safe-repair flag), not a separate `doctor --fix tasks` form.
func (a *App) DoctorTaskLeads(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	if _, err := a.state(ctx); err != nil {
		return nil, err
	}
	var out []DoctorFinding
	for _, src := range a.Stores() {
		rows, err := a.List(ctx, src, schema.TableTasks)
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			lead, assignees := taskLead(r)
			if r.Deleted || lead == "" || slices.Contains(assignees, lead) {
				continue
			}
			f := DoctorFinding{Check: DoctorCheckTaskLead, Level: DoctorWarning, Store: src,
				Message: "task " + r.ID.String() + ": lead " + lead + " is not in its assignees (readers count it; written back on the next edit)",
				Fix:     "lore doctor --fix"}
			if fix {
				if werr := a.writeLeadBack(ctx, src, r, lead, assignees); werr != nil {
					f.Message += " (not fixed: " + werr.Error() + ")"
				} else {
					f.Fixed = true
				}
			}
			out = append(out, f)
		}
	}
	return out, nil
}

// taskLead is a task row's lead and assignee list.
func taskLead(r row.Row) (string, []string) {
	lead := ""
	if v, ok := r.Fields[fieldTaskLead]; ok && v != nil && v.Kind() != value.KindNull {
		lead = v.String()
	}
	return lead, value.ItemStrings(r.Fields[schema.FieldTaskAssignees])
}

// writeLeadBack writes assignees = [lead, assignees...] on one task.
func (a *App) writeLeadBack(ctx context.Context, src store.Source, r row.Row, lead string, assignees []string) error {
	items := make([]value.Value, 0, len(assignees)+1)
	for _, x := range append([]string{lead}, assignees...) {
		t, err := value.TextOf(x)
		if err != nil {
			return err
		}
		items = append(items, t)
	}
	l, err := value.ListOf(value.KindText, items...)
	if err != nil {
		return err
	}
	_, err = a.Update(ctx, engine.Ref{Store: src, Table: schema.TableTasks, ID: r.ID}, map[string]value.Value{schema.FieldTaskAssignees: l})
	return err
}

// agentKindPrefix is the "agent:" a display name may carry (identity
// KindAgent), dropped when matching names as the inbox's labels do.
const agentKindPrefix = "agent:"

// DoctorTriageOwner warns when tasks.triage_owner is set but names no
// actor of any attached store (id, stable key, handle, label or one-word
// display name, a leading @ and case ignored: the inbox's matching,
// FB-TASK-3), because then nobody's inbox lists proposed tasks.
func (a *App) DoctorTriageOwner(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	set, err := st.eng.Policy(store.SourceRepo)
	if err != nil {
		return nil, err
	}
	raw, ok := set.Text(policy.KeyTasksTriageOwner)
	want := normName(raw)
	if !ok || want == "" {
		return nil, nil // unset = nobody, on purpose
	}
	for _, src := range a.Stores() {
		rows, lerr := a.List(ctx, src, schema.TableActors)
		if lerr != nil {
			return nil, lerr
		}
		for _, r := range rows {
			if !r.Deleted && slices.Contains(actorNames(r), want) {
				return nil, nil
			}
		}
	}
	return []DoctorFinding{{Check: DoctorCheckTriageOwner, Level: DoctorWarning,
		Message: string(policy.KeyTasksTriageOwner) + " is " + raw + ", which matches no actor; no inbox lists proposed tasks",
		Fix:     "lore policy set " + string(policy.KeyTasksTriageOwner) + " <id or @handle> --to team"}}, nil
}

// normName is a name as the inbox compares it: trimmed, no leading @,
// lower case.
func normName(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
}

// actorNames are the names tasks.triage_owner may use for an actor row.
func actorNames(r row.Row) []string {
	out := []string{normName(r.ID.String())}
	for _, k := range []string{"stable_key", "handle", "label", "display_name"} {
		v, ok := r.Fields[k]
		if !ok || v == nil || v.Kind() == value.KindNull {
			continue
		}
		n := normName(strings.TrimPrefix(v.String(), agentKindPrefix))
		if n != "" && !strings.ContainsAny(n, " \t") {
			out = append(out, n)
		}
	}
	return out
}

// lockHolder is a backend whose store lock doctor can probe (folder and
// git backends; optional, so another backend is simply not probed).
type lockHolder interface {
	LockHolder() (rowfs.LockState, error)
}

// DoctorStoreLocks names the holder of every store lock held right now
// (the pid and time the holder wrote), once per lock file: the folders of
// one store share it. E_STORE_LOCKED's hint points here.
func (a *App) DoctorStoreLocks(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []DoctorFinding
	for _, src := range a.Stores() {
		lh, ok := st.byName[src].(lockHolder)
		if !ok {
			continue
		}
		ls, perr := lh.LockHolder()
		if perr != nil || !ls.Held || seen[ls.Path] {
			continue // a probe that fails says nothing about a holder
		}
		seen[ls.Path] = true
		out = append(out, DoctorFinding{Check: DoctorCheckStoreLock, Level: DoctorWarning, Store: src,
			Message: "store " + string(src) + ": the lock " + ls.Path + " is held by " + holderText(ls),
			Fix:     "wait for that lore command to finish; the lock is freed when its process exits"})
	}
	return out, nil
}

// holderText is "pid N since <time> (<age> ago)", shorter when the holder
// line was missing.
func holderText(ls rowfs.LockState) string {
	if ls.PID == 0 {
		return "a process that left no holder line"
	}
	s := fmt.Sprintf("pid %d", ls.PID)
	if !ls.Since.IsZero() {
		s += " since " + ls.Since.UTC().Format(time.RFC3339) + " (" + time.Since(ls.Since).Round(time.Second).String() + " ago)"
	}
	return s
}

// gitRowFiler is a backend whose rows sit in a git checkout (the git
// backend), so doctor can read a row's history.
type gitRowFiler interface {
	RowFile(table row.Table, id ids.AnyID) (dir, rel string)
}

// Caps of the purge history walk: doctor must stay quick on a store with
// many purged rows or a long history; past the cap a note says so.
const (
	maxPurgedRowsWalked = 200
	maxVersionsPerRow   = 50
)

// DoctorEditsAfterPurge finds purged rows (tombstones) whose git history
// holds an edit with a higher HLC than the tombstone: a clone edited the
// row after another purged it, and the merge kept the tombstone (rows are
// never resurrected; SPEC 11 rule 3, DQ4), so that edit is lost unless
// someone re-creates it. DECISION P3-DOC-3: the merge result is not
// stored anywhere, so doctor reads it back from git history
// (--full-history, so the merged-away side is walked too); folder stores
// have no history and are not checked.
func (a *App) DoctorEditsAfterPurge(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	var out []DoctorFinding
	walked := 0
	for _, src := range a.Stores() {
		gf, ok := st.byName[src].(gitRowFiler)
		if !ok {
			continue
		}
		for _, table := range CoreTables() {
			rows, lerr := a.List(ctx, src, table)
			if lerr != nil {
				return out, lerr
			}
			for _, r := range rows {
				if !r.Deleted {
					continue
				}
				if walked == maxPurgedRowsWalked {
					return append(out, DoctorFinding{Check: DoctorCheckEditAfterPurge, Level: DoctorNote,
						Message: fmt.Sprintf("only the first %d purged rows were checked", maxPurgedRowsWalked)}), nil
				}
				walked++
				dir, rel := gf.RowFile(r.Table, r.ID)
				if f, found := editAfterPurge(ctx, dir, rel, r); found {
					f.Store = src
					out = append(out, f)
				}
			}
		}
	}
	return out, nil
}

// editAfterPurge walks the history of one tombstone's file for a live
// version with a higher HLC; found is false when there is none or git
// cannot tell (not a checkout, no history).
func editAfterPurge(ctx context.Context, dir, rel string, tomb row.Row) (DoctorFinding, bool) {
	revs, err := liveGit(ctx, dir, "log", "--full-history", "--no-merges", "--format=%H",
		fmt.Sprintf("-n%d", maxVersionsPerRow), "HEAD", "--", rel)
	if err != nil {
		return DoctorFinding{}, false
	}
	for rev := range strings.SplitSeq(strings.TrimSpace(string(revs)), "\n") {
		if rev == "" {
			continue
		}
		doc, serr := liveGit(ctx, dir, "show", rev+":./"+rel)
		if serr != nil {
			continue // the commit removed the file
		}
		old, derr := row.Decode(doc)
		if derr != nil || old.Deleted || old.HLC.Compare(tomb.HLC) <= 0 {
			continue // a v0.1.17 document, another tombstone, or an older edit
		}
		short := rev
		if len(short) > 12 {
			short = short[:12]
		}
		return DoctorFinding{Check: DoctorCheckEditAfterPurge, Level: DoctorWarning,
			Message: string(tomb.Table) + " " + tomb.ID.String() + " was purged (" + tomb.HLC.String() + "), but commit " + short +
				" holds a later edit (" + old.HLC.String() + "); the purge was kept and that edit is not applied",
			Fix: "if the edit mattered, re-create the row from git show " + short + ":" + rel}, true
	}
	return DoctorFinding{}, false
}

// gitWorker is a backend that commits in its own git checkout.
type gitWorker interface{ WorkDir() string }

// DoctorInternalOff lists the commits not yet on the remote (else the
// previous commit) that turned a metadata key's internal, sensitive or
// strict flag off without lore's changed_by record (the check `lore check
// --ci` and the landing run, KeyLooseningsSince; METADATA 3.8 RX7), in
// the code repo and in every git store checkout. DECISION P3-DOC-4: the
// design's "without lore's trailer" is the built changed_by record
// (OV64), so doctor reuses that one check rather than parsing trailers.
func (a *App) DoctorInternalOff(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	type place struct {
		src store.Source
		dir string
	}
	var places []place
	if top, terr := a.knownRepoTop(ctx); terr == nil && top != "" {
		places = append(places, place{src: store.SourceRepo, dir: top})
	}
	for _, src := range a.Stores() {
		if gw, ok := st.byName[src].(gitWorker); ok && src != store.SourceRepo {
			places = append(places, place{src: src, dir: gw.WorkDir()})
		}
	}
	seen := map[string]bool{}
	var out []DoctorFinding
	for _, p := range places {
		if seen[p.dir] {
			continue
		}
		seen[p.dir] = true
		found, kerr := KeyLooseningsSince(ctx, p.dir, "")
		if kerr != nil {
			continue // not a git checkout, or no common history
		}
		for _, f := range found {
			msg, hint := f.Error(), ""
			var e *errs.Error
			if errors.As(f, &e) {
				msg, hint = e.Message, e.Hint
			}
			out = append(out, DoctorFinding{Check: DoctorCheckInternalOff, Level: DoctorWarning, Store: p.src, Message: msg, Fix: hint})
		}
	}
	return out, nil
}
