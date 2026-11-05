# Step 07 (sourced by run.sh): give one internal note to the client on purpose
# with `lore move` (BATCH 2: a row has one home; `lore share` is removed).

begin_step "07 move one note to the client"
SHARE='SHARE-OK-MARKER-1 the staging site resets its database every night at two'
ok dev1 dev1/app "lore memory add --body '$SHARE'"
has "\\[agency/client\\]"
MID=$(grep -o -E 'mem_[0-9a-f]+' <<< "$LAST_OUT" | head -n 1)
ok client client/app "retry git fetch -q origin '+refs/*:refs/remotes/all/*'; git log --all -p | grep -c SHARE-OK-MARKER || true"
check "the client does not see the note before it is moved" "$([ "$LAST_OUT" = 0 ] && echo 0 || echo 1)"
refused any dev1 dev1/app "lore move $MID --to repo </dev/null"
has "confirm on a terminal"
ok dev1 dev1/app "lore move $MID --to repo --yes"
has "$MID moved to"
ok dev1 dev1/app "git switch -q -c chore/share-note && git add .lore/data && git commit -qm 'move the staging reset note' && retry git push -q -u origin chore/share-note && git switch -q main && retry git pull -q && git merge -q --no-ff --no-edit chore/share-note && retry git push -q origin main"
ok client client/app "retry git pull -q --ff-only; lore sync >/dev/null 2>&1; lore memory list"
has "SHARE-OK-MARKER-1"
end_step
