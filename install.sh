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
        if [ -n "${zapret_tmp-}" ] && [ -d "$zapret_tmp" ]; then
            rm -r -- "$zapret_tmp"
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
    pppoe_listing=$(printf '%s\n' "$package/" "$package/VERSION" "$package/PPPOE_SUPPORT" "$package/router-api" \
        "$package/router-agent" "$package/web/" "$package/web/assets/" \
        "$package/web/assets/app.css" "$package/web/assets/app.js" \
        "$package/web/index.html" | sort)
    lan_listing=$(printf '%s\n%s\n' "$pppoe_listing" "$package/LAN_SUPPORT" | sort)
    sing_box_listing=$(printf '%s\n%s\n' "$lan_listing" "$package/SING_BOX_SUPPORT" | sort)
    proxy_routing_listing=$(printf '%s\n%s\n' "$sing_box_listing" "$package/PROXY_ROUTING_SUPPORT" | sort)
    zapret_listing=$(printf '%s\n' "$proxy_routing_listing" "$package/zapret-alt11.sh" \
        "$package/zapret-nft.sh" "$package/debian-router-zapret.service" | sort)
    zapret_profiles_listing=$(printf '%s\n%s\n' "$zapret_listing" "$package/zapret-config.sh" | sort)
    monitor_listing=$(printf '%s\n' "$zapret_profiles_listing" "$package/router-monitor" \
        "$package/debian-router-monitor.service" | sort)
    failover_listing=$(printf '%s\n' "$monitor_listing" "$package/router-failover" \
        "$package/debian-router-failover.service" | sort)
    recovery_listing=$(printf '%s\n' "$failover_listing" "$package/router-recovery" \
        "$package/debian-router-recovery.service" | sort)
    mqtt_listing=$(printf '%s\n' "$recovery_listing" "$package/router-mqtt" \
        "$package/debian-router-mqtt.service" | sort)
    ddns_listing=$(printf '%s\n' "$mqtt_listing" "$package/router-ddns" \
        "$package/debian-router-ddns.service" | sort)
    rs232_listing=$(printf '%s\n' "$ddns_listing" "$package/router-rs232" | sort)
    containers_listing=$(printf '%s\n' "$rs232_listing" "$package/router-containers.sh" | sort)
    actual_listing=$(tar -tzf "$stage/$archive" | sort)
    has_containers=no
    if [ "$actual_listing" = "$containers_listing" ]; then
        has_containers=yes
        actual_listing=$rs232_listing
    fi
    has_rs232=no
    if [ "$actual_listing" = "$rs232_listing" ]; then
        has_rs232=yes
        actual_listing=$ddns_listing
    fi
    has_ddns=no
    if [ "$actual_listing" = "$ddns_listing" ]; then
        has_ddns=yes
        actual_listing=$mqtt_listing
    fi
    has_mqtt=no
    if [ "$actual_listing" = "$mqtt_listing" ]; then
        has_mqtt=yes
        actual_listing=$recovery_listing
    fi
    has_recovery=no
    if [ "$actual_listing" = "$recovery_listing" ]; then
        has_recovery=yes
        actual_listing=$failover_listing
    fi
    has_failover=no
    if [ "$actual_listing" = "$failover_listing" ]; then
        has_failover=yes
        actual_listing=$monitor_listing
    fi
    has_agent=no
    has_pppoe=no
    has_lan=no
    has_sing_box=no
    has_proxy_routing=no
    has_zapret=no
    has_zapret_profiles=no
    has_monitor=no
    case "$actual_listing" in
        "$monitor_listing") has_agent=yes; has_ui=yes; has_pppoe=yes; has_lan=yes; has_sing_box=yes; has_proxy_routing=yes; has_zapret=yes; has_zapret_profiles=yes; has_monitor=yes ;;
        "$zapret_profiles_listing") has_agent=yes; has_ui=yes; has_pppoe=yes; has_lan=yes; has_sing_box=yes; has_proxy_routing=yes; has_zapret=yes; has_zapret_profiles=yes ;;
        "$zapret_listing") has_agent=yes; has_ui=yes; has_pppoe=yes; has_lan=yes; has_sing_box=yes; has_proxy_routing=yes; has_zapret=yes ;;
        "$proxy_routing_listing") has_agent=yes; has_ui=yes; has_pppoe=yes; has_lan=yes; has_sing_box=yes; has_proxy_routing=yes ;;
        "$sing_box_listing") has_agent=yes; has_ui=yes; has_pppoe=yes; has_lan=yes; has_sing_box=yes ;;
        "$lan_listing") has_agent=yes; has_ui=yes; has_pppoe=yes; has_lan=yes ;;
        "$pppoe_listing") has_agent=yes; has_ui=yes; has_pppoe=yes ;;
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
    if [ "$has_pppoe" = yes ] && {
        [ ! -f "$stage/$package/PPPOE_SUPPORT" ] ||
        [ -L "$stage/$package/PPPOE_SUPPORT" ] ||
        [ "$(cat "$stage/$package/PPPOE_SUPPORT")" != pppd ];
    }; then
        echo 'Invalid PPPoE support marker.' >&2
        exit 5
    fi

    if [ "$has_lan" = yes ] && {
        [ ! -f "$stage/$package/LAN_SUPPORT" ] || [ -L "$stage/$package/LAN_SUPPORT" ] ||
        [ "$(cat "$stage/$package/LAN_SUPPORT")" != 'dnsmasq-base nftables' ];
    }; then
        echo 'Invalid LAN support marker.' >&2
        exit 5
    fi

    if [ "$has_sing_box" = yes ]; then
        sing_box_marker=$stage/$package/SING_BOX_SUPPORT
        if [ ! -f "$sing_box_marker" ] || [ -L "$sing_box_marker" ]; then
            echo 'Invalid sing-box support marker.' >&2
            exit 5
        fi
        sing_box_version=$(sed -n '1p' "$sing_box_marker")
        sing_box_hash=$(sed -n '2p' "$sing_box_marker")
        if ! printf '%s\n' "$sing_box_version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' ||
            ! printf '%s\n' "$sing_box_hash" | grep -Eq '^[0-9a-f]{64}$' ||
            ! printf '%s\n' "$sing_box_version" "$sing_box_hash" | cmp -s - "$sing_box_marker"; then
            echo 'Invalid sing-box version or SHA-256.' >&2
            exit 5
        fi
    fi
    if [ "$has_proxy_routing" = yes ]; then
        proxy_marker=$stage/$package/PROXY_ROUTING_SUPPORT
        if [ ! -f "$proxy_marker" ] || [ -L "$proxy_marker" ] || [ "$(cat "$proxy_marker")" != profile-v1 ]; then
            echo 'Invalid proxy routing support marker.' >&2
            exit 5
        fi
    fi
    zapret_files='zapret-alt11.sh zapret-nft.sh debian-router-zapret.service'
    if [ "$has_zapret_profiles" = yes ]; then zapret_files="$zapret_files zapret-config.sh"; fi
    if [ "$has_zapret" = yes ]; then
        for payload in $zapret_files; do
            if [ ! -f "$stage/$package/$payload" ] || [ -L "$stage/$package/$payload" ]; then
                echo 'Invalid Zapret payload.' >&2
                exit 5
            fi
        done
    fi
    monitor_unit=debian-router-monitor.service
    monitor_files="router-monitor $monitor_unit"
    if [ "$has_monitor" = yes ]; then
        for payload in $monitor_files; do
            if [ ! -f "$stage/$package/$payload" ] || [ -L "$stage/$package/$payload" ]; then
                echo 'Invalid monitor payload.' >&2
                exit 5
            fi
        done
    fi
    failover_unit=debian-router-failover.service
    failover_files="router-failover $failover_unit"
    if [ "$has_failover" = yes ]; then
        for payload in $failover_files; do
            if [ ! -f "$stage/$package/$payload" ] || [ -L "$stage/$package/$payload" ]; then
                echo 'Invalid failover payload.' >&2
                exit 5
            fi
        done
    fi
    recovery_unit=debian-router-recovery.service
    recovery_files="router-recovery $recovery_unit"
    if [ "$has_recovery" = yes ]; then
        for payload in $recovery_files; do
            if [ ! -f "$stage/$package/$payload" ] || [ -L "$stage/$package/$payload" ]; then
                echo 'Invalid recovery payload.' >&2
                exit 5
            fi
        done
    fi

    mqtt_unit=debian-router-mqtt.service
    mqtt_files="router-mqtt $mqtt_unit"
    ddns_unit=debian-router-ddns.service
    ddns_files="router-ddns $ddns_unit"
    if [ "$has_ddns" = yes ]; then
        for payload in $ddns_files; do
            if [ ! -f "$stage/$package/$payload" ] || [ -L "$stage/$package/$payload" ]; then
                echo 'Invalid DDNS payload.' >&2
                exit 5
            fi
        done
    fi
    if [ "$has_mqtt" = yes ]; then
        for payload in $mqtt_files; do
            if [ ! -f "$stage/$package/$payload" ] || [ -L "$stage/$package/$payload" ]; then
                echo 'Invalid MQTT payload.' >&2
                exit 5
            fi
        done
    fi

    if [ "$has_rs232" = yes ] &&
        { [ ! -f "$stage/$package/router-rs232" ] || [ -L "$stage/$package/router-rs232" ]; }; then
        echo 'Invalid RS232 payload.' >&2
        exit 5
    fi

    if [ "$has_containers" = yes ] &&
        { [ ! -f "$stage/$package/router-containers.sh" ] || [ -L "$stage/$package/router-containers.sh" ]; }; then
        echo 'Invalid optional containers installer.' >&2
        exit 5
    fi

    prefix=/opt/debian-router-b5-2840
    release_dir=$prefix/releases/$package_version
    service=/etc/systemd/system/debian-router-api.service
    agent_service=/etc/systemd/system/debian-router-agent.service
    pppoe_service=/etc/systemd/system/debian-router-pppoe.service
    agent_current=$prefix/agent-current
    exec 9>/run/lock/debian-router-install.lock
    flock -x 9
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
        if [ "$has_pppoe" = yes ] && {
            [ ! -f "$release_dir/PPPOE_SUPPORT" ] || [ -L "$release_dir/PPPOE_SUPPORT" ] ||
            ! cmp -s "$stage/$package/PPPOE_SUPPORT" "$release_dir/PPPOE_SUPPORT";
        }; then
            echo 'Installed PPPoE marker differs from verified release.' >&2
            exit 6
        fi
        if [ "$has_lan" = yes ] && {
            [ ! -f "$release_dir/LAN_SUPPORT" ] || [ -L "$release_dir/LAN_SUPPORT" ] ||
            ! cmp -s "$stage/$package/LAN_SUPPORT" "$release_dir/LAN_SUPPORT";
        }; then
            echo 'Installed LAN marker differs from verified release.' >&2
            exit 6
        fi
        if [ "$has_sing_box" = yes ] && {
            [ ! -f "$release_dir/SING_BOX_SUPPORT" ] || [ -L "$release_dir/SING_BOX_SUPPORT" ] ||
            ! cmp -s "$sing_box_marker" "$release_dir/SING_BOX_SUPPORT";
        }; then
            echo 'Installed sing-box marker differs from verified release.' >&2
            exit 6
        fi
        if [ "$has_proxy_routing" = yes ] && {
            [ -L "$release_dir/PROXY_ROUTING_SUPPORT" ] ||
            ! cmp -s "$proxy_marker" "$release_dir/PROXY_ROUTING_SUPPORT";
        }; then
            echo 'Installed proxy routing marker differs from verified release.' >&2
            exit 6
        fi
        if [ "$has_zapret" = yes ]; then
            for payload in $zapret_files; do
                if [ -L "$release_dir/$payload" ] || ! cmp -s "$stage/$package/$payload" "$release_dir/$payload"; then
                    echo 'Installed Zapret files differ from the verified release.' >&2
                    exit 6
                fi
            done
        fi
        if [ "$has_monitor" = yes ]; then
            for payload in $monitor_files; do
                if [ -L "$release_dir/$payload" ] || ! cmp -s "$stage/$package/$payload" "$release_dir/$payload"; then
                    echo 'Installed monitor files differ from the verified release.' >&2
                    exit 6
                fi
            done
        fi
        if [ "$has_failover" = yes ]; then
            for payload in $failover_files; do
                if [ -L "$release_dir/$payload" ] || ! cmp -s "$stage/$package/$payload" "$release_dir/$payload"; then
                    echo 'Installed failover files differ from the verified release.' >&2
                    exit 6
                fi
            done
        fi
        if [ "$has_containers" = yes ] &&
            { [ ! -f "$release_dir/router-containers.sh" ] || [ -L "$release_dir/router-containers.sh" ] ||
                ! cmp -s "$stage/$package/router-containers.sh" "$release_dir/router-containers.sh"; }; then
            echo 'Installed optional containers installer differs from the verified release.' >&2
            exit 6
        fi
        if [ "$has_rs232" = yes ] &&
            { [ ! -f "$release_dir/router-rs232" ] || [ -L "$release_dir/router-rs232" ] ||
                ! cmp -s "$stage/$package/router-rs232" "$release_dir/router-rs232"; }; then
            echo 'Installed RS232 file differs from the verified release.' >&2
            exit 6
        fi
        if [ "$has_ddns" = yes ]; then
            for payload in $ddns_files; do
                if [ ! -f "$release_dir/$payload" ] || [ -L "$release_dir/$payload" ] ||
                    ! cmp -s "$stage/$package/$payload" "$release_dir/$payload"; then
                    echo 'Installed DDNS files differ from the verified release.' >&2
                    exit 6
                fi
            done
        fi
        if [ "$has_mqtt" = yes ]; then
            for payload in $mqtt_files; do
                if [ ! -f "$release_dir/$payload" ] || [ -L "$release_dir/$payload" ] ||
                    ! cmp -s "$stage/$package/$payload" "$release_dir/$payload"; then
                    echo 'Installed MQTT files differ from the verified release.' >&2
                    exit 6
                fi
            done
        fi
        if [ "$has_recovery" = yes ]; then
            for payload in $recovery_files; do
                if [ -L "$release_dir/$payload" ] || ! cmp -s "$stage/$package/$payload" "$release_dir/$payload"; then
                    echo 'Installed recovery files differ from the verified release.' >&2
                    exit 6
                fi
            done
        fi
    else
        release_tmp=$prefix/releases/.new-$package_version-$$
        install -d -m 0755 "$release_tmp"
        install -m 0755 "$stage/$package/router-api" "$release_tmp/router-api"
        printf '%s\n' "$package_version" > "$release_tmp/VERSION"
        if [ "$has_agent" = yes ]; then
            install -m 0755 "$stage/$package/router-agent" "$release_tmp/router-agent"
        fi
        if [ "$has_pppoe" = yes ]; then
            install -m 0644 "$stage/$package/PPPOE_SUPPORT" "$release_tmp/PPPOE_SUPPORT"
        fi
        if [ "$has_lan" = yes ]; then
            install -m 0644 "$stage/$package/LAN_SUPPORT" "$release_tmp/LAN_SUPPORT"
        fi
        if [ "$has_sing_box" = yes ]; then
            install -m 0644 "$sing_box_marker" "$release_tmp/SING_BOX_SUPPORT"
        fi
        if [ "$has_proxy_routing" = yes ]; then
            install -m 0644 "$proxy_marker" "$release_tmp/PROXY_ROUTING_SUPPORT"
        fi
        if [ "$has_zapret" = yes ]; then
            install -m 0755 "$stage/$package/zapret-alt11.sh" "$stage/$package/zapret-nft.sh" "$release_tmp/"
            install -m 0644 "$stage/$package/debian-router-zapret.service" "$release_tmp/"
        fi
        if [ "$has_zapret_profiles" = yes ]; then
            install -m 0755 "$stage/$package/zapret-config.sh" "$release_tmp/"
        fi
        if [ "$has_monitor" = yes ]; then
            install -m 0755 "$stage/$package/router-monitor" "$release_tmp/"
            install -m 0644 "$stage/$package/$monitor_unit" "$release_tmp/"
        fi
        if [ "$has_failover" = yes ]; then
            install -m 0755 "$stage/$package/router-failover" "$release_tmp/"
            install -m 0644 "$stage/$package/$failover_unit" "$release_tmp/"
        fi
        if [ "$has_recovery" = yes ]; then
            install -m 0755 "$stage/$package/router-recovery" "$release_tmp/"
            install -m 0644 "$stage/$package/$recovery_unit" "$release_tmp/"
        fi
        if [ "$has_mqtt" = yes ]; then
            install -m 0755 "$stage/$package/router-mqtt" "$release_tmp/"
            install -m 0644 "$stage/$package/$mqtt_unit" "$release_tmp/"
        fi
        if [ "$has_ddns" = yes ]; then
            install -m 0755 "$stage/$package/router-ddns" "$release_tmp/"
            install -m 0644 "$stage/$package/$ddns_unit" "$release_tmp/"
        fi
        if [ "$has_rs232" = yes ]; then
            install -m 0755 "$stage/$package/router-rs232" "$release_tmp/"
        fi
        if [ "$has_containers" = yes ]; then
            install -m 0755 "$stage/$package/router-containers.sh" "$release_tmp/"
        fi
        if [ "$has_ui" = yes ]; then
            install -d -m 0755 "$release_tmp/web" "$release_tmp/web/assets"
            install -m 0644 "$stage/$package/web/index.html" "$release_tmp/web/index.html"
            install -m 0644 "$stage/$package/web/assets/app.js" "$release_tmp/web/assets/app.js"
            install -m 0644 "$stage/$package/web/assets/app.css" "$release_tmp/web/assets/app.css"
        fi
        mv -Tf "$release_tmp" "$release_dir"
        release_tmp=
    fi

    if [ "$has_pppoe" = yes ] && [ ! -x /usr/sbin/pppd ]; then
        if ! apt-get install -y ppp; then
            echo 'Could not install the Debian ppp package; active release was not changed.' >&2
            exit 7
        fi
    fi

    if [ "$has_lan" = yes ] && { [ ! -x /usr/sbin/dnsmasq ] || [ ! -x /usr/sbin/nft ]; }; then
        if ! apt-get install -y dnsmasq-base nftables; then
            echo 'Could not install LAN dependencies; active release was not changed.' >&2
            exit 7
        fi
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
    pppoe_unit_changed=no
    pppoe_unit_exists_before=no
    pppoe_was_enabled=no
    pppoe_was_active=no
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
    if [ -f "$pppoe_service" ]; then
        pppoe_unit_exists_before=yes
        cp -p "$pppoe_service" "$stage/previous-pppoe.service"
        if systemctl is-enabled --quiet debian-router-pppoe.service; then pppoe_was_enabled=yes; fi
        if systemctl is-active --quiet debian-router-pppoe.service; then pppoe_was_active=yes; fi
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

    sing_box_unit=debian-router-sing-box.service
    sing_box_service=/etc/systemd/system/$sing_box_unit
    sing_box_config=/etc/debian-router-sing-box.json
    sing_box_unit_changed=no
    sing_box_config_created=no
    sing_box_was_enabled=no
    sing_box_was_active=no
    sing_box_activation_attempted=no

    prepare_sing_box() {
        vendor_dir=$prefix/sing-box/$sing_box_version
        sing_box_bin=$vendor_dir/sing-box
        if [ ! -x "$sing_box_bin" ]; then
            upstream_package=sing-box-$sing_box_version-linux-amd64
            upstream_url=https://github.com/SagerNet/sing-box/releases/download/v$sing_box_version
            /usr/lib/apt/apt-helper download-file "$upstream_url/$upstream_package.tar.gz" \
                "$stage/sing-box.tar.gz" "SHA256:$sing_box_hash" || return 1
            tar -xOzf "$stage/sing-box.tar.gz" "$upstream_package/sing-box" > "$stage/sing-box" || return 1
            install -d -m 0755 "$vendor_dir" || return 1
            install -m 0755 "$stage/sing-box" "$sing_box_bin.new.$$" || return 1
            mv -Tf "$sing_box_bin.new.$$" "$sing_box_bin" || return 1
        fi

        candidate_config=$sing_box_config
        if [ ! -e "$sing_box_config" ] && [ ! -L "$sing_box_config" ]; then
            candidate_config=$stage/sing-box.json
            cat > "$candidate_config" <<'EOF' || return 1
{
  "log": { "level": "warn" },
  "inbounds": [{
    "type": "mixed",
    "tag": "local-proxy",
    "listen": "127.0.0.1",
    "listen_port": 11080
  }],
  "outbounds": [{ "type": "direct", "tag": "direct" }],
  "route": { "final": "direct" }
}
EOF
        elif [ ! -f "$sing_box_config" ] || [ -L "$sing_box_config" ] ||
            [ "$(stat -c '%u:%a' "$sing_box_config")" != '0:600' ]; then
            echo 'The sing-box config must be a regular file owned by root with mode 0600.' >&2
            return 1
        fi
        loaded_config=$sing_box_config
        validation_config=$candidate_config
        if [ "$has_proxy_routing" = yes ]; then
            proxy_root=/var/lib/debian-router-agent/proxy-routing
            if [ ! -e "$proxy_root/active" ] && [ ! -L "$proxy_root/active" ]; then
                install -d -m 0700 "$proxy_root" "$proxy_root/initial" || return 1
                install -m 0600 "$candidate_config" "$proxy_root/initial/config.json" || return 1
                ln -s initial "$proxy_root/active" || return 1
            fi
            loaded_config=$proxy_root/active/config.json
            validation_config=$loaded_config
        fi
        if ! "$sing_box_bin" check -c "$validation_config" >/dev/null 2>&1; then
            echo 'sing-box rejected its configuration.' >&2
            return 1
        fi

        cat > "$stage/$sing_box_unit" <<EOF || return 1
[Unit]
Description=Debian Router sing-box Proxy
After=network.target

[Service]
Type=exec
DynamicUser=yes
UMask=0077
LoadCredential=config.json:$loaded_config
ExecStartPre=:/bin/sh -c '"\$1" check -c "\$2" >/dev/null 2>&1' sh $sing_box_bin %d/config.json
ExecStart=$sing_box_bin run -c %d/config.json
Restart=on-failure
RestartSec=2s
NoNewPrivileges=yes
CapabilityBoundingSet=
ProtectSystem=strict
ProtectHome=yes
PrivateDevices=yes
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK

[Install]
WantedBy=multi-user.target
EOF
        if [ "$has_proxy_routing" = yes ]; then
            sed -i "/^ExecStart=/a ExecStartPost=$release_dir/router-agent proxy-ready\nTimeoutStartSec=5s\nTimeoutStopSec=2s" "$stage/$sing_box_unit" || return 1
        fi
        systemd-analyze verify "$stage/$sing_box_unit" || return 1
        if [ -f "$sing_box_service" ]; then
            cp -p "$sing_box_service" "$stage/previous-sing-box.service" || return 1
        fi
    }

    activate_sing_box() {
        sing_box_activation_attempted=yes
        if [ "$candidate_config" != "$sing_box_config" ]; then
            install -m 0600 "$candidate_config" "$sing_box_config.new.$$" || return 1
            mv -Tf "$sing_box_config.new.$$" "$sing_box_config" || return 1
            sing_box_config_created=yes
        fi
        if ! cmp -s "$stage/$sing_box_unit" "$sing_box_service"; then
            install -m 0644 "$stage/$sing_box_unit" "$sing_box_service.new.$$" || return 1
            mv -Tf "$sing_box_service.new.$$" "$sing_box_service" || return 1
            sing_box_unit_changed=yes
            systemctl daemon-reload || return 1
        fi
        systemctl enable "$sing_box_unit" || return 1
        if [ "$sing_box_unit_changed" = yes ]; then
            systemctl restart "$sing_box_unit" || return 1
        else
            systemctl start "$sing_box_unit" || return 1
        fi
        sleep 1
        systemctl is-active --quiet "$sing_box_unit"
    }

    if [ "$has_sing_box" = yes ] && ! prepare_sing_box; then
        echo 'Could not prepare sing-box; the active release was not changed.' >&2
        exit 7
    fi

    zapret_unit=debian-router-zapret.service
    zapret_service=/etc/systemd/system/$zapret_unit
    zapret_unit_changed=no
    zapret_was_active=no

    prepare_zapret() {
        zapret_assets=$release_dir/zapret-assets
        if [ ! -x "$zapret_assets/bin/nfqws" ]; then
            zapret_version=v72.13
            flowseal_revision=cf018db758623ed1813b9d93a6d7f3bdf01cc2b4
            base=zapret-$zapret_version
            flowseal=zapret-discord-youtube-$flowseal_revision
            /usr/lib/apt/apt-helper download-file \
                "https://github.com/bol-van/zapret/releases/download/$zapret_version/$base.tar.gz" \
                "$stage/nfqws.tar.gz" SHA256:25c74e6c5f48963fa244c2955e76694a07c39447245a0457e2efdc74b3317e68 || return 1
            /usr/lib/apt/apt-helper download-file \
                "https://codeload.github.com/Flowseal/zapret-discord-youtube/tar.gz/$flowseal_revision" \
                "$stage/alt11.tar.gz" SHA256:bbf6b3fbaa4bfd392db48631230a236bc70912b9836ca94024ce7fa2725eddc2 || return 1
            zapret_tmp=$(mktemp -d "$release_dir/.zapret.XXXXXXXX") || return 1
            chmod 0755 "$zapret_tmp" || return 1
            mkdir -p "$zapret_tmp/bin" "$zapret_tmp/lists" || return 1
            tar -xOzf "$stage/nfqws.tar.gz" "$base/binaries/linux-x86_64/nfqws" > "$zapret_tmp/bin/nfqws" || return 1
            chmod 0755 "$zapret_tmp/bin/nfqws" || return 1
            for payload in ACTIVE_DISCORD_UDP.bin quic_initial_www_google_com.bin \
                tls_clienthello_www_google_com.bin tls_clienthello_www_sferum_ru.bin stun2.bin; do
                tar -xOzf "$stage/alt11.tar.gz" "$flowseal/bin/$payload" > "$zapret_tmp/bin/$payload" || return 1
            done
            for payload in list-general.txt list-google.txt list-exclude.txt ipset-all.txt ipset-exclude.txt; do
                tar -xOzf "$stage/alt11.tar.gz" "$flowseal/lists/$payload" > "$zapret_tmp/lists/$payload" || return 1
            done
            "$release_dir/zapret-alt11.sh" "$zapret_tmp" --dry-run >"$stage/zapret-check.log" 2>&1 || return 1
            mv -T "$zapret_tmp" "$zapret_assets" || return 1
            zapret_tmp=
        else
            "$release_dir/zapret-alt11.sh" "$zapret_assets" --dry-run >"$stage/zapret-check.log" 2>&1 || return 1
        fi
        sed -e "s|@RELEASE@|$release_dir|g" -e "s|@ASSETS@|$zapret_assets|g" \
            "$release_dir/$zapret_unit" > "$stage/$zapret_unit" || return 1
        systemd-analyze verify "$stage/$zapret_unit" || return 1
    }

    activate_zapret() {
        if cmp -s "$stage/$zapret_unit" "$zapret_service"; then return 0; fi
        if [ -f "$zapret_service" ]; then
            cp -p "$zapret_service" "$stage/previous-zapret.service" || return 1
        fi
        if systemctl is-active --quiet "$zapret_unit"; then zapret_was_active=yes; fi
        install -m 0644 "$stage/$zapret_unit" "$zapret_service.new.$$" || return 1
        mv -Tf "$zapret_service.new.$$" "$zapret_service" || return 1
        zapret_unit_changed=yes
        systemctl daemon-reload || return 1
        if [ "$zapret_was_active" = yes ]; then systemctl restart "$zapret_unit" || return 1; fi
    }

    if [ "$has_zapret" = yes ] && ! prepare_zapret; then
        if [ -s "$stage/zapret-check.log" ]; then cat "$stage/zapret-check.log" >&2; fi
        echo 'Could not prepare Zapret; the active release was not changed.' >&2
        exit 7
    fi

    monitor_service=/etc/systemd/system/$monitor_unit
    monitor_changed=no
    monitor_was_active=no
    monitor_was_enabled=no
    if [ "$has_monitor" = yes ]; then
        if [ -L "$monitor_service" ] ||
            ! "$release_dir/router-monitor" check-config ||
            ! sed "s|@RELEASE@|$release_dir|g" "$release_dir/$monitor_unit" > "$stage/$monitor_unit" ||
            ! systemd-analyze verify "$stage/$monitor_unit"; then
            echo 'Could not prepare the monitor unit; the active release was not changed.' >&2
            exit 7
        fi
    fi

    activate_monitor() {
        if [ "$has_monitor" = yes ] && cmp -s "$stage/$monitor_unit" "$monitor_service"; then return 0; fi
        if [ "$has_monitor" = no ] && [ ! -f "$monitor_service" ]; then return 0; fi
        if [ -f "$monitor_service" ]; then
            cp -p "$monitor_service" "$stage/previous-monitor.service" || return 1
            if systemctl is-active --quiet "$monitor_unit"; then monitor_was_active=yes; fi
            if systemctl is-enabled --quiet "$monitor_unit"; then monitor_was_enabled=yes; fi
        fi
        if [ "$has_monitor" = no ]; then
            monitor_changed=yes
            systemctl disable --now "$monitor_unit" || return 1
            rm -- "$monitor_service" || return 1
            systemctl daemon-reload
            return
        fi
        install -m 0644 "$stage/$monitor_unit" "$monitor_service.new.$$" || return 1
        mv -Tf "$monitor_service.new.$$" "$monitor_service" || return 1
        monitor_changed=yes
        systemctl daemon-reload || return 1
        if [ ! -f "$stage/previous-monitor.service" ]; then
            systemctl enable --now "$monitor_unit"
        elif [ "$monitor_was_active" = yes ]; then
            systemctl restart "$monitor_unit"
        fi
    }

    prepare_optional_service() {
        [ "$1" = yes ] || return 0
        optional_unit=$2
        if [ -L "/etc/systemd/system/$optional_unit" ] ||
            ! sed "s|@RELEASE@|$release_dir|g" "$release_dir/$optional_unit" > "$stage/$optional_unit" ||
            ! systemd-analyze verify "$stage/$optional_unit"; then
            echo "Could not prepare $optional_unit." >&2
            return 1
        fi
        if [ -e "$4" ] && ! "$release_dir/$3" check-config; then
            echo "Could not validate the configuration for $optional_unit." >&2
            return 1
        fi
    }
    prepare_optional_service "$has_failover" "$failover_unit" router-failover /etc/debian-router-b5-2840/failover.json || exit 7
    prepare_optional_service "$has_recovery" "$recovery_unit" router-recovery /etc/debian-router-b5-2840/recovery.json || exit 7
    prepare_optional_service "$has_mqtt" "$mqtt_unit" router-mqtt /etc/debian-router-b5-2840/mqtt.json || exit 7
    prepare_optional_service "$has_ddns" "$ddns_unit" router-ddns /etc/debian-router-b5-2840/ddns.json || exit 7

    activate_optional_service() {
        optional_unit=$2
        optional_file=/etc/systemd/system/$optional_unit
        optional_state=$stage/optional-$optional_unit
        if [ "$1" = yes ] && cmp -s "$stage/$optional_unit" "$optional_file"; then return 0; fi
        if [ "$1" = no ] && [ ! -f "$optional_file" ]; then return 0; fi
        mkdir "$optional_state" || return 1
        if [ -f "$optional_file" ]; then
            cp -p "$optional_file" "$optional_state/previous" || return 1
            if systemctl is-active --quiet "$optional_unit"; then
                touch "$optional_state/active" || return 1
            fi
            if systemctl is-enabled --quiet "$optional_unit"; then
                touch "$optional_state/enabled" || return 1
            fi
        fi
        if [ "$1" = no ]; then
            touch "$optional_state/changed" || return 1
            systemctl disable --now "$optional_unit" || return 1
            rm -- "$optional_file" || return 1
            systemctl daemon-reload
            return
        fi
        install -m 0644 "$stage/$optional_unit" "$optional_file.new.$$" || return 1
        touch "$optional_state/changed" || return 1
        mv -Tf "$optional_file.new.$$" "$optional_file" || return 1
        systemctl daemon-reload || return 1
        if [ -e "$optional_state/active" ]; then systemctl restart "$optional_unit"; fi
    }

    rollback_optional_service() {
        optional_unit=$1
        optional_file=/etc/systemd/system/$optional_unit
        optional_state=$stage/optional-$optional_unit
        [ -e "$optional_state/changed" ] || return 0
        systemctl stop "$optional_unit" || :
        if [ -f "$optional_state/previous" ]; then
            mv -Tf "$optional_state/previous" "$optional_file"
        else
            rm -f -- "$optional_file"
        fi
        rm -f -- "$optional_file.new.$$"
        systemctl daemon-reload || :
        if [ -e "$optional_state/enabled" ]; then systemctl enable "$optional_unit" || :; fi
        if [ -e "$optional_state/active" ]; then
            systemctl reset-failed "$optional_unit" || :
            systemctl start "$optional_unit" || :
        fi
    }

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
    if [ "$has_lan" = yes ]; then
        sed -i '/^Environment=ROUTER_AGENT_SOCKET=/a Environment=ROUTER_LAN_HTTPS=yes' "$unit_tmp"
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
After=local-fs.target systemd-networkd.service

