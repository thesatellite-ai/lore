package wire

// coord_standalone_claimshow.go — `lore task show <id> --signals` on a host
// without its own coordination (cmd/lore): standaloneTeam's
// opstask.ClaimShower. The claim is read as seen now (signals.Store.Read:
// fetched when the store's remote answers, else the last fetched copy) from
// the ref the task is coordinated on (openWorldFor: the repo's own, or the
// linked store's), and printed in v0.1.17's layout (saas/cmd/cli
// coord_show.go claimText), so both binaries print the same lines.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

var _ opstask.ClaimShower = standaloneTeam{}

// ClaimText implements opstask.ClaimShower.
func (t standaloneTeam) ClaimText(ctx context.Context, taskID string) (string, error) {
	w, err := standaloneCoord(t).openWorldFor(ctx, taskID)
	if err != nil {
		return "", err
	}
	st := w.signalsStore()
	v, err := st.Read(ctx)
	if err != nil {
		return "", coordErr(err)
	}
	return claimShowText(v, st.Ref, signals.ClaimPath(w.folder, taskID)), nil
}

// claimShowText is the claim at path p of v, v0.1.17's lines: a blank
// line, then claim / token / expires, and branch and owns when set.
func claimShowText(v signals.View, ref, p string) string {
	var b strings.Builder
	b.WriteString("\n")
	s, ok := v.Board[p]
	if !ok {
		fmt.Fprintf(&b, "  claim:    none on %s\n", ref)
		return b.String()
	}
	state := "active"
	if _, active := v.Active(p); !active {
		state = "expired (as seen by this clone)"
	}
	fmt.Fprintf(&b, "  claim:    %s, held by %s (session %s", state, holderName(s), s.Session)
	if s.Host != "" {
		fmt.Fprintf(&b, ", %s", s.Host)
	}
	fmt.Fprintf(&b, ")\n  token:    %s…  epoch %d\n  expires:  %s  renewed_seq %d\n",
		s.ClaimToken[:min(len(s.ClaimToken), coord.TokenPrefixLen)], s.ClaimEpoch, s.Expires.UTC().Format(time.RFC3339), s.RenewedSeq)
	if s.WorkBranch != "" {
		fmt.Fprintf(&b, "  branch:   %s\n", s.WorkBranch)
	}
	if len(s.Owns) > 0 {
		fmt.Fprintf(&b, "  owns:     %s\n", strings.Join(s.Owns, ", "))
	}
	return b.String()
}
