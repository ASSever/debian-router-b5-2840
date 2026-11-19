#!/bin/sh
# This entry point must also work when downloaded as a single file.
set -eu
export LC_ALL=C

usage() {
    printf '%s\n' 'Usage: sh install.sh [--check | --help]' \
        'Stage 01: inspect Debian 13 amd64. Prints a local report; requires no root access.'
}

case "$#:${1-}" in
    0:|1:--check) ;;
    1:--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
esac

if [ "$(uname -s)" != Linux ] || [ ! -r /etc/os-release ]; then
    echo 'Unsupported platform: Debian 13 amd64 required.' >&2
    exit 3
fi
. /etc/os-release
architecture=$(dpkg --print-architecture 2>/dev/null || echo unknown)
if [ "${ID-}/${VERSION_ID-}/$architecture" != debian/13/amd64 ]; then
    printf 'Unsupported platform: %s %s (%s). Debian 13 amd64 required.\n' \
        "${ID-unknown}" "${VERSION_ID-unknown}" "$architecture" >&2
    exit 3
fi

section() { printf '\n[%s]\n' "$1"; }
probe() {
    if ! command -v "$1" >/dev/null 2>&1; then
        printf 'Unavailable: %s is not installed\n' "$1"
    elif ! "$@"; then
        printf 'Unavailable: %s could not report this information\n' "$1"
    fi
}

section Platform
printf 'OS: %s\nArchitecture: %s\nKernel: %s\n' "$PRETTY_NAME" "$architecture" "$(uname -r)"
awk -F ': +' '/^model name[[:space:]]*:/ {print "CPU: " $2; exit}' /proc/cpuinfo
awk '/^MemTotal:/ {print "Memory:", $2, $3}' /proc/meminfo

section Storage
probe lsblk -o NAME,TYPE,SIZE,FSTYPE,MOUNTPOINTS
probe df -hPT -x tmpfs -x devtmpfs -x squashfs

section Interfaces
for port in /sys/class/net/*; do
    [ -d "$port" ] || continue
    if [ -d "$port/wireless" ] || [ -e "$port/phy80211" ]; then
        kind=wifi
    elif [ -e "$port/device" ] && [ "$(cat "$port/type")" = 1 ]; then
        kind=ethernet
    else
        kind=other
    fi
    carrier=$(cat "$port/carrier" 2>/dev/null) || carrier=unknown
    speed=$(cat "$port/speed" 2>/dev/null) || speed=unknown
    case "$speed" in ''|-*|0) speed=unknown ;; esac
    printf '%s type=%s mac=%s carrier=%s speed_Mbps=%s\n' \
        "${port##*/}" "$kind" "$(cat "$port/address")" "$carrier" "$speed"
done
probe ip -brief address
probe ip -4 route show
probe ip -6 route show

section 'SSH management path'
if [ -n "${SSH_CONNECTION-}" ]; then
    # OpenSSH supplies client IP/port and server IP/port in this order.
    read -r client_ip client_port server_ip server_port <<EOF
$SSH_CONNECTION
EOF
    printf 'Local SSH endpoint: %s port %s\n' "$server_ip" "$server_port"
    if command -v ip >/dev/null 2>&1; then
        ip -o address show | awk -v ip="$server_ip" \
            '{split($4, addr, "/"); if (addr[1] == ip) print "Local SSH interface:", $2}'
    fi
    probe ip route get "$client_ip" from "$server_ip"
else
    echo 'No OpenSSH session metadata.'
fi

section Services
probe systemctl list-units --all --type=service --no-pager --no-legend --plain \
    'ssh*.service' networking.service systemd-networkd.service NetworkManager.service \
    'wpa_supplicant*.service' iwd.service systemd-resolved.service systemd-timesyncd.service \
    nftables.service dnsmasq.service

section 'Package candidates (local APT cache)'
probe apt-cache policy iproute2 openssh-server nftables dnsmasq ppp sing-box

section Time
date -u '+UTC: %Y-%m-%dT%H:%M:%SZ'
probe timedatectl show -p Timezone -p NTP -p NTPSynchronized
printf '\n%s\n' 'Platform: Debian 13 amd64 supported. Stage 01 inventory finished.'
