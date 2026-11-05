package main

// coord_inbox_more.go — the TASK 20 inbox items computed on read from the
// task documents, the claims board, the comments and the policy
// (FB-TASK-3, A-TASK-7, A-TASK-8):
//
//	triage     proposed tasks, for the actor tasks.triage_owner names (18.3)
//	child      a sub-task of your task finished; your task has no open sub-tasks left
//	parent     your open task's parent was closed, or is gone (6.13)
//	deleted    a task you hold a claim on went to the trash (6.12, FB-TASK-1)
//	mention    @you in a task body or a comment on a task (comments)
//	recurring  a recurring instance assigned to you, created by someone else (10)
//
// "Your task" is one you lead, are an assignee of, or created. Nothing is
// stored: like the other items, each is derived from rows every clone has,
// so two machines show the same inbox (the --ack marker is per checkout).

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"dbent/schema"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/row"
	lschema "github.com/thesatellite-ai/lore/schema"
)

// Inbox item kinds of this file.
const (
	inboxTriage    = "triage"
	inboxChild     = "child"
	inboxParent    = "parent"
	inboxDeleted   = "deleted"
	inboxMention   = "mention"
	inboxRecurring = "recurring"
)

// Task document keys these items read.
const (
	docKeyParent      = "parent_id"
	docKeyCommitment  = "commitment"
	docKeyCompletedAt = "completed_at"
	docKeyRecur       = "recur"
	docKeyDueAt       = "due_at"
	// comment fields (schema linkDefs)
	docKeyEntityTable = "entity_table"
	docKeyEntityID    = "entity_id"
)

// commitmentProposed is the commitment triage works on (TASK 18.3).
const commitmentProposed = string(schema.TaskCommitmentProposed)

// docOpen: todo, in_progress or blocked (not finished).
func docOpen(d coord.Doc) bool {
	s := d.Status()
	return s != string(schema.TaskStatusDone) && s != string(schema.TaskStatusCancelled)
}

// moreInboxItems returns every item of this file for me over the project's
// live task documents.
func (cv *coordView) moreInboxItems(ctx context.Context, docs []coord.Doc, me string) []inboxItem {
	byID := make(map[string]coord.Doc, len(docs))
	kids := map[string][]coord.Doc{}
	for _, d := range docs {
		byID[d.Str(docKeyID)] = d
		if p := d.Str(docKeyParent); p != "" {
			kids[p] = append(kids[p], d)
		}
	}
	mine := func(d coord.Doc) bool {
		return d.Str(fieldCreatedByActor) == me || d.Str(fieldTaskLead) == me || slices.Contains(docStrs(d, lschema.FieldTaskAssignees), me)
	}
	assigned := func(d coord.Doc) bool {
		return d.Str(fieldTaskLead) == me || slices.Contains(docStrs(d, lschema.FieldTaskAssignees), me)
	}
	var out []inboxItem
	triage := cv.isTriageOwner(ctx, me)
	for _, d := range docs {
		id, title, mission := d.Str(docKeyID), d.Str(fieldTitle), d.Str(fieldMissionID)
		if triage && docOpen(d) && d.Str(docKeyCommitment) == commitmentProposed {
			out = append(out, inboxItem{Mission: mission, Kind: inboxTriage, Who: cv.actorLabel(ctx, d.Str(fieldCreatedByActor)), At: d.Time(docKeyCreatedAt),
				Text: fmt.Sprintf("%s %s: proposed; triage it (lore task accept|decline %s)", id, title, id)})
		}
		if ch := kids[id]; len(ch) > 0 && mine(d) {
			out = append(out, cv.childItems(ctx, d, ch, me)...)
		}
		if p := d.Str(docKeyParent); p != "" && docOpen(d) && assigned(d) {
			if pd, ok := byID[p]; !ok {
				out = append(out, inboxItem{Mission: mission, Kind: inboxParent,
					Text: fmt.Sprintf("%s %s: its parent %s is gone (in the trash or not attached); lore task edit %s --no-parent", id, title, p, id)})
			} else if !docOpen(pd) {
				out = append(out, inboxItem{Mission: mission, Kind: inboxParent, At: pd.Time(docKeyCompletedAt),
					Text: fmt.Sprintf("%s %s: its parent %s %s is %s while this is open", id, title, p, pd.Str(fieldTitle), pd.Status())})
			}
		}
		if it, ok := recurringItem(ctx, cv, d, me, assigned(d)); ok {
			out = append(out, it)
		}
	}
	out = append(out, cv.deletedClaimItems(byID, me)...)
	out = append(out, cv.mentionItems(ctx, docs, byID, me)...)
	return out
}

