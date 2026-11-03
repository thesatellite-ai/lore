# Step 05 (sourced by run.sh): main protected. A real host's branch protection needs
# admin API access, so this step uses a local bare repo whose pre-receive hook answers
# like GitHub (GH006) for refs/heads/main; the ride-along landing is what is checked.

begin_step "05 protected main (local sim)"
P="$T/protected"
mkdir -p "$P" "$T/work/dev1/p" "$T/work/dev2/p"
git init -q --bare -b main "$P/billing.git"
HOME=$T/homes/dev1 git clone -q "$P/billing.git" "$T/work/dev1/p/billing" 2>> "$LOG"
ok dev1 dev1/p/billing "git commit -q --allow-empty -m init && git push -q origin main && lore init --non-interactive --name=billing >/dev/null && git add -A && git commit -qm 'lore: init' && git push -q origin main && lore actor register"
cat > "$P/billing.git/hooks/pre-receive" <<'H'
#!/bin/sh
while read -r old new ref; do
  if [ "$ref" = refs/heads/main ] && [ ! -f "$GIT_DIR/allow-main" ]; then
    echo "error: GH006: Protected branch update failed for refs/heads/main."; exit 1
  fi
done
H
chmod +x "$P/billing.git/hooks/pre-receive"
HOME=$T/homes/dev2 git clone -q "$P/billing.git" "$T/work/dev2/p/billing" 2>> "$LOG"
ok dev2 dev2/p/billing "lore sync >/dev/null 2>&1; lore actor register"
has "main is protected"
ok dev2 dev2/p/billing "git switch -q -c feat/payments && lore task add 'ask the client for the new logo' --assign @dev1"
PTID=$(grep -o -E 'tsk_[0-9a-f]+' <<< "$LAST_OUT" | head -n 1)
ok dev1 dev1/p/billing "git switch -q -c feat/login && lore sync >/dev/null 2>&1; lore task done $PTID && lore explain $PTID"
has "main is protected"
ok dev1 dev1/p/billing "echo 'login ok' > login.txt && git add login.txt && git commit -qm 'fix login timeout' && git push -q -u origin feat/login && git log --oneline -3"
has "lore: land [0-9]+ rows?"
# the pull request merge: only this path may update main
touch "$P/billing.git/allow-main"
ok dev1 dev1/p/billing "git switch -q main && git pull -q && git merge -q --no-ff --no-edit origin/feat/login && git push -q origin main"
rm -f "$P/billing.git/allow-main"
ok dev1 dev1/p/billing "lore sync >/dev/null 2>&1; lore live show; git status --porcelain"
has "^0 live rows"
end_step
