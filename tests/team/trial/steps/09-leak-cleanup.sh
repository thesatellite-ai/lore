# Step 09 (sourced by run.sh): a wrong --to repo task reaches the client repo on
# purpose; purge and compact remove it everywhere. The main client clone does not
# fetch until the cleanup is done; an observer clone plays the client meanwhile.

begin_step "09 leak cleanup, compaction"
HOME=$T/homes/client retry git clone -q "$CLIENT_URL" "$T/work/client/observer" >> "$LOG" 2>&1
ok dev1 dev1/app "lore task add 'INTERNAL-MARKER-13 chase the client about the unpaid March invoice' --to repo"
X=$(grep -o -E 'tsk_[0-9a-f]+' <<< "$LAST_OUT" | head -n 1)
ok client client/observer "lore sync >/dev/null 2>&1; lore task list"
has "INTERNAL-MARKER-13"
ok dev1 dev1/app "lore live purge $X"
has "purged $X"
ok dev1 dev1/app "lore live compact"
has "compacted refs/lore/live"
ok client client/observer "lore sync >/dev/null 2>&1; lore task list"
lacks "INTERNAL-MARKER-13"
ok dev2 dev2/app "lore sync >/dev/null 2>&1; lore task list"
lacks "INTERNAL-MARKER-13"
rm -rf "$T/mirror.git"
HOME=$T/homes/client retry git clone -q --mirror "$CLIENT_URL" "$T/mirror.git" >> "$LOG" 2>&1
found=$(cd "$T/mirror.git" && for c in $(git rev-list --all); do git grep -l -E "$LEAK_RE" "$c" 2>/dev/null; done | head -n 3)
check "a fresh mirror of the client repo holds no internal marker in any commit" "$([ -z "$found" ] && echo 0 || echo 1)"
ok dev2 dev2/app "lore live compact --dry-run"
end_step
