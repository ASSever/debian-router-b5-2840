#!/bin/sh
# This entry point must also work when downloaded as a single file.
set -eu
export LC_ALL=C

usage() {
    printf '%s\n' 'Usage: sh install.sh [--check | --fetch-version VERSION | --install-version VERSION | --help]' \
        'Install the latest verified API and UI release, or select a release explicitly.'
}

case "$#:${1-}" in
    0:|1:--check) ;;
    1:--help) usage; exit 0 ;;
    2:--fetch-version|2:--install-version) ;;
    *) usage >&2; exit 2 ;;
esac

mode=${1---install}
if [ "$mode" = --fetch-version ] || [ "$mode" = --install-version ]; then
    release_version=$2
else
    release_version=latest
fi
if [ "$mode" != --check ]; then
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

if [ "$mode" != --check ]; then
    if [ "$mode" != --fetch-version ] && [ "$(id -u)" -ne 0 ]; then
        echo 'Run the installation as root: sudo sh ./install.sh' >&2
        exit 4
    fi
    for tool in /usr/lib/apt/apt-helper sha256sum tar mktemp sort; do
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
    cleanup() {
        rm -r -- "$stage"
        if [ -n "${release_tmp-}" ] && [ -d "$release_tmp" ]; then
            rm -r -- "$release_tmp"
        fi
    }
    trap cleanup EXIT
    trap 'exit 1' HUP INT TERM
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
    if [ "$mode" = --fetch-version ]; then
        printf '%s\n' 'Package verification finished.'
        exit 0
    fi

    package=debian-router-b5-2840-$package_version
    legacy_listing=$(printf '%s\n' "$package/" "$package/VERSION" "$package/router-api" | sort)
    ui_listing=$(printf '%s\n' "$package/" "$package/VERSION" "$package/router-api" \
        "$package/web/" "$package/web/assets/" "$package/web/assets/app.css" \
        "$package/web/assets/app.js" "$package/web/index.html" | sort)
    agent_listing=$(printf '%s\n' "$package/" "$package/VERSION" "$package/router-api" \
        "$package/router-agent" "$package/web/" "$package/web/assets/" \
        "$package/web/assets/app.css" "$package/web/assets/app.js" \
        "$package/web/index.html" | sort)
    actual_listing=$(tar -tzf "$stage/$archive" | sort)
    has_agent=no
    case "$actual_listing" in
        "$agent_listing") has_agent=yes; has_ui=yes ;;
        "$ui_listing") has_ui=yes ;;
        "$legacy_listing") has_ui=no ;;
        *)
            echo 'Unexpected release archive contents.' >&2
            exit 5
            ;;
    esac
    tar -xzf "$stage/$archive" -C "$stage" --no-same-owner --no-same-permissions
    if [ ! -d "$stage/$package" ] || [ -L "$stage/$package" ] ||
        [ ! -f "$stage/$package/router-api" ] || [ -L "$stage/$package/router-api" ] ||
        [ ! -f "$stage/$package/VERSION" ] || [ -L "$stage/$package/VERSION" ] ||
        [ "$(cat "$stage/$package/VERSION")" != "$package_version" ]; then
        echo 'Invalid release payload.' >&2
        exit 5
    fi
    if [ "$has_ui" = yes ] && {
        [ ! -d "$stage/$package/web/assets" ] || [ -L "$stage/$package/web" ] ||
        [ -L "$stage/$package/web/assets" ] ||
        [ ! -f "$stage/$package/web/index.html" ] || [ -L "$stage/$package/web/index.html" ] ||
        [ ! -f "$stage/$package/web/assets/app.js" ] || [ -L "$stage/$package/web/assets/app.js" ] ||
        [ ! -f "$stage/$package/web/assets/app.css" ] || [ -L "$stage/$package/web/assets/app.css" ];
    }; then
        echo 'Invalid UI payload.' >&2
        exit 5
    fi
    if [ "$has_agent" = yes ] && {
        [ ! -f "$stage/$package/router-agent" ] || [ -L "$stage/$package/router-agent" ];
    }; then
        echo 'Invalid privileged-agent payload.' >&2
        exit 5
    fi

    prefix=/opt/debian-router-b5-2840
    release_dir=$prefix/releases/$package_version
    service=/etc/systemd/system/debian-router-api.service
    agent_service=/etc/systemd/system/debian-router-agent.service
    agent_current=$prefix/agent-current
    mkdir -p "$prefix/releases"
    previous_release=
    if [ -L "$prefix/current" ]; then
        previous_release=$(readlink "$prefix/current")
        if [ ! -x "$previous_release/router-api" ]; then
            echo 'The current release is missing its API binary.' >&2
            exit 6
        fi
    elif [ -e "$prefix/current" ]; then
        echo 'The current release path is not a symbolic link.' >&2
        exit 6
    fi
    if [ -e "$release_dir" ]; then
        if [ -L "$release_dir" ] || [ ! -f "$release_dir/router-api" ] ||
            [ ! -f "$release_dir/VERSION" ] ||
            [ "$(cat "$release_dir/VERSION")" != "$package_version" ] ||
            ! cmp -s "$stage/$package/router-api" "$release_dir/router-api"; then
            echo 'Installed version differs from verified release.' >&2
            exit 6
        fi
        if [ "$has_ui" = yes ] && {
            [ ! -d "$release_dir/web/assets" ] || [ -L "$release_dir/web" ] ||
            [ -L "$release_dir/web/assets" ] ||
            [ ! -f "$release_dir/web/index.html" ] || [ -L "$release_dir/web/index.html" ] ||
            [ ! -f "$release_dir/web/assets/app.js" ] || [ -L "$release_dir/web/assets/app.js" ] ||
            [ ! -f "$release_dir/web/assets/app.css" ] || [ -L "$release_dir/web/assets/app.css" ] ||
            ! cmp -s "$stage/$package/web/index.html" "$release_dir/web/index.html" ||
            ! cmp -s "$stage/$package/web/assets/app.js" "$release_dir/web/assets/app.js" ||
            ! cmp -s "$stage/$package/web/assets/app.css" "$release_dir/web/assets/app.css";
        }; then
            echo 'Installed UI differs from verified release.' >&2
            exit 6
        fi
        if [ "$has_agent" = yes ] && {
            [ ! -f "$release_dir/router-agent" ] || [ -L "$release_dir/router-agent" ] ||
            ! cmp -s "$stage/$package/router-agent" "$release_dir/router-agent";
        }; then
            echo 'Installed privileged-agent binary differs from verified release.' >&2
            exit 6
        fi
    else
        release_tmp=$prefix/releases/.new-$package_version-$$
        mkdir "$release_tmp"
        install -m 0755 "$stage/$package/router-api" "$release_tmp/router-api"
        printf '%s\n' "$package_version" > "$release_tmp/VERSION"
        if [ "$has_agent" = yes ]; then
            install -m 0755 "$stage/$package/router-agent" "$release_tmp/router-agent"
        fi
        if [ "$has_ui" = yes ]; then
            mkdir -p "$release_tmp/web/assets"
            install -m 0644 "$stage/$package/web/index.html" "$release_tmp/web/index.html"
            install -m 0644 "$stage/$package/web/assets/app.js" "$release_tmp/web/assets/app.js"
            install -m 0644 "$stage/$package/web/assets/app.css" "$release_tmp/web/assets/app.css"
        fi
        mv -Tf "$release_tmp" "$release_dir"
        release_tmp=
    fi

    agent_gid=
    if [ "$has_agent" = yes ]; then
        for tool in getent groupadd; do
            if ! command -v "$tool" >/dev/null 2>&1; then
                printf 'Required tool is missing: %s\n' "$tool" >&2
                exit 4
            fi
        done
        agent_group_record=$(getent group debian-router-agent || :)
        if [ -z "$agent_group_record" ]; then
            groupadd --system debian-router-agent
            agent_group_record=$(getent group debian-router-agent)
        fi
        agent_gid=$(printf '%s\n' "$agent_group_record" | cut -d: -f3)
        agent_group_members=$(printf '%s\n' "$agent_group_record" | cut -d: -f4)
        if ! printf '%s\n' "$agent_gid" | grep -Eq '^[0-9]+$' ||
            [ "$agent_gid" = 0 ] ||
            [ -n "$agent_group_members" ]; then
            echo 'The dedicated agent group is invalid or has unrelated members.' >&2
            exit 6
        fi
    fi

    previous_agent_release=
    if [ -L "$agent_current" ]; then
        previous_agent_release=$(readlink "$agent_current")
        if [ ! -x "$previous_agent_release/router-agent" ]; then
            echo 'The current privileged-agent release is missing its binary.' >&2
            exit 6
        fi
    elif [ -e "$agent_current" ]; then
        echo 'The privileged-agent release path is not a symbolic link.' >&2
        exit 6
    fi
    agent_release_changed=no
    agent_unit_exists_before=no
    agent_unit_changed=no
    agent_was_active=no
    agent_was_enabled=no
    if [ -f "$agent_service" ]; then
        agent_unit_exists_before=yes
        cp -p "$agent_service" "$stage/previous-agent.service"
        if systemctl is-active --quiet debian-router-agent.service; then
            agent_was_active=yes
        fi
        if systemctl is-enabled --quiet debian-router-agent.service; then
            agent_was_enabled=yes
        fi
    fi

    case "$package_version" in
        *-step0[0-9]-*) ;;
        *)
            if ! command -v systemd-run >/dev/null 2>&1; then
                echo 'systemd-run is required to initialize the administrator.' >&2
                exit 4
            fi
            run_admin() {
                systemd-run --pipe --quiet --collect \
                    -p DynamicUser=yes \
                    -p StateDirectory=debian-router-b5-2840 \
                    -p StateDirectoryMode=0700 \
                    -p UMask=0077 \
                    -p NoNewPrivileges=yes \
                    -p ProtectSystem=strict \
                    -p ProtectHome=yes \
                    -p PrivateTmp=yes \
                    "$release_dir/router-api" "$1"
            }
            admin_status=$(run_admin admin-status) || {
                echo 'Could not read administrator state; the active release was not changed.' >&2
                exit 7
            }
            case "$admin_status" in
                initialized) ;;
                missing)
                    if [ ! -t 1 ]; then
                        echo 'An interactive terminal is required to show the initial admin password.' >&2
                        exit 4
                    fi
                    run_admin admin-init || {
                        echo 'Administrator setup failed; the active release was not changed.' >&2
                        exit 7
                    }
                    ;;
                *) echo 'Unexpected administrator status; the active release was not changed.' >&2; exit 7 ;;
            esac
            ;;
    esac

    unit_tmp=$stage/debian-router-api.service
    if [ "$has_agent" = yes ]; then
        cat > "$unit_tmp" <<'EOF'
