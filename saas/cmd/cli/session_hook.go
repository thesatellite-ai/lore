// session_hook.go — `lore agent start --tool <slug> [--session] [--model]
// [--hook-stdin]` (AGENT_IDENTITY_DESIGN 6.4, OQ8): the optional per-tool
// session hook. It finds or creates the session row of the detected agent
// actor and fills only the fields it was given, each with source
// session_hook. It never registers a roster agent, never prints
// LORE_ACTOR, always exits 0 and prints at most one line on stderr, so a
// broken hook can never stop the tool's session.
//
//	runAgentHook
//	  ├─ --hook-stdin → toolprofile.SessionHookFor(tool).Parse(stdin)
//	  ├─ clean each value (one line, ≤ 200 chars, no control chars; 6.3)
//	  ├─ actor: the detected agent (same tool) else agent:auto/<tool>/<owner short> (OQ9)
//	  └─ wire.App.FindOrCreateSession in the machine store (machine-local, OQ14, P3-LOCAL-5)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"saas/pkg/aicoder/sessions"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// hookStdinMax bounds the hook JSON lore reads (a hook input is a few
// hundred bytes; the cap keeps a runaway pipe from holding the tool).
const hookStdinMax = 1 << 20

// hookValueMax is the longest value lore keeps from any source (6.3).
const hookValueMax = 200

// runAgentHook is the --tool mode; it never fails the command.
func runAgentHook(ctx context.Context, tool, session, model string, hookStdin bool) {
	if err := agentHook(ctx, tool, session, model, hookStdin, os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "lore agent start: "+cleanHookValue(err.Error()))
	}
}

// agentHook records the session details; the error is shown, not returned.
func agentHook(ctx context.Context, tool, session, model string, hookStdin bool, stdin io.Reader) (err error) {
	tool = cleanHookValue(tool)
	if tool == "" {
		return errors.New("--tool needs a tool slug")
	}
	if hookStdin {
		h, ok := toolprofile.SessionHookFor(tool)
		if !ok {
			return fmt.Errorf("no confirmed session hook input for %s; pass --session and --model instead", tool)
		}
		raw, readAllErr := io.ReadAll(io.LimitReader(stdin, hookStdinMax))
		if readAllErr != nil {
			return fmt.Errorf("read the hook input: %w", readAllErr)
		}
		in, readAllErr := h.Parse(raw)
		if readAllErr != nil {
			return fmt.Errorf("the %s hook input is not the expected JSON: %w", tool, readAllErr)
		}
		if session == "" {
			session = in.SessionID
		}
		if model == "" {
			model = in.Model
		}
	}
	session, model = cleanHookValue(session), cleanHookValue(model)
	if coreApp == nil {
		return errors.New("not in a lore repo")
	}
	who, err := detectedWho(ctx)
	if err != nil {
		return err
	}
	actor, err := hookActor(ctx)
	if err != nil {
		return err
	}
	d, ok := sessionDetailsOf(who, actor)
	if !ok || d.Tool != tool {
		d = hookOnlyDetails(actor, tool)
	}
	src := map[string]string{}
	if session != "" {
		d.ToolSessionID = session
		src["tool_session_id"] = sessions.SourceSessionHook
	}
	if model != "" {
		d.Model = model
		src["model"] = sessions.SourceSessionHook
	}
	for k, v := range d.Sources {
		if _, set := src[k]; !set {
			src[k] = v
		}
	}
	d.Sources = src
	if d.ToolSessionID == "" {
		return errors.New("no session id: the tool gave none and LORE_SESSION is unset")
	}
	if _, err := currentSessionWith(ctx, nil, d, ""); err != nil {
		return err
	}
	return nil
}

// hookOnlyDetails are the details of a hook run whose environment did not
// detect this tool: only what lore knows without the markers.
func hookOnlyDetails(actor, tool string) sessions.Details {
	d := sessions.Details{ActorID: actor, Tool: tool, Signal: sessions.SignalSessionHook}
	d.Repo, d.Branch, d.Cwd = gitPlace()
	return d
}

// hookActor is the act_ id later writes of this session are stamped
// with (coreApp.Actor, the same call currentSession makes), so the hook's
// row is the one every write of the session finds by (actor, tool, id).
func hookActor(ctx context.Context) (string, error) {
	a, err := coreApp.Actor(ctx)
	return a.String(), err
}

// cleanHookValue keeps one line of at most hookValueMax characters with
// control characters removed (6.3).
func cleanHookValue(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.SplitN(s, "\n", 2)[0]))
	if r := []rune(s); len(r) > hookValueMax {
		s = string(r[:hookValueMax])
	}
	return s
}
