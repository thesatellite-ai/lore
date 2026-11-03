# Step 04 (sourced by run.sh): landing of a repo task, an internal task done in the
# store, and main changing only through git merges of the two feature branches.

begin_step "04 landing, merge-only main"
TID=$(row_id dev2 dev2/app "lore task list" "fix checkout totals for INR" tsk)
IID=$(row_id dev2 dev2/app "lore task list" "INTERNAL-MARKER-2" tsk)
check "found the repo task ($TID) and the internal task ($IID)" "$([ -n "$TID" ] && [ -n "$IID" ] && echo 0 || echo 1)"
ok dev2 dev2/app "lore task start $TID && echo 'export const inr = 1' > checkout.js && git add checkout.js && git commit -qm 'fix INR totals' && lore task done $TID --commit \$(git rev-parse --short HEAD)"
has "waiting for merge"
ok dev2 dev2/app "lore task done $IID"
ok dev1 dev1/app "echo 'export const cart = []' > cart.js && git add cart.js .lore/data && git commit -qm 'cart + rule' && retry git push -q -u origin feat/checkout"
ok dev2 dev2/app "retry git push -q -u origin feat/login"
ok dev1 dev1/app "git switch -q main && retry git pull -q && git merge -q --no-ff --no-edit origin/feat/checkout && git merge -q --no-ff --no-edit origin/feat/login && retry git push -q origin main"
ok dev1 dev1/app "lore sync 2>&1 | tail -n 3; lore task list"
has "$TID merged"
for who in dev1 dev2; do
  ok "$who" "$who/app" "lore sync >/dev/null 2>&1; git status --porcelain"
  check "$who's checkout is clean after the merge" "$([ -z "$LAST_OUT" ] && echo 0 || echo 1)"
done
ok client client/app "retry git pull -q --ff-only && lore sync >/dev/null 2>&1; lore rule list"
has "run the tests before you push"
end_step
