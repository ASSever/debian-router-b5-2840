#!/bin/sh
# This entry point must also work when downloaded as a single file.
set -eu
export LC_ALL=C

usage() {
    printf '%s\n' 'Usage: sh install.sh [--check | --fetch-version VERSION | --help]' \
        'Check Debian 13 amd64, or download and verify a GitHub release package.'
}

case "$#:${1-}" in
    0:|1:--check) ;;
    1:--help) usage; exit 0 ;;
    2:--fetch-version) ;;
    *) usage >&2; exit 2 ;;
esac

if [ "${1-}" = --fetch-version ]; then
    release_version=$2
    if [ "$release_version" != latest ] && \
        ! printf '%s\n' "$release_version" | grep -Eq \
            '^[0-9]+\.[0-9]+\.[0-9]+-step[0-9]{2}-[0-9]{8}-[0-9]{4}-r[0-9]+$'; then
        echo 'Invalid release version.' >&2
        exit 2
    fi
fi

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

if [ "${1-}" = --fetch-version ]; then
    for tool in /usr/lib/apt/apt-helper sha256sum tar mktemp; do
        if [ ! -x "$tool" ] && ! command -v "$tool" >/dev/null 2>&1; then
            printf 'Required tool is missing: %s\n' "$tool" >&2
            exit 4
        fi
    done
    if [ "$release_version" = latest ]; then
        release_path=latest/download
    else
        release_path=download/v$release_version
    fi
    release_url=https://github.com/ASSever/debian-router-b5-2840/releases/$release_path
    archive=router-api-linux-amd64.tar.gz
    checksum=$archive.sha256
    stage=$(mktemp -d)
    trap 'rm -r -- "$stage"' EXIT HUP INT TERM
    /usr/lib/apt/apt-helper download-file "$release_url/$checksum" "$stage/$checksum"
    /usr/lib/apt/apt-helper download-file "$release_url/$archive" "$stage/$archive"
    if ! grep -Eq "^[0-9a-f]{64}  $archive\$" "$stage/$checksum" ||
        [ "$(wc -l < "$stage/$checksum")" -ne 1 ]; then
        echo 'Invalid release checksum file.' >&2
        exit 5
    fi
    (cd "$stage" && sha256sum -c "$checksum") || {
        echo 'Release checksum mismatch.' >&2
        exit 5
    }
    tar -tzf "$stage/$archive" >/dev/null || {
        echo 'Invalid release archive.' >&2
        exit 5
    }
    package_version=$(tar -xOzf "$stage/$archive" --wildcards 'debian-router-b5-2840-*/VERSION') || {
        echo 'Release version is missing from archive.' >&2
        exit 5
    }
    if ! printf '%s\n' "$package_version" | grep -Eq \
        '^[0-9]+\.[0-9]+\.[0-9]+-step[0-9]{2}-[0-9]{8}-[0-9]{4}-r[0-9]+$' ||
        { [ "$release_version" != latest ] && [ "$package_version" != "$release_version" ]; }; then
        echo 'Release version does not match the requested package.' >&2
        exit 5
    fi
    printf 'Verified release package: %s\n' "$package_version"
    printf '%s\n' 'Package verification finished. Service installation is the next stage.'
    exit 0
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
