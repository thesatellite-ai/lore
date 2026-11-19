package setup

// ownoffer.go — guided setup offers an admin the repo's owner as an own:
// pattern (devco D-526). An org's own repo no own: pattern names is a
// client repo by the fail-closed default (D-525: guards on, hooks
// skipped); the admin who sets the team up from inside it is asked once
// the step that knows the org (team) or the folder (this_folder) is saved.
//
//	Apply (this_folder | team, admin setup, saved) ─▶ OwnOfferer.OwnOfferHere
//	   none ─▶ nothing
//	   --yes (REST "yes") ─▶ AddOwnPattern (org config set's own path)
//	   terminal ─▶ "Add …? [Y/n]" ─ yes/Enter ─▶ AddOwnPattern; no ─▶ the command, nothing written
//	   no terminal ─▶ nothing written; the warning names the command
//
// Members never get it: the wire side offers only to an admin of the org.

import (
	"bufio"
	"context"
	"io"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/op"
)

// OwnOffer is the own: pattern guided setup offers for this repo, and what
// came of it (ApplyOut.OwnOffer).
type OwnOffer struct {
	Org     string `json:"org"`
	Remote  string `json:"remote"`
	Pattern string `json:"pattern"`
	// Added: the pattern was written to the org's lore-org.yaml.
	Added bool `json:"added"`
	// Command adds it by hand (named whenever it was not added).
	Command string `json:"command"`
}

// OwnOfferer is an optional upgrade of Onboard (wire): the own: pattern to
// offer in the folder lore runs in, and the write that adds it.
type OwnOfferer interface {
	// OwnOfferHere is the offer for setup in the current repo; ok=false
	// when there is none (not an admin of the org, no hosted remote, a
	// pattern or link of the org names the repo already).
	OwnOfferHere(ctx context.Context, setup onboard.SetupEntry) (OwnOffer, bool)
	// AddOwnPattern adds o.Pattern to the org's own: list through the
	// path of lore org config set; lines say what changed.
	AddOwnPattern(ctx context.Context, o OwnOffer) ([]string, error)
}

// ownOfferSteps are the steps after whose save the offer is made: team
// (the org exists from then on) and this_folder (the repo's own step).
var ownOfferSteps = []onboard.StepID{onboard.StepTeam, onboard.StepThisFolder}

// ownOfferIn and ownOfferOut are the terminal the offer asks on (vars so
// tests can answer it).
var (
	ownOfferIn  io.Reader = os.Stdin
	ownOfferOut io.Writer = os.Stderr
	// ownOfferTTY reports whether ownOfferIn is a terminal.
	ownOfferTTY = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
)

// offerOwn makes the offer after step was saved for entry; warnings are
// the lines to show (the command when nothing was written).
//
// The step is saved already, so a failed write is a warning naming the
// command, never a failed step.
func (e engine) offerOwn(ctx context.Context, entry onboard.SetupEntry, step onboard.StepID) (OwnOffer, []string) {
	oo, ok := e.env.(OwnOfferer)
	if !ok || entry.Kind != onboard.KindAdmin || entry.Org == "" || !slices.Contains(ownOfferSteps, step) {
		return OwnOffer{}, nil
	}
	o, ok := oo.OwnOfferHere(ctx, entry)
	if !ok {
		return OwnOffer{}, nil
	}
	why := o.Remote + " is treated as a client repo of " + o.Org + " (no own: pattern matches it)"
	switch {
	case op.Confirmed(ctx):
	case op.Interactive(ctx) && ownOfferTTY():
		if !askDefaultYes(ownOfferIn, ownOfferOut, why+". Add own: pattern "+o.Pattern+" to "+o.Org+"?") {
			return o, []string{"own: pattern " + o.Pattern + " not added; to add it later: " + o.Command}
		}
	default:
		return o, []string{why + "; own: pattern " + o.Pattern + " not added (no terminal to ask): " + o.Command}
	}
	lines, err := oo.AddOwnPattern(ctx, o)
	if err != nil {
		msg, _, _ := strings.Cut(err.Error(), "\n")
		return o, []string{"own: pattern " + o.Pattern + " not added: " + msg + "; " + o.Command}
	}
	o.Added = true
	return o, lines
}

// askDefaultYes prints question with [Y/n] and reads one line: Enter and
// y/yes accept, anything else (or end of input) declines.
func askDefaultYes(in io.Reader, out io.Writer, question string) bool {
	_, _ = io.WriteString(out, question+" [Y/n] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "" || a == "y" || a == "yes"
}
