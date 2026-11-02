// task_note.go — `lore task note <id> "<text>"` (LORE_TEAM_SPEC §7.2
// "Notes", §7.4 "Work branch", F7 table): a progress checkpoint saved as a
// comment on the task. By the holder it is a fenced write — the comment row
// and the task row go out in one atomic push with the signals lease — and
// the holder's work branch is pushed so a replacement agent can fetch it.
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"encoding/json"
	"strconv"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

// tableComments is the comments table (notes are comments on the task).
const tableComments = "comments"

func newTaskNoteCommand() *cobra.Command {
	var to toFlags
	var from string
	cmd := &cobra.Command{
		Use:   "note <id> <text>",
		Short: "Record a progress note on a task (pushes your work branch)",
		Long: `Note saves a checkpoint a replacement agent can resume from. When you hold the task
it is fenced (exit 12–14 if your claim is gone) and pushed with your claim, and your
current work branch is pushed to the remote and recorded on the task.

In a linked client repo the note stays internal (the store project) unless the
org sets agent_details: client; --to repo shares this one note with the client.

Example:
  lore task note tsk_31 "route + loader done, form next"
  lore task note tsk_01 "fixed rounding; see PR #88" --to repo`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args[1:], " ")
			if to.To != "" {
				return sharedNote(cmd.Context(), args[0], text, to)
			}
			return runTaskNote(cmd.Context(), args[0], text, from)
		},
	}
	bindToFlags(cmd, &to, false)
	bindFromFlag(cmd, &from)
	return cmd
}

func runTaskNote(ctx context.Context, id, text, from string) error {
	body, err := textnorm.Normalize(text)
	if err != nil {
		return errcodes.New(errcodes.EmptyBody, err.Error())
	}
	if err := refuseSecrets(body); err != nil {
		return err
	}
	w, t, err := openTaskCoord(ctx, id, from)
	if err != nil {
		return plainNote(ctx, id, body)
	}
	defer t.Close()
	cs, err := readClaim(ctx, w, t, id)
	if err != nil {
		return coordError(err)
	}
	if !(cs.mine || cs.hadClaim) {
		return plainNote(ctx, id, body)
	}
	branch := currentWorkBranch(ctx, w.Repo)
	var cid string
	var doc coord.Doc
	var rows *liveRows
	switch r := t.Rows.(type) {
	case *liveRows:
		rows = r
		if cid, doc, err = createComment(ctx, rows, id, body, w.ActorID); err != nil {
			return err
		}
	case *clientRows:
		if r.internalNote() {
			// Never touches the client checkout's lore.db: the note goes
			// only to the store project, staged with the claim (D8).
			if cid, doc, err = newCommentDoc(id, body, w.ActorID, w.Clock.Now()); err != nil {
				return err
			}
		} else {
			rows = r.inner
			if cid, doc, err = createComment(ctx, rows, id, body, w.ActorID); err != nil {
				return err
			}
		}
	default:
		if cid, doc, err = newCommentDoc(id, body, w.ActorID, w.Clock.Now()); err != nil {
			return err
		}
	}
	_, err = coord.HolderWrite(ctx, w.env(t), id, false, func(d coord.Doc, _ time.Time) ([]coord.RowWrite, error) {
		if branch != "" {
			d.SetStr("work_branch", branch)
		}
		return []coord.RowWrite{{Table: tableComments, ID: cid, Doc: doc}}, nil
	})
	if err != nil {
		// Fenced out or offline: the note must never be exported later.
		if rows != nil {
			_ = rows.client.Comment.DeleteOneID(cid).Exec(ctx)
			_ = lsync.ClearDirty(ctx, rows.db, []lsync.RowRef{{Table: tableComments, ID: cid}})
		}
		return coordError(err)
	}
	pushed := pushWorkBranch(ctx, w)
	fmt.Printf("%s note %s on %s\n", style.Success("✓"), cid, id)
	switch {
	case pushed:
		fmt.Printf("  work branch %s pushed\n", branch)
	case branch != "":
		fmt.Printf("  %s work branch %s NOT pushed (offline or no remote); a replacement cannot fetch it yet\n", style.Warn("!"), branch)
	}
	return nil
}

// newCommentDoc builds a comment row document without any lore.db, for a
// note that lives only in another project's live ref (a store). The keys
// are the comments table's columns as lsync writes them.
func newCommentDoc(taskID, body, actorID string, now time.Time) (string, coord.Doc, error) {
	id, err := ids.New(ids.PrefixComment)
	if err != nil {
		return "", nil, errcodes.New(errcodes.Internal, "mint note id").WithCause(err)
	}
	d := coord.Doc{
		"_table": tableComments, "id": id, "entity_table": tableTasks, "entity_id": taskID,
		"body": body, "created_by_actor_id": actorID, "entity_project_id": nil,
	}
	d["_v"] = json.Number(strconv.Itoa(lsync.FormatVersion))
	d.SetTime("created_at", now)
	d.SetTime("updated_at", now)
	return id, d, nil
}

