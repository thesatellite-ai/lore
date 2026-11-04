package task

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// Date flags read TASK 12's phrase table (logic/dates, T11). The day is
// read in the machine's local zone and stored as the UTC calendar day
// (TSQ11); a clock time is local, stored UTC. Anything outside the table
// is E_DATE with the list of forms. Lore always says what it stored for a
// phrase (R12 "never silent conversion"): parseDate returns that line.

// defaultWeekEnd is tasks.week_end's default (TASK 12.1).
const defaultWeekEnd = time.Friday

// clock is the phrase clock: env's wall clock, the local zone, and
// tasks.week_end as the policy of target resolves it (the default when
// env has no policy reader or no level sets it).
func (o ops) clock(ctx context.Context, target store.Source) (dates.Clock, error) {
	c := dates.Clock{Now: opclock.Now(o.env), Loc: time.Local, WeekEnd: defaultWeekEnd}
	p, ok := upgrade.As[bulk.Policier](o.env)
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

// parseDate reads one date flag value. note is "" for a plain
// YYYY-MM-DD, else "<flag>: Fri 2026-11-13 (from \"next fri\")".
func parseDate(c dates.Clock, flag, raw string) (dates.Result, string, error) {
	r, err := dates.Parse(raw, c)
	if err != nil {
		var de *dates.Error
		if errors.As(err, &de) {
			return dates.Result{}, "", errs.New(errs.Date, "--"+flag+": "+de.Error())
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

// dateValue is parseDate as a stored datetime value.
func dateValue(c dates.Clock, flag, raw string) (value.Value, string, error) {
	r, note, err := parseDate(c, flag, raw)
	if err != nil {
		return nil, "", err
	}
	return value.DateTimeFrom(r.At), note, nil
}

// dayText is a date flag value as YYYY-MM-DD (time filters compare days).
func dayText(c dates.Clock, flag, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	r, _, err := parseDate(c, flag, raw)
	if err != nil {
		return "", err
	}
	if !r.Day {
		return r.At.In(c.Loc).Format(dates.DayLayout), nil
	}
	return r.Text(), nil
}

// noteLines prints the date notes, one indented line each.
func noteLines(notes []string) string {
	var b strings.Builder
	for _, n := range notes {
		b.WriteString("  " + n + "\n")
	}
	return b.String()
}

// DateValue reads a date flag of another work noun (mission --target,
// --starts-at) with the same table, clock now in the local zone.
func DateValue(now time.Time, flag, raw string) (value.Value, string, error) {
	return dateValue(dates.Clock{Now: now, Loc: time.Local, WeekEnd: defaultWeekEnd}, flag, raw)
}
