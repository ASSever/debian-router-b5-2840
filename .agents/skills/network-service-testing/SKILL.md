---
name: network-service-testing
description: Test Debian Router B5-2840 proxy, profile, monitoring, or failover behavior with controlled traffic and service faults.
---

# Prove the service behavior

Use the active roadmap criterion to choose scenarios. Use an isolated client or fixture and controlled destinations. Capture the initial profile and the expected path of test traffic. Main-agent B5 operations follow `verification-before-completion` and the existing network procedures.

- **Import and server checks:** Use synthetic subscriptions and test credentials. Cover supported, malformed and unsupported records. Verify secrets are absent from errors, API views and journals. For a server probe, show that the selected server actually carried the request and retain the safe failure reason if the probe fails.
- **Config and profiles:** Stage and validate the candidate. Show the selected profile on representative traffic, include an excluded or otherwise unaffected flow when the criterion calls for it, and check that a rejected or failed apply preserves the last working configuration.
- **Monitoring and recovery:** Establish a healthy baseline. Inject one failure at a time and distinguish name resolution from transport. Observe the independent service record the resulting event. For failover and recovery, check expected state transitions, the actual path of client traffic, the unaffected flows, and behavior after recovery or a repeat failure as applicable.

Record the fixture, observation point, active service/profile and outcome. Clean up temporary state. Use `network-diagnose` if an observed result differs from the expected route or service state.
