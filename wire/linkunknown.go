package wire

// linkunknown.go — devco D-860 (hunt H-128, follow-up of H-110): a read
// never drops a linked folder without a word. A repo linked to an org
// folder by its remote (`lore org link`, no lore data of its own) is
// linked only by a row in the org store. Offline for longer than
// link_index.max_age, the only copy of that row is too old to decide by,
// so the repo is not linked at all (fail closed, as the client guards
// are) and its folder is not read: `lore search` answered "searched: me"
// as if the folder held nothing. Each read that lists what it searched
// (search, ask, context, every <kind> search) and doctor now say, for
// every such folder:
//
//	<org>/<folder>: link unknown (offline since <date> UTC, link index older than <max age>); not searched; reconnect, then run `lore sync`
//
// under the answer (an empty answer: right under its searched: line) and
// as the top-level --json list "link_unknown" ({store, since, max_age}).
//
//	orgcfg.Gather ─▶ byRemote: only a too-old copy links the repo
//	              └▶ Situation.UnknownLinks (entry, folder, copy's last fetch)
//	Run ─▶ the op reads (the folder is not attached)
//	    ├▶ withUnknownLinks(ctx, a.unknownLinks(ctx))
//	    ├▶ withEmptyNote: the lines under searched:
//	    └▶ noteLinkUnknown: the lines under the answer and the --json list
//	doctor ─▶ unknownLinkFindings (store_access)
//
// Whether such a repo should still read the folder's old copy is an owner
// decision (H-128); until then nothing here changes what is read.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// LinkUnknownField is the top-level --json list of the linked folders a
// read did not search because their link could not be decided offline.
const LinkUnknownField = "link_unknown"

// UnknownLink is one folder that links this repo only in a store copy
// older than link_index.max_age while the store does not answer
// (Options.RepoUnknownLinks).
type UnknownLink struct {
	// Store is "<org entry>/<folder>", the --store that reads it once
	// the link is known again.
	Store string
	// Since is the copy's last fetch that reached the store (zero: never
	// fetched on this machine).
	Since time.Time
	// MaxAge is link_index.max_age.
	MaxAge time.Duration
}

// linkUnknown is one UnknownLink in --json.
type linkUnknown struct {
	// Store is "<org entry>/<folder>".
	Store string `json:"store"`
	// Since is the copy's last good fetch (RFC 3339, UTC); "" when never.
	Since string `json:"since,omitempty"`
	// MaxAge is link_index.max_age ("1h").
	MaxAge string `json:"max_age"`
}

// linkUnknownInfo declares the field for the OpenAPI document.
var linkUnknownInfo = op.FieldInfo{
	Name: LinkUnknownField, Type: op.TypeList,
	Desc: "the folders that link this repo only in a store copy older than link_index.max_age while the store does not answer: not searched until the store is reached again (absent when every link is known)",
	Fields: []op.FieldInfo{
		{Name: "store", Type: op.TypeString, Required: true, Desc: "the folder as <org>/<folder>"},
		{Name: "since", Type: op.TypeString, Desc: "the copy's last fetch that reached the store, a UTC time such as 2026-11-21T16:20:00Z; absent when it was never fetched here"},
		{Name: "max_age", Type: op.TypeString, Required: true, Desc: "link_index.max_age, past which a copy decides no link, such as 1h"},
	},
}

// unknownLinksKey is the context key of withUnknownLinks.
type unknownLinksKey struct{}

// withUnknownLinks returns ctx carrying ls for the answer hooks of Run.
func withUnknownLinks(ctx context.Context, ls []UnknownLink) context.Context {
	if len(ls) == 0 {
		return ctx
	}
	return context.WithValue(ctx, unknownLinksKey{}, ls)
}

// unknownLinksOf is what withUnknownLinks put in ctx.
func unknownLinksOf(ctx context.Context) []UnknownLink {
	ls, _ := ctx.Value(unknownLinksKey{}).([]UnknownLink)
	return ls
}

// unknownLinks asks the org resolution (nil hook, or none: nil).
func (a *App) unknownLinks(ctx context.Context) []UnknownLink {
	if a.opts.RepoUnknownLinks == nil {
		return nil
	}
	return a.opts.RepoUnknownLinks(ctx)
}

