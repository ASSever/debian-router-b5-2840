#!/bin/sh
# Run in the disposable Debian container from the Bootstrap workflow.
set -eu
test -f /.dockerenv
test "$(id -u)" -eq 0

prefix=/opt/debian-router-b5-2840
state=/var/lib/debian-router-b5-2840
unit=/etc/systemd/system/debian-router-api.service
agent=/etc/systemd/system/debian-router-agent.service
lan=/etc/systemd/system/debian-router-lan-dnsmasq.service
volume=/var/lib/docker/volumes/fixture/_data
for path in "$prefix" "$state" "$unit" "$agent" "$lan" "$volume"; do
    test ! -e "$path"
    test ! -L "$path"
done

fixture=$(mktemp -d)
trap 'rm -r -- "$fixture"' EXIT
export fixture
mkdir -p "$fixture/bin" "$prefix/releases/test" "$state" "$volume" /etc/systemd/system /run/lock
ln -s "$prefix/releases/test" "$prefix/current"
ln -s "$prefix/releases/test" "$prefix/agent-current"
printf '%s\n' release >"$prefix/releases/test/VERSION"
printf '%s\n' settings >"$state/router.db"
printf '%s\n' user-data >"$volume/marker"
touch "$unit" "$agent" "$lan" "$fixture/api"

cat >"$fixture/bin/systemctl" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$fixture/calls"
command=$1
shift
case "$command" in
    show)
        property=$1
        shift 3
        case "$property" in
            --property=FragmentPath)
                path=/etc/systemd/system/$1
                if [ -f "$path" ]; then printf '%s\n' "$path"; fi
                ;;
            --property=ActiveState)
                if [ "$1" = debian-router-pppoe.service ] && [ -e "$fixture/pppoe" ]; then
                    echo active
                else
                    echo inactive
                fi
                ;;
            *) exit 5 ;;
        esac
        ;;
    is-active) test "$*" = '--quiet debian-router-api.service' && test -e "$fixture/api" ;;
    stop)
        if [ "$*" = debian-router-api.service ]; then
            rm -f "$fixture/api"
            if [ -e "$fixture/arm" ]; then
                touch /var/lib/debian-router-b5-2840/restore-pending.json
            fi
        fi
        ;;
    start)
        test "$*" = debian-router-api.service
        touch "$fixture/api"
        ;;
    disable|daemon-reload) ;;
    *) exit 5 ;;
esac
EOF
chmod 755 "$fixture/bin/systemctl"
PATH=$fixture/bin:$PATH
export PATH

must_refuse() {
    status=0
    sh install.sh --uninstall >/dev/null 2>&1 || status=$?
    test "$status" -eq 7
    test -f "$unit"
    test -f "$agent"
    test -L "$prefix/current"
    test -L "$prefix/agent-current"
    test -e "$fixture/api"
}

touch "$state/restore-pending.json"
must_refuse
rm "$state/restore-pending.json"
touch "$fixture/pppoe"
must_refuse
rm "$fixture/pppoe"
if grep -E '^(stop|disable|daemon-reload)' "$fixture/calls"; then exit 1; fi

touch "$fixture/arm"
must_refuse
grep -Fx 'start debian-router-api.service' "$fixture/calls"
rm "$fixture/arm" "$state/restore-pending.json"
: >"$fixture/calls"

sh install.sh --uninstall
test ! -e "$unit"
test ! -e "$agent"
test ! -e "$lan"
test ! -L "$prefix/current"
test ! -L "$prefix/agent-current"
test "$(cat "$prefix/releases/test/VERSION")" = release
test "$(cat "$state/router.db")" = settings
test "$(cat "$volume/marker")" = user-data
grep -Fx 'disable -- debian-router-api.service debian-router-lan-dnsmasq.service debian-router-agent.service' "$fixture/calls"
grep -Fx daemon-reload "$fixture/calls"

: >"$fixture/calls"
sh install.sh --uninstall
if grep -E '^(stop|disable|daemon-reload)' "$fixture/calls"; then exit 1; fi
echo 'Uninstall fixture checks passed.'
