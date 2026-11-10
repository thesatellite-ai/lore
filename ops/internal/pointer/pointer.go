// Package pointer applies F15's direction rule (logic/direction) to the
// core ops that store a pointer at another row: task --ref (knowledge_refs),
// comment targets and commit-link targets. Each calls Check with the store
// the pointing row is written to and the row it names, before writing.
package pointer

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Getter is the read Check needs: where a row lives.
type Getter interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
}

// Classifier places an attached store (empty: the default write target)
// in its F15 class; *wire.App implements it. An Env without it is placed
// by name only, with the repo store (and the default target) flagged
// client: without wire's link answer it cannot be told from a client repo
// (fail closed).
type Classifier interface {
	StoreClass(ctx context.Context, name store.Source) (direction.Class, error)
}

// roster tables are not pointers into hidden content: a client row may
// name a person or a team (the client copy of an actor is the F18
// projection), as v0.1.17's checkReferences exempted them.
var roster = map[row.Table]bool{schema.TableActors: true, schema.TableTeams: true}

// Check refuses (E_LEAK_BLOCKED) a pointer stored in a row of store from
// that names the row to, when F15 forbids that direction. A target not
// loaded here (E_NOT_FOUND) cannot be placed and is allowed, as v0.1.17
// allowed it: its readers resolve it to "(not available here)". Any other
// read error is returned (fail closed).
func Check(ctx context.Context, env Getter, from store.Source, to engine.Ref) error {
	if roster[to.Table] {
		return nil
	}
	dst, _, err := env.Get(ctx, to)
	if err != nil {
		if errs.CodeOf(err) == errs.NotFound {
			return nil
		}
		return err
	}
	fc, err := classOf(ctx, env, from)
	if err != nil {
		return err
	}
	tc, err := classOf(ctx, env, dst)
	if err != nil {
		return err
	}
	err = direction.Check(fc, tc)
	var e *errs.Error
	if errors.As(err, &e) {
		named := *e
		named.Message = "pointer to " + to.ID.String() + ": " + e.Message
		return &named
	}
	return err
}

// classOf asks env's Classifier, or places name by itself.
func classOf(ctx context.Context, env Getter, name store.Source) (direction.Class, error) {
	if c, ok := env.(Classifier); ok {
		return c.StoreClass(ctx, name)
	}
	return direction.ClassOf(string(name), name == "" || name == store.SourceRepo), nil
}
