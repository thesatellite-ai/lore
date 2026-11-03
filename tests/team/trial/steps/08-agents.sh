# Step 08 (sourced by run.sh): agents on one mission: claims, a finding, done → merged,
# one agent killed mid-claim, its lease expires, the other agent reclaims, a human
# gate, and the last task done. lease.ttl is 90s in the agency config, so this step
# waits LEASE_WAIT seconds of real time.

begin_step "08a agents: plan, claims, merge"
cat > "$T/work/dev1/plan-trial.yaml" <<Y
mission: {key: trial, title: "INTERNAL-MARKER-6 trial mission", to: agency/client}
approvers: ["@dev1"]
tasks:
  - {key: backend,  title: "INTERNAL-MARKER-7 backend api", repo: $CLIENT_NAME, timebox: 60m}
  - {key: frontend, title: "INTERNAL-MARKER-8 frontend page", repo: $CLIENT_NAME, after: [backend], timebox: 60m}
  - {key: deploy,   title: "INTERNAL-MARKER-9 deploy to staging", role: coordinator, after: [frontend], gate: required}
Y
for a in agent-a agent-b; do
  ok dev1 dev1/app "lore agent start --label $a --owner @dev1"
  grep -o 'LORE_SESSION=[^ ]*' <<< "$LAST_OUT" | head -n 1 | cut -d= -f2 > "$T/homes/dev1/.sess-$a"
  check "agent session for $a recorded" "$([ -s "$T/homes/dev1/.sess-$a" ] && echo 0 || echo 1)"
  ok "$a" "$a/app" "retry git pull -q; lore sync >/dev/null 2>&1; lore whoami"
  has "agent:$a"
done
ok dev1 dev1/app "lore plan apply ../plan-trial.yaml"
has "\\(created\\)"
ok dev1 dev1/app "lore plan apply ../plan-trial.yaml"
has "\\(found\\)"
M=$(grep -o -E 'msn_[0-9a-f]+' <<< "$LAST_OUT" | head -n 1)
B=$(grep 'MARKER-7' <<< "$LAST_OUT" | grep -o -E 'tsk_[0-9a-f]+' | head -n 1)
F=$(grep 'MARKER-8' <<< "$LAST_OUT" | grep -o -E 'tsk_[0-9a-f]+' | head -n 1)
D=$(grep 'MARKER-9' <<< "$LAST_OUT" | grep -o -E 'tsk_[0-9a-f]+' | head -n 1)
check "mission $M with tasks $B $F $D" "$([ -n "$M" ] && [ -n "$B" ] && [ -n "$F" ] && [ -n "$D" ] && echo 0 || echo 1)"
ok agent-a agent-a/app "lore task claim $M"
ok agent-a agent-a/app "lore task next --mission $M --repo $CLIENT_NAME"
has "claimed $B"
refused 10 agent-b agent-b/app "lore task next --mission $M --repo $CLIENT_NAME"
has "E_NO_WORK"
ok agent-a agent-a/app "lore finding add --mission $M 'INTERNAL-MARKER-10 invite links must use the public website address not the server name'"
ok agent-a agent-a/app "lore task note $B 'INTERNAL-MARKER-11 api returns the link; email text in invite.html'"
ok agent-a agent-a/app "git switch -q -c agent-a/backend && echo 'export const api = 1' > api.js && git add api.js && git commit -qm 'backend api' && retry git push -q -u origin agent-a/backend && lore task done $B --commit \$(git rev-parse HEAD) --lesson 'INTERNAL-MARKER-12 invite links stop working after 48 hours per the readme'"
has "waiting for merge"
ok agent-b agent-b/app "lore task next --mission $M --repo $CLIENT_NAME"
has "claimed $F"
ok dev1 dev1/app "git switch -q main && retry git pull -q && git merge -q --no-ff --no-edit origin/agent-a/backend && retry git push -q origin main; lore task list --mission $M"
has "$B merged"
ok agent-b agent-b/app "git switch -q -c agent-b/frontend && echo 'export {}' > page.js && git add page.js && git commit -qm 'frontend page part 1' && lore task note $F 'route done, form next'"
has "work branch agent-b/frontend pushed"
# agent-b keeps its lease alive with the heartbeat daemon, then dies without releasing it.
(cd "$T/work/agent-b/app" && exec env HOME="$T/homes/dev1" LORE_ACTOR=agent:agent-b LORE_SESSION="$(cat "$T/homes/dev1/.sess-agent-b")" lore task heartbeat --daemon "$F") >> "$T/logs/heartbeat-agent-b.log" 2>&1 &
HB_PID=$!
sleep 5
check "agent-b's heartbeat daemon is running (pid $HB_PID)" "$(kill -0 "$HB_PID" 2>/dev/null && echo 0 || echo 1)"
ok dev1 dev1/app "lore mission status $M"
has "claimed +$F"
kill -9 "$HB_PID" 2>/dev/null
wait "$HB_PID" 2>/dev/null
HB_PID=""
echo "killed agent-b's heartbeat daemon at $(date +%T); waiting ${LEASE_WAIT}s for its lease to expire" | tee -a "$LOG" >&2
end_step
sleep "$LEASE_WAIT"

begin_step "08b agents: reclaim, gate, done"
ok dev1 dev1/app "lore inbox"
has "EXPIRED .*$F"
ok agent-a agent-a/app "lore task next --mission $M --repo $CLIENT_NAME"
has "reclaim"
has "work branch agent-b/frontend"
ok agent-a agent-a/app "retry git fetch -q origin agent-b/frontend && git switch -q -c agent-a/frontend FETCH_HEAD"
refused 12 agent-b agent-b/app "lore task done $F"
has "E_LOST_CLAIM"
ok agent-a agent-a/app "echo form >> page.js && git commit -qam 'frontend form' && retry git push -q -u origin agent-a/frontend && lore task done $F --commit \$(git rev-parse HEAD)"
refused 10 agent-a agent-a/app "lore task next --mission $M --role coordinator"
has "to be merged"
ok dev1 dev1/app "git switch -q main && retry git pull -q && git merge -q --no-ff --no-edit origin/agent-a/frontend && retry git push -q origin main; lore sync >/dev/null 2>&1; lore task list --mission $M"
has "$F merged"
refused 10 agent-a agent-a/app "lore task next --mission $M --role coordinator"
has "gate needs approval"
refused any agent-a agent-a/app "lore gate approve $D"
ok dev1 dev1/app "lore gate approve $D"
ok agent-a agent-a/app "lore task next --mission $M --role coordinator"
has "claimed $D"
ok agent-a agent-a/app "lore task done $D"
ok dev1 dev1/app "lore mission status $M"
has "3 done"
end_step
