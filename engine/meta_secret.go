package engine

// Secrets in metadata values (SECRETS_AND_REFERENCES 19.1, METADATA 3.21;
// PASS3-A-2, replaces META-B7-SCAN's flat refusal): the same answer flow
// as a row's fields (FB-SEC-1..4), applied leaf by leaf.
//
//	scanMeta(changed entries)
//	   ├─ leaves: the key name, every string leaf, every member name
//	   ├─ scan each leaf; rules off / allow rows dropped (D2/D9)
//	   ├─ a finding in a key name ─► E_SECRET_DETECTED in every mode
//	   └─ resolve(the write's field hits + these leaf hits): one count,
//	      one numbering, one --secret-ref order, one question (P3-METASEC-2)
//	   ├─ answer given (--secret-ref | --secret-unstored | --not-a-secret --reason)
//	   │     ref/unstored ─► the match is replaced inside its leaf only
//	   │     not a secret ─► confirm, value kept, sc.accepted (allow rows)
//	   └─ no answer: on_detect ask    ─► E_NEEDS_CONFIRM (findings[], flag sets)
//	                 on_detect redact ─► ${secret:unstored} in the leaf
//	                 on_detect refuse ─► E_SECRET_DETECTED
//	   nothing is written on any error: the row put follows applyMeta

