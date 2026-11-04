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
		// Same rule as `task done`: only a checkout without team
		// coordination (no git repo, no live rows, no roster actor) and
		// without store links writes a plain comment here. A linked repo
		// whose target cannot be opened (store unreachable, link status
		// unknown) fails closed: nothing printed, nothing written, so a
		// note on a store task never lands in a client repo (N2).
		if ferr := plainRowFallback(ctx, err); ferr != nil {
			return ferr
		}
		return plainNote(ctx, id, body)
	}
	defer t.Close()
	cs, err := readClaim(ctx, w, t, id)
	if err != nil {
		return coordError(err)
	}
	if !cs.mine && !cs.hadClaim {
		return unheldNote(ctx, w, t, id, body)
	}
	branch := currentWorkBranch(ctx, w.Repo)
	// Comments are core-owned (lsync.CoreOwned): the note is a document
	// staged with the claim; liveRows/clientRows.Stage write it through the
	// core (or, for an internal note, to the store project's live ref), and
	// nothing is ever written into lore.db first.
	cid, doc, err := newCommentDoc(id, body, w.ActorID, w.Clock.Now())
	if err != nil {
		return err
	}
	_, err = coord.HolderWrite(ctx, w.env(t), id, false, func(d coord.Doc, _ time.Time) ([]coord.RowWrite, error) {
		if branch != "" {
			d.SetStr("work_branch", branch)
		}
		return []coord.RowWrite{{Table: tableComments, ID: cid, Doc: doc}}, nil
	})
	if err != nil {
		// Fenced out or offline: nothing was written (the core companion
		// runs only after the signals push).
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

// unheldNote is a note by someone who does not hold the task (§7.6, D8):
// not fenced (there is no claim to fence), but it goes where the task's
// notes live, exactly as a holder's note would. A task of this repo with
// notes on the repo side (own repo, no links, or agent_details: client) is
// a normal comment row in this checkout; a store task, or a linked client
// repo's own task whose notes stay internal, gets the comment on the
// store's live ref. Before N2 every unheld note went to this checkout, so a
// note on a store task was routed to the client repo and refused by the
// leak check after a ✓ had already been printed.
func unheldNote(ctx context.Context, w *coordWorld, t coordTarget, id, body string) error {
	switch r := t.Rows.(type) {
	case *liveRows:
		return plainNote(ctx, id, body)
	case *clientRows:
		if !r.internalNote() {
			return plainNote(ctx, id, body)
		}
	}
	if _, found, err := t.Rows.Get(ctx, id); err != nil {
		return coordError(err)
	} else if !found {
		return errcodes.New(errcodes.NotFound, "task "+id+" not found").
			WithHint("`lore task list` shows the tasks this checkout can reach; --from picks another source")
	}
	cid, doc, err := newCommentDoc(id, body, w.ActorID, w.Clock.Now())
	if err != nil {
		return err
	}
	comps, err := t.Rows.Stage(ctx, coord.RowWrite{Table: tableComments, ID: cid, Doc: doc})
	if err != nil {
		return err
	}
	for _, c := range comps {
		if err := pushCompanion(ctx, c); err != nil {
			return err
		}
	}
	fmt.Printf("%s note %s on %s\n", style.Success("✓"), cid, id)
	return nil
}

// pushCompanion writes one staged row commit on its own, outside a signals
// write: build it, push it to its remote, then move the local ref. A note
// that is not fenced has no signals ref to ride along with.
func pushCompanion(ctx context.Context, c signals.Companion) error {
	var rc signals.RemoteCompanion
	switch v := c.(type) {
	case *remoteStagedRows:
		rc = v
	case *stagedLiveRows:
		rc = &remoteStagedRows{*v}
	default:
		return errcodes.New(errcodes.Internal, fmt.Sprintf("cannot push a %T on its own", c))
	}
	if _, err := rc.Prepare(ctx); err != nil {
		return err
	}
	if err := rc.PushSelf(ctx); err != nil {
		return coordError(&signals.OfflineError{Reason: "live ref push failed", Err: err})
	}
	return rc.Finalize(ctx)
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
	if _, err := rows.refresh(ctx); err != nil {
		return coordError(err)
	}
	cid, doc, err := newCommentDoc(id, body, owner, w.Clock.Now())
	if err != nil {
		return err
	}
	// Comments are core-owned: the row file is written through the core.
	if err := coreWriteDoc(ctx, tableComments, cid, doc); err != nil {
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
	cid, doc, err := newCommentDoc(id, body, by, cliClock().Now())
	if err != nil {
		return err
	}
	if err := coreWriteDoc(ctx, tableComments, cid, doc); err != nil {
		return err
	}
	fmt.Printf("%s note %s on %s\n", style.Success("✓"), cid, id)
	return nil
}
