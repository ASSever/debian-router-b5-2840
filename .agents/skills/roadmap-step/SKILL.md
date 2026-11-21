---
name: roadmap-step
description: Select or resume the next Debian Router B5-2840 roadmap increment, with its acceptance criterion and B5 verification status.
---

# Select a roadmap step

1. Check the worktree and compare the local revision with GitHub's main. Resolve which revision represents the current project before using its roadmap. If main has advanced, inspect the updated roadmap at the fetched revision or in an up-to-date checkout.
2. From the repository root run `python -X utf8 .agents/skills/roadmap-step/scripts/next_step.py`. To read a fetched roadmap without switching branches, pass its contents on standard input and use `-` as the path argument. If the script reports invalid rows or Python is unavailable, inspect the relevant roadmap rows directly.
3. Read only the requirements, documentation and private log entries relevant to that step. A `b5-pending` result directs attention to the outstanding release or device verification. Check the recorded release and evidence before deciding what remains.

Use `docs/roadmap.md` as the source of step statuses. Update the row to `b5-pending` when implementation lands in main and to `verified` after recorded B5 acceptance. See the roadmap for status meanings. The script reads Markdown only.