// unknownLinkLine is the line naming one dropped link.
func unknownLinkLine(l UnknownLink) string {
	since := "offline, copy never fetched here"
	if !l.Since.IsZero() {
		since = "offline since " + l.Since.UTC().Format(staleCopyLayout) + " UTC"
	}
	return l.Store + ": link unknown (" + since + ", link index older than " + shortDuration(l.MaxAge) + "); not searched; reconnect, then run `lore sync`"
}

// unknownLinkLines is the indented lines of ls.
func unknownLinkLines(ls []UnknownLink) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString("  " + unknownLinkLine(l) + "\n")
	}
	return b.String()
}

// unknownLinksJSON is ls as the --json list.
func unknownLinksJSON(ls []UnknownLink) ([]byte, error) {
	out := make([]linkUnknown, 0, len(ls))
	for _, l := range ls {
		j := linkUnknown{Store: l.Store, MaxAge: shortDuration(l.MaxAge)}
		if !l.Since.IsZero() {
			j.Since = l.Since.UTC().Format(time.RFC3339)
		}
		out = append(out, j)
	}
	return json.Marshal(out)
}

// noteLinkUnknown returns res with the dropped links of ctx named, for the
// reads that list what they searched (emptySearchSpec). An empty answer
// names them in its searched block (emptySearchLines), so only its --json
// list is added here; a line already in the text is not added again.
func noteLinkUnknown(ctx context.Context, spec op.Spec, res op.Result, empty bool) op.Result {
	if !emptySearchSpec(spec) {
		return res
	}
	ls := unknownLinksOf(ctx)
	if len(ls) == 0 {
		return res
	}
	raw, err := unknownLinksJSON(ls)
	if err != nil {
		return res
	}
	if !empty {
		res = res.WithTextEdit(func(t string) string {
			var add strings.Builder
			for _, l := range ls {
				if line := unknownLinkLine(l); !strings.Contains(t, line) {
					add.WriteString("  " + line + "\n")
				}
			}
			if add.Len() == 0 {
				return t
			}
			return strings.TrimRight(t, "\n") + "\n" + add.String()
		})
	}
	return res.WithTopJSON(LinkUnknownField, raw)
}

// unknownLinkFindings are doctor's warnings for the dropped links of this
// repo (store_access): the same line the reads print.
func (a *App) unknownLinkFindings(ctx context.Context) []DoctorFinding {
	ls := a.unknownLinks(ctx)
	out := make([]DoctorFinding, 0, len(ls))
	for _, l := range ls {
		out = append(out, DoctorFinding{Check: DoctorCheckStoreAccess, Level: DoctorWarning, Message: unknownLinkLine(l)})
	}
	return out
}

// doctorUnknownLinkNote is doctor's repo note in a repo with no lore data
// whose link by remote dropped out offline (its store_access warning
// names the folder): never "lore init sets one up" for a linked repo.
const doctorUnknownLinkNote = doctorNoProjectPrefix + " (the repo is linked by its remote, but the link cannot be decided offline: see store_access)"

// withUnknownLinkErr turns the "lore isn't set up here" refusal of a read
// in a repo whose only link dropped out offline (devco:D-860) into one
// that names the link: its next step was `lore init` or `lore org link`,
// which would set up or link again a repo that is linked already. Any
// other error, or a repo with no such link, is returned as it is.
func withUnknownLinkErr(ctx context.Context, err error, opts Options) error {
	e, ok := err.(*errs.Error) //nolint:errorlint // only the refusal itself, never one wrapped in another error
	if !ok || e.Code != errs.NotAStore || opts.RepoUnknownLinks == nil {
		return err
	}
	ls := opts.RepoUnknownLinks(ctx)
	if len(ls) == 0 {
		return err
	}
	lines := make([]string, 0, len(ls))
	for _, l := range ls {
		lines = append(lines, unknownLinkLine(l))
	}
	return errs.New(errs.NotAStore, "this repo holds no lore data and is linked by its remote, but its link cannot be decided offline: "+strings.Join(lines, "; ")).
		WithHint("`lore sync` reads the link again once the store answers; `lore whoami` shows the link status").WithNext("lore", "sync")
}
