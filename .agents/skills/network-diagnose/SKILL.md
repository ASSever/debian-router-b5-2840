---
name: network-diagnose
description: Collect a compact, read-only diagnostic snapshot for a Debian Router B5-2840 network symptom or a question about the SSH management path.
---

# Diagnose from targeted evidence

The main agent collects live evidence on B5. Use the local setup log for the expected configuration and live observations for the current state. Pick the smallest set of probes that answers the question.

| Symptom | Evidence to collect |
| --- | --- |
| WAN link or address missing | `ip -br link show dev <iface>`, `ip -br addr show dev <iface>` and `networkctl status <iface> --no-pager`. For PPPoE distinguish the underlying Ethernet link from the PPP interface and inspect the PPPoE unit if needed. |
| Routing or SSH path in doubt | Check the route to the SSH peer from the current session's local address with `ip route get <peer> from <local>`. For LAN forwarding, check policy rules and the route for representative traffic arriving on the LAN interface. A route lookup for traffic originating on B5 answers a different question. |
| Address present but connection fails | From the affected client or equivalent fixture, check the selected gateway or route, then DNS resolution and a connection to the intended destination. Record where the results first diverge from expectations. |
| Network service or rollback timer in doubt | Use `systemctl is-active` or `systemctl show` for the specific unit. If needed, use `journalctl -u <unit> -n 30 --no-pager` for a bounded excerpt. |

Return the observed state, command or probe, traffic vantage point, and remaining unknown. Redact credentials and private device details before sharing evidence. If a cross-component problem or a change affecting SSH warrants another opinion, give `debian_network` the specific question and sanitized observations under `AGENTS.md`. Apply and rollback operations follow the existing procedure for the affected component.
