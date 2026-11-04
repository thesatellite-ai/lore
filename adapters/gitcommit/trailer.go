package gitcommit

import (
	"strings"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// TrailerCIRun is the trailer lore puts on the commits it makes itself in
// CI (live, store, signals, migrate), never on a person's commit (FQ7) and
// never in a client repo (AGENT_IDENTITY_DESIGN 6.5, OQ13): a CI runner is
// thrown away after the job, taking its local session rows with it, so the
// run facts travel in the commit instead.
const TrailerCIRun = "Lore-CI-Run"

// CIRunTrailer is "Lore-CI-Run: <provider> run <id> attempt <n>, workflow
// <w>, job <j>, commit <sha7>" when getenv shows a CI provider, else "".
func CIRunTrailer(getenv func(string) string) string {
	ci := agentdetect.DetectCI(getenv)
	if ci == nil {
		return ""
	}
	return TrailerCIRun + ": " + ci.Trailer()
}

// AppendTrailers adds non-empty "Key: value" trailers to a commit message,
// after one blank line (git interpret-trailers form); msg is returned
// unchanged when every trailer is empty.
func AppendTrailers(msg string, trailers ...string) string {
	var keep []string
	for _, t := range trailers {
		if t = strings.TrimSpace(t); t != "" && !strings.ContainsAny(t, "\r\n") {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		return msg
	}
	return strings.TrimRight(msg, "\n") + "\n\n" + strings.Join(keep, "\n")
}
