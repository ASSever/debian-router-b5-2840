#!/bin/sh
set -eu

base=/var/lib/debian-router-rollback-fixture
unit_prefix=debian-router-rollback-fixture

usage() {
    cat >&2 <<'USAGE'
Usage:
  sh tests/network-rollback-fixture.sh arm [delay-seconds]
  sh tests/network-rollback-fixture.sh verify TRANSACTION_ID
  sh tests/network-rollback-fixture.sh cancel TRANSACTION_ID
  sh tests/network-rollback-fixture.sh cleanup TRANSACTION_ID

This systemd integration fixture only writes under /var/lib/debian-router-rollback-fixture.
USAGE
    exit 2
}

fail() {
    printf 'network rollback fixture: %s\n' "$*" >&2
    exit 1
}

validate_id() {
    case "$1" in
        ????????????????) ;;
        *) return 1 ;;
    esac
    case "$1" in
        *[!0123456789abcdef]*) return 1 ;;
    esac
    [ "${#1}" -eq 16 ]
}

validate_fixture() {
    id=$1
    dir=$base/$id
    [ ! -L "$dir" ] && [ -d "$dir" ] || fail 'fixture directory is missing or unsafe'
    [ "$(/usr/bin/stat -c '%u:%g:%a' "$dir")" = '0:0:700' ] || fail 'fixture directory must be root:root mode 0700'
    for name in snapshot target SHA256SUMS; do
        path=$dir/$name
        [ ! -L "$path" ] && [ -f "$path" ] || fail "fixture file is missing or unsafe: $name"
        [ "$(/usr/bin/stat -c '%u:%g:%a' "$path")" = '0:0:600' ] || fail "fixture file must be root:root mode 0600: $name"
    done
}

require_root() {
    [ "$(id -u)" -eq 0 ] || fail 'run as root'
    [ -x /usr/bin/systemd-run ] || fail 'systemd-run is unavailable'
    [ -x /usr/bin/systemctl ] || fail 'systemctl is unavailable'
    [ -x /usr/bin/stat ] && [ -x /usr/bin/sha256sum ] && [ -x /usr/bin/cmp ] || fail 'required file tools are unavailable'
}

command_name=${1-}
case "$command_name" in
    arm)
        [ "$#" -le 2 ] || usage
        delay=${2:-30}
        case "$delay" in
            1[5-9]|[2-9][0-9]|1[01][0-9]|120) ;;
            *) usage ;;
        esac
        ;;
    verify|cancel|cleanup)
        [ "$#" -eq 2 ] || usage
        id=$2
        validate_id "$id" || usage
        ;;
    *) usage ;;
esac

require_root
if [ -L "$base" ]; then
    fail 'fixture root must not be a symlink'
fi
if [ ! -e "$base" ]; then
    /usr/bin/install -d -o root -g root -m 0700 "$base"
fi
[ -d "$base" ] || fail 'fixture root is not a directory'
[ "$(/usr/bin/stat -c '%u:%g:%a' "$base")" = '0:0:700' ] || fail 'fixture root must be root:root mode 0700'

