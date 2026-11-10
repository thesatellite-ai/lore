package store

// SourceMachine is the store of this machine's machine-local rows
// (PASS3 phase C, P3-LOCAL-1): sessions, runs, bench results, learn and
// pii-pattern state, query and render logs, plugin trust and the other
// tables v0.1.17 kept in lore.db's localTables. Its backend is one local
// SQLite file per machine (<lore home>/state/machine.db); it has no remote,
// so Sync never moves anything and nothing in it is ever committed.
//
// It is attached beside the stores of stores.yaml but is not one of them:
// it is never in the engine's precedence order (Engine.Stores), never a
// `--to` target, and the engine routes a table to it only when the
// table's definition is machine-local (schema.TableDef.MachineLocal), so a
// shared row can never land in it and a machine-local row can never land
// anywhere else.
const SourceMachine Source = "machine"
