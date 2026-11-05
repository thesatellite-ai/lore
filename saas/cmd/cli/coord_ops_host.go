// coord_ops_host.go — the binary's opscoord.Host: the team coordination
// operations (ops/coord) run the v0.1.17 command bodies of this package,
// which still own the signals ref, roster and store links (P3-COORD-1).
//
//	engine.Run ─► ops/coord run ─► Env.Coordinator ─► coordHost ─► runTask…/runHold…/runGate… (buffer) ─► Report lines
package main

import (
	"bytes"
	"context"
	"io"
	"regexp"

	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// coordHost implements opscoord.Host over this package's command bodies.
// It holds no state: every call opens what it needs, as the old commands
// did.
type coordHost struct{}

var _ opscoord.Host = coordHost{}

// ansiSGR matches a terminal colour sequence (style.* output). The run
// bodies colour their ✓ and ! marks when stdout is a terminal; a Report
// is data (its lines are also the --json and lore api output), so the
// sequences are removed rather than leaked into JSON.
var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// report runs body against a buffer and returns what it wrote as lines.
// The lines are returned with the error too, though the surfaces print
// only the error then.
func report(body func(out io.Writer) error) (opscoord.Report, error) {
	var b bytes.Buffer
	err := body(&b)
	return opscoord.Report{Lines: plainLines(b.String())}, hostErr(err)
}

// hostErr is a run body's error as a core *errs.Error: the bodies return
// v0.1.17 CLIErrors (E_OFFLINE exit 15, E_LOST_CLAIM 12, …), which the
// engine would otherwise report as E_INTERNAL. nil stays nil.
func hostErr(err error) error {
	if err == nil {
		return nil
	}
	return asCoreError(err)
}

// plainLines is printed text as lines, colour sequences removed.
func plainLines(s string) []string { return splitLines(ansiSGR.ReplaceAllString(s, "")) }

// TaskClaim implements opscoord.Host.
func (coordHost) TaskClaim(ctx context.Context, in opscoord.ClaimIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runTaskClaim(ctx, out, in) })
}

// TaskHeartbeat implements opscoord.Host.
func (coordHost) TaskHeartbeat(ctx context.Context, in opscoord.HeartbeatIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runTaskHeartbeat(ctx, out, in) })
}

// TaskMerged implements opscoord.Host.
func (coordHost) TaskMerged(ctx context.Context, in opscoord.MergedIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runTaskMerged(ctx, out, in) })
}

// TaskNext implements opscoord.Host.
func (coordHost) TaskNext(ctx context.Context, in opscoord.NextIn) (opscoord.NextOut, error) {
	out, err := runTaskNext(ctx, in)
	return out, hostErr(err)
}

// TaskNote implements opscoord.Host: --to shares the one note through the
// store links (sharedNote), otherwise the holder / unheld note path.
func (coordHost) TaskNote(ctx context.Context, in opscoord.NoteIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error {
		if in.ShareTo != "" {
			return sharedNote(ctx, out, in.ID, in.Body(), toFlags{To: in.ShareTo, Everywhere: in.Everywhere, ForceOrg: in.ForceOrg})
		}
		return runTaskNote(ctx, out, in.ID, in.Body(), in.From)
	})
}

// TaskReassign implements opscoord.Host.
func (coordHost) TaskReassign(ctx context.Context, in opscoord.ReassignIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runTaskReassign(ctx, out, in) })
}

// TaskRelease implements opscoord.Host.
func (coordHost) TaskRelease(ctx context.Context, in opscoord.ReleaseIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runTaskRelease(ctx, out, in) })
}

// TaskRevoke implements opscoord.Host.
func (coordHost) TaskRevoke(ctx context.Context, in opscoord.RevokeIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runTaskRevoke(ctx, out, in) })
}

// HoldClaim implements opscoord.Host.
func (coordHost) HoldClaim(ctx context.Context, in opscoord.HoldClaimIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runHoldClaim(ctx, out, in) })
}

// HoldRelease implements opscoord.Host.
func (coordHost) HoldRelease(ctx context.Context, in opscoord.HoldReleaseIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runHoldRelease(ctx, out, in) })
}

// GateRequest implements opscoord.Host.
func (coordHost) GateRequest(ctx context.Context, in opscoord.GateIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runGateRequest(ctx, out, in.ID, in.From) })
}

// GateApprove implements opscoord.Host.
func (coordHost) GateApprove(ctx context.Context, in opscoord.GateIn) (opscoord.Report, error) {
	return report(func(out io.Writer) error { return runGateApprove(ctx, out, in.ID, in.From) })
}

// Inbox implements opscoord.Host.
func (coordHost) Inbox(ctx context.Context, in opscoord.InboxIn) (opscoord.InboxOut, error) {
	out, err := runInbox(ctx, in)
	return out, hostErr(err)
}

// MissionStatus implements opscoord.Host.
func (coordHost) MissionStatus(ctx context.Context, in opscoord.MissionStatusIn) (opscoord.MissionStatusOut, error) {
	out, err := runMissionStatus(ctx, in)
	return out, hostErr(err)
}
