package recall

import (
	"strings"
	"time"
)

// envHuman is lore's "a person runs this" switch (adapters/identity
// EnvHuman). The identity chain honours it before the agent detection.
const envHuman = "LORE_HUMAN"

// HarnessEnv is the whole, fixed environment a bench runs lore and git in:
// HOME, PATH (binDir first), TMPDIR, TZ, LANG, NO_COLOR, LORE_NOW (left out
// for a zero now) and LORE_HUMAN=1. Nothing comes from the caller's
// environment, so no agent-detection variable of any tool leaks in.
//
// Why LORE_HUMAN=1 (devco:D-878): lore also detects an agent by walking the
// ancestor processes, which a clean environment cannot hide. A bench started
// from inside an agent session then had its setup writes credited to that
// agent, agents.person_only applied, and `lore task add` stopped with exit 20
// asking for --commitment. The bench's lore runs as its person wherever it is
// started; a run that measures an agent gets AgentEnv instead.
func HarnessEnv(home, binDir, tmp string, now time.Time) []string {
	env := []string{
		"HOME=" + home,
		"PATH=" + binDir + ":/usr/local/bin:/usr/bin:/bin",
		"TMPDIR=" + tmp,
		"TZ=UTC",
		"LANG=C.UTF-8",
		"NO_COLOR=1",
		envHuman + "=1",
	}
	if !now.IsZero() {
		env = append(env, "LORE_NOW="+now.Format(time.RFC3339))
	}
	return env
}

// AgentEnv is env without LORE_HUMAN: the environment of the lore wrapper an
// agent under test calls, so its calls are the agent's (detected from the
// agent's process, as for a real user) while the bench's own setup and its
// lore arm stay the person's.
func AgentEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, envHuman+"=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
