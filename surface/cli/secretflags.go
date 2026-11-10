package cli

import (
	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/op"
)

// The secret answer flags (SECRETS_AND_REFERENCES 4), offered on every
// operation whose write path may ask about a secret finding
// (Confirm.MayAsk). They fill op.Caller.Secret; the engine validates them
// (one kind only, a typed reason, a reference per finding).
const (
	FlagNotASecret     = "not-a-secret"
	FlagSecretReason   = "reason"
	FlagSecretRef      = "secret-ref"
	FlagSecretUnstored = "secret-unstored"
	// FlagSecretWhy is the --not-a-secret reason on an operation whose own
	// --reason means something else (task cancel and decline: the close
	// reason, a closed set), so a cancel can carry both.
	FlagSecretWhy = "not-a-secret-why"
)

// secretFlagSet holds the parsed answer flags of one command. A flag the
// operation already defines itself is not added (opReason: the op's own
// --reason value doubles as the --not-a-secret reason; DECISION
// P2-CTX-3).
type secretFlagSet struct {
	on       bool
	not      *bool
	unstored *bool
	refs     *[]string
	reason   *string
	opReason *rawFlag
	why      *string
}

// secretFlags adds the answer flags to c when s may ask about a secret.
func secretFlags(c *cobra.Command, s op.Spec, values map[string]*rawFlag) secretFlagSet {
	var sf secretFlagSet
	// P3-MISC-2: a Local operation writes no row, so it never asks about a
	// secret; its MayAsk is only its own yes/no question.
	if !s.Confirm.MayAsk || s.Local {
		return sf
	}
	taken := func(name string) bool { _, ok := values[name]; return ok }
	sf.on = true
	_, ownReason := values[FlagSecretReason]
	needs := "--" + FlagSecretReason
	if ownReason {
		needs = "--" + FlagSecretWhy
	}
	if !taken(FlagNotASecret) {
		sf.not = c.Flags().Bool(FlagNotASecret, false, "keep a detected secret as a false positive (needs "+needs+"; --yes without a terminal)")
	}
	if !taken(FlagSecretUnstored) {
		sf.unstored = c.Flags().Bool(FlagSecretUnstored, false, "replace every detected secret with ${secret:unstored} and add a store-then-rotate task")
	}
	if !taken(FlagSecretRef) {
		sf.refs = c.Flags().StringArray(FlagSecretRef, nil, "replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)")
	}
	if rf, ok := values[FlagSecretReason]; ok {
		sf.opReason = rf
		if !taken(FlagSecretWhy) {
			sf.why = c.Flags().String(FlagSecretWhy, "", "why the detected text is not a secret (with --not-a-secret; this command's --reason means something else)")
		}
	} else {
		sf.reason = c.Flags().String(FlagSecretReason, "", "why the detected text is not a secret (with --not-a-secret)")
	}
	return sf
}

// choice is the op.SecretChoice the flags of this run give.
func (sf secretFlagSet) choice(cmd *cobra.Command) op.SecretChoice {
	var ch op.SecretChoice
	if !sf.on {
		return ch
	}
	if sf.not != nil {
		ch.NotASecret = *sf.not
	}
	if sf.unstored != nil {
		ch.Unstored = *sf.unstored
	}
	if sf.refs != nil {
		ch.Refs = append(ch.Refs, *sf.refs...)
	}
	switch {
	case sf.why != nil && *sf.why != "":
		ch.Reason = *sf.why
	case sf.reason != nil:
		ch.Reason = *sf.reason
	case sf.opReason != nil && cmd.Flags().Changed(FlagSecretReason) && len(sf.opReason.vals) > 0:
		ch.Reason = sf.opReason.vals[len(sf.opReason.vals)-1]
	}
	return ch
}
