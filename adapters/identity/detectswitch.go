//go:build !loretest

package identity

// DetectionOff is always false in release builds: the LORE_NO_DETECT
// test knob exists only under the loretest build tag (like LORE_NOW), so a
// shipped binary can never be told to hide an agent (AGENT_IDENTITY 10.6).
func DetectionOff() bool { return false }
