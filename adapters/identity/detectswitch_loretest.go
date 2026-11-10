//go:build loretest

package identity

import "os"

// EnvNoDetect turns agent detection off in test builds only (loretest tag,
// like LORE_NOW): docs transcripts and team e2e runs started from a
// coding-agent session then resolve the plain person without LORE_HUMAN
// juggling, and whoami prints no agent/detection lines (cl-gate).
const EnvNoDetect = "LORE_NO_DETECT"

// DetectionOff reports LORE_NO_DETECT=1 (test builds only).
func DetectionOff() bool { return os.Getenv(EnvNoDetect) == "1" }
