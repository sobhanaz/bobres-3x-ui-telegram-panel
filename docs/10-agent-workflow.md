# 10 - Multi-agent workflow and model tiers (how we build this cheaply)

Built-in agents ship pointing at models that do not exist on this gateway (claude-sonnet-4.5,
claude-haiku-4.5) -> `404 model does not exist`. Fix: per-agent overrides in
`~/.bynara/agent/agent-models.json` (survives updates). Restart the CLI after editing (agent list is read at startup).

## Model tiers
| Tier | Model | Agents | Use for |
|---|---|---|---|
| Free, fast | nemotron-3.5-lightning-free | nara-explore | code/doc navigation |
| Free | mimo-v2.6-flash-free | nara-search | research |
| Free, big context | nemotron-3-ultra-free | nara-plan, nara-build, nara-debug | boilerplate, CRUD, tests, docs, plans |
| Free | mimo-v2.5-free | nara-fe, nara-droid | Vue UI, simple UI code |
| Paid (credit) | claude-sonnet-5.5 | nara-architect, nara-review, nara-release | design, security, money logic review, release gates |
Main session model stays the one the owner picks with /model.

## Rules
1. Money, auth, license, crypto, migrations: free model may DRAFT, but a paid-tier review is mandatory before merge.
2. Everything else: free model builds, tests must pass, then one review pass (paid) per phase, not per file.
3. Free-model output is untrusted until tests/linters pass. Never accept "done" without command output.
4. Parallel fan-out for independent units (e.g. 4 services scaffolds, docs, tests) via delegate `tasks`.
5. Chain for dependent work: explore -> plan -> build -> review.
6. Keep prompts atomic: file paths, acceptance test, out-of-scope list.

## Per-step loop
plan (free) -> build in parallel (free, isolated worktrees when files could collide) -> tests/lint locally
-> review (paid, diff only) -> fix -> commit small -> push.

## Known limits
- Free models can be slower, less reliable on long tool chains, and may rate-limit. Retry once, then escalate one tier.
- Quota/credit visible with `naracli status`; check before big fan-outs.
