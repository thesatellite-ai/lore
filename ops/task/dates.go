package task

import (
	"context"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/dateflag"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/store"
)

// Date flags read TASK 12's phrase table (logic/dates, T11). The day is
// read in the machine's local zone and stored as the UTC calendar day
// (TSQ11); a clock time is local, stored UTC. Anything outside the table
// is E_DATE with the list of forms. Lore always says what it stored for a
// phrase (R12 "never silent conversion"): parseDate returns that line.

// defaultWeekEnd is tasks.week_end's default (TASK 12.1).
const defaultWeekEnd = dateflag.DefaultWeekEnd

// clock is the phrase clock of target (dateflag.Clock).
func (o ops) clock(ctx context.Context, target store.Source) (dates.Clock, error) {
	return dateflag.Clock(ctx, o.env, target)
}

// parseDate reads one date flag value (dateflag.Parse, the one reader of
// every date flag).
func parseDate(c dates.Clock, flag, raw string) (dates.Result, string, error) {
	return dateflag.Parse(c, flag, raw)
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

// DateValueFor is DateValue with tasks.week_end read from env's policy at
// target, the clock task flags use (mission --target / --starts-at read
// `eow` the same way, TASK 12.1).
func DateValueFor(ctx context.Context, env work.Env, target store.Source, flag, raw string) (value.Value, string, error) {
	c, err := ops{env: env}.clock(ctx, target)
	if err != nil {
		return nil, "", err
	}
	return dateValue(c, flag, raw)
}
