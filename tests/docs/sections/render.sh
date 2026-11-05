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
