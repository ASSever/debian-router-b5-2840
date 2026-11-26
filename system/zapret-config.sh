#!/bin/sh
set -eu
export LC_ALL=C
umask 077

unit=debian-router-zapret.service
state=/etc/debian-router-b5-2840/zapret
current=$state/current

case "$#:${1-}" in
    1:show|2:check|3:check|2:apply|3:apply) ;;
    *)
        echo 'Usage: zapret-config.sh show | check PROFILE [HOST_FILE] | apply PROFILE [HOST_FILE]' >&2
        exit 2
        ;;
esac
[ "$(id -u)" -eq 0 ] || { echo 'Run as root.' >&2; exit 2; }

exec 9>/run/lock/debian-router-install.lock
flock -x 9
if [ "$1" = show ]; then
    if [ -e "$current" ] || [ -L "$current" ]; then
        cat "$current/profile"
        cat "$current/exclude-hosts.txt"
    else
        printf '%s\n' alt11
    fi
    exit 0
fi

case "$2" in
    alt11|alt11-tcp) ;;
    *) echo 'Unknown Zapret profile. Choose alt11 or alt11-tcp.' >&2; exit 2 ;;
esac

release=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
# The bundled nfqws drops to this group and still needs to read its hostlists.
nfqws_group=2147483647
install -d -m 0710 -g "$nfqws_group" "$state"
candidate=$(mktemp -d "$state/profile.XXXXXXXX")
switch_link=$candidate/current
cleanup() {
    if [ "$(readlink -- "$current" 2>/dev/null || :)" != "$candidate" ]; then
        rm -r -- "$candidate"
    fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

printf '%s\n' "$2" > "$candidate/profile"
: > "$candidate/exclude-hosts.txt"
if [ "$#" -eq 3 ]; then
    [ -f "$3" ] || { echo 'Host list must be a regular file.' >&2; exit 2; }
    awk '
        { sub(/\r$/, ""); sub(/^[ \t]+/, ""); sub(/[ \t]+$/, "") }
        /^($|#)/ { next }
        {
            host = $0
            sub(/^\^/, "", host)
            labels = split(host, label, /\./)
            valid = length(host) > 0 && length(host) <= 253
            for (i = 1; i <= labels; ++i) {
                if (length(label[i]) > 63 || label[i] !~ /^[[:alnum:]]([[:alnum:]-]*[[:alnum:]])?$/) {
                    valid = 0
                }
            }
            if (!valid) {
                printf "Invalid domain on line %d.\n", NR > "/dev/stderr"
                exit 2
            }
            print tolower($0)
        }
    ' < "$3" > "$candidate/exclude-hosts.txt"
fi

chgrp -- "$nfqws_group" "$candidate" "$candidate/exclude-hosts.txt"
chmod 0710 "$candidate"
chmod 0640 "$candidate/exclude-hosts.txt"
if ! ZAPRET_CONFIG_DIR=$candidate "$release/zapret-alt11.sh" "$release/zapret-assets" --dry-run > /dev/null 2>&1; then
    echo 'Zapret rejected the proposed configuration.' >&2
    exit 1
fi
if [ "$1" = check ]; then
    printf 'Zapret profile validated: %s\n' "$2"
    exit 0
fi

previous=
if [ -L "$current" ]; then
    previous=$(readlink -- "$current")
elif [ -e "$current" ]; then
    echo 'Expected a managed Zapret configuration symlink.' >&2
    exit 1
fi
active=$(systemctl show --property=ActiveState --value "$unit")
case "$active" in
    active|inactive|failed) ;;
    *) echo 'Zapret is changing state. Try again after it settles.' >&2; exit 1 ;;
esac

set_current() {
    ln -s -- "$1" "$switch_link" && mv -Tf -- "$switch_link" "$current"
}

set_current "$candidate"
if [ "$active" = active ] && ! systemctl restart "$unit"; then
    if [ -n "$previous" ]; then
        set_current "$previous"
    else
        rm -- "$current"
    fi
    # The failed attempt may have consumed the last start allowed by systemd.
    systemctl reset-failed "$unit" || :
    if ! systemctl restart "$unit"; then
        echo 'Previous Zapret configuration restored, but the service could not restart.' >&2
        exit 1
    fi
    echo 'Zapret restart failed. Previous configuration restored.' >&2
    exit 1
fi

case "$previous" in
    "$state"/profile.????????) rm -r -- "$previous" ;;
esac
printf 'Zapret profile saved: %s\n' "$2"