case "$command_name" in
    arm)
        id=$(/usr/bin/od -An -N8 -tx1 /dev/urandom | /usr/bin/tr -d ' \n')
        validate_id "$id" || fail 'could not generate a transaction ID'
        dir=$base/$id
        /usr/bin/mkdir -m 0700 "$dir"
        umask 077
        printf 'before-rollback\n' > "$dir/snapshot"
        printf 'pending-change\n' > "$dir/target"
        (cd "$dir" && /usr/bin/sha256sum snapshot > SHA256SUMS)
        /usr/bin/chmod 0600 "$dir/snapshot" "$dir/target" "$dir/SHA256SUMS"
        validate_fixture "$id"
        unit=$unit_prefix-$id
        if ! /usr/bin/systemd-run --system --quiet --no-block \
            --unit="$unit" --on-active="${delay}s" --timer-property=AccuracySec=1s \
            --property=Type=oneshot --property=User=root \
            --property=NoNewPrivileges=yes --property=ProtectSystem=strict \
            --property=ProtectHome=yes --property=PrivateTmp=yes \
            --property="ReadWritePaths=$dir" --property=CapabilityBoundingSet= \
            --property=RestrictAddressFamilies=AF_UNIX -- \
            /usr/bin/cp -- "$dir/snapshot" "$dir/target"; then
            /usr/bin/systemctl --system stop "$unit.timer" "$unit.service" >/dev/null 2>&1 || :
            /usr/bin/rm -f -- "$dir/snapshot" "$dir/target" "$dir/SHA256SUMS"
            /usr/bin/rmdir -- "$dir"
            fail 'could not schedule the system-manager timer'
        fi
        printf '%s\n' "$id"
        ;;
    verify)
        validate_fixture "$id"
        (cd "$base/$id" && /usr/bin/sha256sum --check SHA256SUMS)
        dir=$base/$id
        unit=$unit_prefix-$id
        attempts=0
        result=
        service_state=
        while [ "$attempts" -lt 45 ]; do
            result=$(/usr/bin/systemctl --system show --property=Result --value "$unit.service" 2>/dev/null || :)
            service_state=$(/usr/bin/systemctl --system show --property=ActiveState --value "$unit.service" 2>/dev/null || :)
            if [ "$service_state" = failed ] || [ "$result" = exit-code ]; then
                fail "rollback service failed (result: ${result:-unknown})"
            fi
            if [ "$result" = success ] && /usr/bin/cmp -s "$dir/snapshot" "$dir/target"; then
                break
            fi
            /usr/bin/sleep 1
            attempts=$((attempts + 1))
        done
        [ "$result" = success ] || fail "rollback service did not succeed (result: ${result:-unknown})"
        /usr/bin/cmp -s "$dir/snapshot" "$dir/target" || fail 'fixture was not restored'
        timer_state=$(/usr/bin/systemctl --system show --property=ActiveState --value "$unit.timer" 2>/dev/null || :)
        [ "$timer_state" = inactive ] || fail "timer is not inactive after firing (state: ${timer_state:-unknown})"
        protect_system=$(/usr/bin/systemctl --system show --property=ProtectSystem --value "$unit.service")
        protect_home=$(/usr/bin/systemctl --system show --property=ProtectHome --value "$unit.service")
        private_tmp=$(/usr/bin/systemctl --system show --property=PrivateTmp --value "$unit.service")
        read_write_paths=$(/usr/bin/systemctl --system show --property=ReadWritePaths --value "$unit.service")
        no_new_privileges=$(/usr/bin/systemctl --system show --property=NoNewPrivileges --value "$unit.service")
        capability_bounding_set=$(/usr/bin/systemctl --system show --property=CapabilityBoundingSet --value "$unit.service")
        address_families=$(/usr/bin/systemctl --system show --property=RestrictAddressFamilies --value "$unit.service")
        service_user=$(/usr/bin/systemctl --system show --property=User --value "$unit.service")
        [ "$protect_system" = strict ] || fail 'systemd service does not protect the filesystem'
        [ "$protect_home" = yes ] && [ "$private_tmp" = yes ] || fail 'systemd service isolation is incomplete'
        [ "$read_write_paths" = "$dir" ] || fail 'systemd service can write outside the fixture'
        [ "$no_new_privileges" = yes ] && [ -z "$capability_bounding_set" ] || fail 'systemd service has unexpected privileges'
        [ "$address_families" = AF_UNIX ] && [ "$service_user" = root ] || fail 'systemd service has unexpected execution restrictions'
        printf 'ROLLBACK_FIXTURE=PASS id=%s service=%s timer=%s sandbox=PASS\n' "$id" "$result" "$timer_state"
        ;;
    cancel)
        validate_fixture "$id"
        dir=$base/$id
        unit=$unit_prefix-$id
        /usr/bin/systemctl --system stop "$unit.timer"
        /usr/bin/systemctl --system stop "$unit.service" >/dev/null 2>&1 || :
        timer_state=$(/usr/bin/systemctl --system show --property=ActiveState --value "$unit.timer" 2>/dev/null || :)
        service_state=$(/usr/bin/systemctl --system show --property=ActiveState --value "$unit.service" 2>/dev/null || :)
        [ "$timer_state" = inactive ] || fail "timer did not stop (state: ${timer_state:-unknown})"
        [ "$service_state" != active ] || fail 'rollback service is still active'
        [ "$(/usr/bin/cat "$dir/target")" = pending-change ] || fail 'timer fired before cancellation'
        printf 'ROLLBACK_CANCEL=PASS id=%s timer=%s target=pending-change\n' "$id" "$timer_state"
        ;;
    cleanup)
        validate_fixture "$id"
        dir=$base/$id
        unit=$unit_prefix-$id
        /usr/bin/systemctl --system stop "$unit.timer" "$unit.service" >/dev/null 2>&1 || :
        service_state=$(/usr/bin/systemctl --system show --property=ActiveState --value "$unit.service" 2>/dev/null || :)
        [ "$service_state" != active ] || fail 'rollback service is still active; fixture left intact'
        /usr/bin/rm -f -- "$dir/snapshot" "$dir/target" "$dir/SHA256SUMS"
        /usr/bin/rmdir -- "$dir"
        /usr/bin/systemctl --system reset-failed "$unit.timer" "$unit.service" >/dev/null 2>&1 || :
        printf 'ROLLBACK_FIXTURE_CLEANUP=PASS id=%s\n' "$id"
        ;;
esac
