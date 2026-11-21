---
name: systemd-unit
description: Design and verify a new or changed systemd service, timer, or installer-generated unit for Debian Router B5-2840.
---

# Verify the installed unit

1. Find how the unit and its drop-ins are generated and installed. Existing API, agent and PPPoE units in `install.sh` show the current installer and rollback layout. Choose the service user, groups, writable directories, capabilities and ordering from this daemon's actual needs. Give the daemon and its recovery service separate lifecycles where required.
2. On Debian 13, verify the staged unit with `systemd-analyze verify` and make its executable and required dependencies available in the test environment. Validate the daemon's exact candidate config before activation. Keep credentials out of arguments, installer output and journals. Verify required file access under the effective service restrictions.
3. Exercise start and restart under systemd with the intended unit restrictions. Compare the loaded unit and effective properties to the staged files, and verify daemon-specific health and state persistence. Include the invalid-config or start-failure path if installation or recovery behavior changes. For a timer, verify a real trigger and confirm cancellation prevents it from running when that behavior matters.
4. For live rollout and evidence, follow `verification-before-completion`. A change that can affect SSH or routing follows the project's review and rollback procedure.
