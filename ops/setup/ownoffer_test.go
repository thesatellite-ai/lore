package setup

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/op"
)

// offerEnv is a fakeEnv that offers an own: pattern and records the adds.
type offerEnv struct {
	fakeEnv
	offer bool
	added *[]string
}

func (e offerEnv) OwnOfferHere(context.Context, onboard.SetupEntry) (OwnOffer, bool) {
	return OwnOffer{Org: "lumen", Remote: "github.com/lumen-co/atlas", Pattern: "github.com/lumen-co/*",
		Command: "lore org config set --org lumen --key own --add github.com/lumen-co/* --yes"}, e.offer
}

func (e offerEnv) AddOwnPattern(_ context.Context, o OwnOffer) ([]string, error) {
	*e.added = append(*e.added, o.Pattern)
	return []string{"✓ own changed"}, nil
}

// TestOfferOwn pins devco D-526 in setup.apply: an admin saving the team
// or this_folder step is offered the pattern; --yes adds it; a terminal
// asks with Enter meaning yes and "n" meaning no; without a terminal
// nothing is written and the line names the command; a member, another
// step or no offer from wire never asks.
func TestOfferOwn(t *testing.T) {
	admin := onboard.SetupEntry{Name: "lead", Kind: onboard.KindAdmin, Org: "lumen"}
	member := onboard.SetupEntry{Name: "team", Kind: onboard.KindMember, Org: "lumen"}
	tty := op.WithInteractive(context.Background(), true)
	cases := []struct {
		name   string
		entry  onboard.SetupEntry
		step   onboard.StepID
		ctx    context.Context
		offer  bool
		typed  string
		added  bool
		offers bool
		says   string
	}{
		{"admin --yes", admin, onboard.StepThisFolder, op.WithConfirmed(context.Background(), true), true, "", true, true, "✓ own changed"},
		{"admin terminal Enter", admin, onboard.StepTeam, tty, true, "\n", true, true, "✓ own changed"},
		{"admin terminal no", admin, onboard.StepThisFolder, tty, true, "n\n", false, true, "not added; to add it later: lore org config set --org lumen"},
		{"admin no terminal", admin, onboard.StepThisFolder, context.Background(), true, "", false, true, "not added (no terminal to ask): lore org config set --org lumen --key own --add github.com/lumen-co/* --yes"},
		{"member --yes", member, onboard.StepThisFolder, op.WithConfirmed(context.Background(), true), true, "", false, false, ""},
		{"admin other step", admin, onboard.StepTools, op.WithConfirmed(context.Background(), true), true, "", false, false, ""},
		{"nothing to offer", admin, onboard.StepThisFolder, op.WithConfirmed(context.Background(), true), false, "", false, false, ""},
	}
	in, out, isTTY := ownOfferIn, ownOfferOut, ownOfferTTY
	t.Cleanup(func() { ownOfferIn, ownOfferOut, ownOfferTTY = in, out, isTTY })
	ownOfferTTY = func() bool { return true }
	ownOfferOut = io.Discard
	for _, c := range cases {
		var added []string
		ownOfferIn = strings.NewReader(c.typed)
		e := newEngine(offerEnv{fakeEnv: newFakeEnv(), offer: c.offer, added: &added}, fakeCatalog{})
		o, lines := e.offerOwn(c.ctx, c.entry, c.step)
		if (o.Pattern != "") != c.offers || o.Added != c.added || (len(added) > 0) != c.added {
			t.Errorf("%s: offer %+v added %v", c.name, o, added)
		}
		if got := strings.Join(lines, "\n"); !strings.Contains(got, c.says) || (c.says == "" && got != "") {
			t.Errorf("%s: lines %q, want %q", c.name, got, c.says)
		}
	}
}
