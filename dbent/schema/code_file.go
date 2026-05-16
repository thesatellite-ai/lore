package schema

import (
	"saas/pkg/aicoder/ids"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CodeFile is a skeletal index of files in a repo (R33 A3).
//
// v0.1 minimum: just file metadata (path, language, size, sha).
// v0.2 will add code_symbols, code_imports, code_tests, code_owners,
// code_dependencies as separate tables.
//
// Update flow: `aicoder learn-from .` walks the repo, computes content_sha
// per file, INSERT-or-UPDATE if changed since last_indexed_at.
type CodeFile struct {
	ent.Schema
}

func (CodeFile) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixCodeFile},
	}
}

func (CodeFile) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("repo_id").NotEmpty().Immutable(),

		// path: relative to repo root. Forward-slash separators (normalized at write).
		field.String("path").NotEmpty(),

		// language: detected from extension. NULL if unknown.
		field.String("language").Optional().Nillable(),

		// size_bytes: surface "biggest 10 files" stats in doctor.
		field.Int64("size_bytes").Optional().Nillable(),

		// content_sha: lets `learn-from` skip unchanged files.
		field.String("content_sha").Optional().Nillable(),

		field.Time("last_indexed_at").Default(time.Now),

		// Soft-archive when file is deleted from repo (don't immediately purge —
		// might still be referenced by old memories).
		field.Time("archived_at").Optional().Nillable(),
	}
}

func (CodeFile) Indexes() []ent.Index {
	return []ent.Index{
		// Unique path within a repo.
		index.Fields("repo_id", "path").Unique(),
		// "All Go files in this repo" — language filter.
		index.Fields("language"),
	}
}
