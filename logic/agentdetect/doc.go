// Package agentdetect answers "which coding agent ran this lore command?"
// (layer L1, pure logic: no I/O of its own; the environment, the process
// ancestors and the version-metadata file reader come in as inputs).
//
// Decisions: R11 B+ (detectors are built into lore's code, no config file,
// no env var adds one), PQ1 (plugins may ADD detectors, never change or
// remove a built-in), OQ11 (a weak signal without a TTY counts, flag
// weak_only), OQ12 (version from env, install path, package metadata;
// the tool is never run), OQ13 (CI provider), OQ15 (bare AGENT=1 /
// unknown AI_AGENT = unknown_agent), OQ16 (a pinned test per tool).
// LORE_ACTOR and LORE_HUMAN are NOT read here: the identity adapter
// applies them (AGENT_IDENTITY_DESIGN section 3 steps 1-2), so a decision
// never comes from a LORE_* variable.
//
// Flow of Detect (AGENT_IDENTITY_DESIGN section 3, steps 3-5):
//
//	Detect(in)
//	  ├─ validate in.Extra (no built-in slug, no token-like name)  (PQ1)
//	  ├─ strong env signals, table order (qwen before gemini)     step 3.1
//	  │    └─ two tools at once → nearest ancestor wins, other → Outer
//	  ├─ AI_AGENT / AGENT convention values                        step 3.2
//	  ├─ weak signals, only with no TTY → flag weak_only           step 3.3
//	  ├─ extra (plugin) detectors, strong only                     step 3.4
//	  ├─ ancestor walk (≤ 8) by process / script name              step 4
//	  ├─ provenance: session, version (env → path → metadata), model
//	  └─ CI provider (recorded always; decides only with no tool)  step 5
package agentdetect