import (
	"context"
	"errors"
	"strings"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// metaHit is the findings of one string leaf of one metadata entry.
type metaHit struct {
	id       ids.ID[ids.MetaKey]
	leaf     meta.Leaf
	findings []scan.Finding
}

// scanMeta runs the secret scan over the changed metadata values and
// applies secrets.on_detect and the caller's answer (see the file's flow
// sketch) together with the field findings detect kept in sc (resolve).
// Redaction rewrites p.entries in place; accepted and redacted findings
// are added to sc. A finding's field is "meta.<key>" plus the leaf's path
// ("meta.smtp.url", "meta.hosts[1]"), so an allow row pins that leaf (D6).
func (e *Engine) scanMeta(ctx context.Context, cl call, target store.Source, rowID ids.AnyID, p *metaPlan, sc *screened) error {
	if sc == nil {
		return errs.New(errs.Internal, "the metadata secret scan needs the write's screened state")
	}
	_, rules, err := e.settings.Policy(target)
	if err != nil {
		return err
	}
	allows, err := e.settings.Allows(target)
	if err != nil {
		return err
	}
	own := e.actorOf(cl) == e.actor
	var hits []metaHit
	var keyHits []scan.Finding
	seen := map[ids.ID[ids.MetaKey]]bool{}
	for _, id := range p.changed {
		en, ok := p.entries[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		leaves, lerr := meta.Leaves(en.V)
		if lerr != nil {
			return lerr
		}
		// 19.1: the key name is scanned too (an identifier, never redacted)
		leaves = append([]meta.Leaf{{Text: en.Name, Key: true}}, leaves...)
		base := row.KeyMeta + "." + en.Name
		for i, l := range leaves {
			// a finding's field goes into the error, the audit and the
			// warnings, so an identifier that holds the secret is never part
			// of it: the key name's own finding is "meta.(key name)", a member
			// name's is "<key> (member name)" without the path (P3-METASEC-1)
			field := base
			switch {
			case i == 0:
				field = row.KeyMeta + ".(key name)"
			case l.Key:
				field += " (member name)"
			default:
				if ps := l.PathString(); ps != "" {
					field += "." + ps
				}
			}
			found, warn := e.scanText(field, l.Text)
			if warn != nil {
				p.warnings = append(p.warnings, *warn)
			}
			var kept []scan.Finding
			for _, f := range found {
				if !rules.IsOff(f.RuleID, own) {
					kept = append(kept, f)
				}
			}
			kept = scan.Filter(rowID, kept, allows)
			switch {
			case len(kept) == 0:
			case l.Key:
				keyHits = append(keyHits, kept...)
			default:
				hits = append(hits, metaHit{id: id, leaf: l, findings: kept})
			}
		}
	}
	if len(keyHits) > 0 {
		// a key name cannot hold a reference: refused in every mode (19.1);
		// the write's field findings are audited as cancelled (nothing is
		// written), the key's own as refused
		mode := string(sc.pol.OnDetect())
		if aerr := errors.Join(e.auditFindings(ctx, cl, rowID, mode, choiceCancelled, sc.fieldFindings()),
			e.auditFindings(ctx, cl, rowID, mode, choiceRefused, keyHits)); aerr != nil {
			return aerr
		}
		return secretErr(keyHits).WithHint("a key name cannot hold a reference: rename the key, keep the secret in your vault and write ${secret:…} as its value")
	}
	// one answer for the fields' and the metadata's findings (P3-METASEC-2)
	return e.resolve(ctx, cl, target, rowID, sc, &metaPending{p: p, hits: hits})
}

// redactLeaves replaces each hit's matches inside its own leaf: repl[i]
// replaces the i-th finding of hits in order. Only a text, list<text> or
// json value can hold a reference; a typed value (url, email, ip, …) or
// one picked from a key's values: list is refused instead (a reference
// would break its type), never stored half-redacted.
func (p *metaPlan) redactLeaves(hits []metaHit, repl []string) error {
	n := 0
	for _, h := range hits {
		r := repl[n : n+len(h.findings)]
		n += len(h.findings)
		en := p.entries[h.id]
		if !redactableMeta(p.types[h.id]) || len(en.VName) > 0 {
			return secretErr(h.findings).WithHint("a " + p.types[h.id].String() + " value cannot hold a reference; store the secret in your vault and write ${secret:…} in a text key")
		}
		v, err := meta.ReplaceLeaf(en.V, h.leaf.Path, redactText(h.leaf.Text, h.findings, r))
		if err != nil {
			return err
		}
		en.V = v
		p.entries[h.id] = en
	}
	return nil
}

// redactableMeta reports a value type whose string leaves may hold a
// reference: text, a list of text, json (METADATA 3.21: "any text value
// and any text leaf of a json value"). A type without a kind is text.
func redactableMeta(t meta.Type) bool {
	switch t.Kind {
	case value.KindText, value.KindJSON, "":
		return true
	case value.KindList:
		return t.Elem == "" || t.Elem == value.KindText
	case value.KindNumber, value.KindBool, value.KindDate, value.KindDateTime, value.KindDuration,
		value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL, value.KindEmail, value.KindRef,
		value.KindNull, value.KindSecretRef:
		// a typed value: a reference would break its type
	}
	return false
}

// refuseClientMetaRefs is refuseClientSecretRefs for redacted metadata:
// a reference the answer wrote into a client repo's row would show the
// client our vault locator (FLAG5, BATCH 5), so it is refused there.
func (e *Engine) refuseClientMetaRefs(target store.Source, rowID ids.AnyID, hits []metaHit) error {
	if !e.IsClient(target) {
		return nil
	}
	fields := make([]string, 0, len(hits))
	for _, h := range hits {
		fields = append(fields, h.findings[0].Field)
	}
	return errs.New(errs.LeakBlocked, ""+rowID.String()+": a secret reference in metadata ("+strings.Join(fields, ", ")+") cannot live in a client repo; nothing written").
		WithHint("drop the value, or keep the row in one of your own stores (BATCH 5)")
}

// auditFindings records secret.detected once per finding (SECRETS 16,
// FA-AGENT-7): field, rule, mode and the choice; never the value.
func (e *Engine) auditFindings(ctx context.Context, cl call, rowID ids.AnyID, mode, choice string, fs []scan.Finding) error {
	for _, f := range fs {
		if err := e.AuditSecret(ctx, ActionSecretDetected, SecretAudit{Command: cl.op, Row: rowID,
			Result: "field=" + f.Field + " rule=" + f.RuleID + " mode=" + mode + " choice=" + choice}); err != nil {
			return err
		}
	}
	return nil
}