[Unit]
Description=Debian Router API
After=network.target debian-router-agent.service
Requires=debian-router-agent.service

[Service]
Type=simple
DynamicUser=yes
Group=debian-router-agent
StateDirectory=debian-router-b5-2840
StateDirectoryMode=0700
UMask=0077
Environment=ROUTER_UI_DIR=/opt/debian-router-b5-2840/current/web
Environment=ROUTER_AGENT_SOCKET=/run/debian-router-agent/agent.sock
ExecStartPre=/opt/debian-router-b5-2840/current/router-agent request status
ExecStart=/opt/debian-router-b5-2840/current/router-api
Restart=on-failure
RestartSec=2s
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
    else
        cat > "$unit_tmp" <<'EOF'
[Unit]
Description=Debian Router API
After=network.target

[Service]
Type=simple
DynamicUser=yes
StateDirectory=debian-router-b5-2840
StateDirectoryMode=0700
UMask=0077
Environment=ROUTER_UI_DIR=/opt/debian-router-b5-2840/current/web
ExecStart=/opt/debian-router-b5-2840/current/router-api
Restart=on-failure
RestartSec=2s
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
    fi
    if [ ! -f "$service" ] || ! cmp -s "$unit_tmp" "$service"; then
        unit_changed=yes
        if [ -f "$service" ]; then
            cp -p "$service" "$stage/previous.service"
        fi
        install -m 0644 "$unit_tmp" "$service.new.$$"
        mv -Tf "$service.new.$$" "$service"
    else
        unit_changed=no
    fi

    if [ "$has_agent" = yes ]; then
        agent_unit_tmp=$stage/debian-router-agent.service
        cat > "$agent_unit_tmp" <<EOF
