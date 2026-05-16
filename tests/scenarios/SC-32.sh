#!/usr/bin/env bash
# SC-32: bench eval import → 30 YAML tasks land with correct category mix
# Catches: bench engine P2.2 (YAML round-trip)
source "$(dirname "$0")/../lib/common.sh"
need jq
mk_tmp; init_project bench32

# Build a tiny YAML test directory
mkdir -p ./tasks
cat > ./tasks/E1-test.yaml <<'YAML'
id: E1-test
category: rule-trigger
prompt: "test prompt"
grader:
  kind: programmatic
  cmd: 'exit 0'
expected_pass_with: 0.9
expected_pass_baseline: 0.2
YAML
cat > ./tasks/E2-test.yaml <<'YAML'
id: E2-test
category: hotfix-avoid
prompt: "another"
grader:
  kind: programmatic
  cmd: 'exit 0'
YAML

$LORE bench eval import --from=./tasks >/dev/null || fail "import"
count=$($LORE bench eval list --json | jq '.count // (.data | length)')
[ "$count" = "2" ] || fail "expected 2 evals, got $count"

# round-trip: export → re-import → still 2
$LORE bench eval export --to=./out >/dev/null || fail "export"
[ -f ./out/E1-test.yaml ] || fail "export missed E1-test.yaml"
[ -f ./out/E2-test.yaml ] || fail "export missed E2-test.yaml"

pass SC-32
