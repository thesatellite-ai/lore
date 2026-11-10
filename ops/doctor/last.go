package doctor

// last.go — doctor.last: the last doctor results of every setup, read from
// what earlier runs recorded, without probing anything (owner feedback
// 2026-11-10: opening the page's Doctor ran every check of every setup).
//
//	doctor.run (page or CLI) ─▶ HealthSink.RecordHealth ─▶ ~/.lore/cache/doctor-health.json
//	doctor.last              ─▶ LastSource.SetupHealth  ◀─ the same file
//
// A setup with no recorded results, or one saved since they were taken
// (the fingerprint rule of wire/setuphealth.go), is "not checked yet":
// Status is empty and CheckedAt is absent. The page shows these rows at
// once and runs doctor.run only when asked (Run all, or Run on one row).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// OpLast is the operation name (`lore doctor last`).
const OpLast = "doctor.last"

// LastSource is the optional upgrade of Env that doctor.last reads: the
// setups in config.yaml (SetupEnv's store) and the results the last
// doctor run recorded for each (*wire.App's SetupHealth). Without it
// doctor.last answers E_UNSUPPORTED, never an empty list that would read
// as "no setups".
type LastSource interface {
	SetupEnv
	SetupHealth(ctx context.Context, setup string) (results []onboard.CheckResult, ok bool)
}

// LastIn is the input of doctor.last (no options: every setup).
type LastIn struct{}

// LastOut is the output of doctor.last: one row per setup in config.yaml,
// in its order.
type LastOut struct {
	Setups []LastSetup `json:"setups"`
}

// LastSetup is one setup's last doctor results.
type LastSetup struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Org      string `json:"org,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	// Status is the verdict of Checks; empty when not checked yet.
	Status Health `json:"status,omitempty"`
	// CheckedAt is when the newest of Checks was taken; absent when not
	// checked yet.
	CheckedAt time.Time `json:"checked_at,omitzero"`
	// Checks are the recorded results (empty when not checked yet).
	Checks []onboard.CheckResult `json:"checks"`
}

// lastOp is the registry entry of doctor.last.
func lastOp(env Env) op.Entry {
	return op.Define(op.Spec{
		Name: OpLast, Summary: "Show each setup's last doctor results and when they were taken, without running any check",
		Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
		Errors:   []errs.Code{errs.Internal, errs.Unsupported},
		Examples: []op.Example{{Title: "the last results of every setup", CLI: "lore doctor last", Want: ""}},
	}, func(ctx context.Context, _ LastIn) (LastOut, error) {
		return Last(ctx, env)
	})
}

// Last is doctor.last over env.
func Last(ctx context.Context, env Env) (LastOut, error) {
	src, ok := env.(LastSource)
	if !ok {
		return LastOut{}, errs.New(errs.Unsupported, "lore doctor last needs the setup engine, which this binary does not have").
			WithHint("run lore doctor")
	}
	st, err := src.SetupStore(ctx)
	if err != nil {
		return LastOut{}, err
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return LastOut{}, err
	}
	out := LastOut{Setups: make([]LastSetup, 0, len(cfg.Setups))}
	for _, e := range cfg.Setups {
		row := LastSetup{Name: e.Name, Kind: string(e.Kind), Org: e.Org, Disabled: e.Disabled, Checks: []onboard.CheckResult{}}
		if rs, found := src.SetupHealth(ctx, e.Name); found {
			row.Checks = rs
			row.Status = statusOf(rs)
			row.CheckedAt = newest(rs)
		}
		out.Setups = append(out.Setups, row)
	}
	return out, nil
}

// newest is the latest CheckedAt of rs; zero when none carries a time.
func newest(rs []onboard.CheckResult) time.Time {
	var at time.Time
	for _, r := range rs {
		if r.CheckedAt.After(at) {
			at = r.CheckedAt
		}
	}
	return at
}

// notChecked is the text of a setup with no recorded results.
const notChecked = "not checked yet (lore doctor --all --setup %s)"

// Text implements op.Texter: one line per setup.
func (o LastOut) Text() string {
	if len(o.Setups) == 0 {
		return "No setups on this machine.\n"
	}
	var b strings.Builder
	for _, s := range o.Setups {
		fmt.Fprintf(&b, "%s (%s)  ", s.Name, s.Kind)
		switch {
		case s.Disabled:
			b.WriteString("disabled\n")
		case s.Status == "":
			fmt.Fprintf(&b, notChecked+"\n", s.Name)
		case s.CheckedAt.IsZero():
			fmt.Fprintf(&b, "%s · %s\n", s.Status, countsLine(s.Checks))
		default:
			fmt.Fprintf(&b, "%s · %s · checked %s\n", s.Status, countsLine(s.Checks), s.CheckedAt.Local().Format(time.DateTime))
		}
	}
	return b.String()
}

// countsLine counts rs by status ("12 ok, 1 warn"), for the text form.
func countsLine(rs []onboard.CheckResult) string {
	var c Counts
	for _, r := range rs {
		c.add(r)
	}
	var parts []string
	for _, p := range []struct {
		n    int
		name string
	}{{c.OK, "ok"}, {c.Warn, "warn"}, {c.Fail, "fail"}, {c.Error, "error"}, {c.Skip, "skip"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.name))
		}
	}
	if len(parts) == 0 {
		return "no checks"
	}
	return strings.Join(parts, ", ")
}
