# Static WAN IPv4

The **Static IPv4 settings** panel manages the fixed B5 WAN interface `enp1s0`. It accepts one IPv4 address with a prefix, a gateway in that subnet, and one to four distinct IPv4 DNS servers. When a DHCP lease is available, the form starts with its current address, gateway, and DNS values so the operator can review or adjust them.

Applying settings begins a 120-second transaction. The privileged agent saves the exact current `/etc/systemd/network/10-wan.network` file and its permissions, arms a system-manager rollback timer, writes the static profile atomically, then reloads and reconfigures only `enp1s0`. The API rejects an invalid address, gateway, or DNS list before contacting the agent. A gateway must be a usable host address in the WAN subnet.

The page offers **Confirm settings** and **Restore previous settings** while the timer is active. Confirmation succeeds only when systemd-networkd reports the requested address, gateway, DNS, and active link. If the operator does not confirm within 120 seconds, the timer restores the saved file and reconfigures `enp1s0`. The agent also restores a pending transaction after a reboot or restart; its service starts after `systemd-networkd`.

Static WAN routes use metric 5000 so a lower-metric management route can remain preferred during the confirmation window. This step changes only `enp1s0`; it does not configure or change the LAN.

The planned LAN defaults remain `192.168.10.1/24` with a DHCP pool of `192.168.10.100–192.168.10.200`. They are initial values, not fixed rules. Step 21 will let the operator change the router address, prefix, and pool, including to `192.168.1.0/24` or another valid subnet, while checking overlap with WAN and management networks.

## Acceptance checks

- An authorized administrator can sign in and apply a valid static profile.
- A wrong-subnet gateway is rejected before any privileged operation or file change.
- The static address, gateway, and DNS are read back from live networkd state before confirmation is allowed.
- Manual rollback and timer rollback restore the previous file and mode; an interrupted transaction is recovered after agent restart.
- On B5, a temporary application can be observed through the independent Wi-Fi management path and the timer restores the original DHCP configuration after SSH disconnects.