[Service]
Type=simple
User=root
Group=debian-router-agent
RuntimeDirectory=debian-router-agent
RuntimeDirectoryMode=0750
StateDirectory=debian-router-agent
StateDirectoryMode=0700
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
ReadWritePaths=/etc/systemd/network
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX AF_NETLINK

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

    if [ "$has_pppoe" = yes ]; then
        pppoe_unit_tmp=$stage/debian-router-pppoe.service
        cat > "$pppoe_unit_tmp" <<'EOF'
[Unit]
Description=Debian Router WAN PPPoE
After=network.target debian-router-agent.service
Requires=debian-router-agent.service
ConditionPathExists=/var/lib/debian-router-agent/pppoe-active/options

[Service]
Type=simple
ExecStartPre=/usr/bin/test -S /run/debian-router-agent/agent.sock
ExecStart=/usr/sbin/pppd file /var/lib/debian-router-agent/pppoe-active/options nodetach
Restart=on-failure
RestartSec=5s
UMask=0077
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
        if [ ! -f "$pppoe_service" ] || ! cmp -s "$pppoe_unit_tmp" "$pppoe_service"; then
            pppoe_unit_changed=yes
            install -m 0644 "$pppoe_unit_tmp" "$pppoe_service.new.$$"
            mv -Tf "$pppoe_service.new.$$" "$pppoe_service"
        fi
    fi

    rollback_install() {
        rollback_optional_service "$ddns_unit"
        rollback_optional_service "$mqtt_unit"
        rollback_optional_service "$recovery_unit"
        rollback_optional_service "$failover_unit"
        if [ "$monitor_changed" = yes ]; then
            systemctl stop "$monitor_unit" || :
            if [ "$monitor_was_enabled" = no ]; then systemctl disable "$monitor_unit" || :; fi
            if [ -f "$stage/previous-monitor.service" ]; then
                mv -Tf "$stage/previous-monitor.service" "$monitor_service"
            else
                rm -f -- "$monitor_service"
            fi
            systemctl daemon-reload || :
            if [ "$monitor_was_enabled" = yes ]; then systemctl enable "$monitor_unit" || :; fi
            if [ "$monitor_was_active" = yes ]; then
                systemctl reset-failed "$monitor_unit" || :
                systemctl start "$monitor_unit" || :
            fi
        fi
        if [ "$zapret_unit_changed" = yes ]; then
            if [ -f "$stage/previous-zapret.service" ]; then
                cp -p "$stage/previous-zapret.service" "$zapret_service.new.$$"
                mv -Tf "$zapret_service.new.$$" "$zapret_service"
            else
                rm -f -- "$zapret_service"
            fi
            systemctl daemon-reload || :
            if [ "$zapret_was_active" = yes ]; then systemctl restart "$zapret_unit" || :; fi
        fi
        if [ "$sing_box_activation_attempted" = yes ]; then
            if [ "$sing_box_was_enabled" = no ]; then
                systemctl disable "$sing_box_unit" || :
            fi
            if [ "$sing_box_unit_changed" = yes ]; then
                if [ -f "$stage/previous-sing-box.service" ]; then
                    mv -Tf "$stage/previous-sing-box.service" "$sing_box_service"
                else
                    systemctl stop "$sing_box_unit" || :
                    rm -f -- "$sing_box_service"
                fi
                systemctl daemon-reload || :
            fi
            if [ "$sing_box_was_active" = yes ]; then
                if [ "$sing_box_unit_changed" = yes ]; then systemctl restart "$sing_box_unit" || :; fi
            elif [ -f "$sing_box_service" ]; then
                systemctl stop "$sing_box_unit" || :
            fi
            if [ "$sing_box_config_created" = yes ]; then rm -f -- "$sing_box_config"; fi
        fi
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
        if [ "$pppoe_unit_changed" = yes ]; then
            if [ "$pppoe_unit_exists_before" = yes ]; then
                cp -p "$stage/previous-pppoe.service" "$pppoe_service.new.$$"
                mv -Tf "$pppoe_service.new.$$" "$pppoe_service"
            else
                rm -f -- "$pppoe_service"
            fi
        fi
        systemctl daemon-reload || :
        if [ "$pppoe_unit_exists_before" = yes ]; then
            if [ "$pppoe_was_enabled" = yes ]; then systemctl enable debian-router-pppoe.service || :; else systemctl disable debian-router-pppoe.service || :; fi
            if [ "$pppoe_was_active" = yes ]; then systemctl restart debian-router-pppoe.service || :; else systemctl stop debian-router-pppoe.service || :; fi
        elif [ "$pppoe_unit_changed" = yes ]; then
            systemctl disable --now debian-router-pppoe.service || :
        fi
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

    if [ "$unit_changed" = yes ] || [ "$agent_unit_changed" = yes ] || [ "$pppoe_unit_changed" = yes ]; then
        if ! systemctl daemon-reload; then
            rollback_install
            echo 'Could not reload systemd units; the previous release was restored.' >&2
            exit 7
        fi
    fi
    if [ "$has_pppoe" = yes ]; then
        if ! systemctl enable debian-router-pppoe.service; then
            rollback_install
            echo 'Could not enable PPPoE support; the previous release was restored.' >&2
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
    if [ "$has_sing_box" = yes ]; then
        if systemctl is-enabled --quiet "$sing_box_unit"; then sing_box_was_enabled=yes; fi
        if systemctl is-active --quiet "$sing_box_unit"; then sing_box_was_active=yes; fi
        if ! activate_sing_box; then
            rollback_install
            echo 'sing-box startup failed; the previous release was restored.' >&2
            exit 7
        fi
        printf 'sing-box %s: active (%s)\n' "$sing_box_version" "$sing_box_unit"
    fi
    if [ "$has_agent" = no ] && [ "$agent_unit_exists_before" = yes ]; then
        if systemctl disable --now debian-router-agent.service; then
            rm -f -- "$agent_service" "$agent_current"
            systemctl daemon-reload
        fi
    fi
    if [ "$has_zapret" = yes ]; then
        if ! activate_zapret; then
            rollback_install
            echo 'Could not install the Zapret unit; the previous release was restored.' >&2
            exit 7
        fi
        printf 'Zapret service: %s\n' "$zapret_unit"
    fi
    if ! activate_monitor; then
        rollback_install
        echo 'Could not activate the monitor; the previous release was restored.' >&2
        exit 7
    fi
    if [ "$has_monitor" = yes ]; then printf 'Monitor service: %s\n' "$monitor_unit"; fi
    if ! activate_optional_service "$has_failover" "$failover_unit" ||
        ! activate_optional_service "$has_recovery" "$recovery_unit" ||
        ! activate_optional_service "$has_mqtt" "$mqtt_unit" ||
        ! activate_optional_service "$has_ddns" "$ddns_unit"; then
        rollback_install
        echo 'Could not update the optional services; the previous release was restored.' >&2
        exit 7
    fi
    if [ "$has_failover" = yes ]; then printf 'Failover service: %s\n' "$failover_unit"; fi
    if [ "$has_recovery" = yes ]; then printf 'Recovery service: %s\n' "$recovery_unit"; fi
    if [ "$has_mqtt" = yes ]; then printf 'MQTT service: %s\n' "$mqtt_unit"; fi
    if [ "$has_ddns" = yes ]; then printf 'DDNS service: %s\n' "$ddns_unit"; fi
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
