# Debian Router B5-2840

## Project context

- Read the relevant sections of `DEBIAN_ROUTER_B5-2840_CONTEXT.md` for project requirements and `MINIPC_SETUP_LOG.txt` for the device's recorded state. Use targeted reads when the task has a narrow scope.
- Keep development work on Windows separate from operations on the Debian mini PC. The main agent owns changes on the mini PC and records applied settings, installed packages, verification results, and unresolved items in `MINIPC_SETUP_LOG.txt`. Keep credentials out of the repository, logs, and delegated context.

- Deliver working increments to the existing GitHub repository `ASSever/debian-router-b5-2840`. Check the public diff, publish without rewriting remote history and verify the remote revision. Keep the two-command Debian entry point documented and tested. Use `docs/roadmap.md` to select the next small increment.
- Use the repository's `roadmap-step` skill when choosing or resuming a roadmap increment.

## Finding code relationships

- Open a known file directly and use `rg` for exact text, filenames, or an obvious symbol lookup.
- The main agent calls `codegraph_explore` only when it needs to trace callers, implementations, dependencies, or a flow across files. Ask one focused question with a concrete symbol or path. Never spawn a subagent just to query the graph.
- The index started empty. If the first relationship query still reports no indexed code after source files have been added, rebuild it once with `.\.codegraph-tools\1.6.3\codegraph-win32-x64\bin\codegraph.cmd index` (set `CODEGRAPH_TELEMETRY=0` for that command). If the graph has no relevant result, use a targeted file read or `rg`.
- For live Debian configuration and service state, use the actual configuration and device evidence.

## When to delegate

- Use one agent by default. Handle small fixes, routine diagnostics, documentation edits, and straightforward system changes in the main session.
- The main agent handles implementation, B5 deployment, release verification, and the private setup log. Assign agents only independent, bounded analysis or review tasks; do not send them credentials or live-device operations.
- Use `debian_network` for a specific planning or diagnostic question when a change spans several network components or could cut off the current SSH connection, and a separate analysis can materially improve the plan. Skip it when an existing procedure already covers the change.
- Use `change_reviewer` for an independent review of a ready diff or concrete rollout plan before a consequential change to routing, firewall, DHCP/DNS, SSH, or recovery on the mini PC. Routine changes get ordinary review in the main session.
- Use `readability-maintainer` only for a focused, behavior-preserving cleanup when code clarity is a real problem. Its work stays in the Windows checkout.
- Run at most one subagent at a time for this project. If both are useful, finish the network analysis first and give the reviewer the resulting diff or rollout plan. Prefer one focused request over back-and-forth delegation.
- Include only the task, exact file paths or relevant excerpts, known device facts, and the requested output. Avoid forwarding the whole chat, requirements document, or logs. Ask for short findings with evidence. Keep deployment, SSH access, and the setup log with the main agent.

## Model choice and next task

- Prefer GPT-6.2 Sol at medium reasoning for the main agent and ordinary implementation. Use the normal speed tier when conserving credits. For a narrow, repeatable analysis or review, the configured Luna agents may be used; raise the model or effort when the task's risk or complexity warrants it. Do not add agents merely to parallelize a small task.
- In the final reply of every project chat, infer the most likely next concrete task from `docs/roadmap.md` and the verified current state. Add one short line in Russian naming that task and a recommended model and reasoning effort. Reassess the recommendation for each task; use Sol medium by default and Sol high or Astra high only for complex, high-risk investigation. Do not claim that a proposed future task has been completed.
