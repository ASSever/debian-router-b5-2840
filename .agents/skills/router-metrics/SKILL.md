---
name: router-metrics
description: Verify Debian Router B5-2840 traffic counters, stored measurement history, and dashboard service or resource status.
---

# Trace measurements to the dashboard

1. Identify the source of each changed value, its scope (interface, client, service or host), timestamp, and unit. For traffic counters, measure before and after a controlled transfer and compare deltas at the intended observation point. Include another client or idle interval if attribution is part of the claim.
2. Exercise counter resets, missing samples and stale data when the affected path handles them. Check that real zero, unavailable and stale values remain distinguishable in storage, API responses and charts. If persistence changes, use `sqlite-migration` for schema changes.
3. Verify that stored samples reach the API and dashboard in the correct time order and units. Compare a displayed service or resource status to the corresponding Debian observation. Report which results came from fixtures and which were measured on B5.
