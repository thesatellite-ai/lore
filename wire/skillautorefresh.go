package wire

// skillautorefresh.go — every lore command keeps the installed lore skill
// as current as the binary (H-185). `brew upgrade lore` and other package
// managers swap the binary and never touch ~/.<tool>/skills/lore, so an
// agent kept a skill from an older lore and told its person that lore had
// no `lore search rebuild` or `lore ask`. After a command's output:
//
//	state/skill-check names this binary's version and is under a day old
//	   ─▶ nothing more (one stat and one small read: the hot path)
//	else ─▶ stamp it, run skill.refresh: stale copies in folders lore owns
//	        are re-installed (one stderr line says so); a link into a
//	        lore source checkout or any other link is never written
//	        through: one line names its fix (git -C <checkout> pull)
//
// A new binary writes another version, so the first command after an
// upgrade checks at once; a development build compares nothing.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/ops/skill"
)

// The once-a-day stamp of the skill check, in lore's home.
const (
	skillCheckDir   = "state"
	skillCheckFile  = "skill-check"
	skillCheckEvery = 24 * time.Hour
	skillCheckMode  = 0o600
	skillCheckDMode = 0o700
)

// SkillRefreshSkips reports commands after which the skill check never
// runs: help and completion print only, the merge driver runs inside git,
// `lore skill …` manages the skill itself, and `lore upgrade` runs the new
// binary's refresh.
func SkillRefreshSkips(first string) bool {
	switch first {
	case "", "help", "completion", "__complete", "__completeNoDesc", "merge-driver", "skill", "upgrade":
		return true
	}
	return false
}

// SkillAutoRefresh runs the daily skill check (see the file comment). It
// never fails: what cannot be read or written is left for `lore skill
// status` and `lore doctor`.
func (a *App) SkillAutoRefresh(ctx context.Context, w io.Writer) {
	if a.opts.Home == "" || toolprofile.IsDevBuild(a.opts.Version) {
		return
	}
	state := filepath.Join(a.opts.Home, skillCheckDir, skillCheckFile)
	refresh := func(ctx context.Context) (skill.RefreshOut, error) {
		reg, err := skill.Registry(ctx, a)
		if err != nil {
			return skill.RefreshOut{}, err
		}
		home, err := a.SkillHome(ctx)
		if err != nil {
			return skill.RefreshOut{}, err
		}
		return skill.Installer{Home: home, Version: a.opts.Version, Registry: reg}.Refresh(ctx, skill.RefreshIn{})
	}
	autoRefreshSkill(ctx, state, toolprofile.NormVersion(a.opts.Version), time.Now(), refresh, w)
}

// skillCheckDue reports whether the stamp at path is missing, a day old,
// or names another version: one stat and one small read.
func skillCheckDue(path, version string, now time.Time) bool {
	st, err := os.Stat(path)
	if err != nil || now.Sub(st.ModTime()) >= skillCheckEvery {
		return true
	}
	b, err := os.ReadFile(path)
	return err != nil || strings.TrimSpace(string(b)) != version
}

// autoRefreshSkill is SkillAutoRefresh with its inputs explicit (tests
// count how often refresh runs). The stamp is written before the refresh,
// so a failing check is not retried by every command; a home that cannot
// take the stamp is not checked at all, for the same reason.
func autoRefreshSkill(ctx context.Context, state, version string, now time.Time, refresh func(context.Context) (skill.RefreshOut, error), w io.Writer) {
	if !skillCheckDue(state, version, now) {
		return
	}
	if os.MkdirAll(filepath.Dir(state), skillCheckDMode) != nil || os.WriteFile(state, []byte(version+"\n"), skillCheckMode) != nil {
		return
	}
	_ = os.Chtimes(state, now, now) // the clock the next check compares with
	out, err := refresh(ctx)
	if err != nil {
		return
	}
	for _, line := range skillRefreshLines(out) {
		_, _ = fmt.Fprintln(w, line) // best effort: stderr closed is no reason to fail the command
	}
}

// skillRefreshLines are the stderr lines of one automatic refresh: one for
// every copy re-installed, one per stale copy left with its fix.
func skillRefreshLines(out skill.RefreshOut) []string {
	var lines []string
	if len(out.Refreshed) > 0 {
		tools := make([]string, 0, len(out.Refreshed))
		for _, r := range out.Refreshed {
			tools = append(tools, r.Tool)
		}
		lines = append(lines, fmt.Sprintf("lore: refreshed the lore skill for %s to lore %s (it was older; agents now see this lore's commands)",
			strings.Join(tools, ", "), out.BinaryVersion))
	}
	for _, l := range out.Left {
		lines = append(lines, fmt.Sprintf("lore: the %s skill is stale: %s; run %s", l.Tool, l.Detail, l.Fix))
	}
	return lines
}
