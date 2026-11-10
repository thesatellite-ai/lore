package renderhost

// render_trend.go — `lore render --trend` (CONTEXT_BUDGET 6.11): bytes per
// render from render_histories (the machine store, written by every
// render, P3-LOCAL-5), growth per day over the last 14 days, and the projected date
// the core file reaches its budget. Writes nothing.
//
//	--trend ─▶ render_histories rows of the machine store (project, target = --out, last 14 days, oldest first)
//	        ─▶ growth/day = (last − first) / days between them
//	        ─▶ budget (CQ6: smallest target budget) ─▶ days left = (budget − last) / growth
//	        ─▶ text (one line per render + summary) or --json
import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/wire"
)

// renderTrendWindow is the 6.11 window ("growth per day over 14 days").
const renderTrendWindow = 14 * 24 * time.Hour

// trendMinDays is the shortest span a growth rate is computed over.
const trendMinDays = 1.0

// trendPoint is one recorded render.
type trendPoint struct {
	At    time.Time `json:"at"`
	Bytes int       `json:"bytes"`
}

// trendOut is the --trend result (--json).
type trendOut struct {
	Target      string       `json:"target"`
	Budget      int          `json:"budget"`
	Profile     string       `json:"profile"`
	Renders     []trendPoint `json:"renders"`
	GrowthDay   float64      `json:"growth_per_day"`
	ReachBudget string       `json:"reaches_budget,omitempty"` // YYYY-MM-DD; "" when not growing or already over
	Over        bool         `json:"over_budget,omitempty"`
	// Spread: the renders span at least trendMinDays, so GrowthDay is set.
	Spread bool `json:"-"`
}

// renderTrend prints the trend for f.outPath of projectID.
func (h *Host) renderTrend(ctx context.Context, projectID string, f *renderFlags) error {
	target := h.resolveSymlinkTarget(f.outPath)
	now := h.now()
	all, err := h.app.RenderHistory(ctx, projectID) // P3-LOCAL-5: the machine store, oldest first
	if err != nil {
		return errs.Wrap(errs.Internal, "read render history", err)
	}
	since := now.Add(-renderTrendWindow)
	var rows []wire.RenderRecord
	for _, r := range all {
		if r.TargetPath == target && !r.CreatedAt.Before(since) {
			rows = append(rows, r)
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return errs.Wrap(errs.Internal, "getwd", err)
	}
	pick, err := h.resolveTargets(ctx, root, f.targets, h.coreContextTargets)
	if err != nil {
		return err
	}
	profiles := make([]toolprofile.Profile, 0, len(pick.targets))
	for _, t := range pick.targets {
		profiles = append(profiles, t.profile)
	}
	set := h.loadContextSettings(ctx, profiles)
	out := trendOut{Target: target, Budget: set.Budget, Profile: set.Profile}
	for _, r := range rows {
		out.Renders = append(out.Renders, trendPoint{At: r.CreatedAt.UTC(), Bytes: r.TotalBytes})
	}
	trendSummary(&out, now)
	if f.asJSON {
		b, jerr := json.MarshalIndent(out, "", "  ")
		if jerr != nil {
			return errs.Wrap(errs.Internal, "encode trend", jerr)
		}
		fmt.Fprintln(f.out(), string(b))
		return nil
	}
	fmt.Fprint(f.out(), out.text())
	return nil
}

// trendSummary fills growth per day and the projected budget date.
func trendSummary(o *trendOut, now time.Time) {
	n := len(o.Renders)
	if n == 0 {
		return
	}
	last := o.Renders[n-1]
	o.Over = o.Budget > 0 && last.Bytes > o.Budget
	if n < 2 {
		return
	}
	first := o.Renders[0]
	days := last.At.Sub(first.At).Hours() / 24
	if days < trendMinDays {
		return // minutes apart would project a meaningless rate
	}
	o.Spread = true
	o.GrowthDay = float64(last.Bytes-first.Bytes) / days
	if o.GrowthDay > 0 && o.Budget > 0 && !o.Over {
		left := float64(o.Budget-last.Bytes) / o.GrowthDay
		o.ReachBudget = now.Add(time.Duration(math.Ceil(left*24)) * time.Hour).UTC().Format(time.DateOnly)
	}
}

// text is the human form.
func (o trendOut) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: renders in the last 14 days (budget %d bytes, profile %s)\n", o.Target, o.Budget, o.Profile)
	if len(o.Renders) == 0 {
		b.WriteString("  (no render recorded on this machine in 14 days; run `lore render`)\n")
		return b.String()
	}
	for _, p := range o.Renders {
		fmt.Fprintf(&b, "  %s  %7d bytes\n", p.At.Format("2006-01-02 15:04"), p.Bytes)
	}
	switch {
	case !o.Spread:
		b.WriteString("growth: needs renders at least a day apart\n")
	default:
		fmt.Fprintf(&b, "growth: %+.0f bytes/day\n", o.GrowthDay)
	}
	switch {
	case o.Over:
		b.WriteString("over budget now (lore render --report)\n")
	case o.ReachBudget != "":
		fmt.Fprintf(&b, "reaches the budget around %s\n", o.ReachBudget)
	}
	return b.String()
}
