package wire

import (
	"cmp"
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/model/ids"
)

// workBranchWhere says where task d's recorded work branch b is, from
// what git already has here (no fetch, no network):
//
//	this clone's refs/remotes/<remote>/<b> exists → "work branch b (on <remote>)"
//	it does not                                → "work branch b (not pushed; push with: git push -u <remote> <b>)"
//	the task is another repo's                 → "work branch b (in repo <name>: its clone shows whether it is pushed)"
//
// Why (devco:D-619): the inbox's expired item said "work branch pushed (b)"
// from the recorded work_branch alone, but since D-598 a task note no
// longer pushes the branch, so a recorded branch may exist only on the
// holder's machine. A clone that has not fetched since the push says "not
// pushed" until its next fetch: the honest answer from here. A task of
// another repo (a mission across repos) is not judged from this clone's
// refs, which know nothing of that repo's branches.
func (cv *coordView) workBranchWhere(ctx context.Context, d coord.Doc, b string) string {
	if task := d.Str(vkRepo); task != "" {
		if here, ok := cv.a.RepoIdentity(ctx); ok && here != task {
			return "work branch " + b + " (in repo " + engineRows{a: cv.a}.RepoName(task) + ": its clone shows whether it is pushed)"
		}
	}
	remote := cmp.Or(cv.w.remoteName, defaultRemote)
	if cv.w.repo != nil {
		if _, err := cv.w.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--verify", "-q", "refs/remotes/" + remote + "/" + b}}); err == nil {
			return "work branch " + b + " (on " + remote + ")"
		}
	}
	return "work branch " + b + " (not pushed; push with: git push -u " + remote + " " + b + ")"
}

// actorShown is how a coordination message names an actor recorded by id
// (a done marker's or a row's done_by): the roster's handle or name, the
// id only when no attached store knows it, the text itself when it is not
// an actor id (a holder label such as "@ada" or "someone").
//
// Why (devco:D-620): human output names people; "tsk_… was already done
// by act_…" made the reader look the id up.
func (w *coordWorld) actorShown(ctx context.Context, actor string) string {
	if !strings.HasPrefix(actor, string(ids.PrefixActor)+"_") {
		return actor
	}
	return w.a.sourceActorNamer(ctx)(actor) // handle, else name, else the id
}

// namedDoneBy is err with an already-done refusal's finisher named
// (actorShown); any other error is returned as is.
func (w *coordWorld) namedDoneBy(ctx context.Context, err error) error {
	if ad, ok := coord.IsAlreadyDone(err); ok {
		ad.By = w.actorShown(ctx, ad.By)
	}
	return err
}