[Unit]
Description=Debian Router Privileged Agent
After=local-fs.target

[Service]
Type=simple
User=root
Group=debian-router-agent
RuntimeDirectory=debian-router-agent
RuntimeDirectoryMode=0750
UMask=0077
Environment=ROUTER_AGENT_SOCKET=/run/debian-router-agent/agent.sock
Environment=ROUTER_AGENT_ALLOWED_GID=$agent_gid
ExecStart=$agent_current/router-agent
Restart=on-failure
RestartSec=2s
NoNewPrivileges=yes
CapabilityBoundingSet=
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX

[Install]
WantedBy=multi-user.target
EOF
        if [ ! -f "$agent_service" ] || ! cmp -s "$agent_unit_tmp" "$agent_service"; then
            agent_unit_changed=yes
            install -m 0644 "$agent_unit_tmp" "$agent_service.new.$$"
            mv -Tf "$agent_service.new.$$" "$agent_service"
        fi
        if [ "$previous_agent_release" != "$release_dir" ]; then
            ln -s "$release_dir" "$agent_current.new.$$"
            mv -Tf "$agent_current.new.$$" "$agent_current"
            agent_release_changed=yes
        fi
    fi

    rollback_install() {
        if [ -n "$previous_release" ]; then
            ln -s "$previous_release" "$prefix/current.rollback.$$"
            mv -Tf "$prefix/current.rollback.$$" "$prefix/current"
        else
            rm -f -- "$prefix/current"
        fi
        if [ "$agent_release_changed" = yes ]; then
            if [ -n "$previous_agent_release" ]; then
                ln -s "$previous_agent_release" "$agent_current.rollback.$$"
                mv -Tf "$agent_current.rollback.$$" "$agent_current"
            else
                rm -f -- "$agent_current"
            fi
        fi
        if [ "$unit_changed" = yes ]; then
            if [ -f "$stage/previous.service" ]; then
                cp -p "$stage/previous.service" "$service.new.$$"
                mv -Tf "$service.new.$$" "$service"
            else
                rm -f -- "$service"
            fi
        fi
        if [ "$agent_unit_changed" = yes ] && [ "$agent_unit_exists_before" = yes ]; then
            cp -p "$stage/previous-agent.service" "$agent_service.new.$$"
            mv -Tf "$agent_service.new.$$" "$agent_service"
        elif [ "$agent_unit_changed" = yes ]; then
            rm -f -- "$agent_service"
        fi
        systemctl daemon-reload || :
        if [ "$agent_unit_exists_before" = yes ]; then
            if [ "$agent_was_enabled" = yes ]; then
                systemctl enable debian-router-agent.service || :
            else
                systemctl disable debian-router-agent.service || :
            fi
            if [ "$agent_was_active" = yes ]; then
                systemctl restart debian-router-agent.service || :
            else
                systemctl stop debian-router-agent.service || :
            fi
        elif [ "$agent_unit_changed" = yes ]; then
            systemctl disable --now debian-router-agent.service || :
            rm -f -- "$agent_service"
            systemctl daemon-reload || :
        fi
        if [ -n "$previous_release" ]; then
            systemctl restart debian-router-api.service || :
        else
            systemctl disable --now debian-router-api.service || :
        fi
    }

    if [ "$previous_release" != "$release_dir" ]; then
        ln -s "$release_dir" "$prefix/current.new.$$"
        mv -Tf "$prefix/current.new.$$" "$prefix/current"
    fi

    if [ "$unit_changed" = yes ] || [ "$agent_unit_changed" = yes ]; then
        if ! systemctl daemon-reload; then
            rollback_install
            echo 'Could not reload systemd units; the previous release was restored.' >&2
            exit 7
        fi
    fi
    if [ "$has_agent" = yes ]; then
        if ! systemctl enable debian-router-agent.service; then
            rollback_install
            echo 'Could not enable the privileged agent; the previous release was restored.' >&2
            exit 7
        fi
        if [ "$previous_agent_release" != "$release_dir" ] ||
            [ "$agent_unit_changed" = yes ]; then
            if ! systemctl restart debian-router-agent.service ||
                { sleep 1; ! systemctl is-active --quiet debian-router-agent.service; }; then
                rollback_install
                echo 'Privileged agent startup failed; the previous release was restored.' >&2
                exit 7
            fi
        elif ! systemctl start debian-router-agent.service; then
            rollback_install
            echo 'Privileged agent could not be started; the previous release was restored.' >&2
            exit 7
        fi
    fi
    if ! systemctl enable debian-router-api.service; then
        rollback_install
        echo 'Could not enable the API; the previous release was restored.' >&2
        exit 7
    fi
    if [ "$previous_release" = "$release_dir" ] && [ "$unit_changed" = no ]; then
        if ! systemctl start debian-router-api.service ||
            ! systemctl is-active --quiet debian-router-api.service; then
            rollback_install
            echo 'API start failed; the previous release was restored.' >&2
            exit 7
        fi
    elif ! systemctl restart debian-router-api.service ||
        { sleep 1; ! systemctl is-active --quiet debian-router-api.service; }; then
        rollback_install
        echo 'API startup failed; the previous release was restored.' >&2
        exit 7
    fi
    if ! systemctl is-active --quiet debian-router-api.service; then
        rollback_install
        echo 'API became inactive; the previous release was restored.' >&2
        exit 7
    fi
    if [ "$has_agent" = no ] && [ "$agent_unit_exists_before" = yes ]; then
        if systemctl disable --now debian-router-agent.service; then
            rm -f -- "$agent_service" "$agent_current"
            systemctl daemon-reload
        fi
    fi
    printf 'Installed API release: %s\n' "$package_version"
    if [ "$has_agent" = yes ]; then
        printf 'Privileged agent: active, socket %s\n' '/run/debian-router-agent/agent.sock'
    fi
    printf '%s\n' 'Local health endpoint: https://127.0.0.1:8081/healthz'
    printf '%s\n' 'Local UI: https://127.0.0.1:8081/'
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
