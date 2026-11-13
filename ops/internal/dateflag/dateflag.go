// Package dateflag is the one reader of every date flag (--due, --at,
// --since, --until …): TASK 12's phrase table (logic/dates) with the same
// error and the same "what was stored" note on every command. A-10:
// reminder add --due read only YYYY-MM-DD while task add --due took
// "tomorrow"; a person learns one date grammar, so every flag reads it.
package dateflag

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// DefaultWeekEnd is tasks.week_end's default (TASK 12.1).
const DefaultWeekEnd = time.Friday

// Hint is the example every refused date flag shows.
func Hint(flag string) string {
	return "e.g. --" + flag + " 2026-12-01, or a phrase: tomorrow, fri, in 3d"
}

// Clock is the phrase clock: env's wall clock, the local zone, and
// tasks.week_end as the policy of target resolves it (the default when
// env has no policy reader or no level sets it).
func Clock[E any](ctx context.Context, env E, target store.Source) (dates.Clock, error) {
	c := dates.Clock{Now: opclock.Now(env), Loc: time.Local, WeekEnd: DefaultWeekEnd}
	p, ok := upgrade.As[bulk.Policier](env)
	if !ok {
		return c, nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return dates.Clock{}, err
	}
	if e, isSet := set.Get(policy.KeyTasksWeekEnd); isSet && e.Value != nil {
		if wd, known := dates.WeekdayOf(e.Value.String()); known {
			c.WeekEnd = wd
		}
	}
	return c, nil
}

// Parse reads one date flag value. note is "" for a plain YYYY-MM-DD,
// else "<flag>: Fri 2026-11-13 (from \"next fri\")": lore always says what
// it stored for a phrase (R12 "never silent conversion"). A value outside
// the table is E_DATE naming the flag and listing the forms.
func Parse(c dates.Clock, flag, raw string) (dates.Result, string, error) {
	r, err := dates.Parse(raw, c)
	if err != nil {
		var de *dates.Error
		if errors.As(err, &de) {
			return dates.Result{}, "", errs.New(errs.Date, "--"+flag+": "+de.Error()).WithHint(Hint(flag))
		}
		return dates.Result{}, "", errs.Wrap(errs.Date, "--"+flag, err)
	}
	s := strings.TrimSpace(raw)
	if _, perr := time.Parse(dates.DayLayout, s); perr == nil {
		return r, "", nil
	}
	shown := r.At.Format("Mon ") + r.Text()
	if !r.Day {
		shown = r.At.In(c.Loc).Format("Mon 2006-01-02 15:04 MST") + " (" + r.Text() + ")"
	}
	note := flag + ": " + shown + " (from \"" + s + "\")"
	if r.Note != "" {
		note += "; " + r.Note
	}
	return r, note, nil
}
