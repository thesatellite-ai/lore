// file.go — a question that names a file (devco D-815): an agent about to
// change a file asks "add a due_date column in db/migrations/0042.sql",
// and before it edits, the must-rules and critical hotfixes for that file
// have to be in the answer. ask answers that part with exactly what
// `lore context --for-file <path> --for-prompt <words>` picks (globs,
// folder scopes, mentions, topics, semantic rules on the path and the
// words, scoped notes), through the same context host,
// so ask and the agent hooks never disagree about what applies.
//
//	question ─▶ fileOf: the first word that is a path (a / and a file
//	            extension, or a folder ending in /)
//	         ─▶ Env.ContextHost().Fetch(--for-file path --for-prompt
//	            <the question without the path>) ─▶ Out.File,
//	            Out.Applies (id, kind, why), its text first in the answer
//	         ─▶ the best rows: only those holding two or more of the
//	            question's words (one shared word, such as "invoice", is
//	            not about the change)

package ask

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	contextop "github.com/thesatellite-ai/lore/ops/context"
)

// Applied is one row lore context picked for the file the question names.
type Applied struct {
	ID   string `json:"id"`
	Kind string `json:"kind" desc:"rule, hotfix, memory, decision, pattern or architecture-note"`
	Why  string `json:"why" desc:"why it applies: the glob, folder scope, mention or words that matched"`
	// Stale and Reverify: the row states a fact whose evidence changed
	// after it was verified, as lore context marks it (devco:D-953).
	Stale    string `json:"stale,omitempty" desc:"set when the row's fact_evidence changed in git after fact_verified_at: stale: evidence changed since <day> (<files>)"`
	Reverify string `json:"reverify,omitempty" desc:"set with stale: the command that clears it once the row and the code were read again"`
}

// FileContexter is the part of Env that says what applies to a file
// (`lore context --for-file`); *wire.App implements it. A build without
// the context machinery answers E_UNSUPPORTED and ask leaves the part out.
type FileContexter interface {
	ContextHost(ctx context.Context) (contextop.Host, error)
}

// reFile is a path in a question: folders and a file with an extension
// (db/migrations/0042_due.sql, web/src/Login.tsx), or a folder ending in /
// (internal/queue/). A bare <store>/<folder> (acme/orbit) is no path.
var reFile = regexp.MustCompile(`(?:^|[\s"'(\x60])((?:[\w.@-]+/)+(?:[\w@-][\w.@-]*\.[A-Za-z][\w]{0,7}|))(?:$|[\s"'),:;!?\x60]|\.(?:\s|$))`)

// fileOf is the first path the question names ("" when none). A URL is
// no path.
func fileOf(text string) string {
	for _, m := range reFile.FindAllStringSubmatch(text, -1) {
		p := m[1]
		if strings.Contains(p, "://") || strings.HasPrefix(p, "//") || p == "/" {
			continue
		}
		return strings.TrimPrefix(p, "./")
	}
	return ""
}

// fileBudget is the share of --max-chars the file's context may take: it
// comes first (it is what the agent must not miss); the rows get the rest.
func fileBudget(maxChars int) int {
	return max(maxChars*3/4, 600)
}

// fileContext asks the context host what applies to file and what the
// question's own words (prompt, the path left out) add: lore context
// --for-file <file> --for-prompt <prompt>, one fetch. With no context host
// (a build without the machinery) it returns nothing; a failed fetch
// becomes one line of the answer (note) instead of failing the question.
func fileContext(ctx context.Context, env Env, file, prompt string, maxBytes int) (text string, applied []Applied, note string) {
	fc, has := env.(FileContexter)
	if !has {
		return "", nil, ""
	}
	host, err := fc.ContextHost(ctx)
	if err != nil {
		return "", nil, ""
	}
	a, err := host.Fetch(ctx, contextop.FetchIn{ForFile: file, ForPrompt: prompt, MaxBytes: maxBytes}, nil)
	if err != nil {
		return "", nil, "what applies to " + file + " could not be read: " + firstLine(err.Error())
	}
	if len(a.Data) > 0 {
		var picks []Applied
		if json.Unmarshal(a.Data, &picks) == nil {
			applied = picks
		}
	}
	// the context's own title line is the answer's section head here
	t := a.Text
	if strings.HasPrefix(t, "# lore context for ") {
		_, t, _ = strings.Cut(t, "\n")
	}
	return strings.TrimSpace(t), applied, ""
}

// printedIDs are the ids of applied that text prints (full or as a line):
// a pick the cap left out is not cited.
func printedIDs(text string, applied []Applied) []string {
	var ids []string
	for _, a := range applied {
		if a.ID != "" && strings.Contains(text, a.ID) {
			ids = append(ids, a.ID)
		}
	}
	return ids
}
