package heldout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// batchLine is one line of a lore batch apply file.
type batchLine struct {
	Op    string    `json:"op"`
	Input batchItem `json:"input"`
}

// batchItem is the input of one row write; Title is left out for memories.
type batchItem struct {
	To    string   `json:"to"`
	Title string   `json:"title,omitempty"`
	Body  string   `json:"body"`
	Meta  []string `json:"meta"`
}

// Batch is the lore batch apply file that writes the rows of one folder
// of the store, or of every folder for "" (cards are written with lore
// resource add; batch does not take them). The bench applies one file per
// folder: a first batch into two folders just made by lore store folder add
// failed with E_INTERNAL "git: closed" on lore 0.1.49.
func (s *Store) Batch(folder string) ([]byte, error) {
	var b bytes.Buffer
	for _, r := range s.Rows {
		if folder != "" && r.Folder != folder {
			continue
		}
		meta := make([]string, 0, len(r.Meta))
		for _, m := range r.Meta {
			meta = append(meta, m.Name+"="+m.Value)
		}
		raw, err := json.Marshal(batchLine{Op: string(r.Kind) + " add", Input: batchItem{To: r.Folder, Title: r.Title, Body: r.Body, Meta: meta}})
		if err != nil {
			return nil, fmt.Errorf("batch: row %s: %w", r.Key, err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

// dumpFormat is the _FORMAT.md of the dump: what a question writer needs
// to write a question file, and nothing about how lore ranks.
const dumpFormat = `# Held-out questions: how to write them

Each file in this folder is one row of a synthetic team store (a product with twelve subsystems, another client's folder "marlow", and code_repo resource cards). Write questions a developer of the product would ask an agent, in your own words, each answered by exactly one row or card.

One question per line of a JSON-lines file:

    {"id": "Q-001", "question": "…", "answer_key": "<bench_key of the answering row>", "answer_sentence": "<an exact substring of that row's title or body that answers it>", "kind": "<kind>"}

- answer_sentence must be copied exactly from the row (one sentence, or the part of it that answers); the scorer checks that lore printed it.
- kind is one of: exact (a command, flag, error name or constant), default (the value of a setting), subject (a fact inside a long as-built note), why (the reason of a decision), conflict (two rows disagree on a value: name the newer, correct row), sibling (a question that differs from another only by the subsystem), resource (answered by a code_repo card; answer_key card-<name>), howto (what to run or do).
- Mix the kinds; ask about rows of every subsystem; do not copy a row's wording into the question more than a developer would.
`

// WriteDump writes one markdown file per row and card into dir (made if
// missing; files of an earlier dump with the same names are replaced), plus
// _FORMAT.md. This is all a question writer sees of the store.
func (s *Store) WriteDump(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	write := func(name, text string) error {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			return fmt.Errorf("dump: %w", err)
		}
		return nil
	}
	if err := write("_FORMAT.md", dumpFormat); err != nil {
		return err
	}
	for _, r := range s.Rows {
		var b strings.Builder
		fmt.Fprintf(&b, "bench_key: %s\nkind: %s\nfolder: %s\n", r.Key, r.Kind, r.Folder)
		if r.Title != "" {
			fmt.Fprintf(&b, "title: %s\n", r.Title)
		}
		for _, m := range r.Meta[1:] { // bench_key is printed above
			fmt.Fprintf(&b, "meta %s: %s\n", m.Name, m.Value)
		}
		b.WriteString("\n---\n\n" + r.Body + "\n")
		if err := write(r.Key+".md", b.String()); err != nil {
			return err
		}
	}
	for _, c := range s.Cards {
		text := fmt.Sprintf("bench_key: %s\nkind: resource code_repo\nfolder: %s\nname: %s\nprovider: %s\nurl: %s\n\n---\n\npurpose: %s\n", c.Key, OrgFolder, c.Name, c.Provider, c.URL, c.Purpose)
		if err := write(c.Key+".md", text); err != nil {
			return err
		}
	}
	return nil
}
