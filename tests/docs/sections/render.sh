# render: write your notes into the files coding tools read when they start
# (docs/knowledge/render-and-agent-files.md).

# TOOL NEUTRAL: the repo already has a CLAUDE.md before lore arrives, so
# init detects claude_code and wires it (with no tool file it would wire
# AGENTS.md, never CLAUDE.md by default).
solo_repo alice client/website
W=work/website
quiet alice $W "printf '# Website\n\nHand-written notes stay here.\n' > CLAUDE.md && git add CLAUDE.md && git commit -qm 'add CLAUDE.md'"

note "init"
on alice $W "lore init --non-interactive --name=website"
quiet alice $W "git add -A && git commit -qm 'lore: init' && git push -q origin main"
quiet alice $W "lore rule add --body 'Run the tests before you push' && lore rule add --body 'Keep pull requests small' --severity should && lore hotfix add --title 'Checkout button is broken on Safari' --body 'Use the old button until the fix ships.' --severity critical && lore memory add --body 'The client replies faster on WhatsApp than email'"

note "render"
on alice $W "lore render"
on alice $W "cat CLAUDE.md"
on alice $W "grep -n -A3 'Run the tests\|Safari' .lore/LORE.md"

note "render again"
on alice $W "lore render && cat CLAUDE.md"

note "dry run"
on alice $W "lore render --dry-run | head -12"

note "agents md"
on alice $W "lore render --target AGENTS.md"
on alice $W "cat AGENTS.md"

note "out"
on alice $W "lore render --out docs/agent-notes.md --target AGENTS.md"
on alice $W "cat AGENTS.md; ls docs"

note "no pointer"
on alice $W "lore render --no-pointer"

note "directive"
on alice $W "lore directive show | head -8"
on alice $W "lore directive install --target AGENTS.md"
on alice $W "head -3 AGENTS.md"

note "why context"
on alice $W "lore why-context"
on alice $W "lore why-context --rendered | head -8"

note "render trend"
on alice $W "lore render --trend"

note "commit"
on alice $W "git status --short"

note "pin"
on alice $W "lore rule add --body 'Every price change needs the client written OK' --severity must"
local rp; rp=$(id rul)
on alice $W "lore pin set $rp --context line"
on alice $W "lore render >/dev/null && grep -n 'price change' .lore/LORE.md"
on alice $W "lore pin set $rp --context full"
on alice $W "lore render >/dev/null && grep -n -B1 -A2 'price change' .lore/LORE.md"
on alice $W "lore pin unset $rp"

note "scope"
quiet alice $W "mkdir -p src/checkout && echo 'export {}' > src/checkout/pay.ts"
on alice $W "lore rule add --body 'Checkout code never calls the payment API directly' --severity must --scope dir:src/checkout"
on alice $W "lore scope list"
on alice $W "lore scope show src/checkout/pay.ts"
on alice $W "lore render"

note "scope move"
quiet alice $W "git add -A && git commit -qm wip && git mv src/checkout src/payments"
on alice $W "lore scope move src/checkout src/payments"
on alice $W "lore scope list"

# --- the lore skill in each tool's skill folder (docs/knowledge/skill-install.md) ---
note "skill status before"
on alice $W "lore skill status"

note "skill install dry run"
on alice $W "lore skill install --all --dry-run"

note "skill install"
on alice $W "lore skill install --tool claude_code"
on alice $W "ls ~/.claude/skills/lore | head -5; grep -n '^lore_version' ~/.claude/skills/lore/SKILL.md"
on alice $W "lore skill install --tool claude_code --json"

note "skill status after"
on alice $W "lore skill status"

note "skill edited"
quiet alice $W "printf '\nAlways answer in English.\n' >> ~/.claude/skills/lore/SKILL.md"
on alice $W "lore skill status --tool claude_code"
on alice $W "lore skill install --tool claude_code"
