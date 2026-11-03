# Step 06 (sourced by run.sh): deliberate leak attempts in the client repo; every one must be blocked.

begin_step "06 leak attempts"
TXT='they approve estimates faster when we send them on WhatsApp first'
ok dev1 dev1/app "retry git fetch -q origin && git switch -q -c feat/leak origin/main"
refused any dev1 dev1/app "mkdir -p src && echo '// INTERNAL-MARKER-1 $TXT' > src/notes.ts && git add src/notes.ts && git commit -qm 'add notes'"
has "E_LEAK_BLOCKED"
refused any dev1 dev1/app "git reset -q && echo '// $TXT' > src/notes.ts && git add src/notes.ts && git commit -qm 'add notes (text only)'"
has "E_LEAK_BLOCKED"
ok dev1 dev1/app "git commit --no-verify -qm 'add notes, hooks skipped'"
refused any dev1 dev1/app "git push -u origin feat/leak"
has "E_LEAK_BLOCKED"
left=$(retry git ls-remote "$CLIENT_URL" refs/heads/feat/leak)
check "feat/leak never reached the client remote" "$([ -z "$left" ] && echo 0 || echo 1)"
if [ -n "$left" ]; then retry git push -q "$CLIENT_URL" --delete feat/leak >> "$LOG" 2>&1; fi
printf 'Fixes checkout.\n\nThey approve estimates faster when we send them on WhatsApp first.\n' > "$T/work/pr-body.md"
refused 19 dev1 dev1/app "lore check pr-body '$T/work/pr-body.md'"
ok dev1 dev1/app "printf 'Fixes checkout totals for INR.\\n' | lore check pr-body -"
refused 19 dev1 dev1/app "lore task add '$TXT' --to repo"
has "Nothing written"
refused 19 dev1 dev1/app "lore rule add --body 'INTERNAL-MARKER-4 $TXT' --to repo"
ok dev1 dev1/app "git status --porcelain .lore/data"
check "the refused rule left no file behind" "$([ -z "$LAST_OUT" ] && echo 0 || echo 1)"
ok dev1 dev1/app "git switch -q main && git branch -D feat/leak"
end_step