// createComment adds the comment to lore.db and returns its canonical doc.
func createComment(ctx context.Context, rows *liveRows, taskID, body, actorID string) (string, coord.Doc, error) {
	c, err := rows.client.Comment.Create().SetEntityTable(tableTasks).SetEntityID(taskID).SetBody(body).
		SetCreatedByActorID(actorID).Save(ctx)
	if err != nil {
		return "", nil, errcodes.New(errcodes.Internal, "create note").WithCause(err)
	}
	b, ok, err := lsync.RowDoc(ctx, rows.db, tableComments, c.ID)
	if err != nil || !ok {
		_ = rows.client.Comment.DeleteOneID(c.ID).Exec(ctx)
		return "", nil, errcodes.New(errcodes.Internal, "read the note back").WithCause(err)
	}
	d, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return "", nil, errcodes.New(errcodes.Internal, "decode the note").WithCause(err)
	}
	return c.ID, coord.Doc(d), nil
}

// sharedNote writes one note where --to says (a deliberate share, §7.6:
// `--to repo` puts this note in the client repo). Not fenced: sharing is a
// person's or an agent's explicit choice about one text, not claim work.
func sharedNote(ctx context.Context, id, text string, to toFlags) error {
	body, err := textnorm.Normalize(text)
	if err != nil {
		return errcodes.New(errcodes.EmptyBody, err.Error())
	}
	if err := refuseSecrets(body); err != nil {
		return err
	}
	if owner, projected := clientAuthor(ctx); projected && to.To == toRepo {
		return agentSharedNote(ctx, id, body, owner)
	}
	var f commonFlags
	if _, err := routeWrite(ctx, &f, to, kindComment, body); err != nil {
		return err
	}
	return plainNoteIn(ctx, &f, id, body)
}

// agentSharedNote puts an agent's deliberately shared note into the client
// repo as its owning person: the comment row and its live block name the
// owner, never the agent (D8, Q11). It is written straight to the client's
// live ref (not through lore.db, whose live writer stamps the caller).
func agentSharedNote(ctx context.Context, id, body, owner string) error {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return err
	}
	t, err := openRepoTarget(ctx, w)
	if err != nil {
		return err
	}
	defer t.Close()
	rows, ok := t.Rows.(*liveRows)
	if !ok {
		return errRowsNotWired()
	}
	if _, _, err := rows.refresh(ctx); err != nil {
		return coordError(err)
	}
	cid, doc, err := newCommentDoc(id, body, owner, w.Clock.Now())
	if err != nil {
		return err
	}
	sp := rows.spot()
	sp.actor = owner
	st := &remoteStagedRows{stagedLiveRows{spot: sp, rows: []coord.RowWrite{{Table: tableComments, ID: cid, Doc: doc}}}}
	if _, err := st.Prepare(ctx); err != nil {
		return err
	}
	if err := st.PushSelf(ctx); err != nil {
		return coordError(&signals.OfflineError{Reason: "client live ref push failed", Err: err})
	}
	if err := st.Finalize(ctx); err != nil {
		return err
	}
	fmt.Printf("%s note %s on %s (shared with the client repo)\n", style.Success("✓"), cid, id)
	return nil
}

// clientAuthor is the author an agent's shared note carries in a linked
// client repo: its owning person (the org identity of this machine), so
// the client never sees the agent actor (D8, coordinator answer Q11).
// projected=false means: use the caller as usual.
func clientAuthor(ctx context.Context) (string, bool) {
	w, err := openCoordWorld(ctx)
	if err != nil || !w.IsAgent() || w.Role != orgresolve.RoleClient || w.Org == nil ||
		w.Org.EffectiveAgentDetails() == lorecfg.AgentDetailsClient || w.Org.Identity.Key == "" {
		return "", false
	}
	id, err := ids.Deterministic(ids.PrefixActor, w.Org.Identity.Key)
	return id, err == nil
}

// kindComment is the routeWrite kind of notes.
const kindComment = "comment"

// plainNote is a note by someone who does not hold the task: a normal
// comment row.
func plainNote(ctx context.Context, id, body string) error {
	return plainNoteIn(ctx, &commonFlags{}, id, body)
}

func plainNoteIn(ctx context.Context, f *commonFlags, id, body string) error {
	by, projected := clientAuthor(ctx)
	_, client, err := resolveContext(f)
	if err != nil {
		return err
	}
	defer client.Close()
	if !projected {
		if by, err = resolveCurrentActorID(ctx, client); err != nil {
			return err
		}
	}
	c, err := client.Comment.Create().SetEntityTable(tableTasks).SetEntityID(id).SetBody(body).SetCreatedByActorID(by).Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create note").WithCause(err)
	}
	fmt.Printf("%s note %s on %s\n", style.Success("✓"), c.ID, id)
	return nil
}
