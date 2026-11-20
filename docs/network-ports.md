# Network port selection

`GET /api/network/interfaces` returns a read-only snapshot of Linux interface metadata from `/sys/class/net`. The response contains each non-loopback interface name, operational state, carrier state, and negotiated speed in Mbps. A `null` carrier means the kernel did not expose a definite value; a `null` speed means no speed is currently available.

Example:

```json
{
  "interfaces": [
    {
      "name": "enp1s0",
      "operational_state": "up",
      "carrier": true,
      "speed_mbps": 1000
    },
    {
      "name": "enp2s0",
      "operational_state": "down",
      "carrier": false,
      "speed_mbps": null
    }
  ]
}
```

The Network Setup panel shows the live inventory and lets the operator confirm WAN and LAN roles only when both selections name different interfaces. This confirmation stays in browser memory. It does not save or apply a network configuration. Separate WAN panels show DHCP status and manage a temporary Static IPv4 profile on `enp1s0`; persistent port-role storage, LAN, DHCP server, DNS, and firewall configuration remain later roadmap work.
