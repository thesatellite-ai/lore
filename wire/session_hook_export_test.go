package wire

import (
	"io"

	opsagent "github.com/thesatellite-ai/lore/ops/agent"
)

// fixedApp is a boundAppSource over one App (tests).
type fixedApp struct{ a *App }

func (f fixedApp) boundApp() *App { return f.a }

// StandaloneAgentForTest is cmd/lore's agent start host over a, reading
// the hook input from stdin and reporting to stderr.
func StandaloneAgentForTest(a *App, stdin io.Reader, stderr io.Writer) opsagent.Host {
	return &standaloneAgent{opts: a.opts, app: fixedApp{a}, stdin: stdin, stderr: stderr}
}
