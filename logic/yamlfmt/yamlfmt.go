// Package yamlfmt writes a hand-edited YAML file back the way its owner
// wrote it (layer L1, pure): the file's own indent (2 spaces when it shows
// none), an empty top-level mapping as the file's comments alone, or an
// empty file, never `{}`, and nested lists and mappings in block style (one
// key or item per line), never one long flow line. Both config.yaml writers use it (ops/config's
// `config set|unset` and wire's setup edits), so a write never reflows a
// file someone keeps by hand.
package yamlfmt

import (
	"bytes"
	"errors"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/yamlerr"
)

// DefaultIndent is the indent of a file that shows none (new or flat).
const DefaultIndent = 2

// maxIndent bounds a detected indent (yaml.v3 accepts 2..9).
const maxIndent = 9

// Indent is the indent raw uses. The surest sign is how far the first
// key under a top-level key sits (`personal:` then `  store: …` is 2);
// next, how far a top-level list's first "- " sits (yaml.v3 puts it at
// the indent: `setups:` then `    - name: …` is 4); last, how far any
// nested mapping key sits under the key that opens it. A key nested inside
// a list item is only the last resort because yaml.v3 nests it 2 deeper at
// every indent (`    - name: a` then `      github:` then `        user:`
// at indent 4), so reading it first turned a 4-space file into 2 on the
// next write. Comments and blank lines are skipped; an item at its
// parent's column says nothing. DefaultIndent when no such line exists or
// the step is outside what yaml.v3 writes (2..9).
func Indent(raw []byte) int {
	var topKey, topItem, nested int
	prevIndent, prevOpens := 0, false
	for line := range strings.SplitSeq(string(raw), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		n := len(line) - len(trimmed)
		item := strings.HasPrefix(trimmed, "- ") || trimmed == "-"
		if prevOpens && n > prevIndent {
			step := n - prevIndent
			switch {
			case prevIndent == 0 && !item && topKey == 0:
				topKey = step
			case prevIndent == 0 && item && topItem == 0:
				topItem = step
			case prevIndent > 0 && !item && nested == 0:
				nested = step
			}
		}
		if topKey != 0 {
			break
		}
		body, _, _ := strings.Cut(trimmed, " #")
		prevIndent, prevOpens = n, !item && strings.HasSuffix(strings.TrimSpace(body), ":")
	}
	for _, step := range []int{topKey, topItem, nested} {
		if step == 0 {
			continue
		}
		if step >= DefaultIndent && step <= maxIndent {
			return step
		}
		return DefaultIndent
	}
	return DefaultIndent
}

// Encode writes doc (a document whose content is one mapping) with indent.
// An empty mapping is written as the comments the document and the mapping
// still carry (each line as written, `# …`), or as nothing. Block runs
// first, so a flow collection holding lists or mappings is written in block
// style.
func Encode(doc *yaml.Node, indent int) ([]byte, error) {
	if empty(doc) {
		return comments(doc), nil
	}
	Block(doc)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Block rewrites n in place so the file reads like a hand-written one: a
// flow-style list or mapping that holds any list or mapping becomes block
// style, together with everything inside it. A flat flow collection of
// scalars (`tools: [claude, codex]`, `identity: {key: k}`) stays as written.
//
// Why: yaml.v3 parses `setups: []` as a flow node and writes every child of
// a flow node in flow style, so entries appended to an emptied list put the
// whole list on one line; a file already written that way is repaired by
// its next write. A comment at the end of the flow line stays on that
// line, after the key that opens the block.
func Block(n *yaml.Node) {
	if n == nil {
		return
	}
	if flowNested(n) {
		unflow(n)
		return
	}
	for i, c := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 1 && flowNested(c) && c.LineComment != "" {
			k := n.Content[i-1]
			k.LineComment = strings.TrimSpace(k.LineComment + " " + c.LineComment)
			c.LineComment = ""
		}
		Block(c)
	}
}

// FlowKeys lists the dotted paths ("setups", "orgs.0.stores") of the
// collections Block would rewrite: a flow-style list or mapping holding a
// list or mapping, outermost only, in file order ("." for the top level
// itself). Empty when the file already reads in block style. It only
// reads n: doctor and `lore config fmt` decide from it whether a rewrite
// is needed, and a read never rewrites the file.
func FlowKeys(n *yaml.Node) []string {
	var out []string
	flowKeys(n, "", &out)
	return out
}

// flowKeys appends the flow-nested collections under n at path p.
func flowKeys(n *yaml.Node, p string, out *[]string) {
	if n == nil {
		return
	}
	if flowNested(n) {
		if p == "" {
			p = "."
		}
		*out = append(*out, p)
		return
	}
	join := func(part string) string {
		if p == "" {
			return part
		}
		return p + "." + part
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			flowKeys(c, p, out)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			flowKeys(n.Content[i+1], join(n.Content[i].Value), out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			flowKeys(c, join(strconv.Itoa(i)), out)
		}
	case yaml.ScalarNode, yaml.AliasNode:
		// no children
	}
}

// flowNested: n is a flow-style list or mapping holding a list or mapping.
func flowNested(n *yaml.Node) bool {
	return n.Style&yaml.FlowStyle != 0 && collection(n) && nested(n)
}

// collection: n is a list or a mapping.
func collection(n *yaml.Node) bool {
	return n.Kind == yaml.SequenceNode || n.Kind == yaml.MappingNode
}