// childItems: each sub-task of parent finished by someone else, and, when
// parent is open and none of its sub-tasks is, the "close it" line.
func (cv *coordView) childItems(ctx context.Context, parent coord.Doc, ch []coord.Doc, me string) []inboxItem {
	var out []inboxItem
	open := 0
	pid, mission := parent.Str(docKeyID), parent.Str(fieldMissionID)
	for _, c := range ch {
		if docOpen(c) {
			open++
			continue
		}
		by := c.Str(docKeyDoneBy)
		if by == me {
			continue // one's own finish is not news
		}
		out = append(out, inboxItem{Mission: mission, Kind: inboxChild, Who: cv.actorLabel(ctx, by), At: c.Time(docKeyCompletedAt),
			Text: fmt.Sprintf("%s %s: sub-task %s %s is %s", pid, parent.Str(fieldTitle), c.Str(docKeyID), c.Str(fieldTitle), c.Status())})
	}
	if open == 0 && docOpen(parent) {
		out = append(out, inboxItem{Mission: mission, Kind: inboxChild,
			Text: fmt.Sprintf("%s %s: no open sub-tasks left; close it (lore task done %s)", pid, parent.Str(fieldTitle), pid)})
	}
	return out
}

// docKeyDoneBy is who finished a task (coord MarkDone); empty for a close
// outside the claim flow.
const docKeyDoneBy = "done_by"

// recurringItem: an open recurring instance (index > 1) assigned to me and
// created by someone else (TASK 10: finishing the previous instance made it).
func recurringItem(ctx context.Context, cv *coordView, d coord.Doc, me string, assigned bool) (inboxItem, bool) {
	if !assigned || !docOpen(d) || d.Str(fieldCreatedByActor) == me {
		return inboxItem{}, false
	}
	rc, ok := d[docKeyRecur].(map[string]any)
	if !ok {
		return inboxItem{}, false
	}
	n := coord.Doc(rc).Int("index")
	if n < 2 {
		return inboxItem{}, false
	}
	due := ""
	if t := d.Time(docKeyDueAt); !t.IsZero() {
		due = " (due " + t.Format("Mon 2006-01-02") + ")"
	}
	return inboxItem{Mission: d.Str(fieldMissionID), Kind: inboxRecurring, Who: cv.actorLabel(ctx, d.Str(fieldCreatedByActor)), At: d.Time(docKeyCreatedAt),
		Text: fmt.Sprintf("%s %s: next recurring instance created for you%s", d.Str(docKeyID), d.Str(fieldTitle), due)}, true
}

// deletedClaimItems: my claims in this folder whose task is no longer a
// live row (trashed while held, 6.12): release drops the claim (FB-TASK-1).
func (cv *coordView) deletedClaimItems(byID map[string]coord.Doc, me string) []inboxItem {
	if !cv.hasBoard {
		return nil
	}
	var out []inboxItem
	for p, s := range cv.board.Board {
		if s.Kind != signals.KindClaim || s.Holder != me || p != signals.ClaimPath(cv.folder, s.Task) || coord.IsMission(s.Task) {
			continue
		}
		if _, live := byID[s.Task]; live {
			continue
		}
		out = append(out, inboxItem{Kind: inboxDeleted, At: s.Since,
			Text: fmt.Sprintf("%s: the task you claimed was deleted; stop work on it and release the claim (lore task release %s)", s.Task, s.Task)})
	}
	return out
}

