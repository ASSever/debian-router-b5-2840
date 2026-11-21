---
name: verification-before-completion
description: Verify Debian Router B5-2840 changes before reporting them complete, publishing a GitHub revision, or recording acceptance on B5.
---

# Verify the result

## Before publishing

- Inspect the full public diff against the PR base, including new files. Run `git diff --check` against that diff and check for credentials, private B5 reports, and accidental artifacts. Use a local redacting secret scanner if one is available.
- Match checks to the changed behavior. Use the commands in `.github/workflows/api.yml` for Go and frontend, and `.github/workflows/bootstrap.yml` for shell and Debian bootstrap. For a skill or documentation change, verify its instructions, links and any scripts or examples that changed.
- Check GitHub Actions for the revision being published. After merge or push, compare the expected commit with the remote revision. Report the relevant check results and any remaining verification.

## When the increment includes a B5 rollout

- For a release, match the tag and artifact to the intended revision and verify the checksum. Install through the two-command README entry point, pinning the selected version when latest is unsuitable. `--check` reports compatibility. For an installer change, also exercise the affected install, upgrade, or repeat-run path.
- Follow the project rules for independent review before consequential network changes. Use the relevant procedure in `docs/network-*.md` to verify the management path and rollback before confirming the change.
- Verify the installed version, relevant services, and each acceptance criterion for the step. Record the evidence and remaining gaps in `MINIPC_SETUP_LOG.txt` and update the public roadmap with sanitized results. If acceptance is outstanding, retain `b5-pending` and state what is still due.