// nested: one of n's children is a list or a mapping.
func nested(n *yaml.Node) bool {
	for _, c := range n.Content {
		if collection(c) {
			return true
		}
	}
	return false
}

// unflow clears the flow style of n and of every collection under it.
func unflow(n *yaml.Node) {
	if collection(n) {
		n.Style &^= yaml.FlowStyle
	}
	for _, c := range n.Content {
		unflow(c)
	}
}

// DropEmpty removes key from mapping m when its value is an empty list or
// mapping, so a removal that empties a list never leaves `key: []` (or
// `key: {}`) behind; true when it removed one. The key's header comment
// moves on as KeepHeader does.
func DropEmpty(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		v := m.Content[i+1]
		if !collection(v) || len(v.Content) != 0 {
			return false
		}
		KeepHeader(m, i)
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
		return true
	}
	return false
}

// empty reports a document whose top level is an empty mapping.
func empty(doc *yaml.Node) bool {
	return doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 &&
		doc.Content[0].Kind == yaml.MappingNode && len(doc.Content[0].Content) == 0
}

// comments are the head and foot comments of doc and its mapping, one per
// line, "" when none.
func comments(doc *yaml.Node) []byte {
	m := doc.Content[0]
	var parts []string
	for _, c := range []string{doc.HeadComment, m.HeadComment, m.LineComment, m.FootComment, doc.FootComment} {
		if c = strings.TrimSpace(c); c != "" {
			parts = append(parts, c)
		}
	}
	if len(parts) == 0 {
		return []byte{}
	}
	return []byte(strings.Join(parts, "\n") + "\n")
}

// KeepHeader moves the head comment of mapping m's first key before the
// key at index i (0) is removed: yaml.v3 hangs a file's opening comment on
// the first key, so deleting that key would drop the file's header. The
// comment goes to the next key, or to m itself when the key is the last.
func KeepHeader(m *yaml.Node, i int) {
	if i != 0 || len(m.Content) < 2 || m.Content[0].HeadComment == "" {
		return
	}
	head := m.Content[0].HeadComment
	if len(m.Content) >= 4 {
		next := m.Content[2]
		next.HeadComment = strings.TrimSpace(head + "\n" + next.HeadComment)
		return
	}
	m.HeadComment = strings.TrimSpace(m.HeadComment + "\n" + head)
}

// Decode parses raw, a file someone keeps by hand, into doc so that
// EncodeLike writes it back without moving what they wrote:
//   - CRLF line endings are read as LF (yaml.v3 keeps the \r in comments,
//     and a comment line written back then gains a blank line after it);
//   - a comment block at the very end of the file, at column 0, stays the
//     file's last lines: yaml.v3 hangs it on the deepest last key (the
//     last setup's `folders:`), so an edit would indent it into that
//     setup and a trash or purge of the setup would take it along.
func Decode(raw []byte, doc *yaml.Node) error {
	lf := toLF(raw)
	if err := yaml.Unmarshal(lf, doc); err != nil {
		return errors.New(yamlerr.Syntax(lf, err))
	}
	hoistTrailing(doc, lf)
	return nil
}

// EncodeLike is Encode with raw's own indent, and with CRLF line endings
// when raw (the file as read) used them on every line.
func EncodeLike(doc *yaml.Node, raw []byte) ([]byte, error) {
	out, err := Encode(doc, Indent(toLF(raw)))
	if err != nil || !allCRLF(raw) {
		return out, err
	}
	return bytes.ReplaceAll(out, []byte("\n"), []byte("\r\n")), nil
}

// toLF is raw with every CRLF as LF.
func toLF(raw []byte) []byte {
	if !bytes.Contains(raw, []byte("\r\n")) {
		return raw
	}
	return bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
}

// allCRLF: raw has lines and every one ends in CRLF.
func allCRLF(raw []byte) bool {
	n := bytes.Count(raw, []byte("\n"))
	return n > 0 && n == bytes.Count(raw, []byte("\r\n"))
}

// hoistTrailing moves the file's closing comment block (see Decode) from
// the node yaml.v3 hung it on to the document's foot. Only an exact match
// moves: a comment indented under a key is that key's and stays.
func hoistTrailing(doc *yaml.Node, lf []byte) {
	tail := trailingComment(lf)
	if tail == "" || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return
	}
	for n := doc.Content[0]; n != nil && len(n.Content) > 0; {
		last := n.Content[len(n.Content)-1]
		cands := []*yaml.Node{last}
		if n.Kind == yaml.MappingNode && len(n.Content) >= 2 {
			cands = append(cands, n.Content[len(n.Content)-2])
		}
		for _, c := range cands {
			if strings.TrimSpace(c.FootComment) == tail {
				c.FootComment = ""
				doc.FootComment = strings.TrimSpace(doc.FootComment + "\n" + tail)
				return
			}
		}
		n = last
	}
}

// trailingComment is the run of comment lines that ends the file, each at
// column 0 and after the last line holding YAML ("" when the file ends in
// YAML or in an indented comment).
func trailingComment(lf []byte) string {
	lines := strings.Split(strings.TrimRight(string(lf), "\n "), "\n")
	i := len(lines)
	for i > 0 && strings.HasPrefix(lines[i-1], "#") {
		i--
	}
	if i == len(lines) || i == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[i:], "\n"))
}
