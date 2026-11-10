#!/usr/bin/env python3
"""Write a v0.1.17 shared lore.db (Mode B) fixture for the docs examples.

0.1.18 never writes rows into lore.db, so a shared database for the
`lore migrate run` examples is built here with v0.1.17's own column shapes
(the tables `lore project shared-init` + `rule add` / `memory add` left).

usage: v017_shared_db.py <db path> <project name> [rule|memory:<body>]...
prints the new project's id (prj_...).
"""
import os
import sqlite3
import sys
import time
import uuid


def uuid7_hex() -> str:
    """32 hex digits of a UUIDv7 (the id form v0.1.17 wrote)."""
    ms = int(time.time() * 1000)
    rnd = int.from_bytes(os.urandom(10), "big")
    v = (ms & ((1 << 48) - 1)) << 80
    v |= 0x7 << 76
    v |= (rnd >> 68 & 0xFFF) << 64
    v |= 0x2 << 62
    v |= rnd & ((1 << 62) - 1)
    return uuid.UUID(int=v).hex


def now() -> str:
    """A time cell as ent wrote it through modernc (Go's time.String())."""
    t = time.time()
    return time.strftime("%Y-%m-%d %H:%M:%S", time.gmtime(t)) + ".%09d +0000 UTC" % int((t % 1) * 1e9)


SCHEMA = [
    "CREATE TABLE IF NOT EXISTS projects (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, name TEXT NOT NULL, origin_url TEXT, archived_at datetime, last_active_at datetime NOT NULL)",
    "CREATE TABLE IF NOT EXISTS rules (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, trust_score REAL NOT NULL, confidence REAL, last_accessed_at datetime, last_validated_at datetime, archived_at datetime, source_kind TEXT NOT NULL, source_ref TEXT, project_id TEXT, repo_id TEXT, body TEXT NOT NULL, activation TEXT NOT NULL, globs TEXT, applies_to_description TEXT, severity TEXT NOT NULL, superseded_by_id TEXT, created_by_actor_id TEXT)",
    "CREATE TABLE IF NOT EXISTS memories (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, trust_score REAL NOT NULL, confidence REAL, last_accessed_at datetime, last_validated_at datetime, archived_at datetime, source_kind TEXT NOT NULL, source_ref TEXT, kind TEXT NOT NULL, body TEXT NOT NULL, valid_at datetime, valid_until datetime, tx_at datetime NOT NULL, embedding_model_id TEXT, embedding_dim INTEGER, embedding BLOB, created_by_actor_id TEXT, validated_by_actor_id TEXT, superseded_by_id TEXT, project_id TEXT NOT NULL, repo_id TEXT)",
]


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    path, name = sys.argv[1], sys.argv[2]
    db = sqlite3.connect(path)
    for s in SCHEMA:
        db.execute(s)
    pid = "prj_" + uuid7_hex()
    t = now()
    db.execute("INSERT INTO projects (id, created_at, updated_at, name, last_active_at) VALUES (?, ?, ?, ?, ?)", (pid, t, t, name, t))
    for item in sys.argv[3:]:
        kind, _, body = item.partition(":")
        if kind == "rule":
            db.execute(
                "INSERT INTO rules (id, created_at, updated_at, trust_score, source_kind, project_id, body, activation, severity) VALUES (?, ?, ?, 0.5, 'manual', ?, ?, 'always', 'must')",
                ("rul_" + uuid7_hex(), t, t, pid, body))
        elif kind == "memory":
            db.execute(
                "INSERT INTO memories (id, created_at, updated_at, trust_score, source_kind, kind, body, tx_at, project_id) VALUES (?, ?, ?, 0.5, 'manual', 'retrieved', ?, ?, ?)",
                ("mem_" + uuid7_hex(), t, t, body, t, pid))
        else:
            print("unknown row kind " + kind, file=sys.stderr)
            return 2
    db.commit()
    db.close()
    print(pid)
    return 0


if __name__ == "__main__":
    sys.exit(main())