// mentionItems: "@me" in a task body or in a comment on a task of this
// project, written by someone else; one item per task (the newest).
func (cv *coordView) mentionItems(ctx context.Context, docs []coord.Doc, byID map[string]coord.Doc, me string) []inboxItem {
	re := cv.mentionPattern(ctx, me)
	if re == nil {
		return nil
	}
	seen := map[string]inboxItem{}
	add := func(task coord.Doc, it inboxItem) {
		id := task.Str(docKeyID)
		if prev, ok := seen[id]; ok && !it.At.After(prev.At) {
			return
		}
		it.Mission = task.Str(fieldMissionID)
		seen[id] = it
	}
	for _, d := range docs {
		if d.Str(fieldCreatedByActor) != me && re.MatchString(d.Str(fieldBody)) {
			add(d, inboxItem{Kind: inboxMention, Who: cv.actorLabel(ctx, d.Str(fieldCreatedByActor)), At: d.Time(docKeyCreatedAt),
				Text: fmt.Sprintf("%s %s: you are mentioned in the description", d.Str(docKeyID), d.Str(fieldTitle))})
		}
	}
	if coreApp != nil {
		for _, src := range coreApp.Stores() {
			rows, err := coreApp.List(ctx, src, row.Table(lschema.TableComments))
			if err != nil {
				continue // an unreadable store hides its comments, not the inbox
			}
			for _, r := range rows {
				if r.Deleted {
					continue
				}
				c, err := coreDocOf(r)
				if err != nil || c.Str(docKeyEntityTable) != lschema.TableTasks || c.Str(fieldCreatedByActor) == me || !re.MatchString(c.Str(fieldBody)) {
					continue
				}
				task, ok := byID[c.Str(docKeyEntityID)]
				if !ok {
					continue
				}
				add(task, inboxItem{Kind: inboxMention, Who: cv.actorLabel(ctx, c.Str(fieldCreatedByActor)), At: c.Time(docKeyCreatedAt),
					Text: fmt.Sprintf("%s %s: you are mentioned in a comment", task.Str(docKeyID), task.Str(fieldTitle))})
			}
		}
	}
	out := make([]inboxItem, 0, len(seen))
	for _, it := range seen {
		out = append(out, it)
	}
	return out
}

// myNames are the names an @mention or tasks.triage_owner may use for me:
// the actor id, handle, label and a one-word display name (lower case).
func (cv *coordView) myNames(ctx context.Context, me string) []string {
	names := []string{strings.ToLower(me)}
	if d, ok, err := coreDoc(ctx, lschema.TableActors, me); err == nil && ok {
		for _, k := range []string{fieldHandle, fieldLabel, fieldDisplayName} {
			if v := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d.Str(k)), "@")); v != "" && !strings.ContainsAny(v, " \t") {
				names = append(names, v)
			}
		}
	}
	if l := strings.ToLower(strings.TrimPrefix(cv.actorLabel(ctx, me), "@")); l != "" && !strings.ContainsAny(l, " \t") {
		names = append(names, l)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// mentionPattern matches "@<one of my names>" as a whole word, any case.
func (cv *coordView) mentionPattern(ctx context.Context, me string) *regexp.Regexp {
	names := cv.myNames(ctx, me)
	alts := make([]string, 0, len(names))
	for _, n := range names {
		alts = append(alts, regexp.QuoteMeta(n))
	}
	if len(alts) == 0 {
		return nil
	}
	re, err := regexp.Compile(`(?i)(^|[^\w@])@(` + strings.Join(alts, "|") + `)\b`)
	if err != nil {
		return nil
	}
	return re
}

// isTriageOwner: tasks.triage_owner names me (id, handle, label or a
// one-word display name; a leading @ ignored). Unset = nobody.
func (cv *coordView) isTriageOwner(ctx context.Context, me string) bool {
	set, ok := policySet(ctx)
	if !ok {
		return false
	}
	ref, isSet := set.Text(policy.KeyTasksTriageOwner)
	if !isSet {
		return false
	}
	return slices.Contains(cv.myNames(ctx, me), strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ref), "@")))
}
