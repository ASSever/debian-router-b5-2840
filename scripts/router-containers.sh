#!/bin/sh
set -eu

fail() { printf '%s\n' "$*" >&2; exit 1; }

case "$#:${1-}" in
    1:--install-docker|1:--install-portainer) ;;
    *) printf '%s\n' 'Usage: sudo sh router-containers.sh --install-docker | --install-portainer' >&2; exit 2 ;;
esac
[ "$(id -u)" = 0 ] || fail 'Run this command as root.'
. /etc/os-release
[ "${ID-}/${VERSION_ID-}/$(dpkg --print-architecture)" = debian/13/amd64 ] ||
    fail 'Debian 13 amd64 is required.'

exec 9>/run/lock/debian-router-containers.lock
flock -x 9
stage=$(mktemp -d)
masked=
rollback=
cleanup() {
    status=$?
    if [ -n "$rollback" ]; then
        if [ "$status" -ne 0 ]; then
            if ! systemctl disable --now docker.socket docker.service; then
                printf '%s\n' 'Docker rollback is still armed.' >&2
                return
            fi
        fi
        systemctl stop "$rollback.timer" || :
    fi
    if [ -n "$masked" ]; then
        systemctl unmask --runtime docker.service docker.socket || :
        systemctl disable docker.service docker.socket 2>/dev/null || :
    fi
    rm -r -- "$stage"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

config=/etc/docker/daemon.json
cat > "$stage/daemon.json" <<'JSON'
{
  "bridge": "none",
  "iptables": false,
  "ip6tables": false,
  "ip-forward": false,
  "ip-masq": false,
  "userland-proxy": false
}
JSON

managed_config() {
    [ -f "$config" ] && [ ! -L "$config" ] && cmp -s "$stage/daemon.json" "$config"
}

for unit in docker.service docker.socket; do
    for root in /etc/systemd/system /run/systemd/system; do
        for path in "$root/$unit" "$root/$unit.d"; do
            [ ! -e "$path" ] && [ ! -L "$path" ] ||
                fail "Existing systemd override for $unit. Inspect it before using this installer."
        done
    done
done

docker_local() {
    env -u DOCKER_CONTEXT -u DOCKER_HOST -u DOCKER_API_VERSION \
        docker --host unix:///run/docker.sock "$@"
}

case "$1" in
    --install-docker)
        if ! managed_config; then
            [ ! -e "$config" ] && [ ! -L "$config" ] &&
                [ ! -e /var/lib/docker ] && [ ! -L /var/lib/docker ] &&
                [ ! -e /run/docker.sock ] &&
                ! command -v dockerd >/dev/null && ! systemctl is-active --quiet docker.service &&
                ! systemctl is-active --quiet docker.socket ||
                fail 'An existing Docker engine or configuration requires manual review.'
            [ ! -L /etc/docker ] || fail 'Expected a local Docker configuration directory.'
            install -d -m 0755 /etc/docker
            install -m 0644 "$stage/daemon.json" "$config"
        fi
        if ! command -v dockerd >/dev/null || ! command -v docker >/dev/null; then
            ! systemctl is-active --quiet docker.service && ! systemctl is-active --quiet docker.socket ||
                fail 'Stop the existing Docker systemd units before completing installation.'
            masked=yes
            systemctl mask --runtime docker.service docker.socket
            apt-get -y --no-remove --no-install-recommends install docker.io docker-cli
            systemctl unmask --runtime docker.service docker.socket
            masked=
        fi
        dockerd --validate --config-file "$config"
        if ! systemctl is-active --quiet docker.service; then
            [ ! -e /sys/class/net/docker0 ] || fail 'An existing docker0 interface requires manual review.'
            rollback=debian-router-docker-install-rollback
            systemd-run --quiet --unit="$rollback" --on-active=4min \
                /usr/bin/systemctl disable --now docker.socket docker.service
            systemctl enable --now docker.service
        fi
        docker_local version --format '{{.Server.Version}}'
        ;;
    --install-portainer)
        managed_config && systemctl is-active --quiet docker.service ||
            fail 'Install and start the managed Docker service first.'
        name=debian-router-portainer
        owner=io.github.assever.debian-router
        image=docker.io/portainer/portainer-ce@sha256:18616fff2998f8ca0e8ebe7f028658ae1682859cf43f212f81a849c1093ad639
        if label=$(docker_local container inspect --format "{{index .Config.Labels \"$owner\"}}" "$name" 2>/dev/null); then
            [ "$label" = portainer ] || fail "Container $name already exists under separate management."
            docker_local start "$name"
        else
            docker_local pull "$image"
            docker_local run --detach --name "$name" --label "$owner=portainer" \
                --restart unless-stopped --network host \
                --mount type=bind,source=/run/docker.sock,target=/var/run/docker.sock \
                --mount "type=volume,source=$name-data,target=/data" \
                "$image" --http-disabled --bind-https 127.0.0.1:9443 --tunnel-addr 127.0.0.1
        fi
        printf '%s\n' 'Portainer HTTPS: https://127.0.0.1:9443 (use SSH forwarding).'
        ;;
esac
