package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// The answers of the secret menu (SECRETS 2.1). Anything else is asked
// again, up to menuTries times; end of input is cancel.
const (
	menuRedact   = "r"
	menuUnstored = "u"
	menuNot      = "n"
	menuCancel   = "c"
	// menuTries bounds the re-asks after an answer that is not a letter
	// of the menu, so a stuck input never loops.
	menuTries = 3
)

// Menu text (SECRETS 2.1). The findings are printed masked, as the engine
// gave them; the menu never prints a value or suggests a vault entry (SQ6).
const (
	menuHead = `What should lore do with it?
  [r] redact: replace it with a reference to where it is stored
  [u] it is not stored anywhere yet: redact as ${secret:unstored} and add a task "store it, then rotate it"
  [n] not a secret (false positive): keep the text, record an allow entry
  [c] cancel: write nothing
> `
	menuRefAsk    = "Where is it stored? Type the reference, e.g. gopass://team/app/db_password or op://Vault/Item/field"
	menuReasonAsk = "Why is this not a secret? (recorded) > "
)

// secretMenu shows the masked findings of an on_detect ask answer and
// reads the person's choice (SECRETS 2.1, FB-SEC-2). ok is false for
// cancel, end of input, an empty reference or an empty reason: the write
// then stores nothing. It never answers "yes" by itself: every way to
// proceed carries a typed reference, the unstored choice or a typed reason.
func secretMenu(in *bufio.Reader, out io.Writer, fs []errs.Finding) (op.SecretChoice, bool) {
	noun := "secret"
	if len(fs) != 1 {
		noun = "secrets"
	}
	_, _ = fmt.Fprintf(out, "lore found %d possible %s in this write:\n\n", len(fs), noun)
	for _, f := range fs {
		_, _ = fmt.Fprintf(out, "  %d. %s  in %s, line %d\n     %s\n", f.N, f.Rule, f.Field, f.Line, f.Masked)
	}
	_, _ = io.WriteString(out, "\n")
	choice := ""
	for range menuTries {
		_, _ = io.WriteString(out, menuHead)
		line, ok := readLine(in)
		if !ok {
			return op.SecretChoice{}, false
		}
		choice = strings.ToLower(line)
		if choice == menuRedact || choice == menuUnstored || choice == menuNot || choice == menuCancel {
			break
		}
		choice = ""
	}
	switch choice {
	case menuRedact:
		return menuRefs(in, out, fs)
	case menuUnstored:
		return op.SecretChoice{Unstored: true}, true
	case menuNot:
		_, _ = io.WriteString(out, menuReasonAsk)
		reason, ok := readLine(in)
		if !ok || reason == "" {
			return op.SecretChoice{}, false // D12/SQ2: the reason is required
		}
		return op.SecretChoice{NotASecret: true, Reason: reason}, true
	}
	return op.SecretChoice{}, false // cancel, or no valid letter
}

// menuRefs reads one reference per finding (one finding: one prompt). The
// engine validates each (<manager>://<locator>); an empty line cancels.
func menuRefs(in *bufio.Reader, out io.Writer, fs []errs.Finding) (op.SecretChoice, bool) {
	refs := make([]string, 0, len(fs))
	_, _ = io.WriteString(out, menuRefAsk+"\n")
	for _, f := range fs {
		q := "> "
		if len(fs) > 1 {
			q = fmt.Sprintf("finding %d (%s in %s) > ", f.N, f.Rule, f.Field)
		}
		_, _ = io.WriteString(out, q)
		ref, ok := readLine(in)
		if !ok || ref == "" {
			return op.SecretChoice{}, false
		}
		refs = append(refs, ref)
	}
	return op.SecretChoice{Refs: refs}, true
}
