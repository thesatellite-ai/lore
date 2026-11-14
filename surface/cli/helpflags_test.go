package cli

import (
	"context"
	"testing"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/op"
)

// TestHelpHidesFlagsThatNeverApply (B3-3, B3-6): a flag kept only so a
// v0.1.17 script parses is hidden from --help but still parses, and an
// operation whose only question is its own yes/no gets no secret answer
// flags at all; nor does a read (a show whose only question is --reveal,
// batch not-built), which writes no row a secret scan could flag.
func TestHelpHidesFlagsThatNeverApply(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	type in struct {
		Name string `json:"name" arg:"0" desc:"the name"`
		Repo string `json:"repo,omitempty" flag:"repo" desc:"accepted for v0.1.17 scripts; it does nothing"`
	}
	run := func(context.Context, in) (struct{}, error) { return struct{}{}, nil }
	reg, err := op.NewRegistry(
		op.Define(op.Spec{Name: "thing.init", Summary: "s", Permission: op.PermAdmin, Access: op.AccessRead,
			Confirm: op.ConfirmRule{MayAsk: true, NoSecretFindings: true}}, run),
		op.Define(op.Spec{Name: "thing.add", Summary: "s", Permission: op.PermWrite, Access: op.AccessWrite,
			Confirm: op.ConfirmRule{MayAsk: true}}, run),
		// batch not-built: a read whose question is a reveal (every show)
		op.Define(op.Spec{Name: "thing.show", Summary: "s", Permission: op.PermRead, Access: op.AccessRead,
			Confirm: op.ConfirmRule{MayAsk: true}}, run),
	)
	if err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "lore"}
	if err := Mount(root, reg, f); err != nil {
		t.Fatal(err)
	}
	initCmd, addCmd := child(child(root, "thing"), "init"), child(child(root, "thing"), "add")
	showCmd := child(child(root, "thing"), "show")
	if fl := initCmd.Flags().Lookup("repo"); fl == nil || !fl.Hidden {
		t.Fatalf("the v0.1.17 flag is %+v, want defined and hidden", fl)
	}
	for _, name := range []string{FlagNotASecret, FlagSecretRef, FlagSecretUnstored, FlagSecretReason} {
		if initCmd.Flags().Lookup(name) != nil {
			t.Errorf("thing init offers --%s, an answer it never asks for", name)
		}
		if showCmd.Flags().Lookup(name) != nil {
			t.Errorf("thing show (a read: it writes no row) offers --%s", name)
		}
		if addCmd.Flags().Lookup(name) == nil {
			t.Errorf("thing add lost --%s", name)
		}
	}
	if initCmd.Flags().Lookup(FlagYes) == nil || showCmd.Flags().Lookup(FlagYes) == nil {
		t.Error("thing init or thing show lost --yes")
	}
}
