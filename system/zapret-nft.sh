#!/bin/sh
set -eu
export LC_ALL=C

case "$#:${1-}" in
    1:stop) exec /usr/sbin/nft destroy table inet b5_zapret ;;
    3:start) ;;
    *) exit 2 ;;
esac

lan=$2
wan=$3
for interface in "$lan" "$wan"; do
    case "$interface" in
        ''|*[!a-zA-Z0-9_.-]*) echo 'Invalid Zapret interface.' >&2; exit 2 ;;
    esac
    [ "${#interface}" -le 15 ] || exit 2
done
[ "$lan" != "$wan" ] || exit 2

# Wait for nfqws to own the queue before making any traffic pass through it.
attempt=0
while ! grep -Eq '^[[:space:]]*62232[[:space:]]' /proc/net/netfilter/nfnetlink_queue 2>/dev/null; do
    attempt=$((attempt+1))
    if [ "$attempt" -ge 40 ]; then
        echo 'Zapret queue did not become ready.' >&2
        exit 1
    fi
    sleep 0.05
done

# Flag only forwarded client traffic. Queue after SNAT so the injected packets use the WAN source.
# This service owns mark bit 0x02000000 and clears it before handing packets to nfqws.
exec /usr/sbin/nft -f - <<EOF
destroy table inet b5_zapret
table inet b5_zapret {
    chain generated_output {
        type filter hook output priority raw; policy accept;
        # nfqws generates these from packets that already passed SNAT.
        oifname "$wan" meta mark & 0x40000000 != 0 counter notrack
    }
    chain mark_forwarded {
        type filter hook forward priority 10; policy accept;
        iifname "$lan" oifname "$wan" ct direction original ct original packets 1-12 tcp dport {80,443,2053,2083,2087,2096,8443} meta mark set meta mark | 0x02000000
        iifname "$lan" oifname "$wan" ct direction original ct original packets 1-12 udp dport {443,19294-19344,50000-50100} meta mark set meta mark | 0x02000000
    }
    chain process_forwarded {
        type filter hook postrouting priority srcnat + 1; policy accept;
        oifname "$wan" meta mark & 0x02000000 != 0 meta mark set meta mark & 0xfdffffff counter queue num 62232 bypass
    }
}
EOF
