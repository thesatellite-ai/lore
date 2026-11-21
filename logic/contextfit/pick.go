package contextfit

// On-demand picking for `lore context` (CONTEXT_BUDGET 6.8): glob matching
// of a path against a rule's globs, the words of a path or prompt, and
// fitting picked entries under a byte cap without cutting one.
//
//	Pick ─► each entry full while it fits ─► else its line ─► else stop:
//	        Out ends with "more: <command>" (never a cut mid-entry)

import (
	"encoding/json"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/logic/querywords"
)

// SplitGlobs splits a rule's globs field into patterns: a JSON array of
// strings (what `lore rule add --globs` documents), else a comma,
// semicolon, space or line separated list (v0.1.17 rows typed by hand).
func SplitGlobs(s string) []string {
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "[") {
		var arr []string
		if json.Unmarshal([]byte(t), &arr) == nil {
			return arr
		}
	}
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
}

// MatchGlob reports whether the slash path p matches pattern, where "**"
// matches any number of path segments and every other segment uses
// path.Match rules. A pattern without a slash matches the base name at any
// depth (the gitignore convention).
func MatchGlob(pattern, p string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	p = strings.TrimPrefix(p, "./")
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		ok, err := path.Match(pattern, path.Base(p))
		return err == nil && ok
	}
	return matchSegs(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(segs); i++ {
				if matchSegs(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// minWord is the shortest word used for matching (shorter words match
// everything: "go", "src").
const minWord = 4

// Words returns the distinct lower-case words of s (letters and digits) of
// at least four characters, in first-seen order; "_", "-", "/" and "."
// split words, so a path yields its directory and file stem words.
func Words(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) < minWord || seen[w] || stopWords[w] || querywords.IsStop(w) {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// SemanticWords is Words plus the three-letter words that are not filler
// (log, sql, css, jwt, vat), for matching a semantic rule's applies-to
// description: that description is a curated list of topic words, where a
// short word is as telling as a long one. "log failed login attempts" never
// reached the rule that applies to "logging, logs, logger" because "log"
// was below minWord (devco D-815).
func SemanticWords(s string) []string {
	out := Words(s)
	seen := map[string]bool{}
	for _, w := range out {
		seen[w] = true
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) != minWord-1 || seen[w] || shortStopWords[w] || querywords.IsStop(w) || !hasLetter(w) {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// shortStopWords are three-letter words a task or prompt carries without
// naming a topic (the verbs of a request, numbers, filler).
var shortStopWords = map[string]bool{"add": true, "fix": true, "new": true, "use": true, "set": true, "put": true,
	"run": true, "let": true, "see": true, "via": true, "per": true, "out": true, "off": true, "end": true,
	"top": true, "way": true, "one": true, "two": true, "old": true, "own": true, "bug": true, "now": true,
	"too": true, "yet": true, "say": true, "try": true, "lot": true, "few": true, "why": true, "how": true,
	"its": true, "our": true, "who": true, "has": true, "had": true, "was": true, "are": true, "not": true,
	"and": true, "but": true, "for": true, "the": true, "you": true, "can": true, "did": true, "get": true,
	"got": true, "all": true, "any": true, "max": true, "min": true}

// hasLetter reports whether w starts with a letter (10m, 404 are no
// topic; s3a is).
func hasLetter(w string) bool {
	r, _ := utf8.DecodeRuneInString(w)
	return unicode.IsLetter(r)
}

// Floor is the fewest of words a row must hold to be picked on its words
// alone, when the best row holds best of them. One shared word ("invoice",
// "login", "support") is weak: a prompt about invoice e-mails pulled in
// every row naming an invoice, and the agent read mostly noise (devco
// D-815). So a text of three or more words needs two of them in a row;
// a shorter one ("how is the ledger stored") takes one word only while no
// row holds both.
func Floor(words []string, best int) int {
	if len(words) >= 3 {
		return 2
	}
	return min(2, best)
}

// PathFloor is the fewest path words a note that is not scoped to the
// path's folders must hold: every one of them, up to two. A file's words
// are its folder's topic and its own (billing, invoice); a note naming
// only one ("the billing team has lunch on Thursdays", "a bigger font on
// the invoice header") is about something else (devco D-815).
func PathFloor(words []string) int { return min(2, len(words)) }

// PathWords returns the words of a repo path that can name its topic:
// Words(p) without the generic names a file or folder carries in every
// project (main, index, init, util, test, src …) and file-type words
// (json, yaml …). `context --for-file main.go` matched notes on the word
// "main" alone (a branch name) before (D-599); such a path now yields no
// words, so it is matched by globs, scope and mentions only.
func PathWords(p string) []string {
	var out []string
	for _, w := range Words(p) {
		if !genericPathWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// genericPathWords are file stems, folder names and file-type words of
// four letters or more that appear in most repositories and so say nothing
// about what a file is for (D-599). Shorter ones (app, mod, lib, src, cmd)
// are already below minWord.
var genericPathWords = map[string]bool{
	"main": true, "index": true, "init": true, "util": true, "utils": true, "test": true, "tests": true,
	"spec": true, "specs": true, "types": true, "type": true, "common": true, "helper": true, "helpers": true,
	"base": true, "core": true, "misc": true, "default": true, "defaults": true, "module": true, "modules": true,
	"package": true, "source": true, "internal": true, "vendor": true, "public": true, "private": true,
	"shared": true, "libs": true, "export": true, "exports": true, "setup": true, "constants": true,
	"const": true, "mock": true, "mocks": true, "fixture": true, "fixtures": true, "temp": true,
	"json": true, "yaml": true, "toml": true, "html": true, "scss": true, "java": true, "rust": true,
	"swift": true, "kotlin": true, "proto": true, "lock": true, "conf": true, "data": true, "file": true, "files": true,
}

// stopWords are frequent words of four letters or more that carry no
// topic, besides querywords' list (the one search reads, D-435).
var stopWords = map[string]bool{"this": true, "that": true, "with": true, "from": true, "have": true,
	"what": true, "when": true, "where": true, "which": true, "there": true, "their": true, "about": true,
	"would": true, "could": true, "should": true, "into": true, "then": true, "than": true, "them": true,
	// lore's own rows named in a request ("see the ledger notes") are no
	// topic: the topic is the word next to them (devco D-815)
	"note": true, "notes": true, "rule": true, "rules": true}

// Score is how many of words appear in text: querywords.Coverage, the
// matcher `lore search` ranks by too (D-435), so "deploying" in a prompt
// finds a rule about deployment.
func Score(text string, words []string) int { return querywords.Coverage(text, words) }

// Piece is one picked entry: its full and line text.
type Piece struct {
	ID         string
	Full, Line string
}

// Picked is the fitted output.
type Picked struct {
	Text string
	// Shown counts pieces printed (full or line); Lines counts the lines.
	Shown, Lines int
	// More is true when pieces were left out for the cap.
	More bool
	// IDs are the ids of the pieces printed (full or line), in order;
	// pieces without an id (section headers) are not listed. The context
	// hook records them so a session never gets the same row twice (6.9).
	IDs []string
}

// Pick writes pieces in order under maxBytes (0 = no cap), reserving
// reserve bytes for the closing "more:" line: a piece goes in full when it
// fits, else as its line, else picking stops (6.8: never split an entry).
func Pick(pieces []Piece, maxBytes, reserve int) Picked {
	var b strings.Builder
	var out Picked
	for _, p := range pieces {
		switch {
		case maxBytes <= 0 || b.Len()+len(p.Full)+reserve <= maxBytes:
			b.WriteString(p.Full)
		case b.Len()+len(p.Line)+reserve <= maxBytes:
			b.WriteString(p.Line)
			out.Lines++
		default:
			out.More = true
			out.Text = b.String()
			return out
		}
		out.Shown++
		if p.ID != "" {
			out.IDs = append(out.IDs, p.ID)
		}
	}
	out.Text = b.String()
	return out
}
