package host

// live_doctor.go — the live-rows rows of `lore doctor` for a binary built
// from the root module (LORE_TEAM_SPEC §9.4; ported in part from saas/cmd/cli
// live_doctor.go doctorLive, P3-MODEB-7). Only real problems are warnings
// (they degrade health); facts every team with live rows has (rows only in
// live, writes not pushed) are notes:
//
//	warning: the remote refused refs/lore/live, the remote live ref was
//	         rewritten, rows past live.max_age not copied, conflict markers
//	note:    last fetch, writes not pushed, rows only in live, rows waiting
//	         to land (and why: a protected default branch rides on the push)
//
// Not ported: the fetch refspec check, the hook block check, the shallow
// clone warning and the compaction count.

import (
	"context"
	"fmt"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// liveDoctorPrefix starts every live doctor line.
const liveDoctorPrefix = "live: "

// doctorStoresOnlyNote is ops/doctor's note for a binary without lore.db
// checks (its Run adds it only when no Host is set; this Host replaces it).
const doctorStoresOnlyNote = "this build checks the stores only (no lore.db checks)"

// liveDoctor is the doctor Host LiveHook installs: the inner Host's base
// (or ops/doctor's default one) plus the live rows of the cwd project.
type liveDoctor struct {
	h     *liveHost
	inner opsdoctor.Host
}

// Base implements opsdoctor.Host.
func (d liveDoctor) Base(ctx context.Context, fix bool) (opsdoctor.Base, error) {
	base := opsdoctor.Base{Status: opsdoctor.StatusHealthy, DBOK: true, Notes: []string{doctorStoresOnlyNote}}
	if d.inner != nil {
		var err error
		if base, err = d.inner.Base(ctx, fix); err != nil {
			return base, err
		}
	}
	s, folder, err := liveSync{h: d.h}.here(ctx)
	if err != nil || s == nil {
		return base, nil // not a lore project in a git repo: no live rows to report
	}
	warn := func(msg string) {
		base.Warnings = append(base.Warnings, liveDoctorPrefix+msg)
		if base.Status == opsdoctor.StatusHealthy {
			base.Status = opsdoctor.StatusDegraded
		}
	}
	note := func(msg string) { base.Notes = append(base.Notes, liveDoctorPrefix+msg) }
	if why := s.noteText(liveNoteRefused); why != "" {
		warn("the remote refused a push to refs/lore/live, so live rows stay on this machine (the host may allow only branches and tags): " + why)
	}
	if s.blocked != nil {
		warn("the remote live ref was deleted or rewritten; writes are stopped until `lore live restore`")
	}
	now := s.clk.Now()
	if at := live.FetchStamp(s.stateDir); !at.IsZero() {
		note("last fetch " + now.Sub(at).Round(time.Second).String() + " ago")
	}
	if snap, serr := s.ref.Snapshot(ctx); serr == nil && snap.Ahead() {
		p, _ := s.ref.Pending() // display only: max(len, 1) still reports the unpushed state
		note(fmt.Sprintf("%d live write(s) not pushed yet", max(len(p), 1)))
	}
	plan, err := s.plan(ctx, folder, nil)
	if err != nil {
		warn("read live ref: " + err.Error())
		return base, nil
	}
	branch := s.defaultBranch(ctx)
	only, old, waiting, conflicts := 0, 0, 0, 0
	for _, p := range plan {
		if p.File.Deleted() {
			continue
		}
		only++
		if p.File.Block.Conflict || isConflict(p) {
			conflicts++
		}
		if p.Decision.Action != live.LandStay {
			waiting++
		}
		since := p.File.Block.LastCopyAt
		if since.IsZero() {
			since = p.File.Block.LiveSince
		}
		if !since.IsZero() && now.Sub(since) > s.cfg.MaxAge && !p.Leave {
			old++
		}
	}
	if only > 0 {
		note(fmt.Sprintf("%d row(s) exist only in live (forks, ZIP downloads and shallow clones do not carry refs/lore/*; open rows reach %s at live.max_age)", only, branch))
	}
	if waiting > 0 {
		why := "the next live write or `lore sync` lands them"
		if s.noteText(liveNoteProtected) != "" {
			why = branch + " is protected: they ride on your next push"
		}
		note(fmt.Sprintf("%d row(s) waiting to land; %s", waiting, why))
	}
	if old > 0 {
		warn(fmt.Sprintf("%d live row(s) older than live.max_age not copied to %s yet (run `lore sync`)", old, branch))
	}
	if conflicts > 0 {
		warn(fmt.Sprintf("%d live row(s) carry conflict markers and cannot land (`lore sync conflicts`)", conflicts))
	}
	return base, nil
}
